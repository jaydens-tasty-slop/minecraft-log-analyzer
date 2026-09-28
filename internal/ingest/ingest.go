package ingest

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"minecraft-log-analyzer/internal/logparse"
	"minecraft-log-analyzer/internal/store"
)

type Result struct {
	Ingested    int
	Reprocessed int
	Skipped     int
	Events      int
	Lines       int
	Identities  int
}

type Progress struct {
	Phase   string
	Current int
	Total   int
	Path    string
}

type ProgressFunc func(Progress)

type archive struct {
	path     string
	date     time.Time
	dateText string
	sequence int
	gzip     bool
}

var archiveRE = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:-(\d+))?\.log(\.gz)?$`)

// Directory ingests immutable, dated archives. latest.log is intentionally not
// eligible: reading an actively appended file before it is rotated makes exact
// once processing impossible without coordinating with the Minecraft process.
func Directory(db *sql.DB, dir string, loc *time.Location) (Result, error) {
	return DirectoryWithProgress(db, dir, loc, nil)
}

// DirectoryWithProgress is Directory with optional read-only progress events.
// The callback runs synchronously and must return quickly.
func DirectoryWithProgress(db *sql.DB, dir string, loc *time.Location, progress ProgressFunc) (Result, error) {
	archives, err := discover(dir, loc)
	if err != nil {
		return Result{}, err
	}
	if progress != nil {
		progress(Progress{Phase: "discovered", Total: len(archives)})
	}
	var result Result
	for index, item := range archives {
		if progress != nil {
			progress(Progress{Phase: "parsing", Current: index + 1, Total: len(archives), Path: item.path})
		}
		record, err := readArchive(item, loc)
		if err != nil {
			return result, fmt.Errorf("%s: %w", item.path, err)
		}
		saved, err := store.SaveFile(context.Background(), db, record)
		if err != nil {
			return result, err
		}
		result.Identities += saved.Identities
		if saved.Reprocessed {
			result.Reprocessed++
			result.Events += len(record.Events)
			result.Lines += record.Lines
			continue
		}
		if !saved.Added {
			result.Skipped++
			continue
		}
		result.Ingested++
		result.Events += len(record.Events)
		result.Lines += record.Lines
	}
	return result, nil
}

func discover(dir string, loc *time.Location) ([]archive, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	items := make([]archive, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := archiveRE.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		date, err := time.ParseInLocation("2006-01-02", match[1], loc)
		if err != nil {
			continue
		}
		sequence := 1
		if match[2] != "" {
			sequence, _ = strconv.Atoi(match[2])
		}
		path, err := filepath.Abs(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		items = append(items, archive{path: path, date: date, dateText: match[1], sequence: sequence, gzip: match[3] != ""})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].date.Equal(items[j].date) {
			if items[i].sequence == items[j].sequence {
				return items[i].path < items[j].path
			}
			return items[i].sequence < items[j].sequence
		}
		return items[i].date.Before(items[j].date)
	})
	return items, nil
}

func readArchive(item archive, loc *time.Location) (store.FileRecord, error) {
	info, err := os.Stat(item.path)
	if err != nil {
		return store.FileRecord{}, err
	}
	file, err := os.Open(item.path)
	if err != nil {
		return store.FileRecord{}, err
	}
	defer file.Close()
	var reader io.Reader = file
	var gz *gzip.Reader
	if item.gzip {
		gz, err = gzip.NewReader(file)
		if err != nil {
			return store.FileRecord{}, fmt.Errorf("open gzip stream: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	hash := sha256.New()
	parsed, err := logparse.New(item.date, loc).Parse(io.TeeReader(reader, hash))
	if err != nil {
		return store.FileRecord{}, err
	}
	// Parse reads to EOF, so this checksum represents decompressed contents. It
	// deduplicates the same log even if it is recompressed or renamed.
	sha := hex.EncodeToString(hash.Sum(nil))
	return store.FileRecord{
		Path:             filepath.Clean(item.path),
		SHA256:           sha,
		LogDate:          item.dateText,
		ByteSize:         info.Size(),
		Lines:            parsed.Lines,
		TimestampedLines: parsed.TimestampedLines,
		FirstAt:          parsed.FirstAt,
		LastAt:           parsed.LastAt,
		Events:           parsed.Events,
		Identities:       parsed.Identities,
	}, nil
}

// IsArchiveName is exposed for small integrations that want to mirror the
// command's conservative file selection.
func IsArchiveName(name string) bool {
	return archiveRE.MatchString(strings.TrimSpace(name))
}
