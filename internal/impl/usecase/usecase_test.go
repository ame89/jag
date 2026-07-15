package usecase_test

import (
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/usecase"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// setupService seeds an in-memory SQLite ModelStore with a small, hand-built
// station (2 containers: a substation and one bay underneath, 2 pieces of
// switch-like equipment forming one edge each, one Node-role busbar
// section, one electrical group split by an open switch, and geometry for
// the substation) and wraps it in a usecase.Service — enough to exercise
// every usecase implemented in usecase.go without depending on any example
// CIM file.
func setupService(t *testing.T) *usecase.Service {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening sqlite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	m := store.Model()

	containers := []coremodel.Container{
		{ID: "station1", Type: "substation", ParentID: ""},
		{ID: "bay1", Type: "bay", ParentID: "station1"},
	}
	if err := m.UpsertContainers(containers); err != nil {
		t.Fatalf("upserting containers: %v", err)
	}

	equipment := []coremodel.Equipment{
		{ID: "busbarA", ContainerID: "bay1"},
		{ID: "fuse1", ContainerID: "bay1"},
		{ID: "fuse2", ContainerID: "bay1"},
	}
	if err := m.UpsertEquipment(equipment); err != nil {
		t.Fatalf("upserting equipment: %v", err)
	}

	nodes := []coremodel.Node{
		{EquipmentID: "busbarA", Kind: coremodel.NodeKindReal},
		{EquipmentID: "cnB", Kind: coremodel.NodeKindReal},
		{EquipmentID: "cnC", Kind: coremodel.NodeKindReal},
	}
	if err := m.UpsertNodes(nodes); err != nil {
		t.Fatalf("upserting nodes: %v", err)
	}

	edges := []coremodel.Edge{
		{EquipmentID: "fuse1", Terminal1NodeID: "busbarA", Terminal2NodeID: "cnB"},
		{EquipmentID: "fuse2", Terminal1NodeID: "cnB", Terminal2NodeID: "cnC"},
	}
	if err := m.UpsertEdges(edges); err != nil {
		t.Fatalf("upserting edges: %v", err)
	}

	// fuse1 closed (busbarA/cnB share a group), fuse2 open (cnC is its own
	// group) — mirrors how BuildElectricalGroups would have grouped them.
	// All in one owner ("station1") for this simple single-station test.
	groups := map[string]map[string]string{
		"station1": {
			"busbarA": "busbarA",
			"cnB":     "busbarA",
			"cnC":     "cnC",
		},
	}
	if err := m.UpsertElectricalGroups(groups); err != nil {
		t.Fatalf("upserting electrical groups: %v", err)
	}

	geometries := []coremodel.Geometry{
		{OwnerID: "station1", OwnerKind: coremodel.GeometryOwnerContainer, Lat: 52.5, Lon: 13.4},
	}
	if err := m.UpsertGeometry(geometries); err != nil {
		t.Fatalf("upserting geometry: %v", err)
	}

	return usecase.NewService(
		sqlite.ContainerAdapter{ModelStore: m},
		sqlite.EquipmentAdapter{ModelStore: m},
		sqlite.GeometryAdapter{ModelStore: m},
		m,
		sqlite.ElectricalAdapter{ModelStore: m},
	)
}

func TestStationSubgraph(t *testing.T) {
	svc := setupService(t)

	sub, err := svc.StationSubgraph("station1")
	if err != nil {
		t.Fatalf("StationSubgraph: %v", err)
	}
	if len(sub.Containers) != 2 {
		t.Errorf("expected 2 containers (station1 + bay1), got %d: %+v", len(sub.Containers), sub.Containers)
	}
	if len(sub.Equipment) != 3 {
		t.Errorf("expected 3 equipment rows, got %d: %+v", len(sub.Equipment), sub.Equipment)
	}
	if len(sub.Nodes) != 1 {
		t.Errorf("expected 1 node-role equipment (busbarA), got %d: %+v", len(sub.Nodes), sub.Nodes)
	}
	if len(sub.Edges) != 2 {
		t.Errorf("expected 2 edges (fuse1, fuse2), got %d: %+v", len(sub.Edges), sub.Edges)
	}
}

func TestStationSubgraph_UnknownStation(t *testing.T) {
	svc := setupService(t)

	sub, err := svc.StationSubgraph("does-not-exist")
	if err != nil {
		t.Fatalf("StationSubgraph: %v", err)
	}
	if len(sub.Containers) != 0 || len(sub.Equipment) != 0 || len(sub.Nodes) != 0 || len(sub.Edges) != 0 {
		t.Errorf("expected an entirely empty subgraph for an unknown station, got %+v", sub)
	}
}

func TestReachablePhysical(t *testing.T) {
	svc := setupService(t)

	// Physical reachability ignores switching state entirely — all three
	// Nodes are reachable from busbarA even though fuse2 (cnB-cnC) is open.
	reachable, err := svc.ReachablePhysical([]string{"busbarA"})
	if err != nil {
		t.Fatalf("ReachablePhysical: %v", err)
	}
	got := map[string]bool{}
	for _, id := range reachable {
		got[id] = true
	}
	for _, want := range []string{"busbarA", "cnB", "cnC"} {
		if !got[want] {
			t.Errorf("expected %s to be physically reachable from busbarA, reachable set: %v", want, reachable)
		}
	}
}

func TestElectricallyConnected(t *testing.T) {
	svc := setupService(t)

	connected, err := svc.ElectricallyConnected("busbarA", "cnB")
	if err != nil {
		t.Fatalf("ElectricallyConnected(busbarA, cnB): %v", err)
	}
	if !connected {
		t.Errorf("expected busbarA and cnB to be electrically connected (same group), got false")
	}

	connected, err = svc.ElectricallyConnected("busbarA", "cnC")
	if err != nil {
		t.Fatalf("ElectricallyConnected(busbarA, cnC): %v", err)
	}
	if connected {
		t.Errorf("expected busbarA and cnC to NOT be electrically connected (open fuse2 in between), got true")
	}
}

// TestElectricallyConnected_BoundaryNode verifies the multi-owner
// reconciliation (see electrical.Store's doc comment): two Nodes that only
// end up in the same electrical group TRANSITIVELY, through a boundary
// Node shared by two different owners (stations), must still be reported
// as connected — the expansion in Service.ElectricallyConnected must
// follow the boundary Node's second group, not just compare group IDs
// directly.
func TestElectricallyConnected_BoundaryNode(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening sqlite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	m := store.Model()

	// stationA's own local grouping: nodeA and nodeShared share "groupA".
	// stationB's own local grouping: nodeShared and nodeB share "groupB".
	// Neither station sees the other's side, so nodeShared legitimately
	// carries BOTH group IDs.
	owned := map[string]map[string]string{
		"stationA": {
			"nodeA":      "groupA",
			"nodeShared": "groupA",
		},
		"stationB": {
			"nodeShared": "groupB",
			"nodeB":      "groupB",
		},
	}
	if err := m.UpsertElectricalGroups(owned); err != nil {
		t.Fatalf("upserting electrical groups: %v", err)
	}

	svc := usecase.NewService(
		sqlite.ContainerAdapter{ModelStore: m},
		sqlite.EquipmentAdapter{ModelStore: m},
		sqlite.GeometryAdapter{ModelStore: m},
		m,
		sqlite.ElectricalAdapter{ModelStore: m},
	)

	connected, err := svc.ElectricallyConnected("nodeA", "nodeB")
	if err != nil {
		t.Fatalf("ElectricallyConnected(nodeA, nodeB): %v", err)
	}
	if !connected {
		t.Errorf("expected nodeA and nodeB to be electrically connected via the shared boundary Node, got false")
	}
}

func TestGeometryInRegion(t *testing.T) {
	svc := setupService(t)

	inside, err := svc.GeometryInRegion(52.0, 13.0, 53.0, 14.0)
	if err != nil {
		t.Fatalf("GeometryInRegion (inside): %v", err)
	}
	if len(inside) != 1 || inside[0].OwnerID != "station1" {
		t.Errorf("expected station1 inside the bounding box, got %+v", inside)
	}

	outside, err := svc.GeometryInRegion(0, 0, 1, 1)
	if err != nil {
		t.Fatalf("GeometryInRegion (outside): %v", err)
	}
	if len(outside) != 0 {
		t.Errorf("expected no geometry outside the bounding box, got %+v", outside)
	}
}

func TestContainerCounts(t *testing.T) {
	svc := setupService(t)

	counts, err := svc.ContainerCounts()
	if err != nil {
		t.Fatalf("ContainerCounts: %v", err)
	}
	if counts["substation"] != 1 {
		t.Errorf("expected 1 substation, got %d (%+v)", counts["substation"], counts)
	}
	if counts["bay"] != 1 {
		t.Errorf("expected 1 bay, got %d (%+v)", counts["bay"], counts)
	}
}
