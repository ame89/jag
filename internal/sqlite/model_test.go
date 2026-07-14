package sqlite

import (
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// TestModelStore_ContainerHierarchyRoundTrip verifies UpsertContainers,
// ContainerGetByIDs, GetChildren and GetDescendants (the recursive-CTE
// path) against a small 3-level container tree.
func TestModelStore_ContainerHierarchyRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	containers := []coremodel.Container{
		{ID: "station1", Type: "substation", ParentID: ""},
		{ID: "bay1", Type: "bay", ParentID: "station1"},
		{ID: "bay2", Type: "bay", ParentID: "station1"},
		{ID: "eqcont1", Type: "bay", ParentID: "bay1"}, // 3rd level, just to exercise recursion depth > 1
	}
	if err := m.UpsertContainers(containers); err != nil {
		t.Fatalf("UpsertContainers: %v", err)
	}

	got, err := m.ContainerGetByIDs([]string{"station1", "bay1"})
	if err != nil {
		t.Fatalf("ContainerGetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 containers, got %d: %+v", len(got), got)
	}

	children, err := m.GetChildren([]string{"station1"})
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 direct children of station1, got %d: %+v", len(children), children)
	}

	descendants, err := m.GetDescendants([]string{"station1"})
	if err != nil {
		t.Fatalf("GetDescendants: %v", err)
	}
	if len(descendants) != 3 {
		t.Fatalf("expected 3 descendants (bay1, bay2, eqcont1), got %d: %+v", len(descendants), descendants)
	}

	// Re-upsert with a changed type -> must overwrite, not duplicate (no
	// historisation).
	if err := m.UpsertContainers([]coremodel.Container{{ID: "bay1", Type: "busbar", ParentID: "station1"}}); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	got, err = m.ContainerGetByIDs([]string{"bay1"})
	if err != nil {
		t.Fatalf("ContainerGetByIDs after re-upsert: %v", err)
	}
	if len(got) != 1 || got[0].Type != "busbar" {
		t.Fatalf("expected bay1 overwritten to type=busbar, got %+v", got)
	}
}

// TestModelStore_PhysicalTopologyRoundTrip verifies UpsertNodes/UpsertEdges
// (incl. the model_edge_endpoint bridge table), GetNodesByIDs,
// GetEdgesByNodeIDs and GetReachableNodes (the recursive-CTE path) against
// a small A-B-C chain plus an isolated D node.
func TestModelStore_PhysicalTopologyRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	nodes := []coremodel.Node{
		{EquipmentID: "A", Kind: coremodel.NodeKindReal},
		{EquipmentID: "B", Kind: coremodel.NodeKindReal},
		{EquipmentID: "C", Kind: coremodel.NodeKindReal},
		{EquipmentID: "D", Kind: coremodel.NodeKindReal},
	}
	if err := m.UpsertNodes(nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	edges := []coremodel.Edge{
		{EquipmentID: "AB", Terminal1NodeID: "A", Terminal2NodeID: "B"},
		{EquipmentID: "BC", Terminal1NodeID: "B", Terminal2NodeID: "C"},
		// D has no edge at all -> must not show up as reachable from A.
	}
	if err := m.UpsertEdges(edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	gotNodes, err := m.GetNodesByIDs([]string{"A", "D"})
	if err != nil {
		t.Fatalf("GetNodesByIDs: %v", err)
	}
	if len(gotNodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %+v", len(gotNodes), gotNodes)
	}

	edgesAtB, err := m.GetEdgesByNodeIDs([]string{"B"})
	if err != nil {
		t.Fatalf("GetEdgesByNodeIDs: %v", err)
	}
	if len(edgesAtB) != 2 {
		t.Fatalf("expected 2 edges touching B (AB, BC), got %d: %+v", len(edgesAtB), edgesAtB)
	}

	reachable, err := m.GetReachableNodes([]string{"A"})
	if err != nil {
		t.Fatalf("GetReachableNodes: %v", err)
	}
	reachSet := map[string]bool{}
	for _, id := range reachable {
		reachSet[id] = true
	}
	if !reachSet["A"] || !reachSet["B"] || !reachSet["C"] {
		t.Fatalf("expected A, B, C all reachable from A, got %v", reachable)
	}
	if reachSet["D"] {
		t.Fatalf("D must NOT be reachable from A (no edge at all), got %v", reachable)
	}

	// Re-upsert AB with different terminals -> old edge_endpoint rows for
	// AB must be gone (delete-then-insert), not leaked/duplicated.
	if err := m.UpsertEdges([]coremodel.Edge{{EquipmentID: "AB", Terminal1NodeID: "A", Terminal2NodeID: "D"}}); err != nil {
		t.Fatalf("re-Upsert edge: %v", err)
	}
	edgesAtB, err = m.GetEdgesByNodeIDs([]string{"B"})
	if err != nil {
		t.Fatalf("GetEdgesByNodeIDs after re-upsert: %v", err)
	}
	if len(edgesAtB) != 1 {
		t.Fatalf("expected only BC touching B after AB was rewired away, got %d: %+v", len(edgesAtB), edgesAtB)
	}
}

// TestModelStore_GeometryRoundTrip verifies UpsertGeometry/GetByIDsGeometry.
func TestModelStore_GeometryRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	geos := []coremodel.Geometry{
		{OwnerID: "eq1", OwnerKind: coremodel.GeometryOwnerEquipment, Lat: 52.5, Lon: 13.4},
		{OwnerID: "cont1", OwnerKind: coremodel.GeometryOwnerContainer, Lat: 53.5, Lon: 10.0},
	}
	if err := m.UpsertGeometry(geos); err != nil {
		t.Fatalf("UpsertGeometry: %v", err)
	}

	got, err := m.GetByIDsGeometry([]string{"eq1", "cont1", "missing"})
	if err != nil {
		t.Fatalf("GetByIDsGeometry: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 geometries (missing silently omitted), got %d: %+v", len(got), got)
	}
}

// TestModelStore_AttributeRoundTrip verifies UpsertAttributes/
// GetByOwnerIDs, including the multi-value-key case (several rows sharing
// the same OwnerID+Key) and re-upsert shrink behavior (fewer values on
// re-import must not leave stale rows behind).
func TestModelStore_AttributeRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	attrs := []coremodel.Attribute{
		{OwnerID: "eq1", Key: "name", Value: "Trafo 1"},
		{OwnerID: "eq1", Key: "alias", Value: "T1"},
		{OwnerID: "eq1", Key: "alias", Value: "Transformer One"}, // multi-value
	}
	if err := m.UpsertAttributes(attrs); err != nil {
		t.Fatalf("UpsertAttributes: %v", err)
	}

	got, err := m.GetByOwnerIDs([]string{"eq1"})
	if err != nil {
		t.Fatalf("GetByOwnerIDs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 attribute rows, got %d: %+v", len(got), got)
	}

	// Re-upsert "alias" with only 1 value now -> the old 2nd row must be
	// gone, not left stale.
	if err := m.UpsertAttributes([]coremodel.Attribute{{OwnerID: "eq1", Key: "alias", Value: "T1-only"}}); err != nil {
		t.Fatalf("re-Upsert alias: %v", err)
	}
	got, err = m.GetByOwnerIDs([]string{"eq1"})
	if err != nil {
		t.Fatalf("GetByOwnerIDs after re-upsert: %v", err)
	}
	aliasCount := 0
	for _, a := range got {
		if a.Key == "alias" {
			aliasCount++
		}
	}
	if aliasCount != 1 {
		t.Fatalf("expected exactly 1 remaining alias row after shrink, got %d: %+v", aliasCount, got)
	}
}

// TestModelStore_ElectricalGroupRoundTrip verifies UpsertElectricalGroups,
// GetElectricalGroup and GetGroupMembers.
func TestModelStore_ElectricalGroupRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	groups := map[string]string{
		"nodeA": "group1",
		"nodeB": "group1",
		"nodeC": "group2",
	}
	if err := m.UpsertElectricalGroups(groups); err != nil {
		t.Fatalf("UpsertElectricalGroups: %v", err)
	}

	got, err := m.GetElectricalGroup([]string{"nodeA", "nodeC"})
	if err != nil {
		t.Fatalf("GetElectricalGroup: %v", err)
	}
	if got["nodeA"] != "group1" || got["nodeC"] != "group2" {
		t.Fatalf("unexpected group assignment: %+v", got)
	}

	members, err := m.GetGroupMembers([]string{"group1"})
	if err != nil {
		t.Fatalf("GetGroupMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members of group1, got %d: %+v", len(members), members)
	}
}

// TestModelStore_EquipmentRoundTrip verifies UpsertEquipment/GetByIDs.
func TestModelStore_EquipmentRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	equipment := []coremodel.Equipment{
		{ID: "eq1", ContainerID: "station1"},
		{ID: "eq2", ContainerID: "station1"},
	}
	if err := m.UpsertEquipment(equipment); err != nil {
		t.Fatalf("UpsertEquipment: %v", err)
	}

	got, err := m.GetByIDs([]string{"eq1", "missing"})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 1 || got[0].ContainerID != "station1" {
		t.Fatalf("unexpected equipment result: %+v", got)
	}
}
