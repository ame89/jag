package hjsonimport

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ame89/jag/pkg/jagdb"
)

// --- recordOrAdoptMetadata (via Run) -------------------------------------

// TestRun_FreshDatabaseWithoutMetadataFile_RecordsNewMetadata verifies the
// common case: a fresh database (no pre-existing Metadata) and a source
// tree without metadata.hjson falls back to the normal Record flow,
// allocating Number=1 and using the given label.
func TestRun_FreshDatabaseWithoutMetadataFile_RecordsNewMetadata(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", minimalStationHJSON)
	dbPath := filepath.Join(t.TempDir(), "model.db")

	summary, err := Run(root, dbPath, Options{Label: "first"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Metadata.Number != 1 {
		t.Errorf("Number = %d, want 1", summary.Metadata.Number)
	}
	if summary.Metadata.Label != "first" {
		t.Errorf("Label = %q, want %q", summary.Metadata.Label, "first")
	}
	if summary.Metadata.Timestamp.IsZero() {
		t.Error("Timestamp is zero, want a recorded time")
	}
}

// TestRun_FreshDatabaseWithMetadataFile_AdoptsItVerbatim verifies the
// export->import round-trip case: a fresh database (no pre-existing
// Metadata) whose source tree contains metadata.hjson (e.g. produced by
// cmd/hjsonexport from another database) adopts that file's
// Number/Timestamp/Label unchanged via Set, ignoring opts.Label.
func TestRun_FreshDatabaseWithMetadataFile_AdoptsItVerbatim(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", minimalStationHJSON)
	writeHJSONFile(t, root, "metadata.hjson", `{
  number: 99
  timestamp: "2026-01-02T03:04:05.6Z"
  label: "adopted-label"
}
`)
	dbPath := filepath.Join(t.TempDir(), "model.db")

	summary, err := Run(root, dbPath, Options{Label: "ignored"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Metadata.Number != 99 {
		t.Errorf("Number = %d, want 99 (adopted)", summary.Metadata.Number)
	}
	if summary.Metadata.Label != "adopted-label" {
		t.Errorf("Label = %q, want %q (adopted)", summary.Metadata.Label, "adopted-label")
	}
	wantTS, err := time.Parse(time.RFC3339Nano, "2026-01-02T03:04:05.6Z")
	if err != nil {
		t.Fatalf("parsing want timestamp: %v", err)
	}
	if !summary.Metadata.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v (adopted)", summary.Metadata.Timestamp, wantTS)
	}
}

// TestRun_ExistingMetadata_IgnoresMetadataFileAndIncrements verifies the
// second decided case: once a database already has a Metadata record
// (here via opts.KeepExistingFile, re-running Run against the same
// database), a subsequent Run increments Number/updates Timestamp/uses
// opts.Label — even if root/metadata.hjson is present — rather than
// re-adopting the file's values.
func TestRun_ExistingMetadata_IgnoresMetadataFileAndIncrements(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", minimalStationHJSON)
	writeHJSONFile(t, root, "metadata.hjson", `{
  number: 99
  timestamp: "2026-01-02T03:04:05.6Z"
  label: "adopted-label"
}
`)
	dbPath := filepath.Join(t.TempDir(), "model.db")

	// First run adopts metadata.hjson's Number=99 (fresh database).
	first, err := Run(root, dbPath, Options{Label: "ignored"})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Metadata.Number != 99 {
		t.Fatalf("first Number = %d, want 99", first.Metadata.Number)
	}

	// Second run against the same (now non-empty) database, with
	// KeepExistingFile so Run doesn't wipe it first: the database already
	// has Metadata, so this must increment rather than re-adopt.
	second, err := Run(root, dbPath, Options{
		Label:            "second",
		KeepExistingFile: true,
		Backend:          jagdb.SQLite,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Metadata.Number != 100 {
		t.Errorf("second Number = %d, want 100 (incremented)", second.Metadata.Number)
	}
	if second.Metadata.Label != "second" {
		t.Errorf("second Label = %q, want %q", second.Metadata.Label, "second")
	}
	if second.Metadata.Timestamp.Equal(first.Metadata.Timestamp) {
		t.Error("second Timestamp equals first, want an updated timestamp")
	}
}
