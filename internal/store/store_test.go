package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minecraft-log-analyzer/internal/logparse"
)

func TestOpenReadOnlyDoesNotModifyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readonly.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	db, err = OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := LoadEvents(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("read-only database changed while loading report data")
	}
}

func TestDatabasePathWithURICharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server?logs#archive.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database was not created at the requested path: %v", err)
	}
	db, err = OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, _, _, err := LoadEvents(db); err != nil {
		t.Fatal(err)
	}
}

func TestPersistedTablesAreStrict(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "strict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, strict FROM pragma_table_list WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	strict := map[string]int{}
	for rows.Next() {
		var name string
		var value int
		if err := rows.Scan(&name, &value); err != nil {
			t.Fatal(err)
		}
		strict[name] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"source_files", "events", "identity_observations"} {
		if strict[name] != 1 {
			t.Errorf("table %s strict = %d, want 1", name, strict[name])
		}
	}
}

func TestLoadEventsRejectsStaleParserData(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "stale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
        INSERT INTO source_files
            (path, content_sha256, log_date, byte_size, line_count, timestamped_line_count,
             event_count, ingested_at, parser_version)
        VALUES ('old.log.gz', ?, '2026-01-01', 1, 0, 0, 0, '2026-01-01T00:00:00Z', 1)`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = LoadEvents(db)
	if err == nil || !strings.Contains(err.Error(), "run ingestion") {
		t.Fatalf("LoadEvents error = %v, want re-ingestion instruction", err)
	}
}

func TestSaveFileAndLoadEventsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	file := testRecord("one.log.gz", "a", base)
	file.Events = []logparse.Event{
		{At: base, Type: "server_start", Detail: "1.21.4", Line: 1},
		{At: base.Add(time.Minute), Type: "login", Player: "Alice", Detail: "join", Line: 2},
		{At: base.Add(2 * time.Minute), Type: "chat", Player: "Alice", Detail: "hello", Line: 3},
	}
	file.Identities = []logparse.Identity{{At: base.Add(time.Minute), Player: "Alice", UUID: "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", Line: 4}}

	saved, err := SaveFile(context.Background(), db, file)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Added || saved.Reprocessed || saved.Identities != 1 {
		t.Fatalf("SaveFile result = %#v, want added file with one identity", saved)
	}

	events, identities, horizon, sources, err := LoadEvents(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "server_start" || events[1].Key != "alice" || events[2].Detail != "hello" {
		t.Errorf("loaded events = %#v", events)
	}
	if len(identities) != 1 || identities[0].UUID != strings.ToLower(file.Identities[0].UUID) || identities[0].Key != "alice" {
		t.Errorf("loaded identities = %#v", identities)
	}
	if !horizon.Equal(file.LastAt) || sources != 1 {
		t.Errorf("horizon=%v sources=%d, want %v and 1", horizon, sources, file.LastAt)
	}

	// Re-reading a current archive must not duplicate events or identities, but
	// a newly observed identity line should still be retained.
	saved, err = SaveFile(context.Background(), db, file)
	if err != nil || saved.Added || saved.Reprocessed || saved.Identities != 0 {
		t.Fatalf("duplicate SaveFile result = %#v, err=%v", saved, err)
	}
	file.Identities = append(file.Identities, logparse.Identity{At: base.Add(3 * time.Minute), Player: "Alice", UUID: file.Identities[0].UUID, Line: 5})
	saved, err = SaveFile(context.Background(), db, file)
	if err != nil || saved.Identities != 1 {
		t.Fatalf("SaveFile with a new identity result = %#v, err=%v", saved, err)
	}
	saved, err = SaveFile(context.Background(), db, file)
	if err != nil || saved.Identities != 0 {
		t.Fatalf("repeated identity result = %#v, err=%v", saved, err)
	}
	events, identities, _, _, err = LoadEvents(db)
	if err != nil || len(events) != 3 || len(identities) != 2 {
		t.Fatalf("after idempotent saves events=%d identities=%d err=%v", len(events), len(identities), err)
	}
}

func TestSaveFileReprocessesOldParserVersion(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	file := testRecord("upgrade.log", "b", base)
	file.Events = []logparse.Event{{At: base, Type: "login", Player: "OldName", Line: 1}}
	file.Identities = []logparse.Identity{{At: base, Player: "OldName", UUID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", Line: 2}}
	if _, err := SaveFile(context.Background(), db, file); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE source_files SET parser_version = 1`); err != nil {
		t.Fatal(err)
	}

	file.ByteSize = 20
	file.Lines = 4
	file.TimestampedLines = 3
	file.Events = []logparse.Event{{At: base.Add(time.Minute), Type: "login", Player: "NewName", Line: 3}}
	file.Identities = []logparse.Identity{{At: base.Add(time.Minute), Player: "NewName", UUID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", Line: 4}}
	saved, err := SaveFile(context.Background(), db, file)
	if err != nil || !saved.Reprocessed || saved.Added || saved.Identities != 1 {
		t.Fatalf("reprocess result = %#v, err=%v", saved, err)
	}
	events, identities, _, _, err := LoadEvents(db)
	if err != nil || len(events) != 1 || events[0].Player != "NewName" || len(identities) != 1 || identities[0].Player != "NewName" {
		t.Fatalf("reprocessed data events=%#v identities=%#v err=%v", events, identities, err)
	}
	var lines, version int
	if err := db.QueryRow(`SELECT line_count, parser_version FROM source_files`).Scan(&lines, &version); err != nil {
		t.Fatal(err)
	}
	if lines != 4 || version != logparse.Version {
		t.Errorf("metadata lines=%d version=%d, want 4 and %d", lines, version, logparse.Version)
	}
}

func TestSaveFileRejectsChangedAndNewerArchives(t *testing.T) {
	db := openTestDB(t)
	file := testRecord("fixed.log", "c", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if _, err := SaveFile(context.Background(), db, file); err != nil {
		t.Fatal(err)
	}
	changed := file
	changed.SHA256 = strings.Repeat("d", 64)
	if _, err := SaveFile(context.Background(), db, changed); err == nil || !strings.Contains(err.Error(), "changed in place") {
		t.Fatalf("changed archive error = %v", err)
	}
	if _, err := db.Exec(`UPDATE source_files SET parser_version = ?`, logparse.Version+1); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveFile(context.Background(), db, file); err == nil || !strings.Contains(err.Error(), "newer parser version") {
		t.Fatalf("newer parser error = %v", err)
	}
	// The content-addressed fallback must also reject a database entry parsed
	// by a newer binary when the archive is supplied at a different path.
	file.Path = "renamed.log"
	if _, err := SaveFile(context.Background(), db, file); err == nil || !strings.Contains(err.Error(), "newer parser version") {
		t.Fatalf("renamed newer-parser error = %v", err)
	}
}

func TestSaveFileDeduplicatesSameDayContentAtAnotherPath(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first := testRecord("first.log", "e", base)
	first.Identities = []logparse.Identity{{At: base, Player: "Alice", UUID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", Line: 1}}
	if saved, err := SaveFile(context.Background(), db, first); err != nil || !saved.Added {
		t.Fatalf("first save result=%#v err=%v", saved, err)
	}
	second := first
	second.Path = "second.log"
	second.Identities = []logparse.Identity{{At: base, Player: "Alicia", UUID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", Line: 2}}
	saved, err := SaveFile(context.Background(), db, second)
	if err != nil || saved.Added || saved.Identities != 1 {
		t.Fatalf("same content save result=%#v err=%v", saved, err)
	}
	var sources, identities int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM identity_observations`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if sources != 1 || identities != 2 {
		t.Errorf("sources=%d identities=%d, want 1 and 2", sources, identities)
	}
}

func TestSaveFileRollsBackInvalidRecords(t *testing.T) {
	db := openTestDB(t)
	file := testRecord("invalid.log", "f", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	file.Events = []logparse.Event{{At: file.FirstAt, Type: "not-an-event", Player: "Alice", Line: 1}}
	if _, err := SaveFile(context.Background(), db, file); err == nil {
		t.Fatal("SaveFile accepted an event type excluded by the schema")
	}
	var sources int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 0 {
		t.Fatalf("failed save left %d source marker(s); want transaction rollback", sources)
	}
}

func TestStoreErrorBranches(t *testing.T) {
	if got := nullableTime(time.Time{}); got != nil {
		t.Errorf("nullableTime(zero) = %#v, want nil", got)
	}
	if got := nullableTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got == nil {
		t.Error("nullableTime(nonzero) = nil")
	}
	if count, err := insertIdentities(context.Background(), testSQLExecutor{}, 1, nil); err != nil || count != 0 {
		t.Errorf("insertIdentities(empty) = %d, %v", count, err)
	}
	if _, err := insertIdentities(context.Background(), testSQLExecutor{execErr: os.ErrPermission}, 1, []logparse.Identity{{Line: 7}}); err == nil {
		t.Error("insertIdentities ignored executor failure")
	}
	if _, err := insertIdentities(context.Background(), testSQLExecutor{result: testSQLResult{rowsErr: os.ErrPermission}}, 1, []logparse.Identity{{Line: 8}}); err == nil {
		t.Error("insertIdentities ignored RowsAffected failure")
	}

	db := openTestDB(t)
	file := testRecord("bad-identity.log", "2", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	file.Identities = []logparse.Identity{{At: file.FirstAt, Player: "Alice", UUID: "short", Line: 1}}
	if _, err := SaveFile(context.Background(), db, file); err == nil {
		t.Fatal("SaveFile accepted an invalid UUID")
	}
	var sources int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 0 {
		t.Fatalf("identity failure left %d source rows; want transaction rollback", sources)
	}
	file.Identities = nil
	if _, err := SaveFile(context.Background(), db, file); err != nil {
		t.Fatal(err)
	}
	file.Identities = []logparse.Identity{{At: file.FirstAt, Player: "Alice", UUID: "short", Line: 2}}
	if _, err := SaveFile(context.Background(), db, file); err == nil {
		t.Fatal("existing archive identity update accepted an invalid UUID")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveFile(context.Background(), db, file); err == nil {
		t.Fatal("SaveFile on a closed database succeeded")
	}
}

func TestSaveFileReprocessesContentMatchedByDateAndRollsBackBadUpgrade(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := testRecord("old-name.log", "3", base)
	original.Events = []logparse.Event{{At: base, Type: "login", Player: "Alice", Line: 1}}
	if _, err := SaveFile(context.Background(), db, original); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE source_files SET parser_version = 1`); err != nil {
		t.Fatal(err)
	}
	renamed := original
	renamed.Path = "new-name.log"
	renamed.Events = []logparse.Event{{At: base.Add(time.Minute), Type: "login", Player: "Alicia", Line: 2}}
	saved, err := SaveFile(context.Background(), db, renamed)
	if err != nil || !saved.Reprocessed {
		t.Fatalf("content-addressed reprocess result=%#v err=%v", saved, err)
	}
	if _, err := db.Exec(`UPDATE source_files SET parser_version = 1`); err != nil {
		t.Fatal(err)
	}
	badUpgrade := renamed
	badUpgrade.Events = []logparse.Event{{At: base, Type: "invalid", Player: "Alice", Line: 3}}
	if _, err := SaveFile(context.Background(), db, badUpgrade); err == nil {
		t.Fatal("reprocessing accepted an invalid event")
	}
	events, _, _, _, err := LoadEvents(db)
	if err == nil || !strings.Contains(err.Error(), "older parser version") {
		t.Fatalf("failed upgrade did not retain stale marker: %v", err)
	}
	var eventType, player string
	if err := db.QueryRow(`SELECT event_type, player_name FROM events`).Scan(&eventType, &player); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || eventType != "login" || player != "Alicia" {
		t.Fatalf("failed upgrade changed saved events: %#v %s/%s", events, eventType, player)
	}
}

func TestOpenFailuresAndLegacyMigration(t *testing.T) {
	missingParent := filepath.Join(t.TempDir(), "missing", "db.sqlite")
	if db, err := Open(missingParent); err == nil || db != nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("Open missing-parent database = db %v, err %v", db, err)
	}
	if db, err := OpenReadOnly(filepath.Join(t.TempDir(), "missing.sqlite")); err == nil || db != nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("OpenReadOnly missing database = db %v, err %v", db, err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.sqlite")
	if err := os.WriteFile(corrupt, []byte("not a SQLite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(corrupt); err == nil || db != nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("Open corrupt database = db %v, err %v", db, err)
	}

	legacy, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(`CREATE TABLE source_files (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(legacy); err != nil {
		t.Fatal(err)
	}
	var columnCount, userVersion int
	if err := legacy.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('source_files') WHERE name = 'parser_version'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if err := legacy.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 || userVersion != 3 {
		t.Errorf("migration parser_version columns=%d user_version=%d, want 1 and 3", columnCount, userVersion)
	}
}

type testSQLExecutor struct {
	execErr error
	result  sql.Result
}

func (e testSQLExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return e.result, e.execErr
}

type testSQLResult struct{ rowsErr error }

func (testSQLResult) LastInsertId() (int64, error)   { return 0, nil }
func (r testSQLResult) RowsAffected() (int64, error) { return 1, r.rowsErr }

func TestLoadEventsRejectsInvalidStoredTimestamp(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	file := testRecord("bad-time.log", "1", base)
	file.Events = []logparse.Event{{At: base, Type: "server_start", Line: 1}}
	if _, err := SaveFile(context.Background(), db, file); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET occurred_at = 'yesterday'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := LoadEvents(db); err == nil || !strings.Contains(err.Error(), "parse stored timestamp") {
		t.Fatalf("LoadEvents invalid timestamp error = %v", err)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testRecord(path, hash string, at time.Time) FileRecord {
	return FileRecord{
		Path: path, SHA256: strings.Repeat(hash, 64), LogDate: "2026-01-02",
		ByteSize: 10, Lines: 10, TimestampedLines: 2,
		FirstAt: at, LastAt: at.Add(time.Hour),
	}
}
