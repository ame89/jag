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
) (Phase3Result, error) {
	var result Phase3Result

	voltageViolations, err := checkVoltageLevels(store, version, resolved)
	if err != nil {
		return result, fmt.Errorf("common: checking voltage levels: %w", err)
	}
	result.Violations = append(result.Violations, voltageViolations...)

	result.Violations = append(result.Violations, checkConnectivity(nodes, edges)...)

	result.Violations = append(result.Violations, checkBayCableCount(resolved, containersResult)...)

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

	var violations []InvariantViolation
	for _, eqID := range equipmentIDs {
		records, err := store.GetByID(version, eqID)
		if err != nil {
			return nil, err
		}
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
			ends, err := transformerEndBaseVoltages(store, version, eqID)
			if err != nil {
				return nil, err
			}
			for v := range ends {
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

		if len(voltages) == 0 && containerID != "" {
			v, err := containerBaseVoltage(store, version, containerID)
			if err != nil {
				return nil, err
			}
			if v != "" {
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

// containerBaseVoltage resolves the BaseVoltage of a container reached via
// Equipment.EquipmentContainer, which is either a Bay (-> Bay.VoltageLevel
// -> VoltageLevel.BaseVoltage) or a VoltageLevel directly (->
// VoltageLevel.BaseVoltage) — the same two shapes BuildContainers already
// handles for the same reference (see container.go).
func containerBaseVoltage(store staging.Store, version uint64, containerID string) (string, error) {
	records, err := store.GetByID(version, containerID)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	vlID := containerID
	if records[0].Class == "Bay" {
		vlID = ""
		for _, r := range records {
			if r.IsReference && r.Attribute == "Bay.VoltageLevel" {
				vlID = r.Value
				break
			}
		}
		if vlID == "" {
			return "", nil
		}
	}
	vlRecords, err := store.GetByID(version, vlID)
	if err != nil {
		return "", err
	}
	for _, r := range vlRecords {
		if r.IsReference && r.Attribute == "VoltageLevel.BaseVoltage" {
			return r.Value, nil
		}
	}
	return "", nil
}

// transformerEndBaseVoltages returns the distinct BaseVoltage IDs found
// across every PowerTransformerEnd attached to transformerID (found via the
// reverse reference PowerTransformerEnd.PowerTransformer -> transformerID).
func transformerEndBaseVoltages(store staging.Store, version uint64, transformerID string) (map[string]bool, error) {
	incoming, err := store.GetReferencesTo(version, transformerID)
	if err != nil {
		return nil, err
	}
	voltages := map[string]bool{}
	seenEnds := map[string]bool{}
	for _, r := range incoming {
		if r.Attribute != "PowerTransformerEnd.PowerTransformer" || seenEnds[r.ID] {
			continue
		}
		seenEnds[r.ID] = true
		endRecords, err := store.GetByID(version, r.ID)
		if err != nil {
			return nil, err
		}
		for _, er := range endRecords {
			if er.IsReference && er.Attribute == "TransformerEnd.BaseVoltage" {
				voltages[er.Value] = true
			}
		}
	}
	return voltages, nil
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
func checkBayCableCount(resolved map[string]EquipmentTerminals, cr *BuildContainersResult) []InvariantViolation {
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

	var violations []InvariantViolation
	for _, id := range nodeIDs {
		refs, err := store.GetReferencesTo(version, id)
		if err != nil {
			return nil, fmt.Errorf("common: checking references to %s %s: %w", class, id, err)
		}
		count := 0
		for _, r := range refs {
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

	var violations []InvariantViolation
	for _, id := range equipmentIDs {
		if _, ok := cr.EquipmentToCont[id]; ok {
			continue
		}
		class := "unknown class"
		records, err := store.GetByID(version, id)
		if err != nil {
			return nil, fmt.Errorf("common: looking up class of equipment %s: %w", id, err)
		}
		if len(records) > 0 {
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

