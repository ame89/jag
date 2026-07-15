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
//
// A given Node ID can have MORE THAN ONE group assignment, one per
// "owner" (a station root Container ID for Pass A, or the fixed Pass B
// sentinel owner — see impl/common/pass_a_pipeline.go and pass_b.go). This
// is deliberate, not an artifact to be reconciled at write time: a raw
// ConnectivityNode legitimately shared by equipment from two different
// stations (a real cross-station switch coupling — confirmed in
// examples/cgmes/ReliCapGrid_Espheim) gets one independently-correct,
// locally-computed group per owning station, rather than a single value
// that would otherwise be arbitrarily overwritten depending on which
// station's Pass A worker happens to finish last. Callers needing "are
// these two Nodes electrically connected" semantics must treat a
// multi-group Node as a union/expansion point across all of its groups
// (see impl/usecase.ElectricallyConnected).
type Store interface {
	// GetElectricalGroup returns every electrical group ID assigned to
	// each of the given Node IDs (usually exactly one; more than one for a
	// boundary Node shared by multiple owners). Node IDs with no group
	// assignment are omitted from the result map (not an error).
	GetElectricalGroup(nodeIDs []string) (map[string][]string, error)
	// GetGroupMembers returns every Node ID belonging to any of the given
	// group IDs.
	GetGroupMembers(groupIDs []string) ([]string, error)
	// GroupSizes returns, for every distinct group ID currently persisted,
	// the number of distinct Nodes assigned to it — computed DB-side (a
	// single GROUP BY query, not a full node-ID scan through Go), so a
	// caller can report "N circuits, sizes desc" straight from the final
	// model without loading every Node ID into memory first.
	GroupSizes() (map[string]int, error)
	// Upsert replaces each owner's own Node ID -> group ID assignment in
	// bulk (owned is keyed by owner ID). Every owner's contribution is
	// replaced wholesale (delete-then-insert its own rows only), so
	// concurrent or repeated calls for different owners never clobber
	// each other's rows, and a single owner's changed local grouping
	// (e.g. a switch's default state flipping between closed and open on
	// re-import) is always correctly reflected from scratch.
	Upsert(owned map[string]map[string]string) error
}
