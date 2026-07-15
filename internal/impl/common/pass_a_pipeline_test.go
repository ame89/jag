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
			ownedGroups := map[string]ElectricalGroups{}
			err = RunPassA(store, result.Version, 1000, DefaultBatchSize, 4, nopSink{}, nil, false, func(b *BatchResult) error {
				gotNodes = append(gotNodes, b.Nodes...)
				gotEdges = append(gotEdges, b.Edges...)
				for owner, groups := range b.Groups {
					ownedGroups[owner] = groups
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
			// Pass B persists its own (trivial, always-singleton) Groups
			// under its own fixed sentinel owner ID (PassBOwnerID) —
			// coexists independently alongside every Pass A station
			// owner's entry, no special merge/precedence needed.
			for owner, groups := range passB.Groups {
				ownedGroups[owner] = groups
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
			// MergeBusbarSectionNodes/BuildNodesAndEdges used to run ONCE
			// over an entire Pass A batch's pooled multi-station data
			// instead of per station — so a busbar's own canonical-node
			// choice (inside MergeBusbarSectionNodes' Union-Find) could
			// depend on which OTHER, electrically unrelated stations
			// happened to share the same batch, purely a function of
			// batchSize. Confirmed by running the exact same dataset
			// through ProcessStationBatch with batchSize=50 vs.
			// batchSize=1000: 1132 vs. 1133 Circuit nodes for the same
			// physical data. Fixed by scoping those three steps to one
			// station's own Equipment/Containers at a time (see
			// ProcessStationBatch's doc comment) — Pass A + Pass B now
			// reproduces the whole-model baseline's Circuit sizes exactly,
			// batchSize-independent.
			if len(gotCircuits) != wantCircuits {
				t.Errorf("Pass A + Pass B Circuit count = %d, want %d (whole-model baseline)", len(gotCircuits), wantCircuits)
			}
			if !equalInts(gotSizes, wantSizes) {
				t.Errorf("Pass A + Pass B Circuit sizes = %v, want %v (whole-model baseline)", gotSizes, wantSizes)
			}

			// ElectricalGroups: each owner (Pass A station, or Pass B's
			// sentinel owner) persists its OWN independently-computed
			// local grouping — never merged with any other owner's at
			// import time (see model_electrical_group's (node_id,
			// owner_id) composite key and this package's doc comments on
			// pass_a_pipeline.go/pass_b.go). A raw ConnectivityNode shared
			// by two owners (a real inter-station switch coupling,
			// confirmed real in ReliCapGrid_Espheim's Riverlands/
			// Needlehole pair) therefore legitimately carries more than
			// one group ID. reconcileOwnedGroups reproduces exactly the
			// query-time reconciliation a caller like
			// usecase.ElectricallyConnected performs (expand across a
			// boundary Node's every group until a fixpoint) to recover a
			// single flat partition — which must equal the whole-model
			// baseline's partition for EVERY dataset, including
			// ReliCapGrid_Espheim (no more per-dataset exemption needed,
			// unlike the earlier single-group-per-node design).
			gotGroups := reconcileOwnedGroups(ownedGroups)
			if !samePartition(gotGroups, wantGroups) {
				t.Errorf("Pass A/B's reconciled ElectricalGroups partition differs from whole-model ElectricalGroups partition")
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

// reconcileOwnedGroups flattens an owner-keyed ElectricalGroups map (see
// BatchResult.Groups/PassBResult.Groups) into a single node-id -> group-id
// partition, reconciling any boundary Node (one belonging to more than
// one owner's group — see model_electrical_group's (node_id, owner_id)
// composite key) by union-find over group IDs: whenever a Node carries
// more than one group ID, those group IDs are unioned into one. This is
// exactly the reconciliation a query-time caller like
// usecase.Service.ElectricallyConnected performs (expand across a
// boundary Node's every group until a fixpoint) — used here purely to
// recover a single comparable partition for testing against the
// whole-model baseline, not as production code.
func reconcileOwnedGroups(owned map[string]ElectricalGroups) ElectricalGroups {
	nodeGroups := map[string][]string{}
	for _, groups := range owned {
		for node, gid := range groups {
			nodeGroups[node] = append(nodeGroups[node], gid)
		}
	}

	parent := map[string]string{}
	find := func(x string) string {
		if _, ok := parent[x]; !ok {
			parent[x] = x
		}
		for parent[x] != x {
			parent[x] = parent[parent[x]] // path halving
			x = parent[x]
		}
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, gids := range nodeGroups {
		for i := 1; i < len(gids); i++ {
			union(gids[0], gids[i])
		}
	}

	result := ElectricalGroups{}
	for node, gids := range nodeGroups {
		result[node] = find(gids[0])
	}
	return result
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
