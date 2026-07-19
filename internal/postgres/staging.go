// Package postgres implements the core storage interfaces for PostgreSQL
// (see Impl.md, Ports & Adapters), as the second backend alongside
// internal/sqlite — pure persistence only, no domain/business logic lives
// here.
//
// This package deliberately mirrors internal/sqlite's structure,
// query-by-query: same schema (table/column names, same semantics), same
// public API (StagingStore/ModelStore/CatalogStore/FlagStore, same method
// names), same chunking/pagination/index rationale — only the SQL dialect
// differs. See rebind.go's doc comment for how the "?" placeholder style
// shared with internal/sqlite is translated to PostgreSQL's "$N" style,
// and each file's own doc comment for the handful of genuine dialect
// differences (INSERT OR IGNORE -> ON CONFLICT DO NOTHING, REAL -> DOUBLE
// PRECISION, is_reference as a real BOOLEAN column instead of INTEGER
// 0/1). Keeping the two backends structurally identical is deliberate:
// internal/impl/common and internal/exporter/hjson depend only on the
// core/* interfaces, so either backend should be swappable without any
// caller-visible behavior difference.
package postgres

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// stagingSchema creates the Phase 1 staging tables if they don't exist
// yet. See internal/core/staging for the interface this backs, and
// internal/sqlite/staging.go for the SQLite original this mirrors —
// identical except INSERT OR IGNORE -> ON CONFLICT ... DO NOTHING
// (PostgreSQL has no "OR IGNORE" clause) and is_reference is a real
// BOOLEAN column (see this package's doc comment).
const stagingSchema = `
CREATE TABLE IF NOT EXISTS staging_records (
    version       BIGINT NOT NULL,
    id            TEXT NOT NULL,
    profile       TEXT NOT NULL,
    class         TEXT NOT NULL,
    attribute     TEXT NOT NULL,
    value         TEXT NOT NULL,
    is_reference  BOOLEAN NOT NULL,
    seq           INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS staging_errors (
    version       BIGINT NOT NULL,
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
    last_version BIGINT NOT NULL
);
INSERT INTO staging_version_counter (id, last_version) VALUES (1, 0)
    ON CONFLICT (id) DO NOTHING;
`

// insertChunkSize bounds how many rows go into a single multi-row INSERT
// statement, used by every batched Upsert*/InsertBatch method in this
// package (staging records here, plus model_* upserts in model.go).
// Deliberately larger than internal/sqlite's equivalent constant: for
// PostgreSQL every statement is a real network round trip (unlike
// SQLite's in-process access), so the dominant cost is round-trip count,
// not per-statement row count — a real load-test comparison
// (lasttest-200-10-10_10s.xml) showed single-row Exec-per-row upserts
// running ~100x slower than SQLite for the same data purely due to this.
// 1000 rows/statement stays far under PostgreSQL's 65535-parameter-per-
// statement limit even for the widest tables here (model_attribute has 4
// columns -> 4000 params/chunk).
const insertChunkSize = 1000

// StagingStore implements staging.Store on top of a PostgreSQL database.
//
// No writeMu (removed): PostgreSQL supports genuine concurrent writers
// (MVCC + row-level locking). A real lasttest-200-10-10 load-test
// measurement showed the mutex serializing every parallel Pass A/B
// worker's writes onto one connection was an actual bottleneck for this
// backend, so it was removed. The delete-then-insert re-upsert pattern
// (UpsertAttributes, UpsertElectricalGroups, UpsertEdges' endpoint
// maintenance) relies on Pass A/B callers (internal/impl/common) never
// concurrently touching the same owner/key across two workers — an
// invariant already relied upon elsewhere and unaffected by removing this
// lock.
type StagingStore struct {
	db *sql.DB
}


// Open opens a PostgreSQL database via dsn (e.g.
// "postgres://user:pass@host:5432/dbname?sslmode=disable") and ensures the
// staging/catalog/model schemas exist, mirroring internal/sqlite.Open's
// behavior (minus the ":memory:"/WAL/busy_timeout special-casing, which is
// SQLite-specific and has no PostgreSQL equivalent — a real PostgreSQL
// server already handles concurrent connections/durability on its own).
func Open(dsn string) (*StagingStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: opening %s: %w", dsn, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: connecting to %s: %w", dsn, err)
	}

	if err := execSchema(db, stagingSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: creating staging schema: %w", err)
	}
	// Catalog schema is created here too (same physical database/connection
	// pool, see (*StagingStore).Catalog) so a freshly opened database is
	// always ready to be seeded via cmd/catalogimport, regardless of
	// whether the caller ever touches staging at all.
	if err := execSchema(db, catalogSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: creating catalog schema: %w", err)
	}
	// Final-model schema (Node/Edge/Container/Geometry/Attribute/electrical
	// group, see model.go) — created here too so a freshly opened database
	// is always ready for ModelStore, regardless of whether the caller
	// ever touches it.
	if err := execSchema(db, modelSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: creating model schema: %w", err)
	}

	return &StagingStore{db: db}, nil
}

// Close closes the underlying database handle.
func (s *StagingStore) Close() error {
	return s.db.Close()
}

// execSchema runs a multi-statement DDL string one ";"-separated statement
// at a time. Deliberately not a single db.Exec(schema) call: unlike
// modernc.org/sqlite (which happily executes a whole ";"-separated script
// in one Exec), PostgreSQL's extended query protocol (which pgx/
// database/sql use by default) only ever sends one statement per Exec —
// splitting here keeps every schema string in this package readable as an
// ordinary multi-statement SQL script (matching internal/sqlite's schema
// constants line for line) instead of forcing one db.Exec call per
// CREATE TABLE/INDEX.
func execSchema(db *sql.DB, schema string) error {
	for _, stmt := range strings.Split(stripSQLLineComments(schema), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("executing %q: %w", stmt, err)
		}
	}
	return nil
}

// stripSQLLineComments removes every "-- ..." line comment from a
// multi-statement DDL string before it gets split on ";". This is
// necessary because several of this package's schema doc comments contain
// a literal ";" inside their prose (e.g. "backing NextVersion(); kept
// separate from ...") — a naive strings.Split(schema, ";") would cut such
// a comment in half and glue the remainder onto the next CREATE statement,
// producing a syntax error at the leftover comment text. SQLite's
// db.Exec(schema) never had this problem since it executes a whole script
// in one call; PostgreSQL/pgx requires one statement per Exec (see this
// function's caller), which is what makes comment-aware splitting
// necessary here.
func stripSQLLineComments(schema string) string {
	lines := strings.Split(schema, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// stagingIndexes creates the secondary indexes staging_records reads rely
// on. See internal/sqlite/staging.go's identical constant for the
// rationale (building these once after Phase 1's bulk insert, rather than
// maintaining them incrementally on every insert, was measured to matter a
// lot for SQLite; kept identical here for behavioral parity even though
// PostgreSQL's own bulk-insert-then-index performance characteristics
// weren't separately re-measured).
const stagingIndexes = `
CREATE INDEX IF NOT EXISTS idx_staging_records_by_id
    ON staging_records (version, id);
CREATE INDEX IF NOT EXISTS idx_staging_records_by_class
    ON staging_records (version, class, id);
CREATE INDEX IF NOT EXISTS idx_staging_records_by_value
    ON staging_records (version, value, is_reference);
`

// EnsureIndexes (re-)creates the staging_records secondary indexes. See
// staging.Store.EnsureIndexes for when callers must invoke this.
func (s *StagingStore) EnsureIndexes() error {
	if err := execSchema(s.db, stagingIndexes); err != nil {
		return fmt.Errorf("postgres: creating staging indexes: %w", err)
	}
	return nil
}

// NextVersion atomically increments and returns the staging version
// counter.
func (s *StagingStore) NextVersion() (uint64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	if _, err := tx.Exec(rebind(`UPDATE staging_version_counter SET last_version = last_version + 1 WHERE id = 1`)); err != nil {
		return 0, fmt.Errorf("postgres: incrementing version counter: %w", err)
	}

	var version uint64
	if err := tx.QueryRow(rebind(`SELECT last_version FROM staging_version_counter WHERE id = 1`)).Scan(&version); err != nil {
		return 0, fmt.Errorf("postgres: reading version counter: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("postgres: committing version allocation: %w", err)
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
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	for start := 0; start < len(records); start += insertChunkSize {
		end := min(start+insertChunkSize, len(records))
		if err := insertRecordChunk(tx, records[start:end]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing batch: %w", err)
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

	if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
		return fmt.Errorf("postgres: inserting staging record chunk (%d rows): %w", len(chunk), err)
	}
	return nil
}

// InsertErrors bulk-inserts Phase 1 parse errors inside a single
// transaction, using a chunked multi-row INSERT (insertChunkSize per
// statement) exactly like every other bulk write in this package — even
// though error counts are typically small, this keeps the "every write is
// batched, never a per-row Exec loop" rule exception-free rather than
// special-casing this one path.
func (s *StagingStore) InsertErrors(errs []model.StagingError) error {
	if len(errs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	for start := 0; start < len(errs); start += insertChunkSize {
		end := min(start+insertChunkSize, len(errs))
		chunk := errs[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO staging_errors (version, source_file, line, byte_offset, message) VALUES ")
		args := make([]any, 0, len(chunk)*5)
		for i, e := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(5))
			sb.WriteString(")")
			args = append(args, e.Version, e.SourceFile, e.Line, e.ByteOffset, e.Message)
		}

		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: inserting staging error chunk (%d rows): %w", len(chunk), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing error batch: %w", err)
	}
	return nil
}

// GetByID returns all staging records for one object ID within an import
// version, across all profiles, ordered by profile/attribute/seq.
func (s *StagingStore) GetByID(version uint64, id string) ([]model.StagingRecord, error) {
	rows, err := s.db.Query(rebind(`
		SELECT version, id, profile, class, attribute, value, is_reference, seq
		FROM staging_records
		WHERE version = ? AND id = ?
		ORDER BY profile, attribute, seq
	`), version, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying by id: %w", err)
	}
	defer rows.Close()

	var records []model.StagingRecord
	for rows.Next() {
		var r model.StagingRecord
		if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
			return nil, fmt.Errorf("postgres: scanning row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating rows: %w", err)
	}
	return records, nil
}

// maxIDsPerQuery caps how many IDs go into one "IN (...)" query per
// round-trip. PostgreSQL's own parameter limit (65535) is far higher than
// SQLite's (999, see internal/sqlite's identical constant), but this is
// kept at the same conservative value for parity — there is no measured
// benefit to a larger chunk here, and a shared value avoids the two
// backends having gratuitously different batching behavior.
const maxIDsPerQuery = 500

// GetByIDs is GetByID for many IDs at once (see staging.Store.GetByIDs).
func (s *StagingStore) GetByIDs(version uint64, ids []string) ([]model.StagingRecord, error) {
	var out []model.StagingRecord
	for start := 0; start < len(ids); start += maxIDsPerQuery {
		end := min(start+maxIDsPerQuery, len(ids))
		chunk := ids[start:end]

		args := make([]any, 0, len(chunk)+1)
		args = append(args, version)
		for _, id := range chunk {
			args = append(args, id)
		}
		query := rebind(fmt.Sprintf(`
			SELECT version, id, profile, class, attribute, value, is_reference, seq
			FROM staging_records
			WHERE version = ? AND id IN (%s)
		`, placeholders(len(chunk))))

		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("postgres: querying by ids: %w", err)
		}
		for rows.Next() {
			var r model.StagingRecord
			if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
				rows.Close()
				return nil, fmt.Errorf("postgres: scanning row: %w", err)
			}
			out = append(out, r)
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return nil, fmt.Errorf("postgres: iterating rows: %w", rowsErr)
		}
	}
	return out, nil
}

// GetReferencesToAny is GetReferencesTo for many target IDs at once (see
// staging.Store.GetReferencesToAny).
func (s *StagingStore) GetReferencesToAny(version uint64, targetIDs []string) ([]model.StagingRecord, error) {
	var out []model.StagingRecord
	for start := 0; start < len(targetIDs); start += maxIDsPerQuery {
		end := min(start+maxIDsPerQuery, len(targetIDs))
		chunk := targetIDs[start:end]

		args := make([]any, 0, len(chunk)+1)
		args = append(args, version)
		for _, id := range chunk {
			args = append(args, id)
		}
		// is_reference is a real BOOLEAN column here (see this package's
		// doc comment), so the filter is "= true", not SQLite's "= 1".
		query := rebind(fmt.Sprintf(`
			SELECT version, id, profile, class, attribute, value, is_reference, seq
			FROM staging_records
			WHERE version = ? AND is_reference = true AND value IN (%s)
		`, placeholders(len(chunk))))

		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("postgres: querying references to any: %w", err)
		}
		for rows.Next() {
			var r model.StagingRecord
			if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
				rows.Close()
				return nil, fmt.Errorf("postgres: scanning row: %w", err)
			}
			out = append(out, r)
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return nil, fmt.Errorf("postgres: iterating rows: %w", rowsErr)
		}
	}
	return out, nil
}

// ListClasses returns the distinct classes present in the given import
// version (see staging.Store.ListClasses).
func (s *StagingStore) ListClasses(version uint64) ([]string, error) {
	rows, err := s.db.Query(
		rebind(`SELECT DISTINCT class FROM staging_records WHERE version = ? ORDER BY class`),
		version,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing classes: %w", err)
	}
	defer rows.Close()

	var classes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("postgres: scanning class: %w", err)
		}
		classes = append(classes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating classes: %w", err)
	}
	return classes, nil
}

// GetByClass returns a cursor-paginated chunk of staging records for
// objects of the given class (see staging.Store.GetByClass). It first
// selects up to limit distinct object IDs greater than afterID, then fetches
// all rows for exactly those IDs — so a batch never splits an object's
// attribute rows across two calls.
func (s *StagingStore) GetByClass(version uint64, class string, afterID string, limit int) ([]model.StagingRecord, error) {
	idRows, err := s.db.Query(rebind(`
		SELECT DISTINCT id FROM staging_records
		WHERE version = ? AND class = ? AND id > ?
		ORDER BY id
		LIMIT ?
	`), version, class, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: selecting class id page: %w", err)
	}
	var ids []string
	for idRows.Next() {
		var id string
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return nil, fmt.Errorf("postgres: scanning id: %w", err)
		}
		ids = append(ids, id)
	}
	idErr := idRows.Err()
	idRows.Close()
	if idErr != nil {
		return nil, fmt.Errorf("postgres: iterating id page: %w", idErr)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(ids)+2)
	args = append(args, version, class)
	for _, id := range ids {
		args = append(args, id)
	}
	query := rebind(fmt.Sprintf(`
		SELECT version, id, profile, class, attribute, value, is_reference, seq
		FROM staging_records
		WHERE version = ? AND class = ? AND id IN (%s)
		ORDER BY id, profile, attribute, seq
	`, placeholders(len(ids))))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: fetching class page rows: %w", err)
	}
	defer rows.Close()

	var records []model.StagingRecord
	for rows.Next() {
		var r model.StagingRecord
		if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
			return nil, fmt.Errorf("postgres: scanning row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating class page rows: %w", err)
	}
	return records, nil
}

// GetReferencesTo returns all staging records whose value references
// targetID (see staging.Store.GetReferencesTo), using the index on
// (version, value, is_reference) rather than a full scan.
func (s *StagingStore) GetReferencesTo(version uint64, targetID string) ([]model.StagingRecord, error) {
	rows, err := s.db.Query(rebind(`
		SELECT version, id, profile, class, attribute, value, is_reference, seq
		FROM staging_records
		WHERE version = ? AND value = ? AND is_reference = true
		ORDER BY id, profile, attribute, seq
	`), version, targetID)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying references to %s: %w", targetID, err)
	}
	defer rows.Close()

	var records []model.StagingRecord
	for rows.Next() {
		var r model.StagingRecord
		if err := rows.Scan(&r.Version, &r.ID, &r.Profile, &r.Class, &r.Attribute, &r.Value, &r.IsReference, &r.Seq); err != nil {
			return nil, fmt.Errorf("postgres: scanning row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating rows: %w", err)
	}
	return records, nil
}

// CountByVersion returns the number of staging records for the given
// import version.
func (s *StagingStore) CountByVersion(version uint64) (int, error) {
	var count int
	err := s.db.QueryRow(
		rebind(`SELECT COUNT(*) FROM staging_records WHERE version = ?`),
		version,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting version: %w", err)
	}
	return count, nil
}

// CountErrorsByVersion returns the number of staging errors for the given
// import version.
func (s *StagingStore) CountErrorsByVersion(version uint64) (int, error) {
	var count int
	err := s.db.QueryRow(
		rebind(`SELECT COUNT(*) FROM staging_errors WHERE version = ?`),
		version,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting version errors: %w", err)
	}
	return count, nil
}

// DeleteVersion removes all staging records and errors for the given
// import version (see staging.Store.DeleteVersion).
func (s *StagingStore) DeleteVersion(version uint64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	if _, err := tx.Exec(rebind(`DELETE FROM staging_records WHERE version = ?`), version); err != nil {
		return fmt.Errorf("postgres: deleting staging records for version %d: %w", version, err)
	}
	if _, err := tx.Exec(rebind(`DELETE FROM staging_errors WHERE version = ?`), version); err != nil {
		return fmt.Errorf("postgres: deleting staging errors for version %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing version deletion: %w", err)
	}
	return nil
}

// placeholders builds "?, ?, ..." for n placeholders — deliberately still
// "?"-style (not "$N"), same as internal/sqlite's identical helper: the
// surrounding query text always passes through rebind() before execution,
// which renumbers every "?" (including these) into the correct "$N"
// sequence relative to its position in the final query string.
func placeholders(n int) string {
	ph := make([]string, n)
	for i := range ph {
		ph[i] = "?"
	}
	return strings.Join(ph, ", ")
}
