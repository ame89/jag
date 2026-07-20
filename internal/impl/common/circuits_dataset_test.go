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
		dir                       string
		wantCircuits              int
		wantSizesDesc             []int // Node count per Circuit, descending; nil if 0 Circuits
		wantEquipment             int
		wantBusbars               int
		wantSectionsPerBusbarDesc []int // BusbarSection-role Equipment count per Busbar container, descending
		wantEquipmentPerBayDesc   []int // Equipment count per Bay/Feeder container, descending
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
			wantEquipment: 325,
			wantBusbars:   0,
		},
		{
			dir:                       "MicroGrid_NL_BusCoupler",
			wantCircuits:              4,
			wantSizesDesc:             []int{8, 7, 2, 2},
			wantEquipment:             35,
			wantBusbars:               2,
			wantSectionsPerBusbarDesc: []int{2, 2},
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
			dir:                       "MiniGrid_NodeBreaker_Switchgear",
			wantCircuits:              7,
			wantSizesDesc:             []int{58, 14, 11, 7, 4, 4, 4},
			wantEquipment:             124,
			wantBusbars:               10,
			wantSectionsPerBusbarDesc: concatInts([]int{2}, repeatInt(1, 9)),
			wantEquipmentPerBayDesc:   repeatInt(3, 30),
		},
		{
			dir:          "ReliCapGrid_Espheim",
			wantCircuits: 48,
			wantSizesDesc: []int{
				1133, 116, 53, 35, 13, 6, 4, 4, 2, 2, 2, 2,
				1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
			},
			wantEquipment:             1777,
			wantBusbars:               117,
			wantSectionsPerBusbarDesc: concatInts([]int{3}, repeatInt(1, 116)),
			wantEquipmentPerBayDesc:   concatInts(repeatInt(3, 9), repeatInt(2, 369)),
		},
		{
			dir:                       "Telemark_LV_Fuse",
			wantCircuits:              1,
			wantSizesDesc:             []int{52},
			wantEquipment:             74,
			wantBusbars:               7,
			wantSectionsPerBusbarDesc: repeatInt(1, 7),
			wantEquipmentPerBayDesc:   repeatInt(1, 31),
		},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "examples", "cgmes", tt.dir)
			gotCircuits, gotSizes, gotStats := buildCircuitsForDataset(t, dir)

			if gotCircuits != tt.wantCircuits {
				t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, tt.wantCircuits, gotSizes)
			}
			if tt.wantSizesDesc != nil && !equalInts(gotSizes, tt.wantSizesDesc) {
				t.Fatalf("Circuit sizes = %v, want %v", gotSizes, tt.wantSizesDesc)
			}
			checkDatasetStats(t, gotStats, tt.wantEquipment, tt.wantBusbars, tt.wantSectionsPerBusbarDesc, tt.wantEquipmentPerBayDesc)
		})
	}
}

// TestBuildCircuitsAgainstRealDatasetsCGMES3 mirrors
// TestBuildCircuitsAgainstRealDatasets for the larger cgmes3/* example
// datasets, kept as a separate test so a slow/large dataset failure is
// easy to tell apart from the smaller CGMES v2.4.15 datasets above.
func TestBuildCircuitsAgainstRealDatasetsCGMES3(t *testing.T) {
	tests := []struct {
		dir                       string
		wantCircuits              int
		wantSizesDesc             []int
		wantEquipment             int
		wantBusbars               int
		wantSectionsPerBusbarDesc []int
		wantEquipmentPerBayDesc   []int
	}{
		{
			dir:                       "MicroGrid",
			wantCircuits:              7,
			wantSizesDesc:             []int{18, 12, 4, 2, 2, 2, 1},
			wantEquipment:             83,
			wantBusbars:               7,
			wantSectionsPerBusbarDesc: concatInts(repeatInt(3, 2), repeatInt(2, 2), repeatInt(1, 3)),
		},
		{
			dir:                       "MiniGrid",
			wantCircuits:              6,
			wantSizesDesc:             []int{62, 14, 11, 7, 4, 4},
			wantEquipment:             125,
			wantBusbars:               10,
			wantSectionsPerBusbarDesc: concatInts([]int{2}, repeatInt(1, 9)),
			wantEquipmentPerBayDesc:   repeatInt(2, 30),
		},
		{
			dir:                       "SmallGrid",
			wantCircuits:              3,
			wantSizesDesc:             []int{1119, 102, 4},
			wantEquipment:             1547,
			wantBusbars:               115,
			wantSectionsPerBusbarDesc: repeatInt(1, 115),
			wantEquipmentPerBayDesc:   repeatInt(2, 369),
		},
		{
			dir:          "Svedala",
			wantCircuits: 126,
			// Only the largest few groups are pinned exactly; the long tail
			// of many small (<=3-Node) circuits is checked only by count
			// above to keep this test maintainable.
			wantSizesDesc:             nil,
			wantEquipment:             1895,
			wantBusbars:               75,
			wantSectionsPerBusbarDesc: concatInts(repeatInt(3, 16), repeatInt(2, 21), repeatInt(1, 38)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "examples", "cgmes3", tt.dir)
			gotCircuits, gotSizes, gotStats := buildCircuitsForDataset(t, dir)

			if gotCircuits != tt.wantCircuits {
				t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, tt.wantCircuits, gotSizes)
			}
			if tt.wantSizesDesc != nil && !equalInts(gotSizes, tt.wantSizesDesc) {
				t.Fatalf("Circuit sizes = %v, want %v", gotSizes, tt.wantSizesDesc)
			}
			checkDatasetStats(t, gotStats, tt.wantEquipment, tt.wantBusbars, tt.wantSectionsPerBusbarDesc, tt.wantEquipmentPerBayDesc)
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
	wantEquipment := 33
	wantBusbars := 0

	gotCircuits, gotSizes, gotStats := buildCircuitsForDataset(t, dir)

	if gotCircuits != wantCircuits {
		t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, wantCircuits, gotSizes)
	}
	if !equalInts(gotSizes, wantSizesDesc) {
		t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizesDesc)
	}
	checkDatasetStats(t, gotStats, wantEquipment, wantBusbars, nil, nil)
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
		file                      string
		wantCircuits              int
		wantSizesDesc             []int
		wantEquipment             int
		wantBusbars               int
		wantSectionsPerBusbarDesc []int
		wantEquipmentPerBayDesc   []int
	}{
		{
			// Updated 2026-07-20: fixed a container-grouping bug (see
			// container.go's baseBusbarSectionID doc comment) where NSC's
			// no-VoltageLevel BusbarSection equipment was grouped into one
			// shared Busbar container per Substation instead of one per
			// physically distinct busbar. ONS 1 (S-000001) legitimately
			// has 2 separate busbars ("Busbar 1"/"Busbar 2"); before the
			// fix they were wrongly merged into a single Node, artificially
			// joining what should be two separate Circuits. The fix raises
			// this dataset's Circuit count from 9 to 13 and changes several
			// Circuit sizes accordingly.
			file:                      "example_as_cim.xml",
			wantCircuits:              13,
			wantSizesDesc:             []int{28, 15, 13, 13, 8, 6, 6, 6, 5, 4, 3, 3, 3},
			wantEquipment:             156,
			wantBusbars:               9,
			wantSectionsPerBusbarDesc: concatInts([]int{5}, repeatInt(3, 5), repeatInt(2, 3)),
			wantEquipmentPerBayDesc:   concatInts(repeatInt(4, 2), repeatInt(3, 3), repeatInt(2, 7), []int{1}),
		},
		{
			file:                      "Eine_ONS_mit_2_KVS_3_Muffen_und_9_Häuser_ohne_Trafo_MD.xml",
			wantCircuits:              3,
			wantSizesDesc:             []int{23, 18, 6},
			wantEquipment:             64,
			wantBusbars:               3,
			wantSectionsPerBusbarDesc: concatInts([]int{3}, repeatInt(2, 2)),
			wantEquipmentPerBayDesc:   repeatInt(2, 4),
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			gotCircuits, gotSizes, gotStats := buildCircuitsForFiles(t, []string{filepath.Join(dir, tt.file)}, true)

			if gotCircuits != tt.wantCircuits {
				t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, tt.wantCircuits, gotSizes)
			}
			if !equalInts(gotSizes, tt.wantSizesDesc) {
				t.Fatalf("Circuit sizes = %v, want %v", gotSizes, tt.wantSizesDesc)
			}
			checkDatasetStats(t, gotStats, tt.wantEquipment, tt.wantBusbars, tt.wantSectionsPerBusbarDesc, tt.wantEquipmentPerBayDesc)
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
	wantEquipment := 625
	wantBusbars := 8

	gotCircuits, gotSizes, gotStats := buildCircuitsForDataset(t, dir)

	if gotCircuits != wantCircuits {
		t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, wantCircuits, gotSizes)
	}
	if !equalInts(gotSizes, wantSizesDesc) {
		t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizesDesc)
	}
	checkDatasetStats(t, gotStats, wantEquipment, wantBusbars, repeatInt(1, 8), nil)
}

// TestBuildCircuitsAgainstRealDatasetsPandapowerCIM mirrors
// TestBuildCircuitsAgainstRealDatasets for the examples/pandapower-cim
// dataset (CGMES-style profile split: eq/tp/ssh/sv/gl/dl).
func TestBuildCircuitsAgainstRealDatasetsPandapowerCIM(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "pandapower-cim")
	wantCircuits := 2
	wantSizesDesc := []int{12, 1}
	wantEquipment := 36
	wantBusbars := 4

	gotCircuits, gotSizes, gotStats := buildCircuitsForDataset(t, dir)

	if gotCircuits != wantCircuits {
		t.Fatalf("Circuit count = %d, want %d (sizes: %v)", gotCircuits, wantCircuits, gotSizes)
	}
	if !equalInts(gotSizes, wantSizesDesc) {
		t.Fatalf("Circuit sizes = %v, want %v", gotSizes, wantSizesDesc)
	}
	checkDatasetStats(t, gotStats, wantEquipment, wantBusbars, repeatInt(1, 4), nil)
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

	store, version, nodes, edges, _, _ := buildPipelineForFiles(t, files, false)
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
// returns the resulting Circuit count, each Circuit's Node count (sorted
// descending), and container-level datasetStats (Busbar/Bay/Equipment
// counts).
func buildCircuitsForDataset(t *testing.T, dir string) (int, []int, datasetStats) {
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
func buildCircuitsForFiles(t *testing.T, files []string, isNSC bool) (int, []int, datasetStats) {
	t.Helper()

	store, version, nodes, edges, containers, equipmentCount := buildPipelineForFiles(t, files, isNSC)
	defer store.Close()

	circuits, _, _, err := BuildCircuits(store, version, nodes, edges, nil)
	if err != nil {
		t.Fatalf("BuildCircuits: %v", err)
	}

	return len(circuits), circuitSizesDesc(circuits), computeDatasetStats(containers, equipmentCount)
}

// buildPipelineForFiles runs Phase 1 -> ResolveTerminals -> BuildContainers
// -> MergeJunctionNodes -> MergeBusbarSectionNodes -> BuildNodesAndEdges for
// the given files (same pipeline as buildCircuitsForFiles) and returns the
// resulting store/version/Nodes/Edges directly, so a caller can invoke
// BuildCircuits (or BuildElectricalGroups) itself, e.g. multiple times with
// different SwitchStateOverrides, without re-running the import/model-build
// steps for each variant. The caller is responsible for closing the
// returned store.
//
// The extra *BuildContainersResult and equipment-count return values let
// callers additionally pin container-level stats (Busbar/Bay counts,
// Equipment count) via datasetStats/computeDatasetStats below, without a
// second pipeline run.
func buildPipelineForFiles(t *testing.T, files []string, isNSC bool) (*sqlite.StagingStore, uint64, []coremodel.Node, []coremodel.Edge, *BuildContainersResult, int) {
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

	return store, result.Version, nodes, edges, containers, len(resolved)
}

// datasetStats holds container-level regression stats gathered alongside
// Circuit/Node counts: Busbar count, the number of BusbarSection-role
// Equipment grouped under each Busbar container (descending, one entry per
// Busbar), the total Equipment count, and the number of Equipment items
// directly assigned to each Bay/Feeder container (descending, one entry
// per Bay/Feeder).
//
// "Sections" here means Equipment classified as a busbar Node-role member
// (BusbarSection, post NSC-dialect Terminal-split — see
// internal/importer/nsc/normalize.go's doc comment) and grouped under one
// busbar Container by BuildContainers/pass_a.go — NOT the post-merge
// electrically-distinct Node count (see busbarmerge.go). This pins the
// container-grouping step's current behavior, independent of
// MergeBusbarSectionNodes' later electrical-node unification.
type datasetStats struct {
	equipment             int
	busbars               int
	sectionsPerBusbarDesc []int
	equipmentPerBayDesc   []int
}

// computeDatasetStats derives datasetStats from a BuildContainersResult and
// the total resolved-Equipment count returned by buildPipelineForFiles.
func computeDatasetStats(containers *BuildContainersResult, equipmentCount int) datasetStats {
	contType := map[string]coremodel.ContainerType{}
	for _, c := range containers.Containers {
		contType[c.ID] = c.Type
	}

	busbars := 0
	for _, ct := range contType {
		if ct == ContainerTypeBusbar {
			busbars++
		}
	}

	sectionsByBusbar := map[string]int{}
	eqByBay := map[string]int{}
	for eqID, contID := range containers.EquipmentToCont {
		switch contType[contID] {
		case ContainerTypeBusbar:
			sectionsByBusbar[contID]++
		case ContainerTypeBay:
			eqByBay[contID]++
		}
		_ = eqID
	}

	return datasetStats{
		equipment:             equipmentCount,
		busbars:               busbars,
		sectionsPerBusbarDesc: descCounts(sectionsByBusbar),
		equipmentPerBayDesc:   descCounts(eqByBay),
	}
}

// descCounts returns the values of m sorted descending.
func descCounts(m map[string]int) []int {
	out := make([]int, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// repeatInt returns a slice of n copies of v — used to keep the
// per-Busbar/per-Bay want*Desc literals below readable for datasets with
// many identical entries (e.g. "every Bay has exactly 2 Equipment").
func repeatInt(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// concatInts concatenates its arguments into one []int, used together with
// repeatInt to build the want*Desc literals below out of a few
// run-length-encoded groups instead of one long literal.
func concatInts(parts ...[]int) []int {
	var out []int
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// checkDatasetStats compares got against want, reporting each mismatching
// field individually. A nil want*Desc slice skips that particular
// comparison (mirrors wantSizesDesc's "nil = skip" convention above, used
// for datasets whose per-Bay/per-Busbar distribution is too long to
// usefully pin item-by-item).
func checkDatasetStats(t *testing.T, got datasetStats, wantEquipment, wantBusbars int, wantSectionsPerBusbarDesc, wantEquipmentPerBayDesc []int) {
	t.Helper()
	if got.equipment != wantEquipment {
		t.Errorf("Equipment count = %d, want %d", got.equipment, wantEquipment)
	}
	if got.busbars != wantBusbars {
		t.Errorf("Busbar count = %d, want %d", got.busbars, wantBusbars)
	}
	if wantSectionsPerBusbarDesc != nil && !equalInts(got.sectionsPerBusbarDesc, wantSectionsPerBusbarDesc) {
		t.Errorf("Sections per Busbar = %v, want %v", got.sectionsPerBusbarDesc, wantSectionsPerBusbarDesc)
	}
	if wantEquipmentPerBayDesc != nil && !equalInts(got.equipmentPerBayDesc, wantEquipmentPerBayDesc) {
		t.Errorf("Equipment per Bay/Feeder = %v, want %v", got.equipmentPerBayDesc, wantEquipmentPerBayDesc)
	}
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
