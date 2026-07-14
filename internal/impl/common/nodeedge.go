// Package common — this file builds the actual model.Node/model.Edge
// objects from the resolved EquipmentTerminals map (see terminals.go). This
// is the step right after reference resolution: every ConnectivityNode
// becomes a Node (see Konzept.md/model decision: "ConnectivityNode wird zu
// Node", uniformly, no special-casing); every Equipment with two terminals
// becomes an Edge; every Equipment with one terminal (single-terminal
// source/sink, e.g. EnergyConsumer/PowerElectronicsConnection/
// SynchronousMachine) becomes an Edge whose second connection is the
// single, shared GND node.
//
// BusbarSection is the one documented exception (found + fixed with the
// user 2026-07-13): real CGMES data gives it exactly one Terminal too, but
// it is NOT a source/sink — it is purely a Node-role marker for its own
// ConnectivityNode (see Konzept.md, "individual BusbarSection objects ARE
// Nodes"). Wiring it to GND like a genuine single-terminal source/sink was
// a bug: it silently linked otherwise-unrelated busbars together through
// the one shared GND node, masking real disconnected-busbar anomalies that
// checkConnectivity (consistency.go) should have caught. BusbarSection IDs
// (passed in via nodeOnlyIDs) contribute only their own Node1 to the Nodes
// set — no Edge, no GND link.
//
// Junction is the second Node-role exception (see terminals.go's
// nodeRoleClasses/ExtraNodes): a branching splice can have 3+ Terminals,
// all belonging to the same physical point — its Node1 AND ExtraNodes both
// contribute to the Nodes set (already merged onto one canonical ID by
// junctionmerge.go's MergeJunctionNodes before this function runs), and
// like BusbarSection it must be included in nodeOnlyIDs so no Edge/GND
// link is created for it.
package common

import (
	"sort"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// GNDNodeID is the well-known, singleton ID of the virtual ground/earth
// node. Every single-terminal source/sink Equipment's second connection
// points here — there is exactly one GND node per model, shared by all of
// them (not one per Equipment).
const GNDNodeID = "GND"

// BuildNodesAndEdges turns the resolved EquipmentTerminals map into
// model.Node and model.Edge values. Nodes are deduplicated (a
// ConnectivityNode referenced by many Equipments still yields one Node),
// and the GND node is added exactly once, only if at least one genuine
// single-terminal source/sink Equipment actually needs it. nodeOnlyIDs
// marks Equipment (BusbarSection and Junction) that is NOT a source/sink
// despite having one Terminal (or, for Junction, possibly several) — it
// contributes only its own node(s) (Node1 plus ExtraNodes, if any) to the
// Nodes set; no Edge is created for it at all (see this file's doc
// comment).
func BuildNodesAndEdges(resolved map[string]EquipmentTerminals, nodeOnlyIDs map[string]bool) ([]coremodel.Node, []coremodel.Edge) {
	nodeIDs := map[string]bool{}
	needsGND := false

	var equipmentIDs []string
	for eqID := range resolved {
		equipmentIDs = append(equipmentIDs, eqID)
	}
	sort.Strings(equipmentIDs)

	edges := make([]coremodel.Edge, 0, len(equipmentIDs))
	for _, eqID := range equipmentIDs {
		et := resolved[eqID]
		nodeIDs[et.Node1] = true
		for _, extra := range et.ExtraNodes {
			nodeIDs[extra] = true
		}

		if et.Node2 == "" && nodeOnlyIDs[eqID] {
			continue // BusbarSection/Junction: Node-role marker only, no Edge
		}

		terminal2 := et.Node2
		if terminal2 == "" {
			terminal2 = GNDNodeID
			needsGND = true
		} else {
			nodeIDs[terminal2] = true
		}

		edges = append(edges, coremodel.Edge{
			EquipmentID:     eqID,
			Terminal1NodeID: et.Node1,
			Terminal2NodeID: terminal2,
		})
	}

	var nodeIDList []string
	for id := range nodeIDs {
		nodeIDList = append(nodeIDList, id)
	}
	sort.Strings(nodeIDList)

	nodes := make([]coremodel.Node, 0, len(nodeIDList)+1)
	for _, id := range nodeIDList {
		nodes = append(nodes, coremodel.Node{EquipmentID: id, Kind: coremodel.NodeKindReal})
	}
	if needsGND {
		nodes = append(nodes, coremodel.Node{EquipmentID: GNDNodeID, Kind: coremodel.NodeKindVirtual})
	}

	return nodes, edges
}
