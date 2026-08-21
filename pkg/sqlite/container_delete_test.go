package sqlite

import (
	"sort"
	"testing"

	coremodel "github.com/ame89/jag/pkg/core/model"
)

// TestModelStore_DeleteContainers_SimpleCase verifies that removing a
// container whose Equipment/Edge/Node/Attribute data is not shared with
// any other container deletes everything belonging to it, including the
// otherwise-orphaned Node.
func TestModelStore_DeleteContainers_SimpleCase(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	if err := m.UpsertContainers([]coremodel.Container{{ID: "station1", Type: "substation"}}); err != nil {
		t.Fatalf("UpsertContainers: %v", err)
	}
	if err := m.UpsertEquipment([]coremodel.Equipment{
		{ID: "eq1", ContainerID: "station1"},
	}); err != nil {
		t.Fatalf("UpsertEquipment: %v", err)
	}
	if err := m.UpsertNodes([]coremodel.Node{
		{EquipmentID: "nodeA", Kind: coremodel.NodeKindReal},
		{EquipmentID: "nodeB", Kind: coremodel.NodeKindReal},
	}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	if err := m.UpsertEdges([]coremodel.Edge{
		{EquipmentID: "eq1", Terminal1NodeID: "nodeA", Terminal2NodeID: "nodeB"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}
	if err := m.UpsertElectricalGroups(map[string]map[string]string{
		"station1": {"nodeA": "g1", "nodeB": "g1"},
	}); err != nil {
		t.Fatalf("UpsertElectricalGroups: %v", err)
	}
	if err := m.UpsertAttributes([]coremodel.Attribute{
		{OwnerID: "eq1", Key: "Name", Value: "Transformer 1"},
	}); err != nil {
		t.Fatalf("UpsertAttributes: %v", err)
	}

	ids, err := m.ListContainerIDs()
	if err != nil {
		t.Fatalf("ListContainerIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "station1" {
		t.Fatalf("expected [station1], got %+v", ids)
	}

	summary, err := m.DeleteContainers([]string{"station1"})
	if err != nil {
		t.Fatalf("DeleteContainers: %v", err)
	}
	if summary.Containers != 1 || summary.Equipment != 1 || summary.Edges != 1 || summary.Nodes != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	if got, err := m.ContainerGetByIDs([]string{"station1"}); err != nil || len(got) != 0 {
		t.Fatalf("expected container gone, got %+v (err %v)", got, err)
	}
	if got, err := m.GetByIDs([]string{"eq1"}); err != nil || len(got) != 0 {
		t.Fatalf("expected equipment gone, got %+v (err %v)", got, err)
	}
	if got, err := m.GetNodesByIDs([]string{"nodeA", "nodeB"}); err != nil || len(got) != 0 {
		t.Fatalf("expected nodes gone, got %+v (err %v)", got, err)
	}
	if got, err := m.GetEdgesByEquipmentIDs([]string{"eq1"}); err != nil || len(got) != 0 {
		t.Fatalf("expected edge gone, got %+v (err %v)", got, err)
	}
	if got, err := m.GetByOwnerIDs([]string{"eq1"}); err != nil || len(got) != 0 {
		t.Fatalf("expected attributes gone, got %+v (err %v)", got, err)
	}
}

// TestModelStore_DeleteContainers_SharedNodeIsProtected is the regression
// test for the cross-station node-sharing risk documented on
// DeleteContainers: two containers ("stationA", "stationB") both own an
// electrical-group perspective on the very same boundary node ("shared").
// Deleting stationA alone must NOT remove "shared" (stationB still
// references it); only once stationB is also deleted does "shared" become
// a true orphan and disappear.
func TestModelStore_DeleteContainers_SharedNodeIsProtected(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	if err := m.UpsertContainers([]coremodel.Container{
		{ID: "stationA", Type: "substation"},
		{ID: "stationB", Type: "substation"},
	}); err != nil {
		t.Fatalf("UpsertContainers: %v", err)
	}
	if err := m.UpsertEquipment([]coremodel.Equipment{
		{ID: "eqA", ContainerID: "stationA"},
		{ID: "eqB", ContainerID: "stationB"},
	}); err != nil {
		t.Fatalf("UpsertEquipment: %v", err)
	}
	if err := m.UpsertNodes([]coremodel.Node{
		{EquipmentID: "shared", Kind: coremodel.NodeKindReal},
		{EquipmentID: "onlyA", Kind: coremodel.NodeKindReal},
	}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	if err := m.UpsertEdges([]coremodel.Edge{
		{EquipmentID: "eqA", Terminal1NodeID: "onlyA", Terminal2NodeID: "shared"},
		{EquipmentID: "eqB", Terminal1NodeID: "shared", Terminal2NodeID: "shared"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}
	// Both stations independently perceive "shared" as part of their own
	// electrical group — the real cross-station boundary-node case.
	if err := m.UpsertElectricalGroups(map[string]map[string]string{
		"stationA": {"onlyA": "gA", "shared": "gA"},
		"stationB": {"shared": "gB"},
	}); err != nil {
		t.Fatalf("UpsertElectricalGroups: %v", err)
	}

	// Delete stationA: eqA/onlyA/edge(eqA) must go, but "shared" must
	// survive since stationB still owns an electrical-group row on it and
	// stationB's own edge(eqB) still references it.
	summary, err := m.DeleteContainers([]string{"stationA"})
	if err != nil {
		t.Fatalf("DeleteContainers(stationA): %v", err)
	}
	if summary.Containers != 1 || summary.Equipment != 1 || summary.Edges != 1 {
		t.Fatalf("unexpected summary after deleting stationA: %+v", summary)
	}
	if summary.Nodes != 1 {
		t.Fatalf("expected only onlyA deleted (1 node), got %d", summary.Nodes)
	}

	remainingNodes, err := m.GetNodesByIDs([]string{"shared", "onlyA"})
	if err != nil {
		t.Fatalf("GetNodesByIDs: %v", err)
	}
	var remainingIDs []string
	for _, n := range remainingNodes {
		remainingIDs = append(remainingIDs, n.EquipmentID)
	}
	sort.Strings(remainingIDs)
	if len(remainingIDs) != 1 || remainingIDs[0] != "shared" {
		t.Fatalf("expected only 'shared' to survive stationA's deletion, got %+v", remainingIDs)
	}

	// Now delete stationB too: "shared" loses its last remaining
	// reference and must finally disappear.
	summary2, err := m.DeleteContainers([]string{"stationB"})
	if err != nil {
		t.Fatalf("DeleteContainers(stationB): %v", err)
	}
	if summary2.Nodes != 1 {
		t.Fatalf("expected 'shared' to finally be deleted, got summary %+v", summary2)
	}
	if got, err := m.GetNodesByIDs([]string{"shared"}); err != nil || len(got) != 0 {
		t.Fatalf("expected 'shared' gone after stationB deletion, got %+v (err %v)", got, err)
	}
}
