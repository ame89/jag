package postgres

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// catalogSchema creates the ParameterCatalog table if it doesn't exist
// yet. See internal/sqlite/catalog.go for the SQLite original this
// mirrors exactly (no dialect differences here — plain TEXT columns and a
// composite primary key port unchanged).
const catalogSchema = `
CREATE TABLE IF NOT EXISTS catalog_attributes (
    entry_id TEXT NOT NULL,
    key      TEXT NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (entry_id, key)
);
CREATE INDEX IF NOT EXISTS idx_catalog_attributes_by_entry
    ON catalog_attributes (entry_id);
`

// CatalogStore implements catalog.Store on top of a PostgreSQL database.
// It shares its *sql.DB with a StagingStore (see StagingStore.Catalog)
// rather than opening a second connection to the same database.
type CatalogStore struct {
	db *sql.DB
}

// Catalog returns a CatalogStore sharing this StagingStore's database
// connection (opened once in Open, which also creates the catalog schema).
func (s *StagingStore) Catalog() *CatalogStore {
	return &CatalogStore{db: s.db}
}

// Upsert inserts or replaces catalog entries in bulk. Each entry's
// attributes are stored as one row per key, replacing any existing rows for
// that entry_id+key.
func (c *CatalogStore) Upsert(entries []coremodel.CatalogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	// Flatten to one row per (entry_id, key), deduplicated (last write
	// wins) so a chunked multi-row ON CONFLICT DO UPDATE never tries to
	// update the same row twice in one statement (see model.go's
	// dedupeLast doc comment for the full PostgreSQL SQLSTATE 21000
	// rationale).
	type catalogRow struct {
		entryID, key, value string
	}
	rowKey := func(r catalogRow) string { return r.entryID + "\x00" + r.key }
	var rows []catalogRow
	for _, entry := range entries {
		for _, attr := range entry.Attributes {
			encoded, err := json.Marshal(attr.Value)
			if err != nil {
				return fmt.Errorf("postgres: encoding catalog value for %s.%s: %w", entry.ID, attr.Key, err)
			}
			rows = append(rows, catalogRow{entry.ID, string(attr.Key), string(encoded)})
		}
	}
	rows = dedupeLast(rows, rowKey)

	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	for start := 0; start < len(rows); start += insertChunkSize {
		end := min(start+insertChunkSize, len(rows))
		chunk := rows[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO catalog_attributes (entry_id, key, value) VALUES ")
		args := make([]any, 0, len(chunk)*3)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(3))
			sb.WriteString(")")
			args = append(args, r.entryID, r.key, r.value)
		}
		sb.WriteString(" ON CONFLICT (entry_id, key) DO UPDATE SET value = excluded.value")

		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: upserting catalog attribute chunk (%d rows): %w", len(chunk), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing catalog upsert: %w", err)
	}
	return nil
}

// GetByIDs returns catalog entries for the given IDs (see catalog.Store).
func (c *CatalogStore) GetByIDs(ids []string) ([]coremodel.CatalogEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := rebind(fmt.Sprintf(`
		SELECT entry_id, key, value FROM catalog_attributes
		WHERE entry_id IN (%s)
		ORDER BY entry_id, key
	`, placeholders(len(ids))))

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying catalog entries by id: %w", err)
	}
	defer rows.Close()
	return scanCatalogRows(rows)
}

// GetAll returns every catalog entry currently stored (see catalog.Store).
func (c *CatalogStore) GetAll() ([]coremodel.CatalogEntry, error) {
	rows, err := c.db.Query(`SELECT entry_id, key, value FROM catalog_attributes ORDER BY entry_id, key`)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying all catalog entries: %w", err)
	}
	defer rows.Close()
	return scanCatalogRows(rows)
}

// scanCatalogRows groups entry_id/key/value rows (ordered by entry_id) back
// into []coremodel.CatalogEntry.
func scanCatalogRows(rows *sql.Rows) ([]coremodel.CatalogEntry, error) {
	var entries []coremodel.CatalogEntry
	var current *coremodel.CatalogEntry

	for rows.Next() {
		var entryID, key, rawValue string
		if err := rows.Scan(&entryID, &key, &rawValue); err != nil {
			return nil, fmt.Errorf("postgres: scanning catalog row: %w", err)
		}
		var value any
		if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
			return nil, fmt.Errorf("postgres: decoding catalog value for %s.%s: %w", entryID, key, err)
		}

		if current == nil || current.ID != entryID {
			if current != nil {
				entries = append(entries, *current)
			}
			current = &coremodel.CatalogEntry{ID: entryID}
		}
		current.Attributes = append(current.Attributes, coremodel.Attribute{
			OwnerID: entryID,
			Key:     coremodel.AttributeKey(key),
			Value:   value,
		})
	}
	if current != nil {
		entries = append(entries, *current)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating catalog rows: %w", err)
	}
	return entries, nil
}
