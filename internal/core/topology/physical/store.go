// Package physical defines the storage abstraction for the physical
// topology (see Konzept.md, "Topologie"): all installed Edges, regardless of
// electrical/switching state. No logic lives here.
package physical

import "gitlab.com/openk-nsc/jag/internal/core/model"

// Store is the bulk-oriented storage abstraction for the physical topology.
// Backends (sqlite, postgres, file) each implement this interface.
type Store interface {
	// GetEdgesByNodeIDs returns all Edges that have Terminal1NodeID or
	// Terminal2NodeID among the given node IDs.
	GetEdgesByNodeIDs(nodeIDs []string) ([]model.Edge, error)

	// GetReachableNodes returns all node IDs reachable from the given
	// root node IDs via the physical topology (switching state is
	// ignored). Backends implement the traversal DB-side (e.g. via WITH
	// RECURSIVE), not via hop-by-hop Go-side recursion.
	GetReachableNodes(rootNodeIDs []string) ([]string, error)

	// Upsert bulk-inserts/updates the given edges.
	Upsert(edges []model.Edge) error
}
