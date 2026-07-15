package common

// FlagStore is an OPTIONAL capability a staging.Store backend may
// implement (type-asserted, never required) to support the ephemeral
// existence-flag mechanism decided with the user 2026-07-15 for the two
// Phase 3 checks that fundamentally need whole-model visibility to detect
// an ABSENCE ("unreferenced-node", "equipment-without-container" — see
// consistency.go): as Pass A/Pass B batches process their own small
// chunk, they mark each ID they touch with the relevant flag kind
// (FlagReferencedNode/FlagInstalledEquipment/FlagContainedEquipment).
// Once every batch has run, a single paged scan (chunk by chunk, never
// the whole model at once) asks which IDs are still unflagged — an empty
// result means no anomaly. This is deliberately NOT part of the core
// staging.Store interface (the user explicitly delegated this as a pure
// implementation detail, not interface/API-relevant) — a backend that
// doesn't implement FlagStore simply skips these two checks (no hard
// dependency).
//
// Concurrency note: two different workers/batches may legitimately see
// the same boundary ID (e.g. a shared ConnectivityNode between a Pass A
// station batch and a Pass B ACLineSegment) — MarkFlags must never let a
// flag be "downgraded" once set (an INSERT OR IGNORE semantics, not a
// plain overwrite), which the sqlite implementation guarantees.
type FlagStore interface {
	// MarkFlags records that each of ids has reached the given kind's
	// milestone for this import version. Idempotent/safe under concurrent
	// callers marking overlapping IDs.
	MarkFlags(version uint64, kind string, ids []string) error
	// UnmarkedIDs returns the subset of ids that do NOT carry the given
	// kind's flag for this version yet — the caller supplies a bounded
	// chunk (e.g. one page of a class scan), never the whole model's IDs
	// at once.
	UnmarkedIDs(version uint64, kind string, ids []string) ([]string, error)
	// PagedFlagIDs pages through every ID flagged with kind for this
	// version, in ID order, limit at a time (afterID="" starts from the
	// beginning) — lets a caller enumerate a flag's own universe (e.g.
	// "every equipment ID Pass A/B ever marked as installed") without
	// holding the whole list in memory.
	PagedFlagIDs(version uint64, kind string, afterID string, limit int) ([]string, error)
	// ClearFlags deletes every flag row for this version — call once
	// Phase 3's final flagged completeness scans have run; these flags are
	// purely ephemeral import-time bookkeeping, never part of the
	// permanent model.
	ClearFlags(version uint64) error
}

// Flag kinds used by the two flagged Phase 3 completeness checks.
const (
	// FlagReferencedNode marks a ConnectivityNode/TopologicalNode ID that
	// at least one Equipment's Terminal actually referenced (i.e. it
	// contributed to a built Node) — see checkUnreferencedNodesFlagged.
	FlagReferencedNode = "referenced_node"
	// FlagInstalledEquipment marks an Equipment ID whose own Terminals
	// were successfully resolved (i.e. it got a Node/Edge built at all) —
	// the enumeration universe for checkEquipmentWithoutContainerFlagged.
	FlagInstalledEquipment = "installed_equipment"
	// FlagContainedEquipment marks an Equipment ID that was assigned a
	// container — see checkEquipmentWithoutContainerFlagged.
	FlagContainedEquipment = "contained_equipment"
)
