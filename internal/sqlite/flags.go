package sqlite

import (
	"database/sql"
	"fmt"
)

// FlagStore implements common.FlagStore (see internal/impl/common/flags.go
// for the full design rationale) on top of the ephemeral import_flag table
// (see model.go's schema). Shares its *sql.DB/writeMu with ModelStore —
// obtained via StagingStore.Flags(), same pattern as StagingStore.Model()/
// StagingStore.Catalog().
type FlagStore struct {
	db *sql.DB
}


// Flags returns a FlagStore sharing this StagingStore's database
// connection.
func (s *StagingStore) Flags() *FlagStore {
	return &FlagStore{db: s.db}
}

// MarkFlags records that each of ids has reached the given kind's
// milestone for this import version (e.g. "this ConnectivityNode ID was
// referenced by some Equipment's Terminal", "this Equipment ID got a
// container assigned"). INSERT OR IGNORE is deliberate (not a plain
// INSERT/upsert): existence is the only thing that matters here, never
// downgraded — if two different workers/batches happen to see the same
// boundary ID (e.g. a Node shared between a Pass A station batch and a
// Pass B ACLineSegment), both simply try to insert the same row and the
// second one is silently ignored, which is exactly the desired semantics
// (a flag can only go from "unset" to "set", never back).
func (f *FlagStore) MarkFlags(version uint64, kind string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return withTx(f.db, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT OR IGNORE INTO import_flag (version, kind, id) VALUES (?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("sqlite: preparing import_flag insert: %w", err)
		}
		defer stmt.Close()
		for _, id := range ids {
			if _, err := stmt.Exec(version, kind, id); err != nil {
				return fmt.Errorf("sqlite: marking flag %s/%d/%s: %w", kind, version, id, err)
			}
		}
		return nil
	})
}

// UnmarkedIDs returns the subset of ids that do NOT carry the given kind's
// flag for this version — used by the final, paged Phase 3 completeness
// scans (see consistency.go's checkUnreferencedNodesFlagged/
// checkEquipmentWithoutContainerFlagged): the caller pages through a class
// (or through another flag's own IDs) in bounded chunks and asks, for just
// that chunk, "which of these are still unflagged" — never a whole-model
// query.
func (f *FlagStore) UnmarkedIDs(version uint64, kind string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, version, kind)
	args = append(args, idArgs(ids)...)
	rows, err := f.db.Query(fmt.Sprintf(
		`SELECT id FROM import_flag WHERE version = ? AND kind = ? AND id IN (%s)`,
		placeholders(len(ids)),
	), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying flagged ids: %w", err)
	}
	defer rows.Close()

	flagged := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: scanning flagged id: %w", err)
		}
		flagged[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating flagged ids: %w", err)
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
// in ID order, chunkSize at a time (afterID="" starts from the beginning)
// — used to enumerate the "installed equipment" universe for
// checkEquipmentWithoutContainerFlagged without ever holding the whole
// list in memory.
func (f *FlagStore) PagedFlagIDs(version uint64, kind string, afterID string, limit int) ([]string, error) {
	rows, err := f.db.Query(
		`SELECT id FROM import_flag WHERE version = ? AND kind = ? AND id > ? ORDER BY id LIMIT ?`,
		version, kind, afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: paging flagged ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: scanning paged flagged id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating paged flagged ids: %w", err)
	}
	return ids, nil
}

// ClearFlags deletes every import_flag row for this version — called once
// Phase 3's final flagged completeness scans have run, since these flags
// are purely ephemeral import-time bookkeeping (see flags.go/model.go's
// doc comments), never part of the permanent model.
func (f *FlagStore) ClearFlags(version uint64) error {
	_, err := f.db.Exec(`DELETE FROM import_flag WHERE version = ?`, version)
	if err != nil {
		return fmt.Errorf("sqlite: clearing import flags: %w", err)
	}
	return nil
}
