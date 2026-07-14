// Package common — this file is a PROTOTYPE for the "elektrische Topologie"
// view (see Konzept.md, "Topologie"): closed Zero-Ohm switchgear (Fuse,
// Switch, Disconnector, Breaker, ... — any CIM class carrying Switch.open/
// Switch.normalOpen) is treated as 0 Ohm and its two endpoint Nodes are
// merged into one electrical group; an open switch is treated as an
// interruption (infinite Ohm, no merge). No new Node object is created for
// this view (explicit decision, see Konzept.md) — merged real Nodes simply
// share a groupID. This is deliberately NOT wired into CheckInvariants
// (Phase 3) yet — per explicit user instruction, this is a prototype to be
// verified against the CGMES example data first; the Phase 5 partial-model
// merge/reconciliation question (unioning groups across merged models) is
// explicitly deferred, not addressed here at all.
package common

import (
	"fmt"
	"sort"
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// SwitchState describes whether one Equipment carries a CIM Switch.open (or
// its EQ-profile default Switch.normalOpen) attribute and, if so, whether
// it is open or closed. Detected generically by attribute presence, not by
// a hardcoded CIM class whitelist: Switch.open/normalOpen is present on
// every switch-like subclass (Breaker/Disconnector/Fuse/LoadBreakSwitch/
// GroundDisconnector/...), since CIM models them all as subclasses of the
// abstract Switch class (verified directly against the Telemark_LV_Fuse
// and MiniGrid_NodeBreaker_Switchgear example data — Fuse carries
// Switch.open exactly like Breaker does). Per the already-decided "no live
// switching state" rule, this is only ever a static default captured at
// import time: Switch.open (SSH profile — the actual state at import time)
// is preferred over Switch.normalOpen (EQ profile — the class's own
// default) when both are present.
type SwitchState struct {
	IsSwitch bool
	Open     bool
}

// classifySwitch inspects one Equipment's own staging records for
// Switch.open / Switch.normalOpen.
func classifySwitch(records []model.StagingRecord) SwitchState {
	var openVal, normalOpenVal string
	haveOpen, haveNormalOpen := false, false
	for _, r := range records {
		switch r.Attribute {
		case "Switch.open":
			openVal, haveOpen = r.Value, true
		case "Switch.normalOpen":
			normalOpenVal, haveNormalOpen = r.Value, true
		}
	}
	switch {
	case haveOpen:
		return SwitchState{IsSwitch: true, Open: openVal == "true"}
	case haveNormalOpen:
		return SwitchState{IsSwitch: true, Open: normalOpenVal == "true"}
	default:
		return SwitchState{}
	}
}

// SwitchInfo is one switch-like Equipment found while building electrical
// groups, kept for diagnostics/reporting.
type SwitchInfo struct {
	EquipmentID string
	Class       string
	Open        bool
}

// SwitchStateOverrides maps a switch-like Equipment's ID to its current
// open/closed state (true = open), for callers that track a dynamic
// (live) switch state on top of the static default JAG imports (see
// classifySwitch). Pass nil to use the import default for every switch.
// JAG itself does not track live switching state today (see Konzept.md) —
// this is only an extension point so BuildElectricalGroups/BuildCircuits
// can already be driven by a live source once one exists, without any
// further change to the Union-Find logic itself.
type SwitchStateOverrides map[string]bool

// effectiveOpen resolves a switch's open/closed state: the override, if
// present for this Equipment ID, otherwise the static import default. Both
// a nil and an empty overrides map behave identically — every ID simply
// falls back to the import default, since neither has any entries.
func effectiveOpen(overrides SwitchStateOverrides, equipmentID string, importDefault bool) bool {
	if open, ok := overrides[equipmentID]; ok {
		return open
	}
	return importDefault
}

// ElectricalGroups maps each physical Node's ID (ConnectivityNode ID, or
// GNDNodeID) to its electrical group ID. The group ID is the
// lexicographically smallest Node ID among the Nodes merged into that
// group — deterministic, no synthetic ID needed (same convention as
// checkConnectivity's component handling in consistency.go). A Node that
// isn't merged with any other Node is its own, singleton group.
type ElectricalGroups map[string]string

// BuildElectricalGroups computes the electrical topology from the physical
// Node/Edge graph Phase 2 already built (BuildNodesAndEdges): iterative
// Union-Find (path halving, no Go-side recursion, consistent with
// checkConnectivity) over Nodes, unioning the two endpoints of every Edge
// whose Equipment is switch-like AND closed. Edges that aren't switch-like
// at all (cables, transformers, impedance-bearing equipment in general)
// never union, exactly like an open switch — both simply leave their
// endpoints in separate groups.
func BuildElectricalGroups(store staging.Store, version uint64, nodes []coremodel.Node, edges []coremodel.Edge, overrides SwitchStateOverrides) (ElectricalGroups, []SwitchInfo, error) {
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

	edgeIDs := make([]string, len(edges))
	for i, e := range edges {
		edgeIDs[i] = e.EquipmentID
	}
	recordsByID, err := getByIDsIndexed(store, version, edgeIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("common: looking up switch states: %w", err)
	}

	var switches []SwitchInfo
	p := newProgress("electrical-groups")
	for _, e := range edges {
		records := recordsByID[e.EquipmentID]
		if len(records) == 0 {
			p.Tick(1)
			continue
		}
		state := classifySwitch(records)
		if !state.IsSwitch {
			p.Tick(1)
			continue
		}
		open := effectiveOpen(overrides, e.EquipmentID, state.Open)
		switches = append(switches, SwitchInfo{EquipmentID: e.EquipmentID, Class: records[0].Class, Open: open})
		if !open {
			union(e.Terminal1NodeID, e.Terminal2NodeID)
		}
		p.Tick(1)
	}
	p.Done()

	groups := make(ElectricalGroups, len(nodes))
	for _, n := range nodes {
		groups[n.EquipmentID] = find(n.EquipmentID)
	}
	return groups, switches, nil
}

// Circuit is one "Schaltkreis": the set of Nodes and Edges that hang
// together once (a) every PowerTransformer Edge is treated as galvanically
// decoupled (its two sides are never the same circuit — a transformer does
// not conduct DC/is not a simple Zero-Ohm/impedance link between primary
// and secondary in this connectivity sense), (b) every open switch-like
// Edge is treated as an interruption (matches the standing electrical-
// topology rule), and (c) the virtual GND Node is NOT treated as a
// connecting node — traversal stops there. Rule (c) is essential: GND is a
// shared bookkeeping Node used for every single-terminal source/sink
// Equipment (see the Terminal-1/Terminal-2 convention); without stopping
// there, unrelated loads/generators across the whole imported model would
// all appear to hang in one giant "circuit" via that one shared Node,
// which does not reflect any real electrical connection between them.
//
// This is a coarser concept than ElectricalGroups (the Zero-Ohm reduction):
// many electrical groups (busbar sections joined by closed switches) can
// belong to the same Circuit, connected via ordinary impedance-bearing
// Edges (cables, shunt compensators, ...) that never merge Nodes but still
// keep them in the same Circuit.
type Circuit struct {
	ID    string // deterministic: lexicographically smallest member Node ID
	Nodes []coremodel.Node
	Edges []coremodel.Edge
}

// BuildCircuits computes all Circuits for the given physical Node/Edge
// graph (see Circuit's doc comment for the exact rules). It returns the
// circuits themselves plus two lookup maps: nodeCircuit (Node ID -> Circuit
// ID) and edgeCircuits (Equipment ID -> Circuit ID(s) the Edge touches).
// A normal Edge (cable, closed switch, shunt, ...) touches exactly one
// Circuit. A boundary Edge — a PowerTransformer, or an open switch whose
// two sides ended up in different Circuits, or an Edge with GND on one
// side — can touch one or two Circuits; edgeCircuits[id] lists all of
// them (deduplicated, GND side never contributes an entry of its own since
// GND is not part of any Circuit).
func BuildCircuits(store staging.Store, version uint64, nodes []coremodel.Node, edges []coremodel.Edge, overrides SwitchStateOverrides) (circuits map[string]*Circuit, nodeCircuit map[string]string, edgeCircuits map[string][]string, err error) {
	parent := map[string]string{}
	for _, n := range nodes {
		if n.EquipmentID == GNDNodeID {
			continue // GND never participates in a Circuit
		}
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

	type edgeInfo struct {
		class    string
		isSwitch bool
		open     bool
	}
	infoOf := make(map[string]edgeInfo, len(edges))
	edgeIDs := make([]string, len(edges))
	for i, e := range edges {
		edgeIDs[i] = e.EquipmentID
	}
	recordsByID, err := getByIDsIndexed(store, version, edgeIDs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("common: looking up edge classes: %w", err)
	}

	p := newProgress("circuits")
	for _, e := range edges {
		records := recordsByID[e.EquipmentID]
		var info edgeInfo
		if len(records) > 0 {
			info.class = records[0].Class
		}
		state := classifySwitch(records)
		info.isSwitch = state.IsSwitch
		info.open = effectiveOpen(overrides, e.EquipmentID, state.Open)
		infoOf[e.EquipmentID] = info
		p.Tick(1)

		if info.class == "PowerTransformer" {
			continue // galvanically decoupled — never union
		}
		if info.isSwitch && info.open {
			continue // open switch interrupts — never union
		}
		if e.Terminal1NodeID == GNDNodeID || e.Terminal2NodeID == GNDNodeID {
			continue // GND is a dead end, not a connecting hop
		}
		union(e.Terminal1NodeID, e.Terminal2NodeID)
	}
	p.Done()

	nodeCircuit = make(map[string]string, len(nodes))
	circuits = map[string]*Circuit{}
	for _, n := range nodes {
		if n.EquipmentID == GNDNodeID {
			continue
		}
		cid := find(n.EquipmentID)
		nodeCircuit[n.EquipmentID] = cid
		c, ok := circuits[cid]
		if !ok {
			c = &Circuit{ID: cid}
			circuits[cid] = c
		}
		c.Nodes = append(c.Nodes, n)
	}

	edgeCircuits = make(map[string][]string, len(edges))
	for _, e := range edges {
		seen := map[string]bool{}
		var touched []string
		for _, nid := range []string{e.Terminal1NodeID, e.Terminal2NodeID} {
			if nid == GNDNodeID {
				continue
			}
			cid := nodeCircuit[nid]
			if cid != "" && !seen[cid] {
				seen[cid] = true
				touched = append(touched, cid)
			}
		}
		sort.Strings(touched)
		edgeCircuits[e.EquipmentID] = touched
		for _, cid := range touched {
			circuits[cid].Edges = append(circuits[cid].Edges, e)
		}
	}

	return circuits, nodeCircuit, edgeCircuits, nil
}

// GetCircuitElements returns all Nodes and Edges hanging in the Circuit(s)
// that the given Zweipol (Edge, identified by its Equipment ID) touches.
// In the common case the Edge sits fully inside one Circuit and exactly
// one *Circuit is returned. If the Edge is itself a boundary element (a
// PowerTransformer, or an open switch separating two Circuits), it touches
// two distinct Circuits and both are returned — the caller can tell which
// terminal belongs to which via Terminal1NodeID/Terminal2NodeID against
// each Circuit's Nodes.
func GetCircuitElements(store staging.Store, version uint64, nodes []coremodel.Node, edges []coremodel.Edge, edgeID string, overrides SwitchStateOverrides) ([]*Circuit, error) {
	circuits, _, edgeCircuits, err := BuildCircuits(store, version, nodes, edges, overrides)
	if err != nil {
		return nil, err
	}
	cids, ok := edgeCircuits[edgeID]
	if !ok {
		return nil, fmt.Errorf("common: no Edge with Equipment ID %q found", edgeID)
	}
	if len(cids) == 0 {
		return nil, fmt.Errorf("common: Edge %q has no Terminal outside GND — not part of any Circuit", edgeID)
	}
	result := make([]*Circuit, 0, len(cids))
	for _, cid := range cids {
		result = append(result, circuits[cid])
	}
	return result, nil
}

// CheckElectricalTopologyAgainstCGMES cross-checks JAG's own computed
// electrical grouping against CGMES's own ConnectivityNode.TopologicalNode
// reference — present only in node-breaker CGMES sources; entirely absent
// in pure bus-branch sources (no ConnectivityNode layer at all there, see
// Konzept.md's CGMES model-variant decision), in which case this returns no
// violations at all (nothing to compare). A disagreement is reported
// per the explicit decision that any deviation between JAG's own Zero-Ohm
// reduction and CGMES's own precomputed grouping is an import error, not a
// silently accepted difference.
func CheckElectricalTopologyAgainstCGMES(store staging.Store, version uint64, groups ElectricalGroups) ([]InvariantViolation, error) {
	nodeIDs, idx, err := scanClass(store, version, 1000, "ConnectivityNode")
	if err != nil {
		return nil, err
	}

	tnOf := map[string]string{} // ConnectivityNode ID -> TopologicalNode ID
	any := false
	for _, id := range nodeIDs {
		tn := idx.Ref(id, "ConnectivityNode.TopologicalNode")
		if tn != "" {
			tnOf[id] = tn
			any = true
		}
	}
	if !any {
		return nil, nil // bus-branch source (or TP profile not imported) — nothing to compare
	}

	tnToOurGroups := map[string]map[string]bool{}
	groupToTNs := map[string]map[string]bool{}
	for cnID, tn := range tnOf {
		group, ok := groups[cnID]
		if !ok {
			continue // ConnectivityNode never became a Node — caught separately by checkUnreferencedNodes
		}
		if tnToOurGroups[tn] == nil {
			tnToOurGroups[tn] = map[string]bool{}
		}
		tnToOurGroups[tn][group] = true
		if groupToTNs[group] == nil {
			groupToTNs[group] = map[string]bool{}
		}
		groupToTNs[group][tn] = true
	}

	var violations []InvariantViolation

	var tns []string
	for tn := range tnToOurGroups {
		tns = append(tns, tn)
	}
	sort.Strings(tns)
	for _, tn := range tns {
		ourGroups := tnToOurGroups[tn]
		if len(ourGroups) > 1 {
			var gs []string
			for g := range ourGroups {
				gs = append(gs, g)
			}
			sort.Strings(gs)
			violations = append(violations, InvariantViolation{
				ObjectID: tn,
				Rule:     "electrical-topology-mismatch",
				Message: fmt.Sprintf(
					"CGMES TopologicalNode %s spans %d distinct JAG electrical groups (%s) — JAG's own Zero-Ohm reduction disagrees with CGMES's precomputed grouping",
					tn, len(ourGroups), strings.Join(gs, ", "),
				),
			})
		}
	}

	var groupIDs []string
	for g := range groupToTNs {
		groupIDs = append(groupIDs, g)
	}
	sort.Strings(groupIDs)
	for _, g := range groupIDs {
		tnsForGroup := groupToTNs[g]
		if len(tnsForGroup) > 1 {
			var ts []string
			for t := range tnsForGroup {
				ts = append(ts, t)
			}
			sort.Strings(ts)
			violations = append(violations, InvariantViolation{
				ObjectID: g,
				Rule:     "electrical-topology-mismatch",
				Message: fmt.Sprintf(
					"JAG electrical group %s spans %d distinct CGMES TopologicalNodes (%s) — JAG's own Zero-Ohm reduction disagrees with CGMES's precomputed grouping",
					g, len(tnsForGroup), strings.Join(ts, ", "),
				),
			})
		}
	}

	return violations, nil
}
