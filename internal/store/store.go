package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"minecraft-log-analyzer/internal/logparse"
)

const schema = `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS source_files (
    id INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    content_sha256 TEXT NOT NULL CHECK(length(content_sha256) = 64),
    log_date TEXT NOT NULL CHECK(length(log_date) = 10),
    byte_size INTEGER NOT NULL CHECK(byte_size >= 0),
    line_count INTEGER NOT NULL CHECK(line_count >= 0),
    timestamped_line_count INTEGER NOT NULL CHECK(timestamped_line_count >= 0 AND timestamped_line_count <= line_count),
    event_count INTEGER NOT NULL CHECK(event_count >= 0 AND event_count <= line_count),
    first_at TEXT,
    last_at TEXT,
    ingested_at TEXT NOT NULL,
    parser_version INTEGER NOT NULL DEFAULT 1 CHECK(parser_version > 0),
    UNIQUE(log_date, content_sha256)
) STRICT;
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY,
    source_file_id INTEGER NOT NULL REFERENCES source_files(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL CHECK(line_number > 0),
    occurred_at TEXT NOT NULL CHECK(length(occurred_at) > 0),
    event_type TEXT NOT NULL CHECK(event_type IN
        ('login','logout','chat','command','advancement','death','geyser_connect','server_start','server_stop','server_crash')),
    player_name TEXT,
    player_key TEXT,
    detail TEXT NOT NULL DEFAULT '',
    CHECK (
        (event_type IN ('server_start','server_stop','server_crash') AND player_name IS NULL AND player_key IS NULL)
        OR
        (event_type NOT IN ('server_start','server_stop','server_crash') AND player_name IS NOT NULL AND player_key IS NOT NULL)
    ),
    UNIQUE(source_file_id, line_number, event_type)
) STRICT;
CREATE TABLE IF NOT EXISTS identity_observations (
    id INTEGER PRIMARY KEY,
    source_file_id INTEGER NOT NULL REFERENCES source_files(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL CHECK(line_number > 0),
    occurred_at TEXT NOT NULL CHECK(length(occurred_at) > 0),
    player_uuid TEXT NOT NULL CHECK(length(player_uuid) = 36),
    player_name TEXT NOT NULL CHECK(length(player_name) > 0),
    player_key TEXT NOT NULL CHECK(length(player_key) > 0),
    UNIQUE(source_file_id, line_number, player_uuid, player_key)
) STRICT;
CREATE INDEX IF NOT EXISTS events_time_idx ON events(occurred_at, id);
CREATE INDEX IF NOT EXISTS events_player_idx ON events(player_key, occurred_at);
CREATE INDEX IF NOT EXISTS events_type_idx ON events(event_type, occurred_at);
CREATE INDEX IF NOT EXISTS identity_uuid_idx ON identity_observations(player_uuid, occurred_at);
CREATE INDEX IF NOT EXISTS identity_name_idx ON identity_observations(player_key, occurred_at);
CREATE VIEW IF NOT EXISTS player_event_totals AS
SELECT player_key,
       MAX(player_name) AS player_name,
       SUM(event_type = 'login') AS logins,
       SUM(event_type = 'chat') AS chat_messages,
       SUM(event_type = 'command') AS commands,
       SUM(event_type = 'death') AS deaths,
       SUM(event_type = 'advancement') AS advancements,
       SUM(event_type = 'geyser_connect') AS geyser_connections,
       MIN(CASE WHEN event_type = 'login' THEN occurred_at END) AS first_login,
       MAX(CASE WHEN event_type = 'login' THEN occurred_at END) AS last_login
FROM events
WHERE player_key IS NOT NULL
GROUP BY player_key;
CREATE VIEW IF NOT EXISTS chat_messages AS
SELECT e.id,
       e.occurred_at,
       e.player_name,
       e.player_key,
       e.detail AS message,
       s.path AS source_path,
       e.line_number
FROM events e
JOIN source_files s ON s.id = e.source_file_id
WHERE e.event_type = 'chat';
CREATE VIEW IF NOT EXISTS player_aliases AS
SELECT player_uuid,
       player_key,
       MAX(player_name) AS player_name,
       MIN(occurred_at) AS first_seen_at,
       MAX(occurred_at) AS last_seen_at,
       COUNT(*) AS observations
FROM identity_observations
GROUP BY player_uuid, player_key;
`

func Open(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() + "?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return db, nil
}

// OpenReadOnly opens an existing database without schema initialization,
// migrations, or journal-mode changes. It is intended for reproducible report
// builds where the input database must remain byte-for-byte unchanged.
func OpenReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() + "?mode=ro&_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(source_files)`)
	if err != nil {
		return err
	}
	hasParserVersion := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "parser_version" {
			hasParserVersion = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasParserVersion {
		if _, err := db.Exec(`ALTER TABLE source_files ADD COLUMN parser_version INTEGER NOT NULL DEFAULT 1 CHECK(parser_version > 0)`); err != nil {
			return err
		}
	}
	_, err = db.Exec(`PRAGMA user_version = 3`)
	return err
}

type FileRecord struct {
	Path             string
	SHA256           string
	LogDate          string
	ByteSize         int64
	Lines            int
	TimestampedLines int
	FirstAt          time.Time
	LastAt           time.Time
	Events           []logparse.Event
	Identities       []logparse.Identity
}

type SaveResult struct {
	Added       bool
	Reprocessed bool
	Identities  int
}

// SaveFile commits the source-file marker and all of its parsed data together.
// Known content is rebuilt atomically after a parser version change.
func SaveFile(ctx context.Context, db *sql.DB, file FileRecord) (SaveResult, error) {
	var existingID int64
	var existingSHA string
	var existingVersion int
	err := db.QueryRowContext(ctx, `SELECT id, content_sha256, parser_version FROM source_files WHERE path = ?`, file.Path).Scan(&existingID, &existingSHA, &existingVersion)
	if err == nil {
		if existingSHA == file.SHA256 {
			if existingVersion < logparse.Version {
				count, err := reprocessFile(ctx, db, existingID, file)
				return SaveResult{Reprocessed: err == nil, Identities: count}, err
			}
			if existingVersion > logparse.Version {
				return SaveResult{}, fmt.Errorf("archive %s was parsed by newer parser version %d", file.Path, existingVersion)
			}
			count, err := saveIdentities(ctx, db, existingID, file.Identities)
			return SaveResult{Identities: count}, err
		}
		return SaveResult{}, fmt.Errorf("previously ingested archive changed in place: %s", file.Path)
	}
	if err != sql.ErrNoRows {
		return SaveResult{}, err
	}
	err = db.QueryRowContext(ctx, `SELECT id, content_sha256, parser_version FROM source_files WHERE log_date = ? AND content_sha256 = ?`, file.LogDate, file.SHA256).Scan(&existingID, &existingSHA, &existingVersion)
	if err == nil {
		if existingVersion < logparse.Version {
			count, err := reprocessFile(ctx, db, existingID, file)
			return SaveResult{Reprocessed: err == nil, Identities: count}, err
		}
		if existingVersion > logparse.Version {
			return SaveResult{}, fmt.Errorf("matching archive was parsed by newer parser version %d", existingVersion)
		}
		count, err := saveIdentities(ctx, db, existingID, file.Identities)
		return SaveResult{Identities: count}, err
	}
	if err != sql.ErrNoRows {
		return SaveResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SaveResult{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
        INSERT INTO source_files
            (path, content_sha256, log_date, byte_size, line_count, timestamped_line_count,
             event_count, first_at, last_at, ingested_at, parser_version)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		file.Path, file.SHA256, file.LogDate, file.ByteSize, file.Lines, file.TimestampedLines,
		len(file.Events), nullableTime(file.FirstAt), nullableTime(file.LastAt), time.Now().UTC().Format(time.RFC3339), logparse.Version)
	if err != nil {
		return SaveResult{}, err
	}
	fileID, err := result.LastInsertId()
	if err != nil {
		return SaveResult{}, err
	}
	if err := insertEvents(ctx, tx, fileID, file.Events); err != nil {
		return SaveResult{}, err
	}
	identityCount, err := insertIdentities(ctx, tx, fileID, file.Identities)
	if err != nil {
		return SaveResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{Added: true, Identities: identityCount}, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.Format(time.RFC3339)
}

func insertEvents(ctx context.Context, tx *sql.Tx, fileID int64, events []logparse.Event) error {
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO events
            (source_file_id, line_number, occurred_at, event_type, player_name, player_key, detail)
        VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(lower(?), ''), ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, event := range events {
		if _, err := stmt.ExecContext(ctx, fileID, event.Line, event.At.Format(time.RFC3339),
			event.Type, event.Player, event.Player, event.Detail); err != nil {
			return fmt.Errorf("insert event on line %d: %w", event.Line, err)
		}
	}
	return nil
}

func reprocessFile(ctx context.Context, db *sql.DB, fileID int64, file FileRecord) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE source_file_id = ?`, fileID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM identity_observations WHERE source_file_id = ?`, fileID); err != nil {
		return 0, err
	}
	if err := insertEvents(ctx, tx, fileID, file.Events); err != nil {
		return 0, err
	}
	identityCount, err := insertIdentities(ctx, tx, fileID, file.Identities)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE source_files SET byte_size = ?, line_count = ?, timestamped_line_count = ?,
            event_count = ?, first_at = ?, last_at = ?, parser_version = ?
        WHERE id = ?`, file.ByteSize, file.Lines, file.TimestampedLines, len(file.Events),
		nullableTime(file.FirstAt), nullableTime(file.LastAt), logparse.Version, fileID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return identityCount, nil
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertIdentities(ctx context.Context, executor sqlExecutor, fileID int64, identities []logparse.Identity) (int, error) {
	inserted := 0
	for _, identity := range identities {
		result, err := executor.ExecContext(ctx, `
			INSERT INTO identity_observations
			    (source_file_id, line_number, occurred_at, player_uuid, player_name, player_key)
			VALUES (?, ?, ?, lower(?), ?, lower(?))
			ON CONFLICT(source_file_id, line_number, player_uuid, player_key) DO NOTHING`,
			fileID, identity.Line, identity.At.Format(time.RFC3339), identity.UUID, identity.Player, identity.Player)
		if err != nil {
			return inserted, fmt.Errorf("insert identity on line %d: %w", identity.Line, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return inserted, err
		}
		inserted += int(rows)
	}
	return inserted, nil
}

func saveIdentities(ctx context.Context, db *sql.DB, fileID int64, identities []logparse.Identity) (int, error) {
	if len(identities) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count, err := insertIdentities(ctx, tx, fileID, identities)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

type Event struct {
	At     time.Time
	Type   string
	Player string
	Key    string
	Detail string
}

type Identity struct {
	At     time.Time
	UUID   string
	Player string
	Key    string
}

func LoadEvents(db *sql.DB) ([]Event, []Identity, time.Time, int, error) {
	var staleSources int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files WHERE parser_version <> ?`, logparse.Version).Scan(&staleSources); err != nil {
		return nil, nil, time.Time{}, 0, err
	}
	if staleSources > 0 {
		return nil, nil, time.Time{}, 0, fmt.Errorf("%d archives use an older parser version; run ingestion before generating the report", staleSources)
	}
	rows, err := db.Query(`
        SELECT occurred_at, event_type, COALESCE(player_name, ''), COALESCE(player_key, ''), detail
        FROM events ORDER BY occurred_at, id`)
	if err != nil {
		return nil, nil, time.Time{}, 0, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var occurred string
		if err := rows.Scan(&occurred, &event.Type, &event.Player, &event.Key, &event.Detail); err != nil {
			return nil, nil, time.Time{}, 0, err
		}
		event.At, err = time.Parse(time.RFC3339, occurred)
		if err != nil {
			return nil, nil, time.Time{}, 0, fmt.Errorf("parse stored timestamp %q: %w", occurred, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, time.Time{}, 0, err
	}
	identityRows, err := db.Query(`
        SELECT occurred_at, player_uuid, player_name, player_key
        FROM identity_observations ORDER BY occurred_at, id`)
	if err != nil {
		return nil, nil, time.Time{}, 0, err
	}
	defer identityRows.Close()
	var identities []Identity
	for identityRows.Next() {
		var identity Identity
		var occurred string
		if err := identityRows.Scan(&occurred, &identity.UUID, &identity.Player, &identity.Key); err != nil {
			return nil, nil, time.Time{}, 0, err
		}
		identity.At, err = time.Parse(time.RFC3339, occurred)
		if err != nil {
			return nil, nil, time.Time{}, 0, err
		}
		identities = append(identities, identity)
	}
	if err := identityRows.Err(); err != nil {
		return nil, nil, time.Time{}, 0, err
	}

	var horizonText sql.NullString
	var sourceCount int
	if err := db.QueryRow(`SELECT MAX(last_at), COUNT(*) FROM source_files`).Scan(&horizonText, &sourceCount); err != nil {
		return nil, nil, time.Time{}, 0, err
	}
	var horizon time.Time
	if horizonText.Valid {
		horizon, err = time.Parse(time.RFC3339, horizonText.String)
		if err != nil {
			return nil, nil, time.Time{}, 0, err
		}
	}
	return events, identities, horizon, sourceCount, nil
}
