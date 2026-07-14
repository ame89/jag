// Package common — this file unifies a node-role Equipment's (currently:
// Junction) own several ConnectivityNodes onto one canonical Node ID.
//
// Background (decided explicitly with the user, NSC import investigation):
// a Junction (Kabelmuffe) can be a branching splice (Abzweigmuffe/T-Muffe)
// with 3+ Terminals — one per cable segment meeting at that physical
// point. classifyNodeRoleTerminals (terminals.go) already collects all of
// a Junction's distinct ConnectivityNode IDs into Node1 (one of them) plus
// ExtraNodes (the rest); this file unifies them onto one canonical ID so
// BuildNodesAndEdges produces exactly one Node for the splice, not several
// disconnected ones.
//
// Unlike MergeBusbarSectionNodes, no connected-components analysis is
// needed here: a Junction's Terminals all belong to the SAME Equipment, so
// its own Node1 + ExtraNodes are unconditionally the same physical point —
// there is no "genuinely disconnected, leave alone" case to distinguish
// (that distinction only matters when unifying nodes across DIFFERENT
// Equipment, as busbarmerge.go does for BusbarSection).
package common

import "sort"

// MergeJunctionNodes returns a copy of resolved with every node-role
// Equipment's (nodeRoleIDs) own ConnectivityNodes remapped onto one
// canonical ID (the lexicographically smallest of the group). The remap is
// applied across the WHOLE resolved map, since other Equipment may
// reference the same ConnectivityNode IDs via their own Node1/Node2/
// ExtraNodes.
func MergeJunctionNodes(resolved map[string]EquipmentTerminals, nodeRoleIDs map[string]bool) map[string]EquipmentTerminals {
	remap := map[string]string{}
	var ids []string
	for eqID := range nodeRoleIDs {
		ids = append(ids, eqID)
	}
	sort.Strings(ids)
	for _, eqID := range ids {
		et, ok := resolved[eqID]
		if !ok || len(et.ExtraNodes) == 0 {
			continue // single own node, or not resolved (anomaly) — nothing to unify
		}
		all := append([]string{et.Node1}, et.ExtraNodes...)
		sort.Strings(all)
		canonical := all[0]
		for _, n := range all[1:] {
			if n != canonical {
				remap[n] = canonical
			}
		}
	}
	if len(remap) == 0 {
		return resolved
	}

	apply := func(id string) string {
		if r, ok := remap[id]; ok {
			return r
		}
		return id
	}
	out := make(map[string]EquipmentTerminals, len(resolved))
	for eqID, et := range resolved {
		var newExtra []string
		if len(et.ExtraNodes) > 0 {
			newExtra = make([]string, len(et.ExtraNodes))
			for i, n := range et.ExtraNodes {
				newExtra[i] = apply(n)
			}
		}
		out[eqID] = EquipmentTerminals{
			EquipmentID: et.EquipmentID,
			Node1:       apply(et.Node1),
			Node2:       apply(et.Node2),
			ExtraNodes:  newExtra,
		}
	}
	return out
}
