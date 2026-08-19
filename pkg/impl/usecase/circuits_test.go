package usecase_test

import (
	"sort"
	"strings"
	"testing"

	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/impl/common"
	"github.com/ame89/jag/pkg/impl/usecase"
	"github.com/ame89/jag/pkg/sqlite"
)

// setupCircuitService seeds an in-memory SQLite ModelStore modeling:
//   - a Doppelsammelschiene (double busbar): two separate Busbar Containers
//     (busbarA/busbarB), each with one BusbarSection Equipment (busSecA/
//     busSecB, Node role), joined by a Kuppelschalter (coupler, Edge role,
//     persisted Sachdaten "cim_class"="Breaker" and "Switch.open").
//   - a PowerTransformer (trafo, Edge role, persisted "cim_class"=
//     "PowerTransformer") whose OS side hangs off busSecA and whose US side
//     feeds a cable (cableUS) to a further Node (nodeX) — used to verify
//     the galvanic-decoupling / two-Circuit boundary behavior.
//
// All Sachdaten needed for classification (cim_class, Switch.open) are
// seeded as ordinary model_attribute rows via technical.Store.Upsert, NOT
// staging records — exactly the persisted-Sachdaten-only path
// GetSchaltkreis/GetSchaltkreise are built on.
func setupCircuitService(t *testing.T, couplerOpenDefault bool) (*usecase.Service, *sqlite.ModelStore) {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening sqlite store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	m := store.Model()

	containers := []coremodel.Container{
		{ID: "station1", Type: "substation", ParentID: ""},
		{ID: "busbarA", Type: "busbar", ParentID: "station1"},
		{ID: "busbarB", Type: "busbar", ParentID: "station1"},
		{ID: "bay1", Type: "bay", ParentID: "station1"},
	}
	if err := m.UpsertContainers(containers); err != nil {
		t.Fatalf("upserting containers: %v", err)
	}

	equipment := []coremodel.Equipment{
		{ID: "busSecA", ContainerID: "busbarA"},
		{ID: "busSecB", ContainerID: "busbarB"},
		{ID: "coupler", ContainerID: "bay1"},
		{ID: "trafo", ContainerID: "bay1"},
		{ID: "cableUS", ContainerID: "bay1"},
	}
	if err := m.UpsertEquipment(equipment); err != nil {
		t.Fatalf("upserting equipment: %v", err)
	}

	nodes := []coremodel.Node{
		{EquipmentID: "busSecA", Kind: coremodel.NodeKindReal},
		{EquipmentID: "busSecB", Kind: coremodel.NodeKindReal},
		{EquipmentID: "nodeOS", Kind: coremodel.NodeKindReal},
		{EquipmentID: "nodeUS", Kind: coremodel.NodeKindReal},
		{EquipmentID: "nodeX", Kind: coremodel.NodeKindReal},
	}
	if err := m.UpsertNodes(nodes); err != nil {
		t.Fatalf("upserting nodes: %v", err)
	}

	edges := []coremodel.Edge{
		{EquipmentID: "coupler", Terminal1NodeID: "busSecA", Terminal2NodeID: "busSecB"},
		// trafo's own busSecA/nodeOS connection is irrelevant to the
		// coupler scenario, so busSecA is reused directly as trafo's OS
		// terminal to keep the fixture small.
		{EquipmentID: "trafo", Terminal1NodeID: "busSecA", Terminal2NodeID: "nodeUS"},
		{EquipmentID: "cableUS", Terminal1NodeID: "nodeUS", Terminal2NodeID: "nodeX"},
	}
	if err := m.UpsertEdges(edges); err != nil {
		t.Fatalf("upserting edges: %v", err)
	}
	_ = nodes // nodeOS intentionally unused/unwired in this fixture (see below tests that add it directly)

	couplerOpenVal := "false"
	if couplerOpenDefault {
		couplerOpenVal = "true"
	}
	attrs := []coremodel.Attribute{
		{OwnerID: "coupler", Key: common.AttributeKeyClass, Value: "Breaker"},
		{OwnerID: "coupler", Key: "Switch.open", Value: couplerOpenVal},
		{OwnerID: "trafo", Key: common.AttributeKeyClass, Value: "PowerTransformer"},
		{OwnerID: "cableUS", Key: common.AttributeKeyClass, Value: "ACLineSegment"},
	}
	if err := m.UpsertAttributes(attrs); err != nil {
		t.Fatalf("upserting attributes: %v", err)
	}

	svc := usecase.NewService(
		sqlite.ContainerAdapter{ModelStore: m},
		sqlite.EquipmentAdapter{ModelStore: m},
		sqlite.GeometryAdapter{ModelStore: m},
		m,
		sqlite.ElectricalAdapter{ModelStore: m},
		sqlite.TechnicalAdapter{ModelStore: m},
	)
	return svc, m
}

func circuitIDs(circuits []*common.Circuit) []string {
	ids := make([]string, len(circuits))
	for i, c := range circuits {
		ids[i] = c.ID
	}
	sort.Strings(ids)
	return ids
}

// TestGetSchaltkreis_Transformer_TwoCircuits verifies the galvanic
// decoupling rule: a PowerTransformer Edge always touches two distinct
// Circuits (OS side and US side never union), regardless of switchStates.
func TestGetSchaltkreis_Transformer_TwoCircuits(t *testing.T) {
	svc, _ := setupCircuitService(t, false) // coupler closed by default

	circuits, err := svc.GetSchaltkreis("trafo", nil)
	if err != nil {
		t.Fatalf("GetSchaltkreis(trafo): %v", err)
	}
	if len(circuits) != 2 {
		t.Fatalf("expected 2 Circuits for a PowerTransformer boundary Edge, got %d: %+v", len(circuits), circuits)
	}
}

// TestGetSchaltkreis_NormalEdge_OneCircuit verifies the common case: an
// ordinary cable Edge touches exactly one Circuit.
func TestGetSchaltkreis_NormalEdge_OneCircuit(t *testing.T) {
	svc, _ := setupCircuitService(t, false)

	circuits, err := svc.GetSchaltkreis("cableUS", nil)
	if err != nil {
		t.Fatalf("GetSchaltkreis(cableUS): %v", err)
	}
	if len(circuits) != 1 {
		t.Fatalf("expected 1 Circuit for an ordinary cable Edge, got %d: %+v", len(circuits), circuits)
	}
}

// TestGetSchaltkreis_ContainerID_ErrorMentionsTopology verifies that
// passing a Container ID (Bay/Substation/Busbar-Container) instead of an
// Edge's Equipment ID fails with an actionable error mentioning the
// physical topology (Node/Edge) requirement, per explicit user request.
func TestGetSchaltkreis_ContainerID_ErrorMentionsTopology(t *testing.T) {
	svc, _ := setupCircuitService(t, false)

	for _, containerID := range []string{"station1", "bay1", "busbarA"} {
		_, err := svc.GetSchaltkreis(containerID, nil)
		if err == nil {
			t.Fatalf("GetSchaltkreis(%s): expected an error (Container ID is not an Edge), got nil", containerID)
		}
		msg := err.Error()
		if !strings.Contains(msg, "physical topology") || !strings.Contains(msg, "Node") || !strings.Contains(msg, "Edge") {
			t.Errorf("GetSchaltkreis(%s): expected error to mention the physical topology (Node/Edge) requirement, got: %v", containerID, err)
		}
	}
}

// TestGetSchaltkreise_DoubleBusbar_CouplerOpen_TwoCircuits verifies the
// Doppelsammelschiene scenario: with the Kuppelschalter open (its persisted
// import default), both Busbar-Containers' Circuits are distinct.
func TestGetSchaltkreise_DoubleBusbar_CouplerOpen_TwoCircuits(t *testing.T) {
	svc, _ := setupCircuitService(t, true) // coupler open by default

	circuits, err := svc.GetSchaltkreise([]string{"busbarA", "busbarB"}, nil)
	if err != nil {
		t.Fatalf("GetSchaltkreise(busbarA, busbarB): %v", err)
	}
	if len(circuits) != 2 {
		t.Fatalf("expected 2 Circuits (coupler open), got %d: %+v", len(circuits), circuitIDs(circuits))
	}
}

// TestGetSchaltkreise_DoubleBusbar_CouplerClosed_OneCircuit verifies the
// counterpart: with the coupler's persisted default closed, both bars form
// exactly one Circuit.
func TestGetSchaltkreise_DoubleBusbar_CouplerClosed_OneCircuit(t *testing.T) {
	svc, _ := setupCircuitService(t, false) // coupler closed by default

	circuits, err := svc.GetSchaltkreise([]string{"busbarA", "busbarB"}, nil)
	if err != nil {
		t.Fatalf("GetSchaltkreise(busbarA, busbarB): %v", err)
	}
	if len(circuits) != 1 {
		t.Fatalf("expected 1 Circuit (coupler closed), got %d: %+v", len(circuits), circuitIDs(circuits))
	}
}

// TestGetSchaltkreise_SwitchStatesOverride verifies switchStates overrides
// the persisted import default: starting from "coupler open by default"
// (2 Circuits), explicitly overriding it to closed merges the two bars
// into 1 Circuit; the reverse (default closed, override to open) splits 1
// Circuit into 2.
func TestGetSchaltkreise_SwitchStatesOverride(t *testing.T) {
	t.Run("override_open_default_to_closed", func(t *testing.T) {
		svc, _ := setupCircuitService(t, true) // default open

		circuits, err := svc.GetSchaltkreise([]string{"busbarA", "busbarB"}, map[string]bool{"coupler": false})
		if err != nil {
			t.Fatalf("GetSchaltkreise with override: %v", err)
		}
		if len(circuits) != 1 {
			t.Fatalf("expected 1 Circuit after overriding coupler to closed, got %d: %+v", len(circuits), circuitIDs(circuits))
		}
	})

	t.Run("override_closed_default_to_open", func(t *testing.T) {
		svc, _ := setupCircuitService(t, false) // default closed

		circuits, err := svc.GetSchaltkreise([]string{"busbarA", "busbarB"}, map[string]bool{"coupler": true})
		if err != nil {
			t.Fatalf("GetSchaltkreise with override: %v", err)
		}
		if len(circuits) != 2 {
			t.Fatalf("expected 2 Circuits after overriding coupler to open, got %d: %+v", len(circuits), circuitIDs(circuits))
		}
	})
}

// TestGetSchaltkreise_EmptyOverrides_UseImportDefaults verifies nil and an
// empty (non-nil) switchStates map behave identically — both fall back to
// the persisted import default for every switch, per the explicit
// nil/empty-map requirement.
func TestGetSchaltkreise_EmptyOverrides_UseImportDefaults(t *testing.T) {
	svc, _ := setupCircuitService(t, true) // coupler open by default

	withNil, err := svc.GetSchaltkreise([]string{"busbarA", "busbarB"}, nil)
	if err != nil {
		t.Fatalf("GetSchaltkreise(nil): %v", err)
	}
	withEmpty, err := svc.GetSchaltkreise([]string{"busbarA", "busbarB"}, map[string]bool{})
	if err != nil {
		t.Fatalf("GetSchaltkreise(empty map): %v", err)
	}
	if len(withNil) != len(withEmpty) {
		t.Errorf("expected nil and empty switchStates to behave identically, got %d vs %d Circuits", len(withNil), len(withEmpty))
	}
}

// TestGetSchaltkreise_SingleBusbar_OneCircuit verifies the common,
// non-Doppelsammelschiene case: a single Busbar-Container yields exactly
// one Circuit.
func TestGetSchaltkreise_SingleBusbar_OneCircuit(t *testing.T) {
	svc, _ := setupCircuitService(t, true)

	circuits, err := svc.GetSchaltkreise([]string{"busbarA"}, nil)
	if err != nil {
		t.Fatalf("GetSchaltkreise(busbarA): %v", err)
	}
	if len(circuits) != 1 {
		t.Fatalf("expected 1 Circuit for a single Busbar-Container, got %d: %+v", len(circuits), circuitIDs(circuits))
	}
}

// TestGetSchaltkreise_UnknownContainerID_Error verifies a not-found
// Busbar-Container ID surfaces an actionable error rather than an empty,
// silently-successful result.
func TestGetSchaltkreise_UnknownContainerID_Error(t *testing.T) {
	svc, _ := setupCircuitService(t, false)

	_, err := svc.GetSchaltkreise([]string{"does-not-exist"}, nil)
	if err == nil {
		t.Fatalf("expected an error for an unknown Busbar-Container ID, got nil")
	}
}

// TestGetSchaltkreise_NoContainerIDs_Error verifies calling GetSchaltkreise
// with no Busbar-Container IDs at all is rejected up front.
func TestGetSchaltkreise_NoContainerIDs_Error(t *testing.T) {
	svc, _ := setupCircuitService(t, false)

	_, err := svc.GetSchaltkreise(nil, nil)
	if err == nil {
		t.Fatalf("expected an error when no Busbar-Container IDs are given, got nil")
	}
}
