// Package usecase — this file implements the "Schaltkreis" (Circuit)
// usecases (GetSchaltkreis/GetSchaltkreise) purely on top of the persisted
// model (physical.Store's Node/Edge tables + technical.Store's Sachdaten),
// deliberately NOT on pkg/impl/common's staging-based BuildCircuits/
// classifySwitch. That staging-based implementation needs staging.Store
// (raw Phase-1 CIM records), which is deleted by default after every
// successful import (see common.FinalizeImport / JAG_KEEP_STAGING) — a
// usecase built on it would silently break in the normal post-import
// state. Every fact BuildCircuits needs (an Equipment's raw CIM class, and
// its Switch.open/Switch.normalOpen state) is already persisted as
// ordinary Sachdaten during Phase 2 (see common.AttributeKeyClass's doc
// comment and common/sachdaten.go's attributesAndNeighbors, which passes
// through every literal, non-reference attribute — including
// "Switch.open"/"Switch.normalOpen" — verbatim under its raw CIM attribute
// name), so this file re-derives the same classification purely from
// technical.Store instead.
//
// Electrical-topology rules mirrored here (see Konzept.md, "Topologie" /
// common.Circuit's doc comment): a closed switch-like Edge (Fuse/Breaker/
// Disconnector/... — anything carrying Switch.open or Switch.normalOpen)
// is treated as Zero-Ohm and unions its two Nodes; an open switch-like
// Edge is an interruption and never unions; a PowerTransformer Edge is
// galvanically decoupled and never unions (its OS/US sides are always
// different Circuits); the virtual GND Node never participates in a
// Circuit (traversal stops there).
package usecase

import (
	"fmt"
	"sort"

	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/impl/common"
)

// pageLimit bounds how many rows a single AllNodes/AllEdges page fetches,
// mirroring pkg/exporter/hjson's cursor-pagination convention so RAM stays
// bounded regardless of model size.
const pageLimit = 5000

// switchState mirrors common.SwitchState, computed here from persisted
// Sachdaten instead of staging records.
type switchState struct {
	isSwitch bool
	open     bool
}

// classifyEquipment inspects one Equipment's own persisted Sachdaten
// attributes for its raw CIM class (common.AttributeKeyClass) and, if
// present, its Switch.open / Switch.normalOpen default state — the same
// preference order as common.classifySwitch (Switch.open, the SSH-profile
// actual state, wins over Switch.normalOpen, the EQ-profile class
// default, when both are present).
func classifyEquipment(attrs []coremodel.Attribute) (class string, state switchState) {
	var openVal, normalOpenVal string
	haveOpen, haveNormalOpen := false, false
	for _, a := range attrs {
		switch a.Key {
		case common.AttributeKeyClass:
			if s, ok := a.Value.(string); ok {
				class = s
			}
		case "Switch.open":
			if s, ok := a.Value.(string); ok {
				openVal, haveOpen = s, true
			}
		case "Switch.normalOpen":
			if s, ok := a.Value.(string); ok {
				normalOpenVal, haveNormalOpen = s, true
			}
		}
	}
	switch {
	case haveOpen:
		state = switchState{isSwitch: true, open: openVal == "true"}
	case haveNormalOpen:
		state = switchState{isSwitch: true, open: normalOpenVal == "true"}
	}
	return class, state
}

// allNodesAndEdges pages through every persisted Node and Edge via
// physical.Store's cursor-paginated AllNodes/AllEdges, keeping RAM bounded
// to pageLimit rows at a time regardless of overall model size.
func (s *Service) allNodesAndEdges() ([]coremodel.Node, []coremodel.Edge, error) {
	var nodes []coremodel.Node
	for after := ""; ; {
		page, err := s.Physical.AllNodes(after, pageLimit)
		if err != nil {
			return nil, nil, fmt.Errorf("usecase: paging nodes: %w", err)
		}
		nodes = append(nodes, page...)
		if len(page) < pageLimit {
			break
		}
		after = page[len(page)-1].EquipmentID
	}

	var edges []coremodel.Edge
	for after := ""; ; {
		page, err := s.Physical.AllEdges(after, pageLimit)
		if err != nil {
			return nil, nil, fmt.Errorf("usecase: paging edges: %w", err)
		}
		edges = append(edges, page...)
		if len(page) < pageLimit {
			break
		}
		after = page[len(page)-1].EquipmentID
	}
	return nodes, edges, nil
}

// buildCircuits computes every Circuit for the whole persisted model,
// applying switchStates as overrides on top of each switch's persisted
// import-time default (see common.SwitchStateOverrides's doc comment — a
// nil or empty map simply falls back to every switch's default state, no
// special-casing needed).
func (s *Service) buildCircuits(switchStates map[string]bool) (circuits map[string]*common.Circuit, nodeCircuit map[string]string, edgeCircuits map[string][]string, err error) {
	nodes, edges, err := s.allNodesAndEdges()
	if err != nil {
		return nil, nil, nil, err
	}

	edgeIDs := make([]string, len(edges))
	for i, e := range edges {
		edgeIDs[i] = e.EquipmentID
	}
	attrsByOwner, err := s.attributesByOwner(edgeIDs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("usecase: loading Sachdaten for edges: %w", err)
	}

	parent := map[string]string{}
	for _, n := range nodes {
		if n.EquipmentID == common.GNDNodeID {
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

	for _, e := range edges {
		class, state := classifyEquipment(attrsByOwner[e.EquipmentID])
		if class == "PowerTransformer" {
			continue // galvanically decoupled — never union
		}
		open := state.open
		if override, ok := switchStates[e.EquipmentID]; ok {
			open = override
		}
		if state.isSwitch && open {
			continue // open switch interrupts — never union
		}
		if e.Terminal1NodeID == common.GNDNodeID || e.Terminal2NodeID == common.GNDNodeID {
			continue // GND is a dead end, not a connecting hop
		}
		union(e.Terminal1NodeID, e.Terminal2NodeID)
	}

	nodeCircuit = make(map[string]string, len(nodes))
	circuits = map[string]*common.Circuit{}
	for _, n := range nodes {
		if n.EquipmentID == common.GNDNodeID {
			continue
		}
		cid := find(n.EquipmentID)
		nodeCircuit[n.EquipmentID] = cid
		c, ok := circuits[cid]
		if !ok {
			c = &common.Circuit{ID: cid}
			circuits[cid] = c
		}
		c.Nodes = append(c.Nodes, n)
	}

	edgeCircuits = make(map[string][]string, len(edges))
	for _, e := range edges {
		seen := map[string]bool{}
		var touched []string
		for _, nid := range []string{e.Terminal1NodeID, e.Terminal2NodeID} {
			if nid == common.GNDNodeID {
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

// attributesByOwner loads Sachdaten for the given owner IDs in bounded-size
// chunks and indexes them by OwnerID.
func (s *Service) attributesByOwner(ownerIDs []string) (map[string][]coremodel.Attribute, error) {
	const chunkSize = 500
	result := make(map[string][]coremodel.Attribute, len(ownerIDs))
	for start := 0; start < len(ownerIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(ownerIDs) {
			end = len(ownerIDs)
		}
		attrs, err := s.Technical.GetByOwnerIDs(ownerIDs[start:end])
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			result[a.OwnerID] = append(result[a.OwnerID], a)
		}
	}
	return result, nil
}

// GetSchaltkreis implements the "Schaltkreis eines Betriebsmittels"
// usecase: given a Zweipol Equipment ID (an Edge, e.g. a cable segment or
// a transformer), returns every Circuit that Edge touches. In the common
// case the Edge sits fully inside one Circuit and exactly one *Circuit is
// returned. If the Edge is itself a boundary element (a PowerTransformer,
// or an open switch separating two Circuits), it touches two distinct
// Circuits and both are returned.
//
// switchStates overrides individual switches' persisted import-time
// default open/closed state (true = open), keyed by switch Equipment ID.
// A nil or empty map uses every switch's import default unchanged.
func (s *Service) GetSchaltkreis(equipmentID string, switchStates map[string]bool) ([]*common.Circuit, error) {
	circuits, _, edgeCircuits, err := s.buildCircuits(switchStates)
	if err != nil {
		return nil, err
	}
	cids, ok := edgeCircuits[equipmentID]
	if !ok {
		return nil, fmt.Errorf("usecase: %q is not part of the physical topology (Node/Edge model) — no Edge with this Equipment ID; GetSchaltkreis needs the Equipment ID of a Zweipol element actually present in model_node/model_edge (e.g. a cable, transformer, or switch), not a Container ID (Substation/Bay/Feeder/Busbar-Container)", equipmentID)
	}
	if len(cids) == 0 {
		return nil, fmt.Errorf("usecase: Edge %q has no Terminal outside GND — not part of any Circuit", equipmentID)
	}
	result := make([]*common.Circuit, 0, len(cids))
	for _, cid := range cids {
		result = append(result, circuits[cid])
	}
	return result, nil
}

// GetSchaltkreise implements the "Schaltkreise einer Sammelschiene"
// usecase: given one or more Busbar-Container IDs (see Konzept.md's
// Busbar/Container decision — the Container holding the individual
// BusbarSection Nodes), returns the (deduplicated) Circuits any of their
// BusbarSection Equipment currently belongs to.
//
// Normally a single Busbar Container yields exactly one Circuit (the whole
// bar hangs together). Passing both bars of a genuine Doppelsammelschiene
// (double busbar, two separate Busbar Containers joined by a Kuppelschalter/
// bus-tie) is the more common multi-Circuit scenario in practice: with the
// coupler closed, both bars are one Circuit; with it open (or via
// switchStates), they fall apart into two.
//
// switchStates has the same override semantics as GetSchaltkreis.
func (s *Service) GetSchaltkreise(busbarContainerIDs []string, switchStates map[string]bool) ([]*common.Circuit, error) {
	if len(busbarContainerIDs) == 0 {
		return nil, fmt.Errorf("usecase: at least one Busbar-Container ID is required")
	}

	equipment, err := s.Equipment.GetByContainerIDs(busbarContainerIDs)
	if err != nil {
		return nil, fmt.Errorf("usecase: loading busbar equipment for %v: %w", busbarContainerIDs, err)
	}
	if len(equipment) == 0 {
		return nil, fmt.Errorf("usecase: no Equipment found for Busbar-Container(s) %v — GetSchaltkreise expects the Container ID(s) of a Busbar (holding BusbarSection Equipment), not an arbitrary Equipment/Node/Edge ID", busbarContainerIDs)
	}
	equipmentIDs := make([]string, len(equipment))
	for i, e := range equipment {
		equipmentIDs[i] = e.ID
	}

	nodes, err := s.Physical.GetNodesByIDs(equipmentIDs)
	if err != nil {
		return nil, fmt.Errorf("usecase: loading busbar nodes for %v: %w", busbarContainerIDs, err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("usecase: no Node-role Equipment (physical topology) found for Busbar-Container(s) %v", busbarContainerIDs)
	}

	circuits, nodeCircuit, _, err := s.buildCircuits(switchStates)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var result []*common.Circuit
	var cids []string
	for _, n := range nodes {
		cid, ok := nodeCircuit[n.EquipmentID]
		if !ok || seen[cid] {
			continue
		}
		seen[cid] = true
		cids = append(cids, cid)
	}
	sort.Strings(cids)
	for _, cid := range cids {
		result = append(result, circuits[cid])
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("usecase: Busbar-Container(s) %v have no Node currently assigned to any Circuit", busbarContainerIDs)
	}
	return result, nil
}
