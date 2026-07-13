package sqlite

import (
	"testing"

	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

func TestNextVersionIncrementsAndNeverReused(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	v1, err := store.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	v2, err := store.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if v2 != v1+1 {
		t.Fatalf("expected v2 = v1+1, got v1=%d v2=%d", v1, v2)
	}

	// Insert and delete records for v1, then verify a fresh version isn't
	// reissued as v1 again (versions are never reused).
	if err := store.InsertBatch([]model.StagingRecord{{Version: v1, ID: "obj1", Class: "Foo", Attribute: "bar", Value: "1", Seq: 0}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.DeleteVersion(v1); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}
	count, err := store.CountByVersion(v1)
	if err != nil {
		t.Fatalf("CountByVersion: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records after DeleteVersion, got %d", count)
	}

	v3, err := store.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if v3 != v2+1 {
		t.Fatalf("expected v3 = v2+1 (no reuse of deleted v1), got v2=%d v3=%d", v2, v3)
	}
}

func TestInsertBatchChunksAcrossBoundary(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	version, err := store.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}

	// insertChunkSize is 200 — use a count that straddles two chunk
	// boundaries (200 + 200 + partial) to exercise the chunking loop.
	const total = 450
	records := make([]model.StagingRecord, total)
	for i := range records {
		records[i] = model.StagingRecord{
			Version:   version,
			ID:        "obj",
			Class:     "Foo",
			Attribute: "attr",
			Value:     "v",
			Seq:       i,
		}
	}

	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	count, err := store.CountByVersion(version)
	if err != nil {
		t.Fatalf("CountByVersion: %v", err)
	}
	if count != total {
		t.Fatalf("expected %d records, got %d", total, count)
	}
}

func TestInsertErrorsAndDeleteVersion(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	version, err := store.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}

	errs := []model.StagingError{
		{Version: version, SourceFile: "bad.xml", Line: 42, ByteOffset: 123, Message: "malformed XML"},
	}
	if err := store.InsertErrors(errs); err != nil {
		t.Fatalf("InsertErrors: %v", err)
	}

	if err := store.InsertBatch([]model.StagingRecord{{Version: version, ID: "obj1", Class: "Foo", Attribute: "bar", Value: "1"}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	if err := store.DeleteVersion(version); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	var errCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM staging_errors WHERE version = ?`, version).Scan(&errCount); err != nil {
		t.Fatalf("counting staging_errors: %v", err)
	}
	if errCount != 0 {
		t.Fatalf("expected staging_errors cleared by DeleteVersion, got %d", errCount)
	}
}

func TestInsertBatchEmptyIsNoop(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.InsertBatch(nil); err != nil {
		t.Fatalf("InsertBatch(nil): %v", err)
	}
	if err := store.InsertErrors(nil); err != nil {
		t.Fatalf("InsertErrors(nil): %v", err)
	}
}
