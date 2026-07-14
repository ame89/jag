// Package common — shared batch-lookup helpers. Loading one object at a
// time from the staging store in a loop is always the wrong approach (see
// Idee.md's bulk-operations mandate) — every Phase 2 step that needs
// records for many object IDs must fetch them together via
// staging.Store.GetByIDs/GetReferencesToAny, not one-by-one via
// GetByID/GetReferencesTo in a loop.
package common

import (
	"sort"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// getByIDsIndexed bulk-fetches all records for the given IDs (deduplicated)
// in one batched call and groups them by ID for O(1) lookup. Within each
// ID's group, records are sorted by (profile, attribute, seq) — the same
// deterministic order the underlying SQL query used to provide via its own
// ORDER BY, before that was removed from staging.Store.GetByIDs for
// performance reasons (see that method's doc comment). Several callers
// (e.g. classifySwitch picking the "last" Switch.open/normalOpen record
// when more than one is present, BuildObjectIndex's multi-valued attribute
// ordering) implicitly depend on this determinism — sorting here, once,
// on the already-small per-ID result is far cheaper than forcing SQL to
// sort/scan the whole table for it.
func getByIDsIndexed(store staging.Store, version uint64, ids []string) (map[string][]model.StagingRecord, error) {
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}

	records, err := store.GetByIDs(version, unique)
	if err != nil {
		return nil, err
	}

	byID := make(map[string][]model.StagingRecord, len(unique))
	for _, r := range records {
		byID[r.ID] = append(byID[r.ID], r)
	}
	for id := range byID {
		group := byID[id]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Profile != group[j].Profile {
				return group[i].Profile < group[j].Profile
			}
			if group[i].Attribute != group[j].Attribute {
				return group[i].Attribute < group[j].Attribute
			}
			return group[i].Seq < group[j].Seq
		})
	}
	return byID, nil
}

// getReferencesToAnyIndexed bulk-fetches all records referencing any of the
// given target IDs (deduplicated) in one batched call and groups them by
// target ID (the ID being referenced, i.e. record.Value) for O(1) lookup.
// Within each target's group, records are sorted by (id, profile,
// attribute, seq) — same rationale as getByIDsIndexed above.
func getReferencesToAnyIndexed(store staging.Store, version uint64, targetIDs []string) (map[string][]model.StagingRecord, error) {
	seen := make(map[string]bool, len(targetIDs))
	unique := make([]string, 0, len(targetIDs))
	for _, id := range targetIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}

	records, err := store.GetReferencesToAny(version, unique)
	if err != nil {
		return nil, err
	}

	byTarget := make(map[string][]model.StagingRecord, len(unique))
	for _, r := range records {
		byTarget[r.Value] = append(byTarget[r.Value], r)
	}
	for target := range byTarget {
		group := byTarget[target]
		sort.Slice(group, func(i, j int) bool {
			if group[i].ID != group[j].ID {
				return group[i].ID < group[j].ID
			}
			if group[i].Profile != group[j].Profile {
				return group[i].Profile < group[j].Profile
			}
			if group[i].Attribute != group[j].Attribute {
				return group[i].Attribute < group[j].Attribute
			}
			return group[i].Seq < group[j].Seq
		})
	}
	return byTarget, nil
}
