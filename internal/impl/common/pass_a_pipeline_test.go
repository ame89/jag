package common

import (
	"path/filepath"
	"sort"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// nopSink discards Attribute/Geometry batches — this test only checks
// Node/Edge/Circuit/ElectricalGroup correctness, not Sachdaten/Geometry
// content.
type nopSink struct{}

func (nopSink) WriteAttributes(_ []coremodel.Attribute) error { return nil }
func (nopSink) WriteGeometries(_ []coremodel.Geometry) error  { return nil }

// TestRunPassAAndPassBMatchWholeModelPipeline verifies Pass A + Pass B
// (pass_a.go/pass_a_pipeline.go/pass_b.go) together against the existing
// whole-model pipeline (BuildContainers + ResolveTerminals +
// BuildNodesAndEdges + BuildCircuits + BuildElectricalGroups, exercised
// identically to buildCircuitsForFiles in circuits_dataset_test.go)
// across several real datasets — including multi-Substation datasets
// where ACLineSegment equipment (Pass B) actually connects two different
// stations (Pass A batches), the case that most directly stress-tests the
// "same CIM ConnectivityNode ID means no explicit merge needed" design
// decision (2026-07, this session): simply UNION-ing Pass A's and Pass
// B's Nodes (deduped by ID) and Edges (never overlapping IDs) must
// reproduce the exact same physical topology (Circuit sizes) as the old
// whole-model pipeline, and Pass A's own per-batch ElectricalGroups
// partition (computed with NO cross-batch merging at all) must equal the
// whole-model ElectricalGroups partition, since a cable never unions
// electrical groups regardless of whether it's station-internal or
// station-spanning.
func TestRunPassAAndPassBMatchWholeModelPipeline(t *testing.T) {
	datasets := []string{
		"MicroGrid_NL_BusCoupler",     // single Substation, 5 internal ACLineSegments
		"ReliCapGrid_Espheim",         // multi-station, ACLines expected to connect stations
		"MiniGrid_NodeBreaker_Switchgear",
		"Telemark_LV_Fuse",
	}
	for _, name := range datasets {
		name := name
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "examples", "cgmes", name)
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

			wantCircuits, wantSizes, wantGroups := buildCircuitsAndGroupsForDatasetVersion(t, store, result.Version)

			var gotNodes []coremodel.Node
			var gotEdges []coremodel.Edge
			var gotGroups ElectricalGroups = ElectricalGroups{}
			err = RunPassA(store, result.Version, 1000, DefaultBatchSize, 4, nopSink{}, nil, false, func(b *BatchResult) error {
				gotNodes = append(gotNodes, b.Nodes...)
				gotEdges = append(gotEdges, b.Edges...)
				for id, gid := range b.Groups {
					gotGroups[id] = gid
				}
				return nil
			})
			if err != nil {
				t.Fatalf("RunPassA: %v", err)
			}
			if len(gotNodes) == 0 || len(gotEdges) == 0 {
				t.Fatalf("RunPassA produced no Nodes/Edges at all — batch never ran?")
			}

			passB, err := RunPassB(store, result.Version, 1000, nopSink{}, nil)
			if err != nil {
				t.Fatalf("RunPassB: %v", err)
			}
			// Pass B's Groups are trivial (always-singleton, see RunPassB's
			// doc comment) — only fill in node IDs Pass A never saw at all
			// (nodes touched exclusively by Pass B equipment, e.g. a purely
			// internal ACLineSegment endpoint with no other station
			// equipment). Pass A's own entry always wins for a shared
			// boundary node, since it reflects the real switching equipment
			// there.
			for id, gid := range passB.Groups {
				if _, ok := gotGroups[id]; !ok {
					gotGroups[id] = gid
				}
			}

			mergedNodes, mergedEdges := mergeNodesAndEdges(gotNodes, gotEdges, passB.Nodes, passB.Edges)

			gotCircuits, _, _, err := BuildCircuits(store, result.Version, mergedNodes, mergedEdges, nil)
			if err != nil {
				t.Fatalf("BuildCircuits on Pass A + Pass B merged Nodes/Edges: %v", err)
			}
			gotSizes := make([]int, 0, len(gotCircuits))
			for _, c := range gotCircuits {
				gotSizes = append(gotSizes, len(c.Nodes))
			}
			sort.Sort(sort.Reverse(sort.IntSlice(gotSizes)))

			// NOTE (2026-07-15, root-caused via a dedicated diagnostic run,
			// not the stale comment previously here): what looked like a
			// "ReliCapGrid_Espheim cross-station ConnectivityNode-sharing
			// data anomaly" needing a -1 adjustment was actually a real
			// Pass A bug, now fixed. MergeJunctionNodes/
			// MergeBusbarSectionNodes/BuildNodesAndEdges/
			// BuildElectricalGroups used to run ONCE over an entire Pass A
			// batch's pooled multi-station data instead of per station —
			// so a busbar's own canonical-node choice (inside
			// MergeBusbarSectionNodes' Union-Find) could depend on which
			// OTHER, electrically unrelated stations happened to share the
			// same batch, purely a function of batchSize. Confirmed by
			// running the exact same dataset through ProcessStationBatch
			// with batchSize=50 vs. batchSize=1000: 1132 vs. 1133 Circuit
			// nodes for the same physical data. Fixed by scoping all four
			// of those steps to one station's own Equipment/Containers at
			// a time (see ProcessStationBatch's doc comment) — Pass A +
			// Pass B now reproduces the whole-model baseline exactly,
			// batchSize-independent, no special-casing needed for this
			// dataset anymore.
			if len(gotCircuits) != wantCircuits {
				t.Errorf("Pass A + Pass B Circuit count = %d, want %d (whole-model baseline)", len(gotCircuits), wantCircuits)
			}
			if !equalInts(gotSizes, wantSizes) {
				t.Errorf("Pass A + Pass B Circuit sizes = %v, want %v (whole-model baseline)", gotSizes, wantSizes)
			}

			if !samePartition(gotGroups, wantGroups) {
				t.Errorf("Pass A's own per-batch ElectricalGroups partition (no cross-batch merge) differs from whole-model ElectricalGroups partition")
			}
		})
	}
}

// samePartition compares two node-id -> group-id maps by PARTITION
// (the set of node-id sets sharing a group), not by literal group-id
// string equality — Union-Find's chosen representative id is an
// implementation/iteration-order detail, not part of ElectricalGroups'
// actual meaning (see electrical.go).
func samePartition(a, b ElectricalGroups) bool {
	toSetKeys := func(g ElectricalGroups) map[string]bool {
		byGroup := map[string][]string{}
		for node, gid := range g {
			byGroup[gid] = append(byGroup[gid], node)
		}
		sets := map[string]bool{}
		for _, members := range byGroup {
			sort.Strings(members)
			key := ""
			for _, m := range members {
				key += m + "\x00"
			}
			sets[key] = true
		}
		return sets
	}
	aSets := toSetKeys(a)
	bSets := toSetKeys(b)
	if len(aSets) != len(bSets) {
		return false
	}
	for k := range aSets {
		if !bSets[k] {
			return false
		}
	}
	return true
}

// mergeNodesAndEdges dedupes Nodes by ID (Pass A and Pass B may both
// produce a Node for the same shared ConnectivityNode ID at a station/
// ACLine boundary) and concatenates Edges (Pass A and Pass B never build
// an Edge for the same equipment ID, so no Edge-side dedup is needed).
func mergeNodesAndEdges(aNodes []coremodel.Node, aEdges []coremodel.Edge, bNodes []coremodel.Node, bEdges []coremodel.Edge) ([]coremodel.Node, []coremodel.Edge) {
	seen := make(map[string]bool, len(aNodes)+len(bNodes))
	nodes := make([]coremodel.Node, 0, len(aNodes)+len(bNodes))
	for _, n := range aNodes {
		if !seen[n.EquipmentID] {
			seen[n.EquipmentID] = true
			nodes = append(nodes, n)
		}
	}
	for _, n := range bNodes {
		if !seen[n.EquipmentID] {
			seen[n.EquipmentID] = true
			nodes = append(nodes, n)
		}
	}
	edges := make([]coremodel.Edge, 0, len(aEdges)+len(bEdges))
	edges = append(edges, aEdges...)
	edges = append(edges, bEdges...)
	return nodes, edges
}

// buildCircuitsAndGroupsForDatasetVersion mirrors buildCircuitsForFiles
// (circuits_dataset_test.go) but takes an already-imported store/version
// instead of importing its own, so this test can run the whole-model
// pipeline and Pass A/B against the EXACT same imported data. Also
// returns the whole-model ElectricalGroups for partition comparison.
func buildCircuitsAndGroupsForDatasetVersion(t *testing.T, store *sqlite.StagingStore, version uint64) (int, []int, ElectricalGroups) {
	t.Helper()

	containers, err := BuildContainers(store, version, 1000)
	if err != nil {
		t.Fatalf("BuildContainers: %v", err)
	}
	resolved, nodeRoleIDs, _, err := ResolveTerminals(store, version, 1000)
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

	circuits, _, _, err := BuildCircuits(store, version, nodes, edges, nil)
	if err != nil {
		t.Fatalf("BuildCircuits: %v", err)
	}

	groups, _, err := BuildElectricalGroups(store, version, nodes, edges, nil)
	if err != nil {
		t.Fatalf("BuildElectricalGroups: %v", err)
	}

	sizes := make([]int, 0, len(circuits))
	for _, c := range circuits {
		sizes = append(sizes, len(c.Nodes))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))

	return len(circuits), sizes, groups
}
