// Package sqlite — this file implements pkg/core/metadata.Store on top of
// a single-row table, the JAG-wide "global Metadata record" (see
// pkg/core/metadata's doc comment: current state only, no history — the
// row is fully overwritten by every Record call, never appended to).
//
// Deliberately a completely independent counter from
// staging_version_counter (staging.go's NextVersion): that counter
// allocates a fresh number on every Phase 1 run regardless of outcome and
// is purely transient import-time scratch bookkeeping (see
// staging/store.go's doc comment), whereas model_metadata.number only
// advances once per successful import (see Record's doc comment).
package sqlite

import (
	"database/sql"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/ame89/jag/pkg/core/metadata"
)

// metadataSchema creates the single-row model_metadata table if it
// doesn't exist yet.
const metadataSchema = `
CREATE TABLE IF NOT EXISTS model_metadata (
    id        INTEGER PRIMARY KEY CHECK (id = 1),
    number    INTEGER NOT NULL,
    timestamp TEXT NOT NULL,
    label     TEXT NOT NULL
);
`

// MetadataStore implements metadata.Store on top of SQLite. Shares its
// *sql.DB/writeMu with ModelStore/FlagStore — obtained via
// StagingStore.Metadata(), same pattern as StagingStore.Model()/
// StagingStore.Flags().
type MetadataStore struct {
	db      *sql.DB
	writeMu *sync.Mutex
}

// Metadata returns a MetadataStore sharing this StagingStore's database
// connection.
func (s *StagingStore) Metadata() *MetadataStore {
	return &MetadataStore{db: s.db, writeMu: &s.writeMu}
}

// Get returns the current global Metadata record, or ok=false if Record
// has never been called against this database.
func (m *MetadataStore) Get() (metadata.Metadata, bool, error) {
	var number uint64
	var ts, label string
	err := m.db.QueryRow(`SELECT number, timestamp, label FROM model_metadata WHERE id = 1`).Scan(&number, &ts, &label)
	if err == sql.ErrNoRows {
		return metadata.Metadata{}, false, nil
	}
	if err != nil {
		return metadata.Metadata{}, false, fmt.Errorf("sqlite: reading model_metadata: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return metadata.Metadata{}, false, fmt.Errorf("sqlite: parsing model_metadata timestamp %q: %w", ts, err)
	}
	return metadata.Metadata{Number: number, Timestamp: t, Label: label}, true, nil
}

// Record atomically allocates the next Number (starting at 1 if no row
// exists yet) and overwrites the single model_metadata row with
// (Number, now-UTC, label). An empty label defaults to "v"+Number. See
// metadata.Store.Record's doc comment for when callers should invoke
// this.
func (m *MetadataStore) Record(label string) (metadata.Metadata, error) {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	var result metadata.Metadata
	err := withTx(m.db, func(tx *sql.Tx) error {
		var current uint64
		err := tx.QueryRow(`SELECT number FROM model_metadata WHERE id = 1`).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("sqlite: reading current model_metadata number: %w", err)
		}
		next := current + 1
		now := time.Now().UTC()
		if label == "" {
			label = "v" + strconv.FormatUint(next, 10)
		}
		if _, err := tx.Exec(
			`INSERT INTO model_metadata (id, number, timestamp, label) VALUES (1, ?, ?, ?)
             ON CONFLICT (id) DO UPDATE SET number = excluded.number, timestamp = excluded.timestamp, label = excluded.label`,
			next, now.Format(time.RFC3339Nano), label,
		); err != nil {
			return fmt.Errorf("sqlite: upserting model_metadata: %w", err)
		}
		result = metadata.Metadata{Number: next, Timestamp: now, Label: label}
		return nil
	})
	if err != nil {
		return metadata.Metadata{}, err
	}
	return result, nil
}

// Set overwrites the single model_metadata row with the exact given
// values, without the read-then-increment logic Record uses. See
// metadata.Store.Set's doc comment for the intended (restore-only) use
// case.
func (m *MetadataStore) Set(md metadata.Metadata) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	_, err := m.db.Exec(
		`INSERT INTO model_metadata (id, number, timestamp, label) VALUES (1, ?, ?, ?)
         ON CONFLICT (id) DO UPDATE SET number = excluded.number, timestamp = excluded.timestamp, label = excluded.label`,
		md.Number, md.Timestamp.UTC().Format(time.RFC3339Nano), md.Label,
	)
	if err != nil {
		return fmt.Errorf("sqlite: setting model_metadata: %w", err)
	}
	return nil
}
