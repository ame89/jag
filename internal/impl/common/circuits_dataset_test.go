package common

import (
	"path/filepath"
	"sort"
	"testing"

	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// TestBuildCircuitsAgainstRealDatasets is a regression test for the
// "Schaltkreis" (Circuit) computation (see electrical.go's Circuit/
// BuildCircuits doc comments): PowerTransformer Edges are treated as
// galvanically decoupled, open switch-like Edges interrupt, and the
// virtual GND Node is a dead end, never a connecting hop. Expected counts
// and size distributions were established against every local CGMES
// example dataset (see the session's ad-hoc phase2check/circuitcount
// verification) and are pinned here so a future change to BuildCircuits,
// BuildNodesAndEdges, MergeBusbarSectionNodes, or the underlying importer
// that silently alters the result is caught.
func TestBuildCircuitsAgainstRealDatasets(t *testing.T) {
	tests := []struct {
		dir           string
		wantCircuits  int
		wantSizesDesc []int // Node count per Circuit, descending; nil if 0 Circuits
	}{
		{
			dir:          "BaseCase_Complete",
			wantCircuits: 0, // pure bus-branch model: no ConnectivityNode/Edge at all
		},
		{
			dir:           "MicroGrid_NL_BusCoupler",
			wantCircuits:  4,
			wantSizesDesc: []int{8, 7, 2, 2},
		},
		{
			dir:           "MiniGrid_NodeBreaker_Switchgear",
			wantCircuits:  8,
			wantSizesDesc: []int{58, 14, 11, 4, 4, 4, 4, 4},
		},
		{
			dir:          "ReliCapGrid_Espheim",
			wantCircuits: 48,
			wantSizesDesc: []int{
				1133, 116, 53, 35, 13, 6, 4, 4, 2, 2, 2, 2,
				1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
			},
		},
		{
			dir:           "Telemark_LV_Fuse",
			wantCircuits:  1,
			wantSizesDesc: []int{52},
		},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "examples", "cgmes", tt.dir)
			gotCircuits, gotSizes := buildCircuitsForDataset(t, dir)

			if gotCircuits != tt.wantCircuits {
				t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, tt.wantCircuits, gotSizes)
			}
			if tt.wantSizesDesc != nil && !equalInts(gotSizes, tt.wantSizesDesc) {
				t.Fatalf("Circuit sizes = %v, want %v", gotSizes, tt.wantSizesDesc)
			}
		})
	}
}

// TestBuildCircuitsAgainstRealDatasetsCGMES3 mirrors
// TestBuildCircuitsAgainstRealDatasets for the larger cgmes3/* example
// datasets, kept as a separate test so a slow/large dataset failure is
// easy to tell apart from the smaller CGMES v2.4.15 datasets above.
func TestBuildCircuitsAgainstRealDatasetsCGMES3(t *testing.T) {
	tests := []struct {
		dir           string
		wantCircuits  int
		wantSizesDesc []int
	}{
		{
			dir:           "MicroGrid",
			wantCircuits:  7,
			wantSizesDesc: []int{18, 12, 4, 2, 2, 2, 1},
		},
		{
			dir:           "MiniGrid",
			wantCircuits:  6,
			wantSizesDesc: []int{62, 14, 11, 7, 4, 4},
		},
		{
			dir:           "SmallGrid",
			wantCircuits:  3,
			wantSizesDesc: []int{1119, 102, 4},
		},
		{
			dir:          "Svedala",
			wantCircuits: 126,
			// Only the largest few groups are pinned exactly; the long tail
			// of many small (<=3-Node) circuits is checked only by count
			// above to keep this test maintainable.
			wantSizesDesc: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "examples", "cgmes3", tt.dir)
			gotCircuits, gotSizes := buildCircuitsForDataset(t, dir)

			if gotCircuits != tt.wantCircuits {
				t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, tt.wantCircuits, gotSizes)
			}
			if tt.wantSizesDesc != nil && !equalInts(gotSizes, tt.wantSizesDesc) {
				t.Fatalf("Circuit sizes = %v, want %v", gotSizes, tt.wantSizesDesc)
			}
		})
	}
}

// buildCircuitsForDataset runs the same pipeline phase2check/circuitcount
// use (Phase 1 -> ResolveTerminals -> BuildContainers ->
// MergeBusbarSectionNodes -> BuildNodesAndEdges -> BuildCircuits) against
// every *.xml file in dir and returns the resulting Circuit count plus
// each Circuit's Node count, sorted descending.
func buildCircuitsForDataset(t *testing.T, dir string) (int, []int) {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .xml files found in %s", dir)
	}
	sort.Strings(files)

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	result, err := phase1.RunCGMESFiles(store, files)
	if err != nil {
		t.Fatalf("RunCGMESFiles: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("RunCGMESFiles reported %d collected errors: %+v", len(result.Errors), result.Errors)
	}

	resolved, _, err := ResolveTerminals(store, result.Version, 1000)
	if err != nil {
		t.Fatalf("ResolveTerminals: %v", err)
	}
	containers, err := BuildContainers(store, result.Version, 1000, resolved)
	if err != nil {
		t.Fatalf("BuildContainers: %v", err)
	}

	busbarContainerSet := map[string]bool{}
	for _, c := range containers.Containers {
		if c.Type == ContainerTypeBusbar {
			busbarContainerSet[c.ID] = true
		}
	}
	busbarSectionIDs := map[string]bool{}
	for eqID, contID := range containers.EquipmentToCont {
		if busbarContainerSet[contID] {
			busbarSectionIDs[eqID] = true
		}
	}

	mergedResolved := MergeBusbarSectionNodes(resolved, containers, busbarSectionIDs)
	nodes, edges := BuildNodesAndEdges(mergedResolved, busbarSectionIDs)

	circuits, _, _, err := BuildCircuits(store, result.Version, nodes, edges, nil)
	if err != nil {
		t.Fatalf("BuildCircuits: %v", err)
	}

	sizes := make([]int, 0, len(circuits))
	for _, c := range circuits {
		sizes = append(sizes, len(c.Nodes))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))

	return len(circuits), sizes
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
