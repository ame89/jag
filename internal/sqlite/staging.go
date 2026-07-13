// Package sqlite implements the core storage interfaces for SQLite (see
// Impl.md, Ports & Adapters). Pure persistence only — no domain/business
// logic lives here.
package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite"

	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// stagingSchema creates the Phase 1 staging tables if they don't exist yet.
// See internal/core/staging for the interface this backs.
const stagingSchema = `
CREATE TABLE IF NOT EXISTS staging_records (
    version       INTEGER NOT NULL,
    id            TEXT NOT NULL,
    profile       TEXT NOT NULL,
    class         TEXT NOT NULL,
    attribute     TEXT NOT NULL,
    value         TEXT NOT NULL,
    is_reference  INTEGER NOT NULL,
    seq           INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staging_records_by_id
    ON staging_records (version, id);
CREATE INDEX IF NOT EXISTS idx_staging_records_by_class
    ON staging_records (version, class, id);
CREATE INDEX IF NOT EXISTS idx_staging_records_by_value
    ON staging_records (version, value, is_reference);

CREATE TABLE IF NOT EXISTS staging_errors (
    version       INTEGER NOT NULL,
    source_file   TEXT NOT NULL,
    line          INTEGER NOT NULL,
    byte_offset   INTEGER NOT NULL,
    message       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staging_errors_by_version
    ON staging_errors (version);

-- Single-row counter backing NextVersion(); kept separate from MAX(version)
-- over staging_records so a version number is never reused even after its
-- rows have been deleted by DeleteVersion (staging_records for an old,
-- already-cleaned-up version could otherwise collide with a later run).
CREATE TABLE IF NOT EXISTS staging_version_counter (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    last_version INTEGER NOT NULL
);
INSERT OR IGNORE INTO staging_version_counter (id, last_version) VALUES (1, 0);
`

// insertChunkSize bounds how many rows go into a single multi-row INSERT
// statement. Kept well below SQLite's default parameter limit
// (SQLITE_MAX_VARIABLE_NUMBER, typically several thousand) — 8 params/row *
// 200 rows = 1600 params per statement.
const insertChunkSize = 200

// StagingStore implements staging.Store on top of a SQLite database.
type StagingStore struct {
	db *sql.DB
}

// Open opens (creating if necessary) a SQLite database at path and ensures
// the staging schema exists. Use ":memory:" for an in-memory database
// (mainly for tests).
func Open(path string) (*StagingStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: opening %s: %w", path, err)
	}
	// Phase 1 writes are single-writer, bulk/batched — one open
	// connection avoids SQLite's concurrent-writer locking entirely.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(stagingSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: creating staging schema: %w", err)
	}
	// Catalog schema is created here too (same physical database/connection
	// pool, see (*StagingStore).Catalog) so a freshly opened database is
	// always ready to be seeded via cmd/catalogimport, regardless of
	// whether the caller ever touches staging at all.
	if _, err := db.Exec(catalogSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: creating catalog schema: %w", err)
	}

	return &StagingStore{db: db}, nil
}

// Close closes the underlying database handle.
func (s *StagingStore) Close() error {
	return s.db.Close()
}

// NextVersion atomically increments and returns the staging version
// counter.
func (s *StagingStore) NextVersion() (uint64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	if _, err := tx.Exec(`UPDATE staging_version_counter SET last_version = last_version + 1 WHERE id = 1`); err != nil {
		return 0, fmt.Errorf("sqlite: incrementing version counter: %w", err)
	}

	var version uint64
	if err := tx.QueryRow(`SELECT last_version FROM staging_version_counter WHERE id = 1`).Scan(&version); err != nil {
		return 0, fmt.Errorf("sqlite: reading version counter: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlite: committing version allocation: %w", err)
	}
	return version, nil
}

// InsertBatch bulk-inserts the given staging records in a single
// transaction, using multi-row INSERT statements (chunked at
// insertChunkSize) instead of one Exec per row.
func (s *StagingStore) InsertBatch(records []model.StagingRecord) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	for start := 0; start < len(records); start += insertChunkSize {
		end := min(start+insertChunkSize, len(records))
		if err := insertRecordChunk(tx, records[start:end]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: committing batch: %w", err)
	}
	return nil
}

func insertRecordChunk(tx *sql.Tx, chunk []model.StagingRecord) error {
	var sb strings.Builder
	sb.WriteString("INSERT INTO staging_records (version, id, profile, class, attribute, value, is_reference, seq) VALUES ")

	args := make([]any, 0, len(chunk)*8)
	for i, r := range chunk {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		sb.WriteString(placeholders(8))
		sb.WriteString(")")
		args = append(args, r.Version, r.ID, r.Profile, r.Class, r.Attribute, r.Value, r.IsReference, r.Seq)
	}

	if _, err := tx.Exec(sb.String(), args...); err != nil {
		return fmt.Errorf("sqlite: inserting staging record chunk (%d rows): %w", len(chunk), err)
	}
	return nil
}

// InsertErrors bulk-inserts Phase 1 parse errors in a single transaction.
// Error counts are expected to be small (one per failed file at most), so
// this stays a simple per-row insert rather than chunked multi-row.
func (s *StagingStore) InsertErrors(errs []model.StagingError) error {
	if len(errs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	stmt, err := tx.Prepare(`
		INSERT INTO staging_errors (version, source_file, line, byte_offset, message)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing error insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range errs {
		if _, err := stmt.Exec(e.Version, e.SourceFile, e.Line, e.ByteOffset, e.Message); err != nil {
			return fmt.Errorf("sqlite: inserting staging error: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: committing error batch: %w", err)
	}
	return nil
}

// GetByID returns all staging records for one object ID within an import
// version, across all profiles, ordered by profile/attribute/seq.
func (s *StagingStore) GetByID(version uint64, id string) ([]model.StagingRecord, error) {
	rows, err := s.db.Query(`
		SELECT version, id, profile, class, attribute, value, is_reference, seq
		FROM staging_records
		WHERE version = ? AND id = ?
		ORDER BY profile, attribute, seq
	`, version, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying by id: %w", err)
	}
	defer rows.Close()

	var records []model.StagingRecord
	for rows.Next() {
		var r model.StagingRecord
		if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
			return nil, fmt.Errorf("sqlite: scanning row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating rows: %w", err)
	}
	return records, nil
}

// ListClasses returns the distinct classes present in the given import
// version (see staging.Store.ListClasses).
func (s *StagingStore) ListClasses(version uint64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT class FROM staging_records WHERE version = ? ORDER BY class`,
		version,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: listing classes: %w", err)
	}
	defer rows.Close()

	var classes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("sqlite: scanning class: %w", err)
		}
		classes = append(classes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating classes: %w", err)
	}
	return classes, nil
}

// GetByClass returns a cursor-paginated chunk of staging records for
// objects of the given class (see staging.Store.GetByClass). It first
// selects up to limit distinct object IDs greater than afterID, then fetches
// all rows for exactly those IDs — so a batch never splits an object's
// attribute rows across two calls.
func (s *StagingStore) GetByClass(version uint64, class string, afterID string, limit int) ([]model.StagingRecord, error) {
	idRows, err := s.db.Query(`
		SELECT DISTINCT id FROM staging_records
		WHERE version = ? AND class = ? AND id > ?
		ORDER BY id
		LIMIT ?
	`, version, class, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: selecting class id page: %w", err)
	}
	var ids []string
	for idRows.Next() {
		var id string
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return nil, fmt.Errorf("sqlite: scanning id: %w", err)
		}
		ids = append(ids, id)
	}
	idErr := idRows.Err()
	idRows.Close()
	if idErr != nil {
		return nil, fmt.Errorf("sqlite: iterating id page: %w", idErr)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(ids)+2)
	args = append(args, version, class)
	for _, id := range ids {
		args = append(args, id)
	}
	query := fmt.Sprintf(`
		SELECT version, id, profile, class, attribute, value, is_reference, seq
		FROM staging_records
		WHERE version = ? AND class = ? AND id IN (%s)
		ORDER BY id, profile, attribute, seq
	`, placeholders(len(ids)))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: fetching class page rows: %w", err)
	}
	defer rows.Close()

	var records []model.StagingRecord
	for rows.Next() {
		var r model.StagingRecord
		if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
			return nil, fmt.Errorf("sqlite: scanning row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating class page rows: %w", err)
	}
	return records, nil
}

// GetReferencesTo returns all staging records whose value references
// targetID (see staging.Store.GetReferencesTo), using the index on
// (version, value, is_reference) rather than a full scan.
func (s *StagingStore) GetReferencesTo(version uint64, targetID string) ([]model.StagingRecord, error) {
	rows, err := s.db.Query(`
		SELECT version, id, profile, class, attribute, value, is_reference, seq
		FROM staging_records
		WHERE version = ? AND value = ? AND is_reference = 1
		ORDER BY id, profile, attribute, seq
	`, version, targetID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying references to %s: %w", targetID, err)
	}
	defer rows.Close()

	var records []model.StagingRecord
	for rows.Next() {
		var r model.StagingRecord
		if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
			return nil, fmt.Errorf("sqlite: scanning row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating rows: %w", err)
	}
	return records, nil
}

// CountByVersion returns the number of staging records for the given
// import version.
func (s *StagingStore) CountByVersion(version uint64) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM staging_records WHERE version = ?`,
		version,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: counting version: %w", err)
	}
	return count, nil
}

// CountErrorsByVersion returns the number of staging errors for the given
// import version.
func (s *StagingStore) CountErrorsByVersion(version uint64) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM staging_errors WHERE version = ?`,
		version,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: counting version errors: %w", err)
	}
	return count, nil
}

// DeleteVersion removes all staging records and errors for the given
// import version (see staging.Store.DeleteVersion).
func (s *StagingStore) DeleteVersion(version uint64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	if _, err := tx.Exec(`DELETE FROM staging_records WHERE version = ?`, version); err != nil {
		return fmt.Errorf("sqlite: deleting staging records for version %d: %w", version, err)
	}
	if _, err := tx.Exec(`DELETE FROM staging_errors WHERE version = ?`, version); err != nil {
		return fmt.Errorf("sqlite: deleting staging errors for version %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: committing version deletion: %w", err)
	}
	return nil
}

// placeholders builds "?, ?, ..." for n placeholders.
func placeholders(n int) string {
	ph := make([]string, n)
	for i := range ph {
		ph[i] = "?"
	}
	return strings.Join(ph, ", ")
}
