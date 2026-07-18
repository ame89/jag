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
	Attributes      []coremodel.Attribute // container name Sachdaten (AttributeKeyName), OwnerID = Container.ID — see core/model.Container's doc comment
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
//
// Top-down restructuring (2026-07-16, decided with the user): this used to
// take the full-model ResolveTerminals() output as an input parameter,
// which forced the whole pipeline to pay ResolveTerminals' full-model RAM
// cost (see Konzept.md's "Offene Punkte" RAM section) BEFORE container
// membership — a station's own equipment/its containers — could even be
// determined, even though Equipment.EquipmentContainer is a direct raw-data
// reference with NO dependency on Terminal resolution at all. Investigation
// (see Konzept.md's 2026-07-15 update) confirmed only a small, empirically
// bounded set of Equipment genuinely needs a Terminal-based fallback to
// find its container: standalone Junction (Muffe) objects, and previously
// suspected but never actually observed for PowerElectronicsConnection
// (every real dataset has a PowerElectronicsUnit satellite — e.g.
// PhotoVoltaicUnit — carrying its own Equipment.EquipmentContainer, and the
// two resolution paths were verified to agree). So instead of resolving
// Terminals for the WHOLE model up front, this function now:
//  1. Resolves the vast majority of Equipment directly from
//     Equipment.EquipmentContainer (no Terminal dependency at all).
//  2. Collects the small remainder — Equipment with no
//     Equipment.EquipmentContainer at all, and not a recognized
//     PowerElectronicsUnit satellite — and resolves THOSE via a single
//     TARGETED ResolveTerminalsForIDs call (cost scales with the remainder's
//     size, never with the whole model) plus the existing
//     ConnectivityNode.ConnectivityNodeContainer fallback.
//  3. Anything still unresolved after that gets a precise ContainerAnomaly
//     (object ID, class, and exactly what was checked and found missing) —
//     per the explicit "precise error message" requirement — instead of
//     being silently dropped, which is what happened before for standalone
//     Junction (a real, previously undiscovered gap, see Konzept.md).
//
// buildACLineChains similarly no longer needs the full-model map — it
// resolves Terminals for just its own ACLineSegment IDs via the same
// targeted helper.
func BuildContainers(store staging.Store, version uint64, chunkSize int) (*BuildContainersResult, error) {
	p := newProgress("containers")
	defer p.Done()
	res := &BuildContainersResult{EquipmentToCont: map[string]string{}}

	subIDs, subIdx, err := scanClass(store, version, chunkSize, "Substation")
	if err != nil {
		return nil, err
	}
	// PSRType is a tiny, model-size-independent class (a fixed enum of
	// station kinds, not one entry per station) — scanning it whole here is
	// fine even for BuildContainers' otherwise per-class-scan design; see
	// classifyStationType's doc comment for why this is data-driven rather
	// than dialect-flagged.
	_, psrIdx, err := scanClass(store, version, chunkSize, "PSRType")
	if err != nil {
		return nil, err
	}
	subSet := map[string]bool{}
	for _, id := range subIDs {
		subSet[id] = true
		res.Containers = append(res.Containers, coremodel.Container{
			ID: id, Type: classifyStationType(subIdx, psrIdx, id),
		})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: subIdx.NameOf(id)})
	}

	// House (decided 2026-07-14): CIM's Building, standalone top-level
	// container (like Substation/ACLine/Junction) — no parent reference to
	// resolve, unlike Bay/BusbarSection.
	houseIDs, houseIdx, err := scanClass(store, version, chunkSize, "Building")
	if err != nil {
		return nil, err
	}
	houseSet := map[string]bool{}
	for _, id := range houseIDs {
		houseSet[id] = true
		res.Containers = append(res.Containers, coremodel.Container{
			ID: id, Type: ContainerTypeHouse,
		})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: houseIdx.NameOf(id)})
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
			ID: id, Type: ContainerTypeBay, ParentID: sub,
		})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: bayIdx.NameOf(id)})
		bayToContainer[id] = id
	}

	// Feeder is the NSC dialect's equivalent of Bay (decided explicitly
	// with the user, 2026-07-14): same container role ("bay" container
	// type), but CIM's Feeder class has no VoltageLevel of its own — it
	// references its Substation directly via
	// Feeder.NormalEnergizingSubstation instead of Bay's
	// Bay.VoltageLevel -> VoltageLevel.Substation chain.
	feederIDs, feederIdx, err := scanClass(store, version, chunkSize, "Feeder")
	if err != nil {
		return nil, err
	}
	for _, id := range feederIDs {
		sub := feederIdx.Ref(id, "Feeder.NormalEnergizingSubstation")
		if !subSet[sub] {
			res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: "Feeder.NormalEnergizingSubstation unresolved"})
			continue
		}
		res.Containers = append(res.Containers, coremodel.Container{
			ID: id, Type: ContainerTypeBay, ParentID: sub,
		})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: feederIdx.NameOf(id)})
		bayToContainer[id] = id
	}

	bbIDs, bbIdx, err := scanClass(store, version, chunkSize, "BusbarSection")
	if err != nil {
		return nil, err
	}
	// busbarGroup collects the BusbarSections sharing one synthesized busbar
	// container. Normally grouped by VoltageLevel (CGMES: a VoltageLevel can
	// have several BusbarSections, e.g. double-busbar arrangements — they
	// share ONE busbar container). The NSC dialect has no VoltageLevel at
	// all (see the Feeder/Bay decision above) — there, BusbarSection
	// attaches directly to its Substation, so the group key/parent falls
	// back to the Substation itself. The container ID can't simply reuse
	// the Substation ID in that case (already taken by the Substation's own
	// Container), hence the "busbar:" prefix.
	type busbarGroup struct {
		containerID string
		parentID    string
		name        string
		members     []string
	}
	groups := map[string]*busbarGroup{}
	for _, id := range bbIDs {
		container := bbIdx.Ref(id, "Equipment.EquipmentContainer")
		var key, containerID, parentID, name string
		switch {
		case vlToSubstation[container] != "":
			key = container
			containerID = container // no separate BusbarNode object exists in CGMES
			parentID = vlToSubstation[container]
			name = vlIdx.NameOf(container)
		case subSet[container]:
			key = "substation:" + container
			containerID = "busbar:" + container
			parentID = container
			name = subIdx.NameOf(container)
		default:
			res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: "BusbarSection container (expected VoltageLevel or Substation) unresolved"})
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &busbarGroup{containerID: containerID, parentID: parentID, name: name}
			groups[key] = g
		}
		g.members = append(g.members, id)
	}
	var groupKeys []string
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	for _, k := range groupKeys {
		g := groups[k]
		res.Containers = append(res.Containers, coremodel.Container{
			ID: g.containerID, Type: ContainerTypeBusbar, ParentID: g.parentID,
		})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: g.containerID, Key: AttributeKeyName, Value: g.name})
		for _, bbID := range g.members {
			res.EquipmentToCont[bbID] = g.containerID
		}
	}

	// Every other Equipment gets its container from
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
	//
	// PowerElectronicsUnit satellites (PhotoVoltaicUnit, BatteryUnit,
	// Wallbox, ...) DO carry their own Equipment.EquipmentContainer (like
	// real Equipment) but are never wired via their own Terminal — they
	// describe a PowerElectronicsConnection they're attached to (see
	// PowerElectronicsUnit.PowerElectronicsConnection, and
	// findEquipmentWithoutTerminalsParallel's identical exclusion). Giving
	// them their own EquipmentToCont entry would be spurious (they aren't
	// node-edge model Equipment — zero Terminals), so they're skipped here,
	// same as before (previously an accidental side effect of the
	// ResolveTerminals-based isResolved filter, now made explicit).
	//
	// Equipment with NO Equipment.EquipmentContainer at all (observed for
	// standalone Junction/Muffe objects; NSC PowerElectronicsConnection
	// never actually needs this in practice — its container is always
	// reachable via its EquipmentContainer-carrying PowerElectronicsUnit
	// satellite, per Konzept.md's 2026-07-15 empirical check) can only be
	// resolved via its own Terminal -> ConnectivityNode ->
	// ConnectivityNode.ConnectivityNodeContainer. Collected into
	// unresolvedIDs/unresolvedClass for a single TARGETED
	// ResolveTerminalsForIDs call after this loop — cost scales with this
	// (empirically tiny) remainder, never with the whole model.
	cnIDs, cnIdx, err := scanClass(store, version, chunkSize, "ConnectivityNode")
	if err != nil {
		return nil, err
	}
	cnToContainer := map[string]string{}
	for _, id := range cnIDs {
		if c := cnIdx.Ref(id, "ConnectivityNode.ConnectivityNodeContainer"); c != "" {
			cnToContainer[id] = c
		}
	}
	cnIDs, cnIdx = nil, nil // free the scanClass-accumulated records now that only the small map is needed

	var unresolvedIDs []string
	unresolvedClass := map[string]string{}

	classes, err := store.ListClasses(version)
	if err != nil {
		return nil, fmt.Errorf("common: listing classes: %w", err)
	}

	// Pass 1: propagate PowerElectronicsUnit satellite containers to their
	// PowerElectronicsConnection (PEC) BEFORE the ordinary resolution pass
	// below, so that pass's `already resolved` check works regardless of
	// alphabetical class scan order (a PEC's own class is scanned before its
	// satellite's class in a single combined pass, e.g.
	// "PowerElectronicsConnection" < "Wallbox" — without this separate first
	// pass, PEC would be judged "no Equipment.EquipmentContainer at all" and
	// silently skipped before ever seeing its satellite). See the detailed
	// rationale at the satellite-handling branch in pass 2 below.
	for _, class := range classes {
		afterID := ""
		for {
			records, err := store.GetByClass(version, class, afterID, chunkSize)
			if err != nil {
				return nil, fmt.Errorf("common: scanning class %s (satellite pre-pass): %w", class, err)
			}
			if len(records) == 0 {
				break
			}
			idx := BuildObjectIndex(records)
			ids := distinctIDsInOrder(records)
			for _, id := range ids {
				pecID := idx.Ref(id, "PowerElectronicsUnit.PowerElectronicsConnection")
				if pecID == "" {
					continue
				}
				if _, already := res.EquipmentToCont[pecID]; already {
					continue
				}
				container := idx.Ref(id, "Equipment.EquipmentContainer")
				if container == "" {
					continue
				}
				switch {
				case bayToContainer[container] != "":
					res.EquipmentToCont[pecID] = bayToContainer[container]
				case vlToSubstation[container] != "":
					res.EquipmentToCont[pecID] = vlToSubstation[container]
				case subSet[container]:
					res.EquipmentToCont[pecID] = container
				case houseSet[container]:
					res.EquipmentToCont[pecID] = container
				}
			}
			afterID = ids[len(ids)-1]
			if len(ids) < chunkSize {
				break
			}
		}
	}

	// Pass 2: ordinary Equipment.EquipmentContainer resolution. Any PEC
	// already resolved by pass 1 above is skipped immediately via the
	// `already` check — it never reaches the "no EquipmentContainer at all"
	// branch, so it never needs the Terminal/ConnectivityNode-based fallback
	// below (empirically confirmed 2026-07-15: every PEC in
	// lasttest-200-10-10 has exactly one such satellite).
	for _, class := range classes {
		switch class {
		case "Terminal", "ConnectivityNode", "Substation", "VoltageLevel", "Bay", "Feeder", "BusbarSection", "ACLineSegment", "Building":
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
				if _, already := res.EquipmentToCont[id]; already {
					continue
				}
				if idx.HasAttr(id, "PowerElectronicsUnit.PowerElectronicsConnection") {
					continue // satellite metadata, not node-edge model Equipment — its PEC's container was already handled in pass 1 above
				}
				if !idx.HasAttr(id, "Equipment.EquipmentContainer") {
					// Not CIM Equipment at all (e.g. UsagePoint, Location,
					// TimeSchedule, RegulatingControl, PositionPoint — ancillary
					// satellite/Sachdaten classes that ride along with their
					// owning Equipment but never carry their own container
					// membership or Terminals). These must never be pushed into
					// unresolvedIDs/the Terminal-based fallback below — doing so
					// was a real bug (found 2026-07-15): it inflated the
					// "unresolved" set from ~20,000 genuine container-less
					// Equipment (PowerElectronicsConnection) to 205,000+ objects,
					// causing ResolveTerminalsForIDs' single batched
					// GetReferencesToAny call to blow up in RAM. Mirrors the
					// same HasAttr guard already used by
					// scanClassMissingEquipment in terminals.go.
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
				case houseSet[container]:
					res.EquipmentToCont[id] = container // house-internal Equipment (Meter, Fuse, ...) attached directly to its House/Building
				case container != "":
					res.Anomalies = append(res.Anomalies, ContainerAnomaly{ObjectID: id, Message: fmt.Sprintf("class %s: Equipment.EquipmentContainer=%q does not resolve to a known Bay, VoltageLevel, Substation or House", class, container)})
				default:
					// No Equipment.EquipmentContainer at all — defer to the
					// targeted Terminal-based ConnectivityNode fallback below.
					unresolvedIDs = append(unresolvedIDs, id)
					unresolvedClass[id] = class
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
	// Built BEFORE the unresolved-Equipment/Junction fallback below (moved
	// up 2026-07-15) so aclineNodeToContainer is available when resolving a
	// standalone Junction's own container membership — see that map's doc
	// comment on buildACLineChains for why.
	aclineContainers, aclineOf, aclineNames, aclAnomalies, aclineNodeToContainer, err := buildACLineChains(store, version, aclIDs)
	if err != nil {
		return nil, err
	}
	res.Anomalies = append(res.Anomalies, aclAnomalies...)
	res.Containers = append(res.Containers, aclineContainers...)
	res.Attributes = append(res.Attributes, aclineNames...)
	for segID, containerID := range aclineOf {
		res.EquipmentToCont[segID] = containerID
	}

	if len(unresolvedIDs) > 0 {
		sort.Strings(unresolvedIDs)
		nodeRoleIDs := map[string]bool{}
		for _, id := range unresolvedIDs {
			if nodeRoleClasses[unresolvedClass[id]] {
				nodeRoleIDs[id] = true
			}
		}
		termResolved, termAnomalies, err := ResolveTerminalsForIDs(store, version, unresolvedIDs, nodeRoleIDs)
		if err != nil {
			return nil, fmt.Errorf("common: resolving Terminals for %d container-less Equipment (ConnectivityNode fallback): %w", len(unresolvedIDs), err)
		}
		for _, a := range termAnomalies {
			res.Anomalies = append(res.Anomalies, ContainerAnomaly{
				ObjectID: a.EquipmentID,
				Message:  fmt.Sprintf("class %s: no Equipment.EquipmentContainer, and its own Terminal(s) could not be resolved either (%s)", unresolvedClass[a.EquipmentID], a.Message),
			})
		}
		for _, id := range unresolvedIDs {
			et, ok := termResolved[id]
			if !ok {
				continue // already reported via termAnomalies above
			}
			container := cnToContainer[et.Node1]
			if container == "" {
				container = cnToContainer[et.Node2]
			}
			for _, extra := range et.ExtraNodes {
				if container != "" {
					break
				}
				container = cnToContainer[extra]
			}
			switch {
			case bayToContainer[container] != "":
				res.EquipmentToCont[id] = bayToContainer[container]
			case vlToSubstation[container] != "":
				res.EquipmentToCont[id] = vlToSubstation[container]
			case subSet[container]:
				res.EquipmentToCont[id] = container
			case houseSet[container]:
				res.EquipmentToCont[id] = container
			case unresolvedClass[id] == "Junction":
				// Junction/Muffe never gets its own dedicated container
				// type (decided with the user 2026-07-15, superseding the
				// earlier "Muffen-Container" auto-creation idea): a
				// standalone splice is just a NODE, and Container
				// membership for it is bookkeeping only — it joins
				// whichever "acline" its own ConnectivityNode(s) already
				// belong to (built just above, before this block, exactly
				// so it's available here). Deterministic tie-break for a
				// branch point (Abzweigmuffe touching multiple chains) is
				// handled inside buildACLineChains' nodeToContainer
				// construction (smallest containerID wins).
				aclContainer := aclineNodeToContainer[et.Node1]
				if aclContainer == "" {
					aclContainer = aclineNodeToContainer[et.Node2]
				}
				for _, extra := range et.ExtraNodes {
					if aclContainer != "" {
						break
					}
					aclContainer = aclineNodeToContainer[extra]
				}
				if aclContainer == "" {
					// No ACLineSegment touches this Junction's
					// ConnectivityNode(s) either (e.g. a Junction with no
					// adjacent cable segment in this partial model) —
					// nothing to attach it to; report instead of silently
					// dropping it.
					res.Anomalies = append(res.Anomalies, ContainerAnomaly{
						ObjectID: id,
						Message:  "Junction's ConnectivityNode(s) have no resolvable ConnectivityNodeContainer and no adjacent ACLineSegment to derive an acline container from",
					})
					continue
				}
				res.EquipmentToCont[id] = aclContainer
			default:
				res.Anomalies = append(res.Anomalies, ContainerAnomaly{
					ObjectID: id,
					Message:  fmt.Sprintf("class %s: no Equipment.EquipmentContainer, and its resolved ConnectivityNode(s) have no ConnectivityNodeContainer either — container membership cannot be resolved (checked Equipment.EquipmentContainer, PowerElectronicsUnit.PowerElectronicsConnection satellite, and Terminal->ConnectivityNode->ConnectivityNodeContainer)", unresolvedClass[id]),
				})
			}
		}
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
//
// Resolves Terminals for just aclIDs via a single TARGETED
// ResolveTerminalsForIDs call (2026-07-16, top-down restructuring) instead
// of requiring the full-model ResolveTerminals map — ACLineSegment is not a
// node-role class (nodeRoleIDs is nil), so classifyTerminals' normal 1/2
// terminal shape applies. Any ACLineSegment whose own Terminals can't be
// resolved is now reported as a ContainerAnomaly (previously silently
// skipped — its own Anomaly from the full-model ResolveTerminals was
// reported elsewhere in the old pipeline, but this function itself never
// surfaced it).
//
// The final return value, nodeToContainer, maps every ConnectivityNode ID
// touched by at least one grouped ACLineSegment to that chain's container
// ID — see its own doc comment below (near where it's built) for why this
// exists: it lets a caller assign a standalone Junction/Muffe (a Node, not
// an Edge) to the same "acline" container its physical location belongs
// to, instead of giving Junction its own dedicated container type.
func buildACLineChains(store staging.Store, version uint64, aclIDs []string) ([]coremodel.Container, map[string]string, []coremodel.Attribute, []ContainerAnomaly, map[string]string, error) {
	resolved, termAnomalies, err := ResolveTerminalsForIDs(store, version, aclIDs, nil)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("common: resolving Terminals for %d ACLineSegment: %w", len(aclIDs), err)
	}
	var anomalies []ContainerAnomaly
	for _, a := range termAnomalies {
		anomalies = append(anomalies, ContainerAnomaly{ObjectID: a.EquipmentID, Message: "ACLineSegment: " + a.Message})
	}

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
	var names []coremodel.Attribute
	aclineOf := map[string]string{}
	for _, root := range roots {
		members := groups[root]
		sort.Strings(members)
		containerID := "acline:" + members[0] + ":" + members[len(members)-1]
		containers = append(containers, coremodel.Container{
			ID: containerID, Type: ContainerTypeACLine,
		})
		// members[0] isn't guaranteed to be a long CIM mRID UUID (short
		// human-readable IDs, e.g. in the cigre_mv example data, can be
		// shorter than 8 characters) — cap the slice to avoid a
		// out-of-range panic.
		names = append(names, coremodel.Attribute{OwnerID: containerID, Key: AttributeKeyName, Value: "ACLine " + members[0][:min(8, len(members[0]))]})
		for _, m := range members {
			aclineOf[m] = containerID
		}
	}

	// nodeToContainer lets callers assign a standalone Junction/Muffe (a
	// Node, not an ACLineSegment/Edge) to the SAME "acline" container as
	// the ACLineSegment chain(s) touching its own ConnectivityNode(s) —
	// decided with the user 2026-07-15: a Junction's Container membership
	// is bookkeeping only (real cross-cable queries go through topology,
	// not Container.ParentID), so a Junction should never get its own
	// dedicated container type — it simply joins whichever "acline" its
	// physical location already belongs to. A branch point (an
	// Abzweigmuffe/T-Muffe touching 3+ segments, or any node where
	// multiple different chains meet) intentionally does NOT merge those
	// chains (see the ACLine-boundary decision: a branch always ends one
	// chain and starts new ones) — it just means this map may see more
	// than one candidate containerID for the same node; keep the
	// lexicographically smallest one for a deterministic, stable
	// tie-break (no special multi-parent handling needed).
	nodeToContainer := map[string]string{}
	for node, segs := range nodeToSegments {
		for _, segID := range segs {
			containerID, ok := aclineOf[segID]
			if !ok {
				continue
			}
			if existing, has := nodeToContainer[node]; !has || containerID < existing {
				nodeToContainer[node] = containerID
			}
		}
	}

	return containers, aclineOf, names, anomalies, nodeToContainer, nil
}
