package postgres

import "testing"

// See pkg/sqlite/metadata_test.go for the SQLite originals these mirror.
// Requires JAG_TEST_POSTGRES_DSN (see openTestStore's doc comment) —
// skips cleanly if not set.

func TestMetadataStore_GetBeforeAnyRecordIsNotOK(t *testing.T) {
	store := openTestStore(t)

	m, ok, err := store.Metadata().Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false on a fresh database, got %+v", m)
	}
}

func TestMetadataStore_RecordIncrementsIndependentlyOfStagingVersion(t *testing.T) {
	store := openTestStore(t)

	// Advance the (unrelated) staging version counter first, to prove
	// model_metadata.number is its own independent counter, not derived
	// from staging.Store.NextVersion.
	if _, err := store.NextVersion(); err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if _, err := store.NextVersion(); err != nil {
		t.Fatalf("NextVersion: %v", err)
	}

	meta := store.Metadata()

	m1, err := meta.Record("first import")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m1.Number != 1 {
		t.Fatalf("expected first Record to allocate Number=1, got %d", m1.Number)
	}
	if m1.Label != "first import" {
		t.Fatalf("expected label %q, got %q", "first import", m1.Label)
	}
	if m1.Timestamp.IsZero() {
		t.Fatalf("expected a non-zero Timestamp")
	}

	m2, err := meta.Record("second import")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m2.Number != 2 {
		t.Fatalf("expected second Record to allocate Number=2, got %d", m2.Number)
	}

	got, ok, err := meta.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true after Record")
	}
	if got.Number != 2 || got.Label != "second import" {
		t.Fatalf("expected Get to reflect the latest Record (overwrite, not history), got %+v", got)
	}
}

func TestMetadataStore_RecordDefaultsEmptyLabelToVNumber(t *testing.T) {
	store := openTestStore(t)

	meta := store.Metadata()

	m1, err := meta.Record("")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m1.Label != "v1" {
		t.Fatalf("expected default label %q, got %q", "v1", m1.Label)
	}

	m2, err := meta.Record("")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if m2.Label != "v2" {
		t.Fatalf("expected default label %q, got %q", "v2", m2.Label)
	}
}
