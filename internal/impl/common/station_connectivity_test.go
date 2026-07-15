package common

import (
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// TestCheckStationConnectivity_DetectsDisconnectedIslandWithinStation is the
// positive test for the new check (2026-07-14): a Substation with two
// internally-disconnected equipment islands must be flagged, so we don't
// just rely on "0 violations on real datasets" as evidence the check works.
func TestCheckStationConnectivity_DetectsDisconnectedIslandWithinStation(t *testing.T) {
	cr := &BuildContainersResult{
		Containers: []coremodel.Container{
			{ID: "S1", Type: ContainerTypeSubstation, ParentID: ""},
		},
		EquipmentToCont: map[string]string{
			"A":  "S1",
			"B":  "S1",
			"AB": "S1",
			"C":  "S1",
			"D":  "S1",
			"CD": "S1",
		},
	}
	nodes := []coremodel.Node{
		{EquipmentID: "A"},
		{EquipmentID: "B"},
		{EquipmentID: "C"},
		{EquipmentID: "D"},
	}
	edges := []coremodel.Edge{
		// island 1: A-B connected via equipment "AB"
		{EquipmentID: "AB", Terminal1NodeID: "A", Terminal2NodeID: "B"},
		// island 2: C-D connected via equipment "CD" — NOT linked to island 1
		{EquipmentID: "CD", Terminal1NodeID: "C", Terminal2NodeID: "D"},
	}

	violations := checkStationConnectivity(nodes, edges, cr)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 station-connectivity violation for the disconnected island, got %d: %+v", len(violations), violations)
	}
	v := violations[0]
	if v.Rule != "station-connectivity" {
		t.Errorf("expected Rule=station-connectivity, got %q", v.Rule)
	}
	if v.ObjectID != "S1" {
		t.Errorf("expected ObjectID=S1, got %q", v.ObjectID)
	}
}

// TestCheckStationConnectivity_ConnectedStationHasNoViolation verifies no
// false positive when the station is fully internally connected.
func TestCheckStationConnectivity_ConnectedStationHasNoViolation(t *testing.T) {
	cr := &BuildContainersResult{
		Containers: []coremodel.Container{
			{ID: "S1", Type: ContainerTypeSubstation, ParentID: ""},
		},
		EquipmentToCont: map[string]string{
			"A":  "S1",
			"B":  "S1",
			"C":  "S1",
			"AB": "S1",
			"BC": "S1",
		},
	}
	nodes := []coremodel.Node{
		{EquipmentID: "A"},
		{EquipmentID: "B"},
		{EquipmentID: "C"},
	}
	edges := []coremodel.Edge{
		{EquipmentID: "AB", Terminal1NodeID: "A", Terminal2NodeID: "B"},
		{EquipmentID: "BC", Terminal1NodeID: "B", Terminal2NodeID: "C"},
	}

	violations := checkStationConnectivity(nodes, edges, cr)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for a fully connected station, got %d: %+v", len(violations), violations)
	}
}

// TestCheckStationConnectivity_GNDNotTraversed verifies that two
// single-terminal pieces of equipment sharing only the virtual GND node do
// NOT get merged into one component — GND must never be traversed.
func TestCheckStationConnectivity_GNDNotTraversed(t *testing.T) {
	cr := &BuildContainersResult{
		Containers: []coremodel.Container{
			{ID: "S1", Type: ContainerTypeSubstation, ParentID: ""},
		},
		EquipmentToCont: map[string]string{
			"A": "S1",
			"B": "S1",
		},
	}
	nodes := []coremodel.Node{
		{EquipmentID: "A"},
		{EquipmentID: "B"},
	}
	edges := []coremodel.Edge{
		// both A and B are single-terminal equipment whose Terminal2 points
		// at the shared virtual GND sentinel — this must NOT be treated as
		// a real connection between A and B.
		{EquipmentID: "A", Terminal1NodeID: "A", Terminal2NodeID: GNDNodeID},
		{EquipmentID: "B", Terminal1NodeID: "B", Terminal2NodeID: GNDNodeID},
	}

	violations := checkStationConnectivity(nodes, edges, cr)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (A and B are two separate islands, GND excluded), got %d: %+v", len(violations), violations)
	}
}

// TestCheckConnectivity_GNDNotTraversed is the regression test for the bug
// fixed 2026-07-14 (found while building checkStationConnectivity):
// checkConnectivity used to union() through GNDNodeID unconditionally,
// TestCheckStationConnectivity_OutOfScopeContainerTypeIgnored verifies that
// containers of a type not in {Substation, House, DistributionBox, ACLine}
// (e.g. a Bay, which is a sub-container of a Substation, not itself a
// top-level owner) never produce violations directly — only the top-level
// owner resolved via stationOwnerOf matters.
func TestCheckStationConnectivity_JunctionOutOfScope(t *testing.T) {
	cr := &BuildContainersResult{
		Containers: []coremodel.Container{
			{ID: "J1", Type: "junction", ParentID: ""},
		},
		EquipmentToCont: map[string]string{
			"A": "J1",
			"B": "J1",
		},
	}
	nodes := []coremodel.Node{
		{EquipmentID: "A"},
		{EquipmentID: "B"},
	}
	edges := []coremodel.Edge{}

	violations := checkStationConnectivity(nodes, edges, cr)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for out-of-scope Junction container, got %d: %+v", len(violations), violations)
	}
}
