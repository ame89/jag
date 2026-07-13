// Package electrical defines the storage abstraction for the electrical
// topology (see Konzept.md, "Topologie"): the Zero-Ohm-reduced view, where
// closed switches/fuses/disconnectors are collapsed away and open switches
// are treated as an interruption. No logic lives here — computing the
// grouping itself (including the CGMES-hybrid cross-check) is business
// logic living in /internal/impl.
package electrical

// Store is the bulk-oriented storage abstraction for the electrical
// topology grouping. Backends (sqlite, postgres, file) each
// implement this interface.
//
// GroupID is a purely internal identifier (no CGMES relation). For CGMES
// sources, it is additionally cross-checked against
// ConnectivityNode.TopologicalNode; a mismatch is a hard import error
// (Phase 3/4), not a silent adoption — see Konzept.md's CGMES hybrid
// decision.
type Store interface {
	// GetElectricalGroup returns, for each requested node ID, the
	// Zero-Ohm group it currently belongs to.
	GetElectricalGroup(nodeIDs []string) (map[string]string, error)

	// GetGroupMembers returns all node IDs belonging to the given group
	// IDs.
	GetGroupMembers(groupIDs []string) ([]string, error)

	// Upsert bulk-inserts/updates the given node-ID-to-group-ID mapping,
	// e.g. the grouping computed during Phase 2 import.
	Upsert(groups map[string]string) error
}
