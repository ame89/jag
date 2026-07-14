// Package electrical defines the storage abstraction for the electrical
// topology view (see Konzept.md, "Topologie") — the Zero-Ohm-reduced
// grouping of the physical Node graph (closed switches/fuses/disconnectors
// collapse a set of real Nodes into one shared electrical group; open
// switches interrupt it). Pure interface + data only — no domain/business
// logic lives here (see Impl.md, Ports & Adapters).
//
// Deliberately no new Node/Edge objects are introduced for this view (see
// Konzept.md's explicit decision) — merged real Nodes simply share a
// common group ID, computed import-driven (Phase 2/5), not per-query.
package electrical

// Store persists the electrical group assignment (Node ID -> group ID)
// computed at import/merge time (see Konzept.md — the critical moment is
// Phase 5's merge of independently-computed partial-model groupings, not
// every individual read).
type Store interface {
	// GetElectricalGroup returns the electrical group ID for each of the
	// given Node IDs. Node IDs with no group assignment are omitted from
	// the result map (not an error).
	GetElectricalGroup(nodeIDs []string) (map[string]string, error)
	// GetGroupMembers returns every Node ID belonging to any of the given
	// group IDs.
	GetGroupMembers(groupIDs []string) ([]string, error)
	// GroupSizes returns, for every distinct group ID currently persisted,
	// the number of Nodes assigned to it — computed DB-side (a single
	// GROUP BY query, not a full node-ID scan through Go), so a caller can
	// report "N circuits, sizes desc" straight from the final model
	// without loading every Node ID into memory first.
	GroupSizes() (map[string]int, error)
	// Upsert inserts or replaces Node ID -> group ID assignments in bulk
	// (e.g. persisting a grouping freshly computed during import).
	Upsert(groups map[string]string) error
}
