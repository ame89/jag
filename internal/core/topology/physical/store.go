// Package physical defines the storage abstraction for the physical
// topology view (see Konzept.md, "Topologie") — the full Node/Edge graph of
// everything actually installed, independent of switching state. Pure
// interface + data only — no domain/business logic lives here (see
// Impl.md, Ports & Adapters).
//
// Node storage is included here (not a separate package) because a Node
// only has meaning in the context of the physical graph it participates
// in — there is no standalone "Node store" concept elsewhere in Konzept.md.
package physical

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Store persists coremodel.Node and coremodel.Edge values (the physical
// topology graph). The electrical topology (Zero-Ohm-reduced view) is a
// deliberately separate interface (see topology/electrical) — it never
// introduces new Node/Edge objects of its own, only a grouping over the
// Node IDs stored here.
type Store interface {
	// GetNodesByIDs returns Node entries for the given IDs. IDs with no
	// matching Node are silently omitted from the result (not an error).
	GetNodesByIDs(ids []string) ([]coremodel.Node, error)
	// GetEdgesByNodeIDs returns every Edge touching at least one of the
	// given Node IDs (via either terminal). Implementations should back
	// this with an indexed node_id -> edge_id lookup (bridge table), not a
	// "terminal1 IN (...) OR terminal2 IN (...)" join, which can defeat
	// index usage (see Idee.md's graph-traversal performance guidance).
	GetEdgesByNodeIDs(nodeIDs []string) ([]coremodel.Edge, error)
	// GetEdgesByEquipmentIDs returns Edge entries by their own Equipment
	// ID (the Edge's primary key) — useful when a caller already has a
	// set of Equipment IDs (e.g. from hierarchy.EquipmentStore.
	// GetByContainerIDs, see Usecases.md UC1) and wants their Edge role,
	// as opposed to GetEdgesByNodeIDs' node-centric lookup.
	GetEdgesByEquipmentIDs(equipmentIDs []string) ([]coremodel.Edge, error)
	// GetReachableNodes returns every Node ID reachable from the given
	// root Node IDs via the physical graph (ignores switching state — a
	// physical, not electrical, traversal). Computed DB-side (e.g. a
	// recursive CTE), never via Go-side recursive traversal (see Idee.md's
	// "avoid recursion in Go application code" rule) — a recursive CTE is
	// fixpoint iteration executed by the DB engine as a single query, not
	// stack recursion.
	GetReachableNodes(rootNodeIDs []string) ([]string, error)
	// UpsertNodes inserts or replaces Node entries in bulk.
	UpsertNodes(nodes []coremodel.Node) error
	// UpsertEdges inserts or replaces Edge entries in bulk.
	UpsertEdges(edges []coremodel.Edge) error
}
