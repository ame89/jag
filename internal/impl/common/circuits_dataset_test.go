package common

import (
	"path/filepath"
	"sort"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
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
			// Pure bus-branch model: no ConnectivityNode at all, only
			// Terminal.TopologicalNode references (see terminals.go's
			// ResolveTerminals fallback). Before that fallback existed,
			// every Terminal failed to resolve and the model imported
			// empty (0 Circuits) — this now reflects the real topology.
			dir:           "BaseCase_Complete",
			wantCircuits:  3,
			wantSizesDesc: []int{105, 12, 1},
		},
		{
			dir:           "MicroGrid_NL_BusCoupler",
			wantCircuits:  4,
			wantSizesDesc: []int{8, 7, 2, 2},
		},
		{
			// Updated 2026-07-14: BusbarSection was added to
			// terminals.go's nodeRoleClasses (previously only Junction),
			// fixing a bug where any BusbarSection with >2 Terminals (a
			// perfectly normal busbar with many feeder connections) was
			// wrongly rejected as an Anomaly instead of being treated as
			// a Node-role marker for its own ConnectivityNode(s) — see
			// nodeedge.go's doc comment. This dataset had 6 such
			// previously-rejected BusbarSections; correctly including
			// them merges two previously-separate Circuits into one.
			dir:           "MiniGrid_NodeBreaker_Switchgear",
			wantCircuits:  7,
			wantSizesDesc: []int{58, 14, 11, 7, 4, 4, 4},
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

// TestBuildCircuitsAgainstRealDatasetsCigreMV mirrors
// TestBuildCircuitsAgainstRealDatasets for the examples/cigre_mv dataset,
// which (unlike examples/cgmes/*) has no per-scenario subdirectory — its
// profile files (Equipment/Topology/SteadyStateHypothesis/StateVariables/
// DiagramLayout) sit directly under examples/cigre_mv.
func TestBuildCircuitsAgainstRealDatasetsCigreMV(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "cigre_mv")
	wantCircuits := 3
	wantSizesDesc := []int{11, 3, 1}

	gotCircuits, gotSizes := buildCircuitsForDataset(t, dir)

	if gotCircuits != wantCircuits {
		t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, wantCircuits, gotSizes)
	}
	if !equalInts(gotSizes, wantSizesDesc) {
		t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizesDesc)
	}
}

// TestBuildCircuitsAgainstRealDatasetsNSC mirrors
// TestBuildCircuitsAgainstRealDatasets for the examples/nsc dataset
// (NSC dialect, via phase1.RunNSCFiles). Unlike the CGMES datasets above,
// examples/nsc's two ".xml" files are independent scenarios that happen to
// share an object ID ("IS123") — RunNSCFiles treats any shared ID across
// files passed to a single call as a hard error (see RunNSCFiles' doc
// comment), so each file is imported in its own call here rather than as a
// single whole-directory glob.
//
// examples/nsc-problem is intentionally excluded — the user has asked to
// only inspect it on explicit request, not as part of this regression
// suite.
func TestBuildCircuitsAgainstRealDatasetsNSC(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "nsc")

	tests := []struct {
		file          string
		wantCircuits  int
		wantSizesDesc []int
	}{
		{
			file:          "example_as_cim.xml",
			wantCircuits:  9,
			wantSizesDesc: []int{35, 20, 13, 13, 8, 6, 6, 3, 3},
		},
		{
			file:          "Eine_ONS_mit_2_KVS_3_Muffen_und_9_Häuser_ohne_Trafo_MD.xml",
			wantCircuits:  3,
			wantSizesDesc: []int{23, 18, 6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			gotCircuits, gotSizes := buildCircuitsForFiles(t, []string{filepath.Join(dir, tt.file)}, true)

			if gotCircuits != tt.wantCircuits {
				t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, tt.wantCircuits, gotSizes)
			}
			if !equalInts(gotSizes, tt.wantSizesDesc) {
				t.Fatalf("Circuit sizes = %v, want %v", gotSizes, tt.wantSizesDesc)
			}
		})
	}
}

// TestBuildCircuitsAgainstRealDatasetsPfCimBeispielOrtsnetz mirrors
// TestBuildCircuitsAgainstRealDatasets for the examples/pf-cim-beispiel-ortsnetz
// dataset (CGMES-style profile split: eq/tp/ssh/gl, real Ortsnetz example,
// no GL-based geometry cross-check needed here since this test only pins
// Circuit/Node counts, not geometry).
func TestBuildCircuitsAgainstRealDatasetsPfCimBeispielOrtsnetz(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "pf-cim-beispiel-ortsnetz")
	wantCircuits := 2
	wantSizesDesc := []int{587, 1}

	gotCircuits, gotSizes := buildCircuitsForDataset(t, dir)

	if gotCircuits != wantCircuits {
		t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, wantCircuits, gotSizes)
	}
	if !equalInts(gotSizes, wantSizesDesc) {
		t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizesDesc)
	}
}

// TestBuildCircuitsAgainstRealDatasetsPandapowerCIM mirrors
// TestBuildCircuitsAgainstRealDatasets for the examples/pandapower-cim
// dataset (CGMES-style profile split: eq/tp/ssh/sv/gl/dl).
func TestBuildCircuitsAgainstRealDatasetsPandapowerCIM(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "pandapower-cim")
	wantCircuits := 2
	wantSizesDesc := []int{12, 1}

	gotCircuits, gotSizes := buildCircuitsForDataset(t, dir)

	if gotCircuits != wantCircuits {
		t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, wantCircuits, gotSizes)
	}
	if !equalInts(gotSizes, wantSizesDesc) {
		t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizesDesc)
	}
}

// TestBuildCircuitsWithSwitchOverrides is a regression/behavior test for
// "Schaltkreis"-queries (Circuit/BuildCircuits) under different
// SwitchStateOverrides against a real, fully-imported dataset
// (examples/cgmes/MiniGrid_NodeBreaker_Switchgear: 90 switch-like Equipment,
// 2 open / 88 closed per the SSH profile). It checks three variants:
//
//  1. Default (no overrides, import-default switch states): must match the
//     baseline pinned in TestBuildCircuitsAgainstRealDatasets (7 Circuits,
//     sizes [58 14 11 7 4 4 4]).
//  2. One deviating switch state: closing either of the two default-open
//     Breakers (788bb5ff-...-ea1b7268ba03 or a5a962a6-...-e29131bcba36 —
//     both sit between the same pair of Circuits in the default topology)
//     merges the 58-Node and one 4-Node Circuit into one 62-Node Circuit,
//     dropping the count from 7 to 6.
//  3. A different switch: opening a normally-closed Breaker
//     (052682ba-...-0fa4e2e01011) splits one of the 4-Node Circuits into
//     two 2-Node Circuits, raising the count from 7 to 8.
//
// These concrete switch IDs and resulting counts/sizes were determined by
// running BuildElectricalGroups/BuildCircuits against the real dataset (not
// guessed) — see the session notes for the discovery run.
func TestBuildCircuitsWithSwitchOverrides(t *testing.T) {
	const (
		openBreaker1  = "788bb5ff-f36e-406b-b6a6-ea1b7268ba03" // default open
		openBreaker2  = "a5a962a6-2f47-4ef1-960f-e29131bcba36" // default open
		closedBreaker = "052682ba-a4e5-41d5-9728-0fa4e2e01011" // default closed
	)

	dir := filepath.Join("..", "..", "..", "examples", "cgmes", "MiniGrid_NodeBreaker_Switchgear")
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .xml files found in %s", dir)
	}
	sort.Strings(files)

	store, version, nodes, edges := buildPipelineForFiles(t, files, false)
	defer store.Close()

	t.Run("default switch states", func(t *testing.T) {
		circuits, _, _, err := BuildCircuits(store, version, nodes, edges, nil)
		if err != nil {
			t.Fatalf("BuildCircuits: %v", err)
		}
		wantCircuits := 7
		wantSizes := []int{58, 14, 11, 7, 4, 4, 4}
		if len(circuits) != wantCircuits {
			t.Fatalf("Circuit count = %d, want %d", len(circuits), wantCircuits)
		}
		if gotSizes := circuitSizesDesc(circuits); !equalInts(gotSizes, wantSizes) {
			t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizes)
		}
	})

	t.Run("deviating switch state (closing a default-open breaker merges two circuits)", func(t *testing.T) {
		overrides := SwitchStateOverrides{openBreaker1: false}
		circuits, nodeCircuit, _, err := BuildCircuits(store, version, nodes, edges, overrides)
		if err != nil {
			t.Fatalf("BuildCircuits: %v", err)
		}
		wantCircuits := 6
		wantSizes := []int{62, 14, 11, 7, 4, 4}
		if len(circuits) != wantCircuits {
			t.Fatalf("Circuit count = %d, want %d", len(circuits), wantCircuits)
		}
		if gotSizes := circuitSizesDesc(circuits); !equalInts(gotSizes, wantSizes) {
			t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizes)
		}

		// Both default-open breakers sat between the same pair of Circuits;
		// closing just one of them must already fully merge that pair, so
		// closing the other one too changes nothing further.
		overridesBoth := SwitchStateOverrides{openBreaker1: false, openBreaker2: false}
		circuitsBoth, _, _, err := BuildCircuits(store, version, nodes, edges, overridesBoth)
		if err != nil {
			t.Fatalf("BuildCircuits (both closed): %v", err)
		}
		if gotSizes := circuitSizesDesc(circuitsBoth); !equalInts(gotSizes, wantSizes) {
			t.Fatalf("Circuit sizes (both closed) = %v, want %v", gotSizes, wantSizes)
		}

		// Sanity: with the override applied, the two former boundary Nodes
		// of openBreaker1 must now report the same Circuit.
		var t1, t2 string
		for _, e := range edges {
			if e.EquipmentID == openBreaker1 {
				t1, t2 = e.Terminal1NodeID, e.Terminal2NodeID
			}
		}
		if t1 == "" || t2 == "" {
			t.Fatalf("could not find edge for switch %s", openBreaker1)
		}
		if nodeCircuit[t1] != nodeCircuit[t2] {
			t.Fatalf("expected both terminal Nodes of %s to share a Circuit after closing it, got %s vs %s", openBreaker1, nodeCircuit[t1], nodeCircuit[t2])
		}
	})

	t.Run("different switch (opening a default-closed breaker splits a circuit)", func(t *testing.T) {
		overrides := SwitchStateOverrides{closedBreaker: true}
		circuits, nodeCircuit, _, err := BuildCircuits(store, version, nodes, edges, overrides)
		if err != nil {
			t.Fatalf("BuildCircuits: %v", err)
		}
		wantCircuits := 8
		wantSizes := []int{58, 14, 11, 7, 4, 4, 2, 2}
		if len(circuits) != wantCircuits {
			t.Fatalf("Circuit count = %d, want %d", len(circuits), wantCircuits)
		}
		if gotSizes := circuitSizesDesc(circuits); !equalInts(gotSizes, wantSizes) {
			t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizes)
		}

		// Sanity: the two terminal Nodes of the now-open switch must be
		// split into two different Circuits.
		var t1, t2 string
		for _, e := range edges {
			if e.EquipmentID == closedBreaker {
				t1, t2 = e.Terminal1NodeID, e.Terminal2NodeID
			}
		}
		if t1 == "" || t2 == "" {
			t.Fatalf("could not find edge for switch %s", closedBreaker)
		}
		if nodeCircuit[t1] == nodeCircuit[t2] {
			t.Fatalf("expected both terminal Nodes of %s to be split into different Circuits after opening it, got same Circuit %s", closedBreaker, nodeCircuit[t1])
		}
	})
}

// buildCircuitsForDataset runs the same pipeline phase2check/circuitcount
// use (Phase 1 -> ResolveTerminals -> BuildContainers -> MergeJunctionNodes
// -> MergeBusbarSectionNodes -> BuildNodesAndEdges -> BuildCircuits) against
// every *.xml file in dir (CGMES dialect, via phase1.RunCGMESFiles) and
// returns the resulting Circuit count plus each Circuit's Node count,
// sorted descending.
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

	return buildCircuitsForFiles(t, files, false)
}

// buildCircuitsForFiles is the dialect-aware core shared by
// buildCircuitsForDataset (CGMES, whole-directory glob) and the NSC tests
// below (which must import one file at a time — see RunNSCFiles' doc
// comment on why NSC scenario files sharing an object ID across files is a
// hard error, not something a directory-wide glob can handle here).
func buildCircuitsForFiles(t *testing.T, files []string, isNSC bool) (int, []int) {
	t.Helper()

	store, version, nodes, edges := buildPipelineForFiles(t, files, isNSC)
	defer store.Close()

	circuits, _, _, err := BuildCircuits(store, version, nodes, edges, nil)
	if err != nil {
		t.Fatalf("BuildCircuits: %v", err)
	}

	return len(circuits), circuitSizesDesc(circuits)
}

// buildPipelineForFiles runs Phase 1 -> ResolveTerminals -> BuildContainers
// -> MergeJunctionNodes -> MergeBusbarSectionNodes -> BuildNodesAndEdges for
// the given files (same pipeline as buildCircuitsForFiles) and returns the
// resulting store/version/Nodes/Edges directly, so a caller can invoke
// BuildCircuits (or BuildElectricalGroups) itself, e.g. multiple times with
// different SwitchStateOverrides, without re-running the import/model-build
// steps for each variant. The caller is responsible for closing the
// returned store.
func buildPipelineForFiles(t *testing.T, files []string, isNSC bool) (*sqlite.StagingStore, uint64, []coremodel.Node, []coremodel.Edge) {
	t.Helper()

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	var result phase1.Result
	if isNSC {
		result, err = phase1.RunNSCFiles(store, files)
	} else {
		result, err = phase1.RunCGMESFiles(store, files)
	}
	if err != nil {
		t.Fatalf("Run*Files: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Run*Files reported %d collected errors: %+v", len(result.Errors), result.Errors)
	}

	containers, err := BuildContainers(store, result.Version, 1000)
	if err != nil {
		t.Fatalf("BuildContainers: %v", err)
	}
	resolved, nodeRoleIDs, _, err := ResolveTerminals(store, result.Version, 1000)
	if err != nil {
		t.Fatalf("ResolveTerminals: %v", err)
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

	nodeOnlyIDs := map[string]bool{}
	for eqID := range busbarSectionIDs {
		nodeOnlyIDs[eqID] = true
	}
	for eqID := range nodeRoleIDs {
		nodeOnlyIDs[eqID] = true
	}

	junctionMerged := MergeJunctionNodes(resolved, nodeRoleIDs)
	mergedResolved := MergeBusbarSectionNodes(junctionMerged, containers, nodeOnlyIDs)
	nodes, edges := BuildNodesAndEdges(mergedResolved, nodeOnlyIDs)

	return store, result.Version, nodes, edges
}

// circuitSizesDesc returns each Circuit's Node count, sorted descending.
func circuitSizesDesc(circuits map[string]*Circuit) []int {
	sizes := make([]int, 0, len(circuits))
	for _, c := range circuits {
		sizes = append(sizes, len(c.Nodes))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	return sizes
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
