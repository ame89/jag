// Package common — this file unifies BusbarSection nodes that belong to
// the same busbar Container but would otherwise end up as separate
// connected components in the physical topology.
//
// Background (decided explicitly with the user 2026-07-13): a busbar can
// legitimately be modeled with one BusbarSection per Bay/Feld instead of
// one shared BusbarSection — every Feld electrically touches the very
// same physical bar either way. If the source provides no explicit
// connecting Equipment (e.g. a bus-tie/coupler) between those per-Bay
// BusbarSection ConnectivityNodes, the physical topology graph would show
// them as disconnected islands, even though the busbar itself is one
// continuous conductor. This is different from a genuine double-busbar
// arrangement (two physically separate bars under the same VoltageLevel),
// which stays linked via a real coupler Equipment — and since physical
// topology includes every installed element regardless of switch state
// (open switches are edges too, not removed), a real coupler already
// keeps those two bars in the same connected component without any help
// from this file.
//
// So: only BusbarSection nodes of the same busbar Container that end up in
// *different* connected components (i.e. genuinely no connecting Equipment
// at all, not even an open coupler) get unified here — onto one canonical
// node ID, chosen deterministically as the lexicographically smallest of
// the group. This is not a new virtual/synthetic construct: it recognizes
// that these separate ConnectivityNode IDs represent the same physical
// point, the same way BuildNodesAndEdges already deduplicates a
// ConnectivityNode referenced by many Equipments into one Node.
//
// This relies on the related BuildNodesAndEdges fix (see nodeedge.go):
// BusbarSection must NOT be wired to GND as if it were a single-terminal
// source/sink, or every busbar in the model would appear falsely connected
// through the shared GND node, hiding the very gap this file is meant to
// close.
package common

import "sort"

// MergeBusbarSectionNodes returns a copy of resolved with BusbarSection
// node IDs remapped as described above. busbarSectionIDs must contain
// exactly the BusbarSection Equipment IDs (e.g. derived from
// containers.EquipmentToCont, restricted to busbar-type containers).
//
// It must ALSO include any other node-role-only Equipment IDs (e.g.
// Junction, see terminals.go's nodeRoleClasses) that this function should
// not treat as a single-terminal source/sink when replicating
// BuildNodesAndEdges's topology internally (see the union-find loop below)
// — nodesByContainer still only picks up the ones whose container is
// actually busbar-typed, so including Junction IDs here is harmless for
// that part and only affects the GND-exclusion check.
func MergeBusbarSectionNodes(resolved map[string]EquipmentTerminals, containers *BuildContainersResult, busbarSectionIDs map[string]bool) map[string]EquipmentTerminals {
	busbarContainer := map[string]bool{}
	for _, c := range containers.Containers {
		if c.Type == ContainerTypeBusbar {
			busbarContainer[c.ID] = true
		}
	}

	nodesByContainer := map[string][]string{}
	for eqID := range busbarSectionIDs {
		contID := containers.EquipmentToCont[eqID]
		if !busbarContainer[contID] {
			continue
		}
		et, ok := resolved[eqID]
		if !ok || et.Node1 == "" {
			continue
		}
		nodesByContainer[contID] = append(nodesByContainer[contID], et.Node1)
	}
	if len(nodesByContainer) == 0 {
		return resolved
	}

	// Union-Find over the physical topology exactly as the (fixed)
	// BuildNodesAndEdges will produce it: every 2-terminal Equipment's two
	// nodes are unioned; every 1-terminal Equipment that is NOT a
	// BusbarSection is unioned with GND; BusbarSection's own single
	// terminal contributes no edge at all.
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if _, ok := parent[x]; !ok {
			parent[x] = x
		}
		for parent[x] != x {
			parent[x] = parent[parent[x]] // path halving
			x = parent[x]
		}
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for eqID, et := range resolved {
		if et.Node1 == "" {
			continue
		}
		find(et.Node1)
		if et.Node2 != "" {
			union(et.Node1, et.Node2)
		} else if !busbarSectionIDs[eqID] {
			union(et.Node1, GNDNodeID)
		}
	}

	// For each busbar container, if its BusbarSection nodes span more than
	// one component, unify them onto one canonical node ID.
	remap := map[string]string{}
	var containerKeys []string
	for cid := range nodesByContainer {
		containerKeys = append(containerKeys, cid)
	}
	sort.Strings(containerKeys)
	for _, cid := range containerKeys {
		nodeIDs := nodesByContainer[cid]
		byRoot := map[string]bool{}
		for _, n := range nodeIDs {
			byRoot[find(n)] = true
		}
		if len(byRoot) <= 1 {
			continue // already one connected component (shared node, or linked via real coupler Equipment) — nothing to do
		}
		unique := map[string]bool{}
		var all []string
		for _, n := range nodeIDs {
			if !unique[n] {
				unique[n] = true
				all = append(all, n)
			}
		}
		sort.Strings(all)
		canonical := all[0]
		for _, n := range all[1:] {
			remap[n] = canonical
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
