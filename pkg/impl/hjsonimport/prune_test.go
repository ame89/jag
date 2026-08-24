package hjsonimport

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// --- EnumerateSourceContainerIDs --------------------------------------

// TestEnumerateSourceContainerIDs_ReturnsOnlyTopLevelContainers verifies
// that ONS/KVS/Haushalte files contribute their container ID, while
// Kabel (ACLine) and Grenzknoten (Boundary) files — which don't
// correspond to a model_container row, see PruneRemoved's doc comment —
// are excluded even though they are valid, classifiable hjson files.
func TestEnumerateSourceContainerIDs_ReturnsOnlyTopLevelContainers(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", "{}")
	writeHJSONFile(t, root, "Nord/KVS/K-1.hjson", "{}")
	writeHJSONFile(t, root, "Nord/Haushalte/H-1.hjson", "{}")
	writeHJSONFile(t, root, "Nord/Kabel/acline_A_B.hjson", "{}")
	writeHJSONFile(t, root, "Nord/Kabel/acline_muffe_seg2.hjson", "{}")
	writeHJSONFile(t, root, "Nord/Grenzknoten/Boundary.hjson", "{}")

	ids, err := EnumerateSourceContainerIDs(root)
	if err != nil {
		t.Fatalf("EnumerateSourceContainerIDs: %v", err)
	}
	sort.Strings(ids)
	want := []string{"H-1", "K-1", "S-1"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
}

// TestEnumerateSourceContainerIDs_IgnoresNonHjsonFiles verifies that
// non-*.hjson files (e.g. a stray .gitignore or .md file living alongside
// the tree, see ensureGitRepo) are silently skipped rather than causing a
// classification error.
func TestEnumerateSourceContainerIDs_IgnoresNonHjsonFiles(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", "{}")
	writeHJSONFile(t, root, ".gitignore", "*\n!*/\n!*.md\n!*.hjson\n")
	writeHJSONFile(t, root, "README.md", "# notes\n")

	ids, err := EnumerateSourceContainerIDs(root)
	if err != nil {
		t.Fatalf("EnumerateSourceContainerIDs: %v", err)
	}
	if want := []string{"S-1"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
}

// TestEnumerateSourceContainerIDs_EmptyRootReturnsNoIDs verifies an empty
// tree yields a nil/empty slice rather than an error.
func TestEnumerateSourceContainerIDs_EmptyRootReturnsNoIDs(t *testing.T) {
	root := t.TempDir()
	ids, err := EnumerateSourceContainerIDs(root)
	if err != nil {
		t.Fatalf("EnumerateSourceContainerIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("got %v, want no ids", ids)
	}
}

// --- diffContainerIDs ---------------------------------------------------

func TestDiffContainerIDs(t *testing.T) {
	tests := []struct {
		name      string
		dbIDs     []string
		sourceIDs []string
		want      []string
	}{
		{"nothing stale", []string{"A", "B"}, []string{"A", "B"}, nil},
		{"one removed", []string{"A", "B", "C"}, []string{"A", "C"}, []string{"B"}},
		{"all removed", []string{"A", "B"}, nil, []string{"A", "B"}},
		{"empty db", nil, []string{"A"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffContainerIDs(tt.dbIDs, tt.sourceIDs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("diffContainerIDs(%v, %v) = %v, want %v", tt.dbIDs, tt.sourceIDs, got, tt.want)
			}
		})
	}
}

// --- PruneRemoved --------------------------------------------------------

// stationHJSON returns a minimal valid Fachmodell substation (see
// minimalStationHJSON) with the given bay/equipment/node IDs substituted
// in, so multiple distinct stations used in the same test don't
// accidentally collide on a shared bay ID.
func stationHJSON(bayID, equipmentID, node1ID, node2ID string) string {
	return `{
  bays: [
    {
      id: ` + bayID + `
      equipments: [
        {
          id: @` + equipmentID + `
          class: Fuse
          connects: [
            @` + node1ID + `
            @` + node2ID + `
          ]
        }
      ]
    }
  ]
}
`
}

// TestPruneRemoved_DeletesContainerWhoseFileWasRemoved is PruneRemoved's
// basic case: two stations (each with its own, distinctly-IDed bay) are
// imported, one station's hjson file then disappears from the source
// tree, and PruneRemoved must remove that station's own top-level
// container. Per PruneRemoved's doc comment (its "important caveat"),
// EnumerateSourceContainerIDs cannot see either station's nested bay
// container, so BOTH bays end up in the stale set too — B1 (S-1's own,
// still-present bay) just as much as B2 (S-2's) — and get deleted here
// as a side effect; only S-1 itself survives PruneRemoved on its own,
// with B1 expected to be recreated by the Run call that always follows
// it in real usage (see TestPruneRemoved_ThenKeepExistingFileRun...
// below for that combined, end-to-end-correct sequence).
func TestPruneRemoved_DeletesContainerWhoseFileWasRemoved(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", stationHJSON("B1", "E1", "N1a", "N1b"))
	writeHJSONFile(t, root, "Nord/ONS/S-2.hjson", stationHJSON("B2", "E2", "N2a", "N2b"))

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Run(root, dbPath, testOptions()); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	assertContainerIDs(t, dbPath, []string{"S-1", "B1", "S-2", "B2"})

	if err := removeFile(t, root, "Nord/ONS/S-2.hjson"); err != nil {
		t.Fatalf("removing S-2.hjson: %v", err)
	}

	summary, err := PruneRemoved(root, dbPath)
	if err != nil {
		t.Fatalf("PruneRemoved: %v", err)
	}
	if summary.Containers != 3 { // S-2, plus both bays B1/B2 (see doc comment above)
		t.Fatalf("summary.Containers = %d, want 3 (got %+v)", summary.Containers, summary)
	}
	assertContainerIDs(t, dbPath, []string{"S-1"})
}

// TestPruneRemoved_TopLevelContainerSurvivesWhenSourceUnchanged verifies
// that PruneRemoved never removes a top-level container (Substation/
// DistributionBox/House) whose hjson file is still present, even though
// — per PruneRemoved's doc comment — it always removes that same
// station's nested bay container as a side effect (since
// EnumerateSourceContainerIDs cannot see nested containers at all, they
// are unconditionally "stale"). This is NOT a true no-op, unlike what a
// naive reading of "nothing changed in the source" might suggest — see
// PruneRemoved's doc comment for why that's safe in jaggit's actual
// usage (always immediately followed by a full Run).
func TestPruneRemoved_TopLevelContainerSurvivesWhenSourceUnchanged(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", stationHJSON("B1", "E1", "N1a", "N1b"))

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Run(root, dbPath, testOptions()); err != nil {
		t.Fatalf("initial Run: %v", err)
	}

	summary, err := PruneRemoved(root, dbPath)
	if err != nil {
		t.Fatalf("PruneRemoved: %v", err)
	}
	if summary.Containers != 1 { // B1, the nested bay — see doc comment
		t.Fatalf("summary.Containers = %d, want 1 (got %+v)", summary.Containers, summary)
	}
	assertContainerIDs(t, dbPath, []string{"S-1"})
}

// TestPruneRemoved_ThenKeepExistingFileRunReproducesApplyWorkflow covers
// the intended end-to-end usage documented on PruneRemoved/
// Options.KeepExistingFile: PruneRemoved first removes containers whose
// hjson file disappeared, then Run(..., KeepExistingFile: true) rebuilds
// the surviving/changed source on top, without a full-file rebuild ever
// discarding the pruning that was just done.
func TestPruneRemoved_ThenKeepExistingFileRunReproducesApplyWorkflow(t *testing.T) {
	root := t.TempDir()
	writeHJSONFile(t, root, "Nord/ONS/S-1.hjson", stationHJSON("B1", "E1", "N1a", "N1b"))
	writeHJSONFile(t, root, "Nord/ONS/S-2.hjson", stationHJSON("B2", "E2", "N2a", "N2b"))

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Run(root, dbPath, testOptions()); err != nil {
		t.Fatalf("initial Run: %v", err)
	}

	if err := removeFile(t, root, "Nord/ONS/S-2.hjson"); err != nil {
		t.Fatalf("removing S-2.hjson: %v", err)
	}

	if _, err := PruneRemoved(root, dbPath); err != nil {
		t.Fatalf("PruneRemoved: %v", err)
	}

	opts := testOptions()
	opts.KeepExistingFile = true
	if _, err := Run(root, dbPath, opts); err != nil {
		t.Fatalf("Run with KeepExistingFile: %v", err)
	}

	assertContainerIDs(t, dbPath, []string{"S-1", "B1"})
}

// removeFile deletes dir/relPath.
func removeFile(t *testing.T, dir, relPath string) error {
	t.Helper()
	return os.Remove(filepath.Join(dir, filepath.FromSlash(relPath)))
}
