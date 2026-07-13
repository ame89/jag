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

