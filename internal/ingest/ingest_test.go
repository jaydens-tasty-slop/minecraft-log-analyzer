package ingest

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"minecraft-log-analyzer/internal/logparse"
	"minecraft-log-analyzer/internal/store"
)

func TestDirectoryStreamsGzipIsIdempotentAndKeepsIdenticalDays(t *testing.T) {
	dir := t.TempDir()
	line := "[02:38:01] [Server thread/INFO]: Automatic saving is now disabled\n"
	writeGzip(t, filepath.Join(dir, "2026-01-01-1.log.gz"), line)
	writeGzip(t, filepath.Join(dir, "2026-01-02-1.log.gz"), line)
	// The active file must never be consumed by the exactly-once archive path.
	if err := os.WriteFile(filepath.Join(dir, "latest.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := Directory(db, dir, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ingested != 2 || first.Skipped != 0 {
		t.Fatalf("first result = %#v", first)
	}
	second, err := Directory(db, dir, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if second.Ingested != 0 || second.Skipped != 2 {
		t.Fatalf("second result = %#v", second)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("source files = %d, want 2", count)
	}
	if _, err := db.Exec(`UPDATE source_files SET parser_version = 1`); err != nil {
		t.Fatal(err)
	}
	third, err := Directory(db, dir, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if third.Ingested != 0 || third.Reprocessed != 2 || third.Skipped != 0 {
		t.Fatalf("parser upgrade result = %#v", third)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files WHERE parser_version = ?`, logparse.Version).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("current parser source files = %d, want 2", count)
	}
}

func TestDiscoverSortsArchivesAndSkipsInvalidEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"2026-01-01.log",
		"2026-01-01-2.log",
		"2026-01-01-01.log",
		"2026-01-02.log.gz",
		"2026-13-01.log",
		"latest.log",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "2026-01-03.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	items, err := discover(dir, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(items))
	for index, item := range items {
		got[index] = filepath.Base(item.path)
	}
	want := []string{"2026-01-01-01.log", "2026-01-01.log", "2026-01-01-2.log", "2026-01-02.log.gz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("discovered archive order = %#v, want %#v", got, want)
	}
	if !items[0].date.Equal(items[1].date) || items[0].sequence != items[1].sequence || !items[3].gzip {
		t.Fatalf("archive metadata = %#v", items)
	}
	if !IsArchiveName(" 2026-01-01.log.gz ") || IsArchiveName("latest.log") {
		t.Error("IsArchiveName did not trim or conservatively reject names")
	}
}

func TestReadArchivePlainAndCompressedFailures(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "2026-01-01.log")
	if err := os.WriteFile(plainPath, []byte("[12:00:00] [Server thread/INFO]: <Alice> hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := archive{path: plainPath, date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), dateText: "2026-01-01"}
	record, err := readArchive(plain, time.UTC)
	if err != nil || record.Lines != 1 || len(record.Events) != 1 || record.Events[0].Detail != "hello" {
		t.Fatalf("plain archive record=%#v err=%v", record, err)
	}
	if _, err := readArchive(archive{path: filepath.Join(dir, "missing.log")}, time.UTC); err == nil {
		t.Fatal("readArchive accepted a missing path")
	}

	badGzipPath := filepath.Join(dir, "bad.log.gz")
	if err := os.WriteFile(badGzipPath, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(archive{path: badGzipPath, gzip: true}, time.UTC); err == nil || !strings.Contains(err.Error(), "open gzip stream") {
		t.Fatalf("invalid gzip error = %v", err)
	}

	checksumPath := filepath.Join(dir, "checksum.log.gz")
	writeGzip(t, checksumPath, "[12:00:00] [Server thread/INFO]: Alice joined the game\n")
	compressed, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatal(err)
	}
	compressed[len(compressed)-8] ^= 0xff
	if err := os.WriteFile(checksumPath, compressed, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(archive{path: checksumPath, gzip: true, date: plain.date, dateText: plain.dateText}, time.UTC); err == nil {
		t.Fatal("readArchive accepted a corrupt gzip checksum")
	}
}

func TestDirectoryReportsProgressAndReturnsDiscoveryErrors(t *testing.T) {
	if _, err := discover(filepath.Join(t.TempDir(), "missing"), time.UTC); err == nil {
		t.Fatal("discover accepted a missing directory")
	}
	dir := t.TempDir()
	writeGzip(t, filepath.Join(dir, "2026-01-01.log.gz"), "[12:00:00] [Server thread/INFO]: Alice joined the game\n")
	path := filepath.Join(dir, "2026-01-01.log.gz")
	compressed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed[len(compressed)-8] ^= 0xff
	if err := os.WriteFile(path, compressed, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var phases []string
	_, err = DirectoryWithProgress(db, dir, time.UTC, func(progress Progress) {
		phases = append(phases, progress.Phase)
	})
	if err == nil || !strings.Contains(err.Error(), "2026-01-01.log.gz") {
		t.Fatalf("DirectoryWithProgress error = %v", err)
	}
	if !reflect.DeepEqual(phases, []string{"discovered", "parsing"}) {
		t.Errorf("progress phases = %#v", phases)
	}
}

func writeGzip(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
