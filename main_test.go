package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"minecraft-log-analyzer/internal/ingest"
	"minecraft-log-analyzer/internal/logparse"
	"minecraft-log-analyzer/internal/store"
)

func TestSplitWords(t *testing.T) {
	if got := splitWords("  "); got != nil {
		t.Fatalf("splitWords(empty) = %#v, want nil", got)
	}
	got := splitWords(" Fuck, , SHIT ,damn ")
	want := []string{"fuck", "shit", "damn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitWords() = %#v, want %#v", got, want)
	}
}

func TestMainIngestOnly(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for day := 2; day <= 4; day++ {
		name := filepath.Join(logsDir, time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")+".log")
		logLine := "[12:00:00] [Server thread/INFO]: Alice joined the game\n"
		if err := os.WriteFile(name, []byte(logLine), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(dir, "minecraft.sqlite")
	callMain(t, "-logs", logsDir, "-db", dbPath, "-out", filepath.Join(dir, "report"), "-timezone", "UTC", "-ingest-only")

	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type = 'login' AND player_key = 'alice'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Fatalf("ingested login events = %d, want 3", events)
	}
	if _, err := os.Stat(filepath.Join(dir, "report", "index.html")); !os.IsNotExist(err) {
		t.Fatalf("ingest-only unexpectedly created a report: %v", err)
	}
}

func TestMainReportOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "existing.sqlite")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	_, err = store.SaveFile(context.Background(), db, store.FileRecord{
		Path: "seed.log", SHA256: strings.Repeat("a", 64), LogDate: "2026-01-02",
		ByteSize: 10, Lines: 2, TimestampedLines: 2, FirstAt: base, LastAt: base.Add(time.Hour),
		Events: []logparse.Event{
			{At: base, Type: "login", Player: "Alice", Line: 1},
			{At: base.Add(time.Minute), Type: "login", Player: "Bob", Line: 2},
		},
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "report")
	callMain(t,
		"-db", dbPath,
		"-logs", filepath.Join(dir, "missing-logs"),
		"-out", outDir,
		"-timezone", "UTC",
		"-server-name", "  Test Realm  ",
		"-report-only",
		"-max-session", "90m",
		"-swear-words", "",
		"-generated-at", "2026-09-26T12:00:00Z",
	)
	index, err := os.ReadFile(filepath.Join(outDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "Test Realm stats") || !strings.Contains(string(index), "Sep 26, 2026 12:00 PM UTC") {
		t.Fatalf("report did not use CLI options: %s", index[:min(len(index), 500)])
	}
}

func callMain(t *testing.T, args ...string) {
	t.Helper()
	if code := runCommand(args); code != 0 {
		t.Fatalf("runCommand(%q) = %d, want 0", args, code)
	}
}

func TestRunRejectsInvalidOptionsAndInputs(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"conflicting modes", []string{"-ingest-only", "-report-only"}, "cannot be used together"},
		{"unknown timezone", []string{"-timezone", "Not/A_Zone"}, "load timezone"},
		{"nonpositive session", []string{"-max-session", "0"}, "must be positive"},
		{"invalid generated time", []string{"-generated-at", "yesterday"}, "RFC3339"},
		{"unknown option", []string{"-does-not-exist"}, "flag provided but not defined"},
		{"missing readonly database", []string{"-db", filepath.Join(dir, "missing.sqlite"), "-report-only"}, "open database"},
		{"missing logs directory", []string{"-db", filepath.Join(dir, "ingest.sqlite"), "-logs", filepath.Join(dir, "missing-logs"), "-timezone", "UTC", "-ingest-only"}, "ingest logs"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run(%q) error = %v, want substring %q", test.args, err, test.want)
			}
		})
	}
}

func TestRunCommandFailuresAndUnrecognizedProgress(t *testing.T) {
	if code := runCommand([]string{"-max-session", "0"}); code != 1 {
		t.Fatalf("runCommand(invalid options) = %d, want 1", code)
	}
	if err := run([]string{"-h"}); err != nil {
		t.Fatalf("run(help) = %v, want nil", err)
	}
	logIngestProgress(ingest.Progress{Phase: "unknown"})
}

func TestRunRejectsStaleDatabaseAndReportOutputFailure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "stale.sqlite")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO source_files
		(path, content_sha256, log_date, byte_size, line_count, timestamped_line_count,
		 event_count, ingested_at, parser_version)
		VALUES ('old.log', ?, '2026-01-01', 1, 0, 0, 0, '2026-01-01T00:00:00Z', 1)`, strings.Repeat("0", 64)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-db", dbPath, "-report-only", "-timezone", "UTC"}); err == nil || !strings.Contains(err.Error(), "load events") {
		t.Fatalf("stale database error = %v", err)
	}

	readyDB := filepath.Join(dir, "ready.sqlite")
	db, err = store.Open(readyDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(outFile, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-db", readyDB, "-report-only", "-out", outFile, "-timezone", "UTC"}); err == nil || !strings.Contains(err.Error(), "write reports") {
		t.Fatalf("report output error = %v", err)
	}
}
