package hjsonimport

import (
	"os"
	"path/filepath"
	"testing"

	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/sqlite"
)

// writeHJSONFile creates the given content at dir/relPath, creating
// parent directories as needed — mirrors pkg/importer/hjson's own test
// helper of the same name (kept package-local here since test helpers are
// not exported across packages).
func writeHJSONFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating dir for %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", full, err)
	}
}

// minimalStationHJSON is the smallest valid Fachmodell substation: one bay
// with one Fuse equipment wired between two local nodes.
const minimalStationHJSON = `{
  bays: [
    {
      id: A
      equipments: [
        {
          id: @E1
          class: Fuse
          connects: [
            @N1
            @N2
          ]
        }
      ]
    }
  ]
}
`

// TestRun_ZeroValueOptionsProcessesData is the regression test, at this
// package's own level, for the chunkSize=0 bug described on
// common.DefaultChunkSize: before that fix, Run(root, dbPath, Options{})
// (the natural zero value, and exactly what Options.ChunkSize's doc
// comment already claimed would "fall back to the same defaults RunPassA/
// RunPassB use") silently persisted an empty database — Pass A's
// underlying chunkSize=0 turned into a SQL "LIMIT 0", so it queried zero
// Substation/Building roots and produced zero Containers/Equipment/Nodes/
// Edges, with no error at all. Now that RunPassA/RunPassB default
// chunkSize<=0 themselves, a bare Options{} must produce the same result
// as an explicitly-tuned Options.
func TestRun_ZeroValueOptionsProcessesData(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", minimalStationHJSON)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Run(root, dbPath, Options{}); err != nil {
		t.Fatalf("Run with zero-value Options{}: %v", err)
	}

	assertContainerIDs(t, dbPath, []string{"S-1", "A"})
}

// TestRun_KeepExistingFile_DefaultRebuildsFile covers Run's documented
// default behavior (Options.KeepExistingFile's doc comment): with the
// zero value (KeepExistingFile == false), any pre-existing file at dbPath
// is deleted before Run rebuilds it, so anything present in that old file
// but absent from the current hjson source (here, a manually inserted
// "MARKER" container standing in for stale/pre-existing state) does not
// survive.
func TestRun_KeepExistingFile_DefaultRebuildsFile(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", minimalStationHJSON)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Run(root, dbPath, testOptions()); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	insertMarkerContainer(t, dbPath)

	if _, err := Run(root, dbPath, testOptions()); err != nil {
		t.Fatalf("second Run (default rebuild): %v", err)
	}

	assertContainerIDs(t, dbPath, []string{"S-1", "A"})
}

// TestRun_KeepExistingFile_PreservesPriorState covers Run's opt-in
// KeepExistingFile behavior: with KeepExistingFile == true, Run must open
// the existing SQLite file instead of deleting it first, so state placed
// there before the call (here, a manually inserted "MARKER" container,
// standing in for jaggit's "apply" workflow — see Options.KeepExistingFile's
// doc comment — which first calls DeleteContainers itself and then wants
// Run to build on top of that result) survives alongside whatever the
// current hjson source contributes.
func TestRun_KeepExistingFile_PreservesPriorState(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", minimalStationHJSON)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Run(root, dbPath, testOptions()); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	insertMarkerContainer(t, dbPath)

	if _, err := Run(root, dbPath, Options{KeepExistingFile: true, ChunkSize: 100, BatchSize: 10, StationWorkers: 1, PassBWorkers: 1, PassBBatchSize: 10}); err != nil {
		t.Fatalf("second Run (KeepExistingFile): %v", err)
	}

	assertContainerIDs(t, dbPath, []string{"MARKER", "S-1", "A"})
}

// testOptions returns Options with explicit, small non-zero ChunkSize/
// BatchSize/worker counts, used by the KeepExistingFile tests below purely
// to keep their in-test datasets fast/deterministic — no longer required
// to avoid the chunkSize=0 bug (see TestRun_ZeroValueOptionsProcessesData
// and common.DefaultChunkSize's doc comment for that fix), since
// RunPassA/RunPassB now default chunkSize<=0 themselves.
func testOptions() Options {
	return Options{ChunkSize: 100, BatchSize: 10, StationWorkers: 1, PassBWorkers: 1, PassBBatchSize: 10}
}

// insertMarkerContainer opens dbPath and upserts a single standalone
// container with ID "MARKER" that is NOT part of the hjson source tree —
// a stand-in for state a caller placed into the file before invoking Run
// (e.g. jaggit's own DeleteContainers-then-Run "apply" sequence).
func insertMarkerContainer(t *testing.T, dbPath string) {
	t.Helper()
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("opening store to insert marker: %v", err)
	}
	defer store.Close()
	if err := store.Model().UpsertContainers([]coremodel.Container{
		{ID: "MARKER", Type: "substation"},
	}); err != nil {
		t.Fatalf("inserting marker container: %v", err)
	}
}

// assertContainerIDs opens dbPath and asserts its model_container ids
// exactly match want (order-independent).
func assertContainerIDs(t *testing.T, dbPath string, want []string) {
	t.Helper()
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("opening store to verify containers: %v", err)
	}
	defer store.Close()
	ids, err := store.Model().ListContainerIDs()
	if err != nil {
		t.Fatalf("ListContainerIDs: %v", err)
	}
	gotSet := map[string]bool{}
	for _, id := range ids {
		gotSet[id] = true
	}
	if len(gotSet) != len(want) {
		t.Fatalf("got container ids %v, want %v", ids, want)
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Fatalf("got container ids %v, want %v (missing %q)", ids, want, w)
		}
	}
}
