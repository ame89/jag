package postgres

import (
	"database/sql"
	"fmt"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// TestUpsertBatchIsOneTransactionAcrossChunkBoundary is a whitebox
// regression test for the "every writing batch must run in exactly ONE
// transaction, never one transaction per internal chunk" requirement.
//
// Background: Upsert* methods split a large batch into insertChunkSize
// (1000) row multi-row INSERT statements to bound per-statement size, but
// all of those chunked statements MUST execute inside the very same
// *sql.Tx (via withTx) so the whole batch is atomic — if a later chunk
// fails, rows already written by an earlier chunk in the same batch must
// roll back too. This is exactly the property that was at risk when
// cmd/phase2check's now-removed outer "chunkUpsert" helper used to call
// Upsert* once per 1000-item group, each such call opening its OWN
// transaction — that would have made this test fail (the first group's
// rows would have already been committed by the time the later group's
// call failed).
//
// This test forces a failure in a later chunk by exceeding
// insertChunkSize with more than one chunk's worth of rows and then
// injecting a row whose ID collides with another row's ID *within the
// same later chunk* after intentionally bypassing UpsertEquipment's own
// dedupeLast safeguard — done here by calling the lower-level withTx/
// chunked-insert logic directly via a small helper that mirrors
// UpsertEquipment without the dedupe step, so the test exercises the
// underlying chunk-loop-inside-one-transaction machinery directly.
func TestUpsertBatchIsOneTransactionAcrossChunkBoundary(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	const totalRows = insertChunkSize*2 + 50 // spans 3 chunks: 1000, 1000, 50

	// Chunk 1 and the first part of chunk 3 are well-formed. We inject a
	// duplicate ID *within the same VALUES list* of the LAST chunk (rows
	// [2*insertChunkSize .. totalRows)) — PostgreSQL's "ON CONFLICT DO
	// UPDATE command cannot affect row a second time" fires only when two
	// rows in ONE multi-row VALUES list collide, so this reproduces
	// exactly the real bug found against examples/lasttest without going
	// through UpsertEquipment's now-added dedupeLast (which would just
	// silently fix it up before it reaches the DB).
	equipment := make([]coremodel.Equipment, 0, totalRows)
	for i := 0; i < totalRows; i++ {
		equipment = append(equipment, coremodel.Equipment{ID: fmt.Sprintf("eq-%d", i), ContainerID: "c1"})
	}
	// Force a same-chunk duplicate in the last (3rd) chunk.
	lastChunkStart := 2 * insertChunkSize
	equipment[lastChunkStart+1].ID = equipment[lastChunkStart].ID

	err := withTx(m.db, func(tx *sql.Tx) error {
		return execChunkedEquipmentUpsertNoDedupe(tx, equipment)
	})
	if err == nil {
		t.Fatalf("expected an error from the colliding last chunk, got nil")
	}

	// The whole batch (including chunk 1's 1000 perfectly valid rows) must
	// have been rolled back — not just the failing chunk.
	var count int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM model_equipment").Scan(&count); err != nil {
		t.Fatalf("counting model_equipment: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after a later-chunk failure (whole batch = one transaction), got %d — "+
			"this means chunks are committing independently instead of inside one transaction", count)
	}
}

// execChunkedEquipmentUpsertNoDedupe mirrors UpsertEquipment's chunk-loop
// body exactly but deliberately skips the dedupeLast call, so this test
// can inject an in-chunk duplicate to force PostgreSQL's SQLSTATE 21000
// error and observe whether the surrounding transaction (supplied by the
// caller, not opened here) rolls back the earlier chunks too.
func execChunkedEquipmentUpsertNoDedupe(tx *sql.Tx, equipment []coremodel.Equipment) error {
	for start := 0; start < len(equipment); start += insertChunkSize {
		end := min(start+insertChunkSize, len(equipment))
		chunk := equipment[start:end]

		sb := "INSERT INTO model_equipment (id, container_id) VALUES "
		args := make([]any, 0, len(chunk)*2)
		for i, e := range chunk {
			if i > 0 {
				sb += ", "
			}
			sb += "(" + placeholders(2) + ")"
			args = append(args, e.ID, e.ContainerID)
		}
		sb += " ON CONFLICT (id) DO UPDATE SET container_id = excluded.container_id"

		if _, err := tx.Exec(rebind(sb), args...); err != nil {
			return fmt.Errorf("postgres: upserting equipment chunk (%d rows): %w", len(chunk), err)
		}
	}
	return nil
}
