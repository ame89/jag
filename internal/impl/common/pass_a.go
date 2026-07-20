// Package common — Pass A of the per-station "Pull-Pool" rewrite (see
// plan.md / Konzept.md, 2026-07 RAM-scaling session): a batch-scoped
// counterpart to BuildContainers (container.go) that resolves Container
// membership for ONE batch of Substation/Building root IDs at a time,
// using staging.Store.GetReferencesToAny (an indexed "who references any of
// these IDs" reverse lookup) instead of scanning every class in the WHOLE
// model. Cost scales with the batch's own containers/Equipment, never with
// total model size — this is the structural fix for the confirmed
// RAM-grows-with-total-model-size bug (lt500 peaked ~1420MB vs lt200's
// ~600MB at identical chunk/worker settings, proving chunk size alone
// cannot fix it).
package common

import (
	"fmt"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
	importmodel "gitlab.com/openk-nsc/jag/internal/importer/model"
)

// BatchContainersResult is everything ResolveBatchContainers produces for
// one batch — the batch-scoped analogue of BuildContainersResult. There is
// no Anomalies field: see ResolveBatchContainers' doc comment for why
// dangling/unresolvable Equipment.EquipmentContainer references can no
// longer be reported per-object here without reintroducing a whole-model
// scan; a cheap total-count comparison (Phase 1 count vs. Pass A + Pass B)
// takes over that role instead (decided with the user, 2026-07).
type BatchContainersResult struct {
	Containers      []coremodel.Container
	EquipmentToCont map[string]string     // EquipmentID -> ContainerID, batch-local equipment only
	Attributes      []coremodel.Attribute // container name Sachdaten (AttributeKeyName)
}

// attachInfo records one candidate Equipment's own Equipment.EquipmentContainer
// resolution target plus its CIM class (needed to recognize
// GeneratingUnit/PowerElectronicsUnit satellites, exactly as BuildContainers
// does).
type attachInfo struct {
	container string
	class     string
}

// ResolveBatchContainers resolves the Container hierarchy and Equipment
// container-membership for ONE batch of Substation/Building root IDs,
// walking the tree BACKWARD (seeded from the batch's own roots) instead of
// BuildContainers' forward whole-model class scan:
//
//	Substation/Building (batch roots)
//	  -> GetReferencesToAny(subIDs): VoltageLevel.Substation,
//	     Feeder.NormalEnergizingSubstation, Equipment.EquipmentContainer
//	     (direct Substation attach, e.g. a Transformer spanning
//	     VoltageLevels, or a Substation-grouped BusbarSection in the NSC
//	     dialect)
//	  -> GetReferencesToAny(vlIDs): Bay.VoltageLevel,
//	     Equipment.EquipmentContainer (direct VoltageLevel attach, incl.
//	     BusbarSection)
//	  -> GetReferencesToAny(bayIDs+feederIDs): Equipment.EquipmentContainer
//	     (the bulk of ordinary station Equipment)
//	  -> GetReferencesToAny(houseIDs): Equipment.EquipmentContainer
//	     (house-internal Equipment)
//
// PowerElectronicsUnit satellites (PhotoVoltaicUnit, Wallbox, ...) and
// GeneratingUnit satellites are excluded from getting their own
// EquipmentToCont entry, mirroring BuildContainers exactly (see that
// function's doc comment) — satellite containers propagate to their
// PowerElectronicsConnection/SynchronousMachine instead.
//
// Equipment with NO Equipment.EquipmentContainer at all (standalone
// Junction/Muffe) is intentionally NOT resolved here — nothing points FROM
// a station container TO it, so a backward walk can never discover it. Per
// the Konzept.md decision this is deferred entirely to Pass B
// (buildACLineChains plus a small dedicated Junction pass), which already
// runs independently of station batching (bounded by class size, not total
// model size).
//
// Because every container ID this function ever assigns comes from
// literally walking down from subIDs/houseIDs, it can never itself produce
// a "reference doesn't resolve to a known container" anomaly the way
// BuildContainers does — that class of error (Equipment.EquipmentContainer
// pointing outside this batch's own reachable tree, e.g. a data typo)
// would require knowing about containers system-wide, which is exactly the
// whole-model cost this rewrite avoids. Per the explicit user decision
// (2026-07, this session), such dangling references are no longer caught
// per-object; a cheap total Equipment-count comparison (Phase 1 vs. Pass A
// + Pass B) is the only remaining defense, with a targeted (on-demand,
// rare) deeper scan only if that count ever mismatches.
func ResolveBatchContainers(store staging.Store, version uint64, subIDs, houseIDs []string) (*BatchContainersResult, error) {
	res := &BatchContainersResult{EquipmentToCont: map[string]string{}}
	if len(subIDs) == 0 && len(houseIDs) == 0 {
		return res, nil
	}

	ownIDs := append(append([]string{}, subIDs...), houseIDs...)
	ownRecs, err := getByIDsIndexed(store, version, ownIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching batch root records: %w", err)
	}
	ownIdx := BuildObjectIndex(flattenRecords(ownRecs))

	// Resolve each Substation's PowerSystemResource.PSRType reference (if
	// any) to classify ONS vs. KVS — see classifyStationType's doc comment
	// (containertype.go) and Konzept.md's 2026-07-18 "Offener Punkt". Only
	// the small, batch-local set of actually-referenced PSRType objects is
	// fetched (not a whole-class scan), keeping this batch-scoped like the
	// rest of ResolveBatchContainers.
	var psrTypeIDs []string
	for _, id := range subIDs {
		if psr := ownIdx.Ref(id, "PowerSystemResource.PSRType"); psr != "" {
			psrTypeIDs = append(psrTypeIDs, psr)
		}
	}
	psrRecs, err := getByIDsIndexed(store, version, psrTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching %d PSRType records: %w", len(psrTypeIDs), err)
	}
	psrIdx := BuildObjectIndex(flattenRecords(psrRecs))

	subSet := map[string]bool{}
	for _, id := range subIDs {
		subSet[id] = true
		res.Containers = append(res.Containers, coremodel.Container{ID: id, Type: classifyStationType(ownIdx, psrIdx, id)})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: ownIdx.NameOf(id)})
	}
	houseSet := map[string]bool{}
	for _, id := range houseIDs {
		houseSet[id] = true
		res.Containers = append(res.Containers, coremodel.Container{ID: id, Type: ContainerTypeHouse})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: ownIdx.NameOf(id)})
	}

	// Step 1: reverse lookup from the batch's own Substation IDs.
	refsToSub, err := getReferencesToAnyIndexed(store, version, subIDs)
	if err != nil {
		return nil, fmt.Errorf("common: reverse-lookup for %d Substation IDs: %w", len(subIDs), err)
	}
	var vlIDs, feederIDs []string
	vlToSubstation := map[string]string{}
	directSubAttach := map[string]attachInfo{} // equipmentID -> direct Substation attach (BusbarSection in NSC dialect, or ordinary Equipment e.g. a Transformer spanning VoltageLevels)
	for subID, recs := range refsToSub {
		for _, r := range recs {
			switch r.Attribute {
			case "VoltageLevel.Substation":
				vlIDs = append(vlIDs, r.ID)
				vlToSubstation[r.ID] = subID
			case "Feeder.NormalEnergizingSubstation":
				feederIDs = append(feederIDs, r.ID)
			case "Equipment.EquipmentContainer":
				directSubAttach[r.ID] = attachInfo{container: subID, class: r.Class}
			}
		}
	}
	// feederToSubstation kept separately since Feeder objects (unlike Bay,
	// which resolves its Substation indirectly via VoltageLevel) reference
	// their Substation directly.
	feederToSubstation := map[string]string{}
	for subID, recs := range refsToSub {
		for _, r := range recs {
			if r.Attribute == "Feeder.NormalEnergizingSubstation" {
				feederToSubstation[r.ID] = subID
			}
		}
	}

	// Step 2: reverse lookup from the found VoltageLevel IDs.
	refsToVL, err := getReferencesToAnyIndexed(store, version, vlIDs)
	if err != nil {
		return nil, fmt.Errorf("common: reverse-lookup for %d VoltageLevel IDs: %w", len(vlIDs), err)
	}
	var bayIDs []string
	bayToVL := map[string]string{}
	directVLAttach := map[string]attachInfo{} // equipmentID -> direct VoltageLevel attach (BusbarSection, or a Bay-less Equipment)
	for vlID, recs := range refsToVL {
		for _, r := range recs {
			switch r.Attribute {
			case "Bay.VoltageLevel":
				bayIDs = append(bayIDs, r.ID)
				bayToVL[r.ID] = vlID
			case "Equipment.EquipmentContainer":
				directVLAttach[r.ID] = attachInfo{container: vlID, class: r.Class}
			}
		}
	}

	// Own records of VoltageLevel (names, for busbar-container naming) and
	// Bay/Feeder (names, container parenting).
	vlOwnRecs, err := getByIDsIndexed(store, version, vlIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching %d VoltageLevel records: %w", len(vlIDs), err)
	}
	vlNameOf := map[string]string{}
	for _, id := range vlIDs {
		vlNameOf[id] = BuildObjectIndex(vlOwnRecs[id]).NameOf(id)
	}

	bayRoleIDs := append(append([]string{}, bayIDs...), feederIDs...)
	bayOwnRecs, err := getByIDsIndexed(store, version, bayRoleIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching %d Bay/Feeder records: %w", len(bayRoleIDs), err)
	}
	bayToContainer := map[string]string{}
	for _, id := range bayIDs {
		subID, ok := vlToSubstation[bayToVL[id]]
		if !ok {
			continue // Bay's VoltageLevel wasn't found in this batch's own reverse-lookup — cannot happen given bayToVL is only populated from vlIDs above, kept defensive
		}
		res.Containers = append(res.Containers, coremodel.Container{ID: id, Type: ContainerTypeBay, ParentID: subID})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: BuildObjectIndex(bayOwnRecs[id]).NameOf(id)})
		bayToContainer[id] = id
	}
	for _, id := range feederIDs {
		subID := feederToSubstation[id]
		res.Containers = append(res.Containers, coremodel.Container{ID: id, Type: ContainerTypeBay, ParentID: subID})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: id, Key: AttributeKeyName, Value: BuildObjectIndex(bayOwnRecs[id]).NameOf(id)})
		bayToContainer[id] = id
	}

	// Own records of the BusbarSection Equipment themselves (needed for
	// their own Name attribute — a busbar container is now named after its
	// own BusbarSection, not the owning Substation/VoltageLevel, see the
	// busbar-splitting decision below).
	var bbSectionIDs []string
	for bbID, info := range directVLAttach {
		if info.class == "BusbarSection" {
			bbSectionIDs = append(bbSectionIDs, bbID)
		}
	}
	for bbID, info := range directSubAttach {
		if info.class == "BusbarSection" {
			bbSectionIDs = append(bbSectionIDs, bbID)
		}
	}
	bbOwnRecs, err := getByIDsIndexed(store, version, bbSectionIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching %d BusbarSection records: %w", len(bbSectionIDs), err)
	}
	bbNameOf := map[string]string{}
	for _, id := range bbSectionIDs {
		bbNameOf[id] = BuildObjectIndex(bbOwnRecs[id]).NameOf(id)
	}

	// BusbarSection grouping — one shared "busbar" container per
	// VoltageLevel (or, NSC dialect with no VoltageLevel, per busbar's own
	// base ID within the Substation — see container.go's
	// baseBusbarSectionID/busbar-splitting doc comment), mirroring
	// BuildContainers' identical grouping exactly.
	type busbarGroup struct {
		containerID string
		parentID    string
		name        string
		members     []string
	}
	groups := map[string]*busbarGroup{}
	groupOrder := []string{}
	addBusbar := func(bbID string, info attachInfo) {
		var key, containerID, parentID, name string
		switch {
		case vlToSubstation[info.container] != "":
			key = info.container
			containerID = info.container
			parentID = vlToSubstation[info.container]
			name = vlNameOf[info.container]
		case subSet[info.container]:
			base := baseBusbarSectionID(bbID)
			key = "substation:" + info.container + ":" + base
			containerID = "busbar:" + base
			parentID = info.container
			name = bbNameOf[bbID]
		default:
			return
		}
		g, ok := groups[key]
		if !ok {
			g = &busbarGroup{containerID: containerID, parentID: parentID, name: name}
			groups[key] = g
			groupOrder = append(groupOrder, key)
		}
		g.members = append(g.members, bbID)
	}
	for bbID, info := range directVLAttach {
		if info.class == "BusbarSection" {
			addBusbar(bbID, info)
		}
	}
	for bbID, info := range directSubAttach {
		if info.class == "BusbarSection" {
			addBusbar(bbID, info)
		}
	}
	for _, key := range groupOrder {
		g := groups[key]
		res.Containers = append(res.Containers, coremodel.Container{ID: g.containerID, Type: ContainerTypeBusbar, ParentID: g.parentID})
		res.Attributes = append(res.Attributes, coremodel.Attribute{OwnerID: g.containerID, Key: AttributeKeyName, Value: g.name})
		for _, bbID := range g.members {
			res.EquipmentToCont[bbID] = g.containerID
		}
	}

	// Step 3: the bulk of ordinary station Equipment — reverse lookup from
	// the found Bay/Feeder IDs, plus House IDs, plus the direct
	// VoltageLevel/Substation attaches collected above (excluding
	// BusbarSection, already handled).
	refsToBay, err := getReferencesToAnyIndexed(store, version, bayRoleIDs)
	if err != nil {
		return nil, fmt.Errorf("common: reverse-lookup for %d Bay/Feeder IDs: %w", len(bayRoleIDs), err)
	}
	refsToHouse, err := getReferencesToAnyIndexed(store, version, houseIDs)
	if err != nil {
		return nil, fmt.Errorf("common: reverse-lookup for %d Building IDs: %w", len(houseIDs), err)
	}

	candidates := map[string]attachInfo{}
	for bayID, recs := range refsToBay {
		for _, r := range recs {
			if r.Attribute == "Equipment.EquipmentContainer" {
				candidates[r.ID] = attachInfo{container: bayToContainer[bayID], class: r.Class}
			}
		}
	}
	for houseID, recs := range refsToHouse {
		for _, r := range recs {
			if r.Attribute == "Equipment.EquipmentContainer" {
				candidates[r.ID] = attachInfo{container: houseID, class: r.Class}
			}
		}
	}
	for eqID, info := range directVLAttach {
		if info.class != "BusbarSection" {
			candidates[eqID] = attachInfo{container: vlToSubstation[info.container], class: info.class}
		}
	}
	for eqID, info := range directSubAttach {
		if info.class != "BusbarSection" {
			candidates[eqID] = info // info.container is already the Substation ID
		}
	}

	candidateIDs := make([]string, 0, len(candidates))
	for id := range candidates {
		candidateIDs = append(candidateIDs, id)
	}
	fullRecs, err := getByIDsIndexed(store, version, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("common: fetching %d candidate Equipment records: %w", len(candidateIDs), err)
	}
	eqIdx := BuildObjectIndex(flattenRecords(fullRecs))

	// Pass 1: PowerElectronicsUnit satellites (Wallbox, PhotoVoltaicUnit,
	// ...) propagate their own resolved container to their
	// PowerElectronicsConnection instead of getting their own
	// EquipmentToCont entry — must run before pass 2 so a PEC that's ALSO
	// directly a batch candidate (own Equipment.EquipmentContainer) is
	// correctly recognized as "already resolved" (mirrors BuildContainers'
	// two-pass ordering, needed there only because of alphabetical class
	// scan order — kept here for identical behavior/determinism, though
	// this rewrite no longer scans classes at all).
	for id, info := range candidates {
		pecID := eqIdx.Ref(id, "PowerElectronicsUnit.PowerElectronicsConnection")
		if pecID == "" {
			continue
		}
		if _, already := res.EquipmentToCont[pecID]; !already {
			res.EquipmentToCont[pecID] = info.container
		}
	}
	// Pass 2: ordinary Equipment.
	for id, info := range candidates {
		if isGeneratingUnitClass(info.class) {
			continue // satellite metadata of a SynchronousMachine, never its own EquipmentToCont entry
		}
		if eqIdx.HasAttr(id, "PowerElectronicsUnit.PowerElectronicsConnection") {
			continue // satellite, handled in pass 1 above
		}
		if _, already := res.EquipmentToCont[id]; already {
			continue
		}
		res.EquipmentToCont[id] = info.container
	}

	return res, nil
}

// flattenRecords concatenates every ID's record group from a
// getByIDsIndexed-style result into one flat slice, for BuildObjectIndex.
func flattenRecords(byID map[string][]importmodel.StagingRecord) []importmodel.StagingRecord {
	var flat []importmodel.StagingRecord
	for _, recs := range byID {
		flat = append(flat, recs...)
	}
	return flat
}
