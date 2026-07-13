// Package common — this file builds the Container hierarchy (see Konzept.md,
// "Container / Hierarchie") from Phase 1 staging data: Substation, Bay,
// VoltageLevel and BusbarSection objects, plus a purely topological
// derivation of ACLine containers (see the "ACLine boundary is topological"
// decision). Busbar and ACLine containers have no direct CIM counterpart and
// are synthesized here, per the Muffe/Busbar/ACLine auto-creation rules.
package common

import (
	"fmt"
	"sort"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
	importmodel "gitlab.com/openk-nsc/jag/internal/importer/model"
)

// ContainerAnomaly describes a container-hierarchy resolution problem
// (dangling/unresolvable reference) found while building containers.
// Collected instead of aborting, consistent with Phase 4's "gather all
// errors" approach used elsewhere in Phase 2.
type ContainerAnomaly struct {
	ObjectID string
	Message  string
}

// BuildContainersResult is everything BuildContainers produces.
type BuildContainersResult struct {
	Containers      []coremodel.Container
	EquipmentToCont map[string]string     // EquipmentID -> ContainerID
	LineRefs        []coremodel.Attribute // raw cim:Line reference kept as Sachdaten per equipment (untrusted, not used for container assignment)
	Anomalies       []ContainerAnomaly
}

// scanClass performs one chunked class scan (see staging.Store.GetByClass)
// and returns the distinct IDs (in ID order) plus an ObjectIndex over all
// their attributes. Small/medium classes only (Substation/Bay/VoltageLevel/
// BusbarSection/ACLineSegment are all far smaller than Terminal).
func scanClass(store staging.Store, version uint64, chunkSize int, class string) ([]string, *ObjectIndex, error) {
	var all []importmodel.StagingRecord
	afterID := ""
	for {
		records, err := store.GetByClass(version, class, afterID, chunkSize)
		if err != nil {
			return nil, nil, fmt.Errorf("common: scanning class %s: %w", class, err)
		}
		if len(records) == 0 {
			break
		}
		all = append(all, records...)
		ids := distinctIDsInOrder(records)
		afterID = ids[len(ids)-1]
		if len(ids) < chunkSize {
			break
		}
	}
	idx := BuildObjectIndex(all)
	return distinctIDsInOrder(all), idx, nil
}

// BuildContainers derives the Container tree plus each resolved Equipment's
// ContainerID:
//
//   - Substation -> "substation" container, 1:1, top-level.
//   - Bay -> "bay" container, 1:1, parented under its VoltageLevel's
//     Substation (VoltageLevel itself is not a JAG container type, see
//     Konzept.md — it's used only to resolve the parent link).
//   - BusbarSection -> grouped by their VoltageLevel (a VoltageLevel can
//     have several BusbarSections, e.g. double-busbar arrangements — they
//     all share ONE "busbar" container, since a busbar belongs to exactly
//     one voltage level; decided explicitly with the user rather than
//     assumed). The synthesized busbar container's ID is derived from the
//     VoltageLevel ID (there is no separate "BusbarNode" object in CGMES).
//   - ACLineSegment -> grouped into "acline" containers purely by topology
//     (see the "ACLine boundary is topological" decision): a
//     ConnectivityNode where exactly two ACLineSegment ends meet is a
//     pass-through point (same ACLine continues); any other degree (0, 1,
//     3+) is a chain boundary. Espheim has no explicit Junction/Muffe
//     objects to distinguish Durchgangsmuffe/Abzweigmuffe, so node degree
//     is used directly as the decided fallback. The raw cim:Line grouping
//     CIM already provides is NOT trusted for this (per explicit user
//     decision — CGMES's own Line grouping was found to disagree with real
//     topology for some segments) but is kept as an untrusted Sachdaten
//     reference alongside the Equipment.
func BuildContainers(store staging.Store, version uint64, chunkSize int, resolved map[string]EquipmentTerminals) (*BuildContainersResult, error) {
	res := &BuildContainersResult{EquipmentToCont: map[string]string{}}

	subIDs, subIdx, err := scanClass(store, version, chunkSize, "Substation")
	if err != nil {
		return nil, err
	}
	subSet := map[string]bool{}
	for _, id := range subIDs {
		subSet[id] = true
		res.Containers = append(res.Containers, coremodel.Container{
			ID: id, Name: subIdx.NameOf(id), Type: coremodel.ContainerTypeSubstation,
		})
	}

	vlIDs, vlIdx, err := scanClass(store, version, chunkSize, "VoltageLevel")
	if err != nil {
		return nil, err
	}
	vlToSubstation := map[string]string{}
	for _, id := range vlIDs {
		sub := vlIdx.Ref(id, "VoltageLevel.Substation")
		if sub == "" || !subSet[sub] {
			res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: "VoltageLevel.Substation unresolved"})
			continue
		}
		vlToSubstation[id] = sub
	}

	bayIDs, bayIdx, err := scanClass(store, version, chunkSize, "Bay")
	if err != nil {
		return nil, err
	}
	bayToContainer := map[string]string{}
	for _, id := range bayIDs {
		vl := bayIdx.Ref(id, "Bay.VoltageLevel")
		sub, ok := vlToSubstation[vl]
		if !ok {
			res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: "Bay.VoltageLevel unresolved"})
			continue
		}
		res.Containers = append(res.Containers, coremodel.Container{
			ID: id, Name: bayIdx.NameOf(id), Type: coremodel.ContainerTypeBay, ParentID: sub,
		})
		bayToContainer[id] = id
	}

	bbIDs, bbIdx, err := scanClass(store, version, chunkSize, "BusbarSection")
	if err != nil {
		return nil, err
	}
	busbarByVL := map[string][]string{} // VoltageLevel ID -> BusbarSection IDs
	for _, id := range bbIDs {
		vl := bbIdx.Ref(id, "Equipment.EquipmentContainer")
		if _, ok := vlToSubstation[vl]; !ok {
			res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: "BusbarSection container (expected VoltageLevel) unresolved"})
			continue
		}
		busbarByVL[vl] = append(busbarByVL[vl], id)
	}
	var vlKeys []string
	for vl := range busbarByVL {
		vlKeys = append(vlKeys, vl)
	}
	sort.Strings(vlKeys)
	for _, vl := range vlKeys {
		containerID := vl // busbar container ID derived from its VoltageLevel (no separate BusbarNode object exists in CGMES)
		res.Containers = append(res.Containers, coremodel.Container{
			ID: containerID, Name: vlIdx.NameOf(vl), Type: coremodel.ContainerTypeBusbar, ParentID: vlToSubstation[vl],
		})
		for _, bbID := range busbarByVL[vl] {
			res.EquipmentToCont[bbID] = containerID
		}
	}

	// Every other resolved Equipment gets its container from
	// Equipment.EquipmentContainer: if that resolves to a Bay, use the
	// Bay's own container; if it resolves directly to a VoltageLevel (no
	// Bay in between — confirmed to genuinely occur in real CGMES data,
	// e.g. Espheim, not just a data-quality glitch), attach directly to
	// that VoltageLevel's Substation container instead (the Bay layer is
	// simply skipped for that Equipment, per explicit user decision).
	// Discovered generically across all remaining classes (not per-class)
	// so it covers all "station structure" Equipment (breakers,
	// disconnectors, transformers, meters, ...), analogous to
	// findEquipmentWithoutTerminals in terminals.go.
	classes, err := store.ListClasses(version)
	if err != nil {
		return nil, fmt.Errorf("common: listing classes: %w", err)
	}
	for _, class := range classes {
		switch class {
		case "Terminal", "ConnectivityNode", "Substation", "VoltageLevel", "Bay", "BusbarSection", "ACLineSegment":
			continue
		}
		if isGeneratingUnitClass(class) {
			continue
		}

		afterID := ""
		for {
			records, err := store.GetByClass(version, class, afterID, chunkSize)
			if err != nil {
				return nil, fmt.Errorf("common: scanning class %s: %w", class, err)
			}
			if len(records) == 0 {
				break
			}
			idx := BuildObjectIndex(records)
			ids := distinctIDsInOrder(records)
			for _, id := range ids {
				if _, isResolved := resolved[id]; !isResolved {
					continue
				}
				if _, already := res.EquipmentToCont[id]; already {
					continue
				}
				container := idx.Ref(id, "Equipment.EquipmentContainer")
				switch {
				case bayToContainer[container] != "":
					res.EquipmentToCont[id] = bayToContainer[container]
				case vlToSubstation[container] != "":
					res.EquipmentToCont[id] = vlToSubstation[container] // no Bay in between — attach straight to the Substation
				case subSet[container]:
					res.EquipmentToCont[id] = container // e.g. a two-winding Transformer spanning VoltageLevels, attached directly to the Substation
				case container != "":
					res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: "Equipment.EquipmentContainer does not resolve to a known Bay, VoltageLevel or Substation"})
				}
			}
			afterID = ids[len(ids)-1]
			if len(ids) < chunkSize {
				break
			}
		}
	}

	lineIDs, lineIdx, err := scanClass(store, version, chunkSize, "Line")
	if err != nil {
		return nil, err
	}
	lineExists := map[string]bool{}
	for _, id := range lineIDs {
		lineExists[id] = true
	}

	aclIDs, aclIdx, err := scanClass(store, version, chunkSize, "ACLineSegment")
	if err != nil {
		return nil, err
	}
	for _, id := range aclIDs {
		lineRef := aclIdx.Ref(id, "Equipment.EquipmentContainer")
		if lineRef == "" {
			continue
		}
		res.LineRefs = append(res.LineRefs, coremodel.Attribute{OwnerID: id, Key: "cim:ACLineSegment.Line", Value: lineRef})
		if !lineExists[lineRef] {
			continue // dangling external reference (missing boundary profile) — nothing to pull attributes from
		}
		// Line's own literal attributes (e.g. IdentifiedObject.name,
		// Line.Region) are attached to the ACLineSegment as untrusted
		// Sachdaten too, prefixed with "cim:Line." to distinguish them from
		// the segment's own attributes — never used for container/topology
		// decisions (per the "CIM's Line grouping isn't trustworthy"
		// decision), just carried along losslessly.
		for attr, values := range lineIdx.AllAttrs(lineRef) {
			for _, v := range values {
				res.LineRefs = append(res.LineRefs, coremodel.Attribute{
					OwnerID: id,
					Key:     coremodel.AttributeKey("cim:Line." + attr),
					Value:   v.Value,
				})
			}
		}
	}
	aclineContainers, aclineOf, err := buildACLineChains(aclIDs, resolved)
	if err != nil {
		return nil, err
	}
	res.Containers = append(res.Containers, aclineContainers...)
	for segID, containerID := range aclineOf {
		res.EquipmentToCont[segID] = containerID
	}

	return res, nil
}

// buildACLineChains groups ACLineSegment equipment into "acline" containers
// purely by node degree (see BuildContainers' doc comment): a
// ConnectivityNode where exactly two ACLineSegment ends meet is a
// pass-through and merges the two segments into the same chain (via
// union-find); any other degree is a chain boundary. The container ID is
// derived from the two lexicographically smallest/largest segment IDs in
// the resulting chain — a stable, deterministic stand-in for "first and
// last element" (Konzept.md), since true physical start/end ordering isn't
// needed for a synthetic ID.
func buildACLineChains(aclIDs []string, resolved map[string]EquipmentTerminals) ([]coremodel.Container, map[string]string, error) {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" {
			parent[x] = x
		}
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	nodeToSegments := map[string][]string{}
	for _, segID := range aclIDs {
		et, ok := resolved[segID]
		if !ok {
			continue // anomaly already reported by ResolveTerminals
		}
		find(segID)
		if et.Node1 != "" {
			nodeToSegments[et.Node1] = append(nodeToSegments[et.Node1], segID)
		}
		if et.Node2 != "" {
			nodeToSegments[et.Node2] = append(nodeToSegments[et.Node2], segID)
		}
	}
	for _, segs := range nodeToSegments {
		if len(segs) == 2 {
			union(segs[0], segs[1])
		}
	}

	groups := map[string][]string{}
	for _, segID := range aclIDs {
		if _, ok := resolved[segID]; !ok {
			continue
		}
		root := find(segID)
		groups[root] = append(groups[root], segID)
	}

	var roots []string
	for root := range groups {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	var containers []coremodel.Container
	aclineOf := map[string]string{}
	for _, root := range roots {
		members := groups[root]
		sort.Strings(members)
		containerID := "acline:" + members[0] + ":" + members[len(members)-1]
		containers = append(containers, coremodel.Container{
			ID: containerID, Name: "ACLine " + members[0][:8], Type: coremodel.ContainerTypeACLine,
		})
		for _, m := range members {
			aclineOf[m] = containerID
		}
	}
	return containers, aclineOf, nil
}
