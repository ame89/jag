package postgres

import (
	"database/sql"
	"fmt"
	"strings"
)

// FlagStore implements common.FlagStore on top of the ephemeral
// import_flag table (see model.go's schema). Shares its *sql.DB with
// ModelStore — obtained via StagingStore.Flags(), same pattern as
// StagingStore.Model()/StagingStore.Catalog(). See internal/sqlite/flags.go
// for the full design rationale this mirrors; the only real port change is
// MarkFlags' "INSERT OR IGNORE" becoming an "ON CONFLICT DO NOTHING",
// since PostgreSQL has no OR IGNORE syntax.
type FlagStore struct {
	db *sql.DB
}

// Flags returns a FlagStore sharing this StagingStore's database
// connection.
func (s *StagingStore) Flags() *FlagStore {
	return &FlagStore{db: s.db}
}

// MarkFlags records that each of ids has reached the given kind's
// milestone for this import version. ON CONFLICT (version, kind, id) DO
// NOTHING is deliberate (not a plain upsert): existence is the only thing
// that matters here, never downgraded — a flag can only go from "unset" to
// "set", never back (see internal/sqlite/flags.go's identical doc comment
// for the full rationale).
func (f *FlagStore) MarkFlags(version uint64, kind string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return withTx(f.db, func(tx *sql.Tx) error {
		for start := 0; start < len(ids); start += insertChunkSize {
			end := min(start+insertChunkSize, len(ids))
			chunk := ids[start:end]

			var sb strings.Builder
			sb.WriteString("INSERT INTO import_flag (version, kind, id) VALUES ")
			args := make([]any, 0, len(chunk)*3)
			for i, id := range chunk {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString("(")
				sb.WriteString(placeholders(3))
				sb.WriteString(")")
				args = append(args, version, kind, id)
			}
			// DO NOTHING (not DO UPDATE) so a chunk containing the same id
			// twice is fine — no "affect row a second time" restriction
			// applies to ON CONFLICT DO NOTHING, unlike DO UPDATE.
			sb.WriteString(" ON CONFLICT (version, kind, id) DO NOTHING")

			if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
				return fmt.Errorf("postgres: marking flag chunk %s/%d (%d ids): %w", kind, version, len(chunk), err)
			}
		}
		return nil
	})
}

// UnmarkedIDs returns the subset of ids that do NOT carry the given kind's
// flag for this version — used by the final, paged Phase 3 completeness
// scans.
func (f *FlagStore) UnmarkedIDs(version uint64, kind string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, version, kind)
	args = append(args, idArgs(ids)...)
	rows, err := f.db.Query(rebind(fmt.Sprintf(
		`SELECT id FROM import_flag WHERE version = ? AND kind = ? AND id IN (%s)`,
		placeholders(len(ids)),
	)), args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying flagged ids: %w", err)
	}
	defer rows.Close()

	flagged := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning flagged id: %w", err)
		}
		flagged[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating flagged ids: %w", err)
	}

	var unmarked []string
	for _, id := range ids {
		if !flagged[id] {
			unmarked = append(unmarked, id)
		}
	}
	return unmarked, nil
}

// PagedFlagIDs pages through every ID flagged with kind for this version,
// in ID order, chunkSize at a time (afterID="" starts from the beginning).
func (f *FlagStore) PagedFlagIDs(version uint64, kind string, afterID string, limit int) ([]string, error) {
	rows, err := f.db.Query(
		rebind(`SELECT id FROM import_flag WHERE version = ? AND kind = ? AND id > ? ORDER BY id LIMIT ?`),
		version, kind, afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: paging flagged ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning paged flagged id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating paged flagged ids: %w", err)
	}
	return ids, nil
}

// ClearFlags deletes every import_flag row for this version — called once
// Phase 3's final flagged completeness scans have run, since these flags
// are purely ephemeral import-time bookkeeping, never part of the
// permanent model.
func (f *FlagStore) ClearFlags(version uint64) error {
	_, err := f.db.Exec(rebind(`DELETE FROM import_flag WHERE version = ?`), version)
	if err != nil {
		return fmt.Errorf("postgres: clearing import flags: %w", err)
	}
	return nil
}
