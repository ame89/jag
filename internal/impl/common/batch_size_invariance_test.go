package common

import (
	"path/filepath"
	"sort"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// TestPassABatchSizeInvariance is a permanent regression guard for the
// confirmed batch-size-dependent bug found and fixed in ProcessStationBatch
// (pass_a_pipeline.go) on 2026-07-15 against examples/cgmes/
// ReliCapGrid_Espheim (107 Substations — large enough that different
// JAG_BATCH_SIZE values actually split stations across different batches):
// MergeBusbarSectionNodes' internal shadow topology (which unions every
// single-terminal Equipment onto the single shared GND node to decide
// whether two BusbarSection nodes are "already connected") produced a
// different canonical busbar-node choice depending on which OTHER,
// electrically unrelated stations happened to share the same batch —
// confirmed as a real 1132-vs-1133 Circuit-node discrepancy purely from
// changing batchSize. Fixed by scoping MergeJunctionNodes/
// MergeBusbarSectionNodes/BuildNodesAndEdges/BuildElectricalGroups per
// station.
//
// NOTE on ElectricalGroups scope (2026-07-15, see Konzept.md's "Offene
// Punkte"): an earlier attempt also tried an explicit CROSS-station
// ElectricalGroups merge step (for a real inter-station switch coupling
// found in this same dataset) but that only reconciled stations sharing
// one BATCH, so it re-introduced the identical batch-size dependence one
// level up (batchSize=1 produced MORE groups than batchSize=1000 for the
// same model). That merge step was reverted per explicit user decision:
// persisted ElectricalGroups is only ever guaranteed correct WITHIN one
// station; any real cross-station switch coupling is deliberately left for
// query-time reconstruction (see Usecases.md/Konzept.md's "Circuits are a
// query-time construct" decision), not solved at import time. Since each
// station's own ElectricalGroups is now computed from ONLY that station's
// own Equipment/Containers (see stationEquipment/stationContainers in
// ProcessStationBatch), completely independent of which batch it happens
// to land in or which other stations share that batch, the per-station
// partition itself IS fully batch-size invariant again — this test
// verifies exactly that (not equality with any whole-model baseline, which
// this dataset intentionally no longer matches for ElectricalGroups, see
// TestRunPassAAndPassBMatchWholeModelPipeline's ReliCapGrid_Espheim
// exemption).
//
// This test imports the dataset once, then runs the full Pass A + Pass B
// pipeline multiple times against the EXACT same imported data with
// different batchSize values (including sizes both smaller and larger
// than the station count, and non-divisors of it), and asserts the
// resulting Circuit sizes AND ElectricalGroups partition are byte-for-byte
// identical across every batchSize — i.e., importing the same model must
// never produce a different node-edge/topology result merely because of a
// throughput/batching knob.
func TestPassABatchSizeInvariance(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "examples", "cgmes", "ReliCapGrid_Espheim")
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

	// 107 Substations in this dataset (see circuits_dataset_test.go) — these
	// batchSizes deliberately span both sides of that count, including
	// non-divisors, so stations get split across batches differently every
	// time.
	batchSizes := []int{1, 3, 7, 10, 50, 107, 200, 1000, 5000}

	type snapshot struct {
		batchSize int
		sizes     []int
		groups    map[string][]string // canonical groupID -> sorted member node IDs
	}
	var snapshots []snapshot

	for _, bs := range batchSizes {
		bs := bs
		var gotNodes []coremodel.Node
		var gotEdges []coremodel.Edge
		ownedGroups := map[string]ElectricalGroups{}
		err := RunPassA(store, result.Version, 1000, bs, 4, nopSink{}, nil, false, func(b *BatchResult) error {
			gotNodes = append(gotNodes, b.Nodes...)
			gotEdges = append(gotEdges, b.Edges...)
			for owner, groups := range b.Groups {
				ownedGroups[owner] = groups
			}
			return nil
		})
		if err != nil {
			t.Fatalf("batchSize=%d: RunPassA: %v", bs, err)
		}
		passB, err := RunPassB(store, result.Version, 1000, nopSink{}, nil)
		if err != nil {
			t.Fatalf("batchSize=%d: RunPassB: %v", bs, err)
		}
		for owner, groups := range passB.Groups {
			ownedGroups[owner] = groups
		}

		mergedNodes, mergedEdges := mergeNodesAndEdges(gotNodes, gotEdges, passB.Nodes, passB.Edges)
		circuits, _, _, err := BuildCircuits(store, result.Version, mergedNodes, mergedEdges, nil)
		if err != nil {
			t.Fatalf("batchSize=%d: BuildCircuits: %v", bs, err)
		}
		sizes := make([]int, 0, len(circuits))
		for _, c := range circuits {
			sizes = append(sizes, len(c.Nodes))
		}
		sort.Sort(sort.Reverse(sort.IntSlice(sizes)))

		// byGroup flattens across ALL owners — a boundary Node shared by
		// more than one owner now legitimately contributes to more than
		// one group (see model_electrical_group's (node_id, owner_id)
		// composite key), which this loop preserves correctly since it
		// never overwrites a single flat node->group map.
		byGroup := map[string][]string{}
		for _, groups := range ownedGroups {
			for node, gid := range groups {
				byGroup[gid] = append(byGroup[gid], node)
			}
		}
		for _, members := range byGroup {
			sort.Strings(members)
		}

		snapshots = append(snapshots, snapshot{batchSize: bs, sizes: sizes, groups: byGroup})
	}

	base := snapshots[0]
	for _, snap := range snapshots[1:] {
		if !equalInts(snap.sizes, base.sizes) {
			t.Errorf("Circuit sizes differ: batchSize=%d got %v, batchSize=%d got %v",
				snap.batchSize, snap.sizes, base.batchSize, base.sizes)
		}
		if len(snap.groups) != len(base.groups) {
			t.Errorf("ElectricalGroups group count differs: batchSize=%d got %d groups, batchSize=%d got %d groups",
				snap.batchSize, len(snap.groups), base.batchSize, len(base.groups))
			continue
		}
		for gid, members := range base.groups {
			gotMembers, ok := snap.groups[gid]
			if !ok {
				t.Errorf("batchSize=%d: missing group %s (present at batchSize=%d with %d members)",
					snap.batchSize, gid, base.batchSize, len(members))
				continue
			}
			if !equalStrings(gotMembers, members) {
				t.Errorf("batchSize=%d: group %s members = %v, want %v (batchSize=%d)",
					snap.batchSize, gid, gotMembers, members, base.batchSize)
			}
		}
	}
}

func equalStrings(a, b []string) bool {
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
