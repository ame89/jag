// Package common — this file builds the actual model.Node/model.Edge
// objects from the resolved EquipmentTerminals map (see terminals.go). This
// is the step right after reference resolution: every ConnectivityNode
// becomes a Node (see Konzept.md/model decision: "ConnectivityNode wird zu
// Node", uniformly, no special-casing); every Equipment with two terminals
// becomes an Edge; every Equipment with one terminal (single-terminal
// source/sink) becomes an Edge whose second connection is the single,
// shared GND node.
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
// and the GND node is added exactly once, only if at least one
// single-terminal Equipment actually needs it.
func BuildNodesAndEdges(resolved map[string]EquipmentTerminals) ([]coremodel.Node, []coremodel.Edge) {
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
