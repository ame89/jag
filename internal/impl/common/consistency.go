// Package common — this file implements Phase 3 ("Konsistenzprüfung", see
// Konzept.md's "Die Import-Pipeline"): checking the model Phase 2 already
// built against the documented invariants, once it's fully assembled (not
// interleaved into Phase 2 resolution itself). A violation here does not
// abort anything — like every other Phase 2/3 anomaly collector in this
// package, all violations are gathered and reported (Idee.md's Phase 4:
// run to completion, don't stop at the first error).
package common

import (
	"fmt"
	"sort"
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// InvariantViolation describes one Phase 3 invariant failure. Rule
// identifies which invariant was violated (currently "voltage-level",
// "connectivity", "bay-cable-count"), so callers can filter/count by kind
// without parsing Message.
type InvariantViolation struct {
	ObjectID string
	Rule     string
	Message  string
}

// Phase3Result is everything CheckInvariants produces.
type Phase3Result struct {
	Violations []InvariantViolation
}

// CheckInvariants runs Phase 3 against an already Phase-2-built model:
//
//   - "voltage-level": every Equipment must resolve to exactly one
//     BaseVoltage, except PowerTransformer, which is allowed up to two (one
//     per winding side) — see Konzept.md's invariant list.
//   - "connectivity": the whole physical Node/Edge graph must form a single
//     connected component (no islands) — see Konzept.md's invariant list.
//   - "bay-cable-count": at most one cable/ACLine may be connected to any
//     one Bay (decided explicitly with the user 2026-07-13; a Bay/Feeder
//     can have zero or one outgoing cable connection, never two).
//     Relaxed for the NSC dialect (decided 2026-07-14): NSC Feeders may
//     genuinely have several downstream cables/house-connection stubs, so
//     this check is skipped entirely when isNSC is true; CGMES keeps the
//     strict rule.
//   - "container-path": every Container must fit one of the 6 decided
//     parent-child path templates (Konzept.md, "Eltern-Kind-Regeln des
//     Container-Baums") — wrong nesting (e.g. a busbar not directly under a
//     substation/distribution-box) is a hard error.
//   - "kvs-no-transformer": a distribution-box (KVS) container must never
//     have a PowerTransformer assigned to it (Konzept.md, explicit decision).
//   - "unreferenced-node": a ConnectivityNode object with reference count 0
//     is an error (Idee.md's invariant list) — previously only an ad-hoc
//     diagnostic in cmd/phase2check, now a formal Phase 3 rule.
//   - "equipment-without-container": every resolved Equipment must be
//     assigned to exactly one container (Konzept.md's invariant list: "Jedes
//     Betriebsmittel muss ... einem Container zugeordnet sein").
//
// resolved/containersResult/nodes/edges are exactly what Phase 2's
// ResolveTerminals/BuildContainers/BuildNodesAndEdges already produced —
// CheckInvariants does not redo that work, only validates it.
func CheckInvariants(
	store staging.Store,
	version uint64,
	resolved map[string]EquipmentTerminals,
	containersResult *BuildContainersResult,
	nodes []coremodel.Node,
	edges []coremodel.Edge,
	isNSC bool,
) (Phase3Result, error) {
	p := newProgress("phase3-invariants")
	defer p.Done()
	var result Phase3Result

	// TODO(2026-07-14, temporarily disabled per explicit user request):
	// the "voltage-level" check is switched off for now. Root cause: the
	// NSC dialect has no VoltageLevel class at all — Equipment attaches
	// directly to a Feeder (JAG's Bay-equivalent, see BuildContainers) or
	// straight to a Substation, so the Bay.VoltageLevel -> BaseVoltage
	// fallback chain this check relies on can never resolve for NSC data,
	// producing a large number of false-positive violations (e.g. every
	// Fuse in examples/nsc/example_as_cim.xml). Re-enable once a real
	// NSC-appropriate voltage-level source is decided (e.g. a Sachdaten
	// key/attribute carrying nominal voltage directly on Feeder, or some
	// other NSC-specific resolution) — do not just silently leave this
	// off going forward.
	//
	// voltageViolations, err := checkVoltageLevels(store, version, resolved)
	// if err != nil {
	// 	return result, fmt.Errorf("common: checking voltage levels: %w", err)
	// }
	// result.Violations = append(result.Violations, voltageViolations...)

	result.Violations = append(result.Violations, checkConnectivity(nodes, edges)...)
	result.Violations = append(result.Violations, checkStationConnectivity(nodes, edges, containersResult)...)
	result.Violations = append(result.Violations, checkBayCableCount(resolved, containersResult, isNSC)...)
	result.Violations = append(result.Violations, checkContainerPaths(containersResult)...)

	kvsViolations, err := checkKVSNoTransformer(store, version, containersResult)
	if err != nil {
		return result, fmt.Errorf("common: checking KVS-no-transformer: %w", err)
	}
	result.Violations = append(result.Violations, kvsViolations...)

	unreferencedViolations, err := checkUnreferencedNodes(store, version)
	if err != nil {
		return result, fmt.Errorf("common: checking unreferenced nodes: %w", err)
	}
	result.Violations = append(result.Violations, unreferencedViolations...)

	missingContainerViolations, err := checkEquipmentWithoutContainer(store, version, resolved, containersResult)
	if err != nil {
		return result, fmt.Errorf("common: checking equipment-without-container: %w", err)
	}
	result.Violations = append(result.Violations, missingContainerViolations...)

	return result, nil
}


// checkVoltageLevels enforces "every Equipment belongs to exactly one
// voltage level, except the Transformer (which spans two)". The voltage
// level(s) an Equipment resolves to come primarily from its own
// ConductingEquipment.BaseVoltage reference; real CGMES data, however,
// leaves this attribute unset on switchgear (Breaker/Disconnector/Fuse/
// Switch) — there the voltage level is only implied indirectly via the
// container chain (Equipment.EquipmentContainer -> Bay.VoltageLevel, or
// directly -> VoltageLevel) -> VoltageLevel.BaseVoltage, so that chain is
// used as a fallback whenever the direct attribute is absent. For
// PowerTransformer (which carries no BaseVoltage itself either way — see
// Idee.md's observed-attributes section), the voltage levels instead come
// from each attached PowerTransformerEnd's TransformerEnd.BaseVoltage.
func checkVoltageLevels(store staging.Store, version uint64, resolved map[string]EquipmentTerminals) ([]InvariantViolation, error) {
	var equipmentIDs []string
	for id := range resolved {
		equipmentIDs = append(equipmentIDs, id)
	}
	sort.Strings(equipmentIDs)

	eqRecords, err := getByIDsIndexed(store, version, equipmentIDs)
	if err != nil {
		return nil, err
	}

	type eqInfo struct {
		class       string
		voltages    map[string]bool
		containerID string
	}
	infos := make(map[string]eqInfo, len(equipmentIDs))
	var transformerIDs, containerIDs []string
	for _, eqID := range equipmentIDs {
		records := eqRecords[eqID]
		if len(records) == 0 {
			continue // dangling/external reference, nothing to check here
		}
		class := records[0].Class

		voltages := map[string]bool{}
		var containerID string
		for _, r := range records {
			if r.IsReference && r.Attribute == "ConductingEquipment.BaseVoltage" {
				voltages[r.Value] = true
			}
			if r.IsReference && r.Attribute == "Equipment.EquipmentContainer" {
				containerID = r.Value
			}
		}
		if class == "PowerTransformer" {
			transformerIDs = append(transformerIDs, eqID)
		} else if containerID != "" {
			containerIDs = append(containerIDs, containerID)
		}
		infos[eqID] = eqInfo{class: class, voltages: voltages, containerID: containerID}
	}

	// PowerTransformerEnd.BaseVoltage for every PowerTransformer, batched:
	// one reverse-reference lookup for all transformers, then one forward
	// lookup for all their ends.
	incomingToTransformers, err := getReferencesToAnyIndexed(store, version, transformerIDs)
	if err != nil {
		return nil, err
	}
	var endIDs []string
	for _, incoming := range incomingToTransformers {
		for _, r := range incoming {
			if r.Attribute == "PowerTransformerEnd.PowerTransformer" {
				endIDs = append(endIDs, r.ID)
			}
		}
	}
	endRecords, err := getByIDsIndexed(store, version, endIDs)
	if err != nil {
		return nil, err
	}

	// Container fallback (Equipment.EquipmentContainer -> Bay/VoltageLevel
	// -> BaseVoltage) for every non-transformer equipment's container,
	// batched: one lookup for all containers, one for all Bay->VoltageLevel
	// targets found among them.
	containerRecords, err := getByIDsIndexed(store, version, containerIDs)
	if err != nil {
		return nil, err
	}
	var vlIDs []string
	for _, records := range containerRecords {
		if len(records) == 0 || records[0].Class != "Bay" {
			continue
		}
		for _, r := range records {
			if r.IsReference && r.Attribute == "Bay.VoltageLevel" {
				vlIDs = append(vlIDs, r.Value)
			}
		}
	}
	vlRecords, err := getByIDsIndexed(store, version, vlIDs)
	if err != nil {
		return nil, err
	}

	transformerEndVoltages := func(transformerID string) map[string]bool {
		voltages := map[string]bool{}
		seenEnds := map[string]bool{}
		for _, r := range incomingToTransformers[transformerID] {
			if r.Attribute != "PowerTransformerEnd.PowerTransformer" || seenEnds[r.ID] {
				continue
			}
			seenEnds[r.ID] = true
			for _, er := range endRecords[r.ID] {
				if er.IsReference && er.Attribute == "TransformerEnd.BaseVoltage" {
					voltages[er.Value] = true
				}
			}
		}
		return voltages
	}

	containerBaseVoltage := func(containerID string) string {
		records := containerRecords[containerID]
		if len(records) == 0 {
			return ""
		}
		lookupRecords := records
		if records[0].Class == "Bay" {
			vlID := ""
			for _, r := range records {
				if r.IsReference && r.Attribute == "Bay.VoltageLevel" {
					vlID = r.Value
					break
				}
			}
			if vlID == "" {
				return ""
			}
			lookupRecords = vlRecords[vlID]
		}
		for _, r := range lookupRecords {
			if r.IsReference && r.Attribute == "VoltageLevel.BaseVoltage" {
				return r.Value
			}
		}
		return ""
	}

	var violations []InvariantViolation
	for _, eqID := range equipmentIDs {
		info, ok := infos[eqID]
		if !ok {
			continue // dangling/external reference, nothing to check here
		}
		voltages := info.voltages

		if info.class == "PowerTransformer" {
			for v := range transformerEndVoltages(eqID) {
				voltages[v] = true
			}
			switch {
			case len(voltages) == 0:
				violations = append(violations, InvariantViolation{
					ObjectID: eqID, Rule: "voltage-level",
					Message: "PowerTransformer has no resolvable voltage level (no PowerTransformerEnd.BaseVoltage found)",
				})
			case len(voltages) > 2:
				violations = append(violations, InvariantViolation{
					ObjectID: eqID, Rule: "voltage-level",
					Message: fmt.Sprintf("PowerTransformer spans %d distinct voltage levels, expected at most 2", len(voltages)),
				})
			}
			continue
		}

		if len(voltages) == 0 && info.containerID != "" {
			if v := containerBaseVoltage(info.containerID); v != "" {
				voltages[v] = true
			}
		}

		switch len(voltages) {
		case 1:
			// ok
		case 0:
			violations = append(violations, InvariantViolation{
				ObjectID: eqID, Rule: "voltage-level",
				Message: "no ConductingEquipment.BaseVoltage found (directly or via its Bay/VoltageLevel container)",
			})
		default:
			violations = append(violations, InvariantViolation{
				ObjectID: eqID, Rule: "voltage-level",
				Message: fmt.Sprintf("equipment references %d distinct voltage levels, expected exactly 1", len(voltages)),
			})
		}
	}
	return violations, nil
}

// checkConnectivity enforces "all elements form one connected graph from
// the highest voltage level down to GND" by computing connected components
// over the whole physical Node/Edge graph via Union-Find. Everything is
// already resident in memory (built by Phase 2's BuildNodesAndEdges), so
// this is a plain iterative Union-Find over that in-memory adjacency —
// no DB round-trips, no Go-side stack recursion (find() uses an iterative
// loop with path halving, not recursive path compression). The largest
// component is assumed to be the main network; every other (smaller)
// component is reported as a disconnected island.
func checkConnectivity(nodes []coremodel.Node, edges []coremodel.Edge) []InvariantViolation {
	if len(nodes) == 0 {
		return nil
	}

	parent := map[string]string{}
	for _, n := range nodes {
		parent[n.EquipmentID] = n.EquipmentID
	}

	find := func(x string) string {
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

	for _, e := range edges {
		if e.Terminal1NodeID != "" && e.Terminal2NodeID != "" {
			union(e.Terminal1NodeID, e.Terminal2NodeID)
		}
	}

	components := map[string][]string{}
	for _, n := range nodes {
		root := find(n.EquipmentID)
		components[root] = append(components[root], n.EquipmentID)
	}
	if len(components) <= 1 {
		return nil
	}

	var roots []string
	for r := range components {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool {
		if len(components[roots[i]]) != len(components[roots[j]]) {
			return len(components[roots[i]]) > len(components[roots[j]])
		}
		return roots[i] < roots[j] // deterministic tie-break
	})

	var violations []InvariantViolation
	for _, r := range roots[1:] { // keep the largest component as "the main network"
		members := components[r]
		sort.Strings(members)
		violations = append(violations, InvariantViolation{
			ObjectID: members[0],
			Rule:     "connectivity",
			Message:  fmt.Sprintf("disconnected component of %d node(s) not connected to the main network (e.g. member %s)", len(members), members[0]),
		})
	}
	return violations
}

// checkStationConnectivity is the local counterpart to checkConnectivity
// (decided with the user 2026-07-14): checkConnectivity only verifies that
// the WHOLE physical Node/Edge graph forms one component across the entire
// imported model; it says nothing about whether the equipment INSIDE one
// individual Station (Substation/distribution-box), House, or ACLine is
// itself correctly wired together. A piece of equipment could be correctly
// assigned to a Container (equipment-without-container passes) yet still
// be internally disconnected from the rest of that same Container's
// equipment, as long as some other path ties the overall model together —
// checkConnectivity alone would never catch that.
//
// Scope (explicit user decision): only the four "self-contained" top-level
// Container types are checked — Substation, House (CIM Building), a KVS
// (distribution-box), and ACLine (a single cable/line chain, checked
// per-individual-chain, not lumped together). Junction is intentionally
// NOT checked here (out of scope for now, not decided as "correct" or
// "incorrect" — a Muffe/splice container usually holds exactly one
// element, checking it adds no value currently). The user explicitly
// deferred the analogous GLOBAL "orphaned components across the whole
// model" question (checkConnectivity's existing, coarser check already
// covers that, and is intentionally left permissive/non-blocking for now —
// only Station/House/ACLine-internal wiring must be correct today).
//
// GND is deliberately never traversed (explicit user requirement): GND is
// a shared virtual reference point every single-terminal piece of
// equipment's Terminal 2 points at (see nodeedge.go's GNDNodeID), not a
// real physical link between otherwise-unrelated equipment — including it
// in the union-find would falsely merge any two GND-connected pieces of
// equipment into one "component" even if nothing else connects them.
//
// Implementation note (possible future optimization, not done yet):
// BuildNodesAndEdges already runs purely in-memory (no DB access) before
// BuildSachdatenAndGeometryParallel's per-station goroutines start (see
// parallel.go), so this per-station check could in principle be computed
// directly inside each station worker (on that worker's own node/edge
// subset, no additional DB round-trip) instead of as a separate sequential
// Phase 3 pass — deferred for now to keep the first implementation simple
// and easy to verify; revisit once correctness is confirmed.
func checkStationConnectivity(nodes []coremodel.Node, edges []coremodel.Edge, cr *BuildContainersResult) []InvariantViolation {
	byID := make(map[string]coremodel.Container, len(cr.Containers))
	for _, c := range cr.Containers {
		byID[c.ID] = c
	}

	// checkedType reports whether owner (a top-level Container ID, as
	// returned by stationOwnerOf) is one of the four in-scope types.
	checkedType := func(owner string) (coremodel.ContainerType, bool) {
		c, ok := byID[owner]
		if !ok {
			return "", false
		}
		switch c.Type {
		case ContainerTypeSubstation, ContainerTypeHouse, ContainerTypeDistributionBox, ContainerTypeACLine:
			return c.Type, true
		}
		return "", false
	}

	// nodeOwner: Node.EquipmentID -> owning top-level Container ID, only
	// for nodes whose owner is in scope. groupNodes: owner -> its member
	// Node.EquipmentIDs, for the per-owner component check below.
	nodeOwner := make(map[string]string, len(nodes))
	groupNodes := map[string][]string{}
	for _, n := range nodes {
		contID := cr.EquipmentToCont[n.EquipmentID]
		if contID == "" {
			continue // already flagged by equipment-without-container
		}
		owner := stationOwnerOf(contID, byID)
		if _, ok := checkedType(owner); !ok {
			continue
		}
		nodeOwner[n.EquipmentID] = owner
		groupNodes[owner] = append(groupNodes[owner], n.EquipmentID)
	}
	if len(groupNodes) == 0 {
		return nil
	}

	parent := map[string]string{}
	for eqID := range nodeOwner {
		parent[eqID] = eqID
	}
	find := func(x string) string {
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

	for _, e := range edges {
		if e.Terminal1NodeID == "" || e.Terminal2NodeID == "" {
			continue
		}
		if e.Terminal1NodeID == GNDNodeID || e.Terminal2NodeID == GNDNodeID {
			continue // never traverse through GND for this check
		}
		eqContID := cr.EquipmentToCont[e.EquipmentID]
		if eqContID == "" {
			continue
		}
		owner := stationOwnerOf(eqContID, byID)
		if _, ok := checkedType(owner); !ok {
			continue // this Edge's own Container isn't in scope
		}
		// Only union if both endpoints actually belong to this same
		// owner's group — a boundary Edge (e.g. a feeder cable reaching
		// out to an external ACLine/House) has its OWN owner excluded by
		// construction already (its EquipmentID resolves to the ACLine's
		// owner, not the Station's), but this guard keeps the check
		// robust even if a Node's own Container assignment looks
		// otherwise inconsistent.
		if nodeOwner[e.Terminal1NodeID] != owner || nodeOwner[e.Terminal2NodeID] != owner {
			continue
		}
		union(e.Terminal1NodeID, e.Terminal2NodeID)
	}

	var owners []string
	for owner := range groupNodes {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	var violations []InvariantViolation
	for _, owner := range owners {
		members := groupNodes[owner]
		components := map[string][]string{}
		for _, m := range members {
			root := find(m)
			components[root] = append(components[root], m)
		}
		if len(components) <= 1 {
			continue
		}
		var roots []string
		for r := range components {
			roots = append(roots, r)
		}
		sort.Slice(roots, func(i, j int) bool {
			if len(components[roots[i]]) != len(components[roots[j]]) {
				return len(components[roots[i]]) > len(components[roots[j]])
			}
			return roots[i] < roots[j] // deterministic tie-break
		})
		ownerTyp, _ := checkedType(owner)
		for _, r := range roots[1:] { // keep the largest sub-component as "the main part of this container"
			mem := components[r]
			sort.Strings(mem)
			violations = append(violations, InvariantViolation{
				ObjectID: owner,
				Rule:     "station-connectivity",
				Message: fmt.Sprintf(
					"container %s (type %q) has a disconnected internal component of %d node(s) not connected to its main part (e.g. member %s), GND excluded",
					owner, ownerTyp, len(mem), mem[0],
				),
			})
		}
	}
	return violations
}

// checkBayCableCount enforces "at most one cable/ACLine connected per Bay"
// (decided explicitly with the user 2026-07-13, not inferred). A cable is
// "connected to" a Bay if one of its boundary ConnectivityNodes (a Node
// touched by an ACLineSegment that also sits at the edge of its acline
// chain) coincides with a ConnectivityNode also touched by Equipment
// assigned to that Bay. Cables are deduplicated by their acline container
// ID (an acline chain of many ACLineSegments still counts as one cable),
// per BuildContainers' topological ACLine-chain grouping. A cable with
// neither end touching any Bay (or House) is not flagged here at all — an
// isolated cable is either fully floating (both ends open, itself an
// anomaly, but already caught by checkConnectivity as a disconnected
// component) or terminates at a House instead of a Bay (not an anomaly),
// neither of which this specific check needs to distinguish.
func checkBayCableCount(resolved map[string]EquipmentTerminals, cr *BuildContainersResult, isNSC bool) []InvariantViolation {
	// Relaxed for NSC (decided 2026-07-14): the original "at most one
	// cable/ACLine per Bay" rule (decided 2026-07-13) assumed one outgoing
	// feeder cable per Bay, but real NSC example data (example_as_cim.xml)
	// has Feeders legitimately serving multiple downstream cable
	// runs/house-connection stubs (observed up to 5 per Feeder) — this is a
	// genuine NSC distribution-topology characteristic, not a data defect.
	// CGMES data keeps the strict rule unchanged.
	if isNSC {
		return nil
	}

	bayIDs := map[string]bool{}
	for _, c := range cr.Containers {
		if c.Type == ContainerTypeBay {
			bayIDs[c.ID] = true
		}
	}
	if len(bayIDs) == 0 {
		return nil
	}

	// node -> set of Bay container IDs touching it (via Equipment assigned
	// to that Bay whose Node1/Node2 lands there).
	nodeToBays := map[string]map[string]bool{}
	// node -> set of acline container IDs touching it (via ACLineSegment
	// Equipment whose Node1/Node2 lands there).
	nodeToCables := map[string]map[string]bool{}

	for eqID, et := range resolved {
		contID := cr.EquipmentToCont[eqID]
		if contID == "" {
			continue
		}
		isBay := bayIDs[contID]
		isCable := strings.HasPrefix(contID, "acline:")
		if !isBay && !isCable {
			continue
		}
		for _, node := range [2]string{et.Node1, et.Node2} {
			if node == "" {
				continue
			}
			if isBay {
				if nodeToBays[node] == nil {
					nodeToBays[node] = map[string]bool{}
				}
				nodeToBays[node][contID] = true
			}
			if isCable {
				if nodeToCables[node] == nil {
					nodeToCables[node] = map[string]bool{}
				}
				nodeToCables[node][contID] = true
			}
		}
	}

	bayToCables := map[string]map[string]bool{}
	for node, bays := range nodeToBays {
		cables := nodeToCables[node]
		if len(cables) == 0 {
			continue
		}
		for bay := range bays {
			if bayToCables[bay] == nil {
				bayToCables[bay] = map[string]bool{}
			}
			for cable := range cables {
				bayToCables[bay][cable] = true
			}
		}
	}

	var bayKeys []string
	for bay := range bayToCables {
		bayKeys = append(bayKeys, bay)
	}
	sort.Strings(bayKeys)

	var violations []InvariantViolation
	for _, bay := range bayKeys {
		cables := bayToCables[bay]
		if len(cables) <= 1 {
			continue
		}
		var names []string
		for c := range cables {
			names = append(names, c)
		}
		sort.Strings(names)
		violations = append(violations, InvariantViolation{
			ObjectID: bay,
			Rule:     "bay-cable-count",
			Message:  fmt.Sprintf("bay has %d connected cables/aclines, expected at most 1 (%s)", len(cables), strings.Join(names, ", ")),
		})
	}
	return violations
}

// checkContainerPaths enforces "every Container must fit one of the 6
// decided parent-child path templates" (Konzept.md, "Eltern-Kind-Regeln des
// Container-Baums"): substation/acline/junction/distribution-box are
// top-level (no parent, per the Stations-/Kabel-/Muffen-/KVS-Struktur
// templates); bay/busbar must be parented directly under a substation or
// distribution-box (per the Stations-/Sammelschienen-/KVS-Struktur
// templates). Wrong nesting is reported with both the offending
// container's own ID+type and (if resolvable) its parent's ID+type, so a
// user can immediately see which container from the CIM-derived hierarchy
// is misplaced.
func checkContainerPaths(cr *BuildContainersResult) []InvariantViolation {
	byID := make(map[string]coremodel.Container, len(cr.Containers))
	for _, c := range cr.Containers {
		byID[c.ID] = c
	}

	ids := make([]string, 0, len(cr.Containers))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var violations []InvariantViolation
	for _, id := range ids {
		c := byID[id]
		switch c.Type {
		case ContainerTypeSubstation, ContainerTypeACLine, ContainerTypeJunction, ContainerTypeDistributionBox:
			if c.ParentID != "" {
				violations = append(violations, InvariantViolation{
					ObjectID: id,
					Rule:     "container-path",
					Message: fmt.Sprintf(
						"container %s (type %q) must be top-level per its path template, but has parent %s",
						id, c.Type, describeContainer(byID, c.ParentID),
					),
				})
			}
		case ContainerTypeBay, ContainerTypeBusbar:
			if c.ParentID == "" {
				violations = append(violations, InvariantViolation{
					ObjectID: id,
					Rule:     "container-path",
					Message: fmt.Sprintf(
						"container %s (type %q) has no parent, but must be nested directly under a substation or distribution-box container",
						id, c.Type,
					),
				})
				continue
			}
			parent, ok := byID[c.ParentID]
			if !ok {
				violations = append(violations, InvariantViolation{
					ObjectID: id,
					Rule:     "container-path",
					Message: fmt.Sprintf(
						"container %s (type %q) references parent container %s, which does not exist in the built container set",
						id, c.Type, c.ParentID,
					),
				})
				continue
			}
			if parent.Type != ContainerTypeSubstation && parent.Type != ContainerTypeDistributionBox {
				violations = append(violations, InvariantViolation{
					ObjectID: id,
					Rule:     "container-path",
					Message: fmt.Sprintf(
						"container %s (type %q) has parent %s (type %q), but its path template requires a substation or distribution-box parent",
						id, c.Type, c.ParentID, parent.Type,
					),
				})
			}
		}
	}
	return violations
}

// describeContainer renders a short "id (type X)" description for a
// container ID, falling back to "id (unknown container)" when the parent
// itself can't be resolved — used to keep container-path violation
// messages self-contained even when the parent reference is dangling.
func describeContainer(byID map[string]coremodel.Container, id string) string {
	if c, ok := byID[id]; ok {
		return fmt.Sprintf("%s (type %q)", id, c.Type)
	}
	return fmt.Sprintf("%s (unknown container)", id)
}

// checkKVSNoTransformer enforces the explicit decision "a KVS
// (distribution-box) container must never have a PowerTransformer assigned
// to it" (Konzept.md). It scans the PowerTransformer class directly
// (chunked, see staging.Store.GetByClass/scanClass) rather than relying
// only on already-resolved Equipment, so a mis-nested Transformer is
// reported under its own specific rule name (not just generically via
// checkContainerPaths), with the PowerTransformer's own ID and its
// distribution-box container's ID both named in the message.
func checkKVSNoTransformer(store staging.Store, version uint64, cr *BuildContainersResult) ([]InvariantViolation, error) {
	kvsIDs := map[string]bool{}
	for _, c := range cr.Containers {
		if c.Type == ContainerTypeDistributionBox {
			kvsIDs[c.ID] = true
		}
	}
	if len(kvsIDs) == 0 {
		return nil, nil
	}

	transformerIDs, _, err := scanClass(store, version, 1000, "PowerTransformer")
	if err != nil {
		return nil, err
	}

	var violations []InvariantViolation
	for _, id := range transformerIDs {
		contID := cr.EquipmentToCont[id]
		if contID != "" && kvsIDs[contID] {
			violations = append(violations, InvariantViolation{
				ObjectID: id,
				Rule:     "kvs-no-transformer",
				Message: fmt.Sprintf(
					"PowerTransformer %s is assigned to distribution-box (KVS) container %s, but a KVS must never contain a Transformer",
					id, contID,
				),
			})
		}
	}
	return violations, nil
}

// checkUnreferencedNodes enforces "a ConnectivityNode object with reference
// count 0 is an error" (Idee.md's invariant list; previously only an ad-hoc
// diagnostic in cmd/phase2check). It scans the raw ConnectivityNode class
// (chunked) and, for each one, counts incoming Terminal.ConnectivityNode
// references directly against the staging store (staging.Store.
// GetReferencesTo) rather than checking membership in the already-built
// Node set: the built Node set can legitimately omit a ConnectivityNode's
// original ID after BusbarSection auto-merging (see busbarmerge.go), which
// would make a membership-based check report a false positive for a node
// that is, in fact, correctly wired up. Checking the raw reference count
// against the staging data instead reports only genuinely orphaned CIM
// objects, naming the object's own ID plus its CIM class directly in the
// message so it's immediately clear which element in the source file is
// dangling.
//
// Pure bus-branch CGMES sources have no ConnectivityNode class at all (see
// terminals.go's ResolveTerminals fallback / Konzept.md's "CGMES kennt zwei
// grundverschiedene Modellvarianten") — there, TopologicalNode is the only
// node layer, referenced directly via Terminal.TopologicalNode. The same
// "orphaned node" invariant is checked there too, so it isn't silently
// skipped for such sources.
func checkUnreferencedNodes(store staging.Store, version uint64) ([]InvariantViolation, error) {
	connViolations, err := checkUnreferencedNodesOfClass(store, version, "ConnectivityNode", "Terminal.ConnectivityNode")
	if err != nil {
		return nil, err
	}
	topoViolations, err := checkUnreferencedNodesOfClass(store, version, "TopologicalNode", "Terminal.TopologicalNode")
	if err != nil {
		return nil, err
	}
	return append(connViolations, topoViolations...), nil
}

// checkUnreferencedNodesOfClass is the shared implementation behind
// checkUnreferencedNodes for one (node class, referencing Terminal
// attribute) pair.
func checkUnreferencedNodesOfClass(store staging.Store, version uint64, class, refAttribute string) ([]InvariantViolation, error) {
	nodeIDs, _, err := scanClass(store, version, 1000, class)
	if err != nil {
		return nil, err
	}

	refsByTarget, err := getReferencesToAnyIndexed(store, version, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("common: checking references to %s nodes: %w", class, err)
	}

	var violations []InvariantViolation
	for _, id := range nodeIDs {
		count := 0
		for _, r := range refsByTarget[id] {
			if r.IsReference && r.Attribute == refAttribute {
				count++
			}
		}
		if count == 0 {
			violations = append(violations, InvariantViolation{
				ObjectID: id,
				Rule:     "unreferenced-node",
				Message:  fmt.Sprintf("%s %s (CIM class %s) is never referenced by any %s — reference count 0", class, id, class, refAttribute),
			})
		}
	}
	return violations, nil
}

// checkEquipmentWithoutContainer enforces "every resolved Equipment must be
// assigned to exactly one container" (Konzept.md's invariant list). This is
// a completeness check complementary to BuildContainers' own Anomalies list
// (which only reports *invalid*/unresolvable container references, not
// *missing* ones): any Equipment ID present in `resolved` (i.e. it passed
// Terminal resolution) but absent from EquipmentToCont never got a
// container assigned at all. The CIM class of each affected Equipment is
// looked up directly (store.GetByID) so the message names both its ID and
// its concrete CIM type, e.g. "Breaker abc123 has no assigned container",
// making it immediately clear which kind of element in the source file is
// missing its Equipment.EquipmentContainer.
func checkEquipmentWithoutContainer(store staging.Store, version uint64, resolved map[string]EquipmentTerminals, cr *BuildContainersResult) ([]InvariantViolation, error) {
	var equipmentIDs []string
	for id := range resolved {
		equipmentIDs = append(equipmentIDs, id)
	}
	sort.Strings(equipmentIDs)

	var missingIDs []string
	for _, id := range equipmentIDs {
		if _, ok := cr.EquipmentToCont[id]; !ok {
			missingIDs = append(missingIDs, id)
		}
	}

	missingRecords, err := getByIDsIndexed(store, version, missingIDs)
	if err != nil {
		return nil, fmt.Errorf("common: looking up classes of equipment without container: %w", err)
	}

	var violations []InvariantViolation
	for _, id := range missingIDs {
		class := "unknown class"
		if records := missingRecords[id]; len(records) > 0 {
			class = records[0].Class
		}
		violations = append(violations, InvariantViolation{
			ObjectID: id,
			Rule:     "equipment-without-container",
			Message:  fmt.Sprintf("%s %s has no assigned container (Equipment.EquipmentContainer missing or unresolved)", class, id),
		})
	}
	return violations, nil
}

