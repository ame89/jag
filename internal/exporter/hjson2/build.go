package hjson2

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	importhjson "gitlab.com/openk-nsc/jag/internal/importer/hjson2"
)

// gndToken mirrors internal/importer/hjson's gndToken (unexported there) —
// duplicated rather than exported across the import/export package
// boundary, since it's a single reserved literal, not shared logic.
const gndToken = "GND"

// kwToMWKeys mirrors internal/importer/hjson's own (unexported) unit
// table, reversed here (MW/MVAr -> kW/kvar) for export. Kept as a small,
// separately-documented duplicate rather than exporting the importer's
// internal table, consistent with this package's own doc comment about
// deliberately small, explicit duplication at this dialect boundary.
var kwToMWKeys = map[string]bool{
	"EnergyConsumer.p":                  true,
	"EnergyConsumer.q":                  true,
	"PowerElectronicsConnection.p":      true,
	"PowerElectronicsConnection.q":      true,
	"PowerElectronicsConnection.ratedS": true,
}

// FileOutput is one file this exporter will write: its final relative
// path (Netzregion/TopLevelDir/id.hjson) and its parsed-shape content
// (reusing internal/importer/hjson's File/Busbar/Bay/Equipment/Segment
// types, so the export side is structurally guaranteed to stay in sync
// with whatever the importer accepts).
type FileOutput struct {
	Netzregion string
	Dir        string // "ONS", "KVS", "Kabel", "Haushalte"
	ID         string // container ID, becomes the filename (before .hjson)
	File       importhjson.File
}

// dirForType maps a container type to its Fachmodell top-level directory
// name (see internal/importer/hjson/toplevel.go's dirNameToType, reversed).
var dirForType = map[coremodel.ContainerType]string{
	common.ContainerTypeSubstation:      "ONS",
	common.ContainerTypeDistributionBox: "KVS",
	common.ContainerTypeHouse:           "Haushalte",
	common.ContainerTypeACLine:          "Kabel",
}

// Build groups a whole Snapshot into one FileOutput per top-level
// container (Substation/KVS/House/ACLine), ready to be written by Write.
// defaultNetzregion is used for every container that has no "region"
// Sachdaten (see this package's doc comment — the current Phase 2
// pipeline doesn't persist container-level Sachdaten at all yet, so this
// fallback applies to every container exported from raw CIM/CGMES/NSC
// data).
func Build(s *Snapshot, defaultNetzregion string) ([]FileOutput, error) {
	var roots []coremodel.Container
	for _, c := range s.Containers {
		if _, ok := dirForType[c.Type]; ok {
			roots = append(roots, c)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].ID < roots[j].ID })

	// nodeEquipment is a global (whole-model) map of every node touched by
	// any Equipment's Edge, used only by classifyInternalACLines below to
	// decide whether an ACLineSegment's remote end(s) stay within a single
	// station. Kept separate from buildBusbarSections' own (per-station)
	// convergence detection, which only needs to see one station's own
	// branches (root/Bay/owned-ACLine) at a time.
	nodeEquipment := map[string][]string{}
	for eqID, edge := range s.Edges {
		for _, n := range []string{edge.Terminal1NodeID, edge.Terminal2NodeID} {
			if n == "" || n == gndToken {
				continue
			}
			nodeEquipment[n] = append(nodeEquipment[n], eqID)
		}
	}

	acLineOwner := classifyInternalACLines(s, roots, nodeEquipment)
	stationACLines := map[string][]coremodel.Container{}
	for _, root := range roots {
		if owner, ok := acLineOwner[root.ID]; ok {
			stationACLines[owner] = append(stationACLines[owner], root)
		}
	}
	for k := range stationACLines {
		sort.Slice(stationACLines[k], func(i, j int) bool { return stationACLines[k][i].ID < stationACLines[k][j].ID })
	}

	var outputs []FileOutput
	for _, root := range roots {
		if _, owned := acLineOwner[root.ID]; owned {
			continue // folded into its owning station's file instead of its own Kabel file — see classifyInternalACLines
		}
		region := regionOf(s, root.ID, defaultNetzregion)
		f := importhjson.File{}
		// Container-level Sachdaten (currently just AttributeKeyName,
		// the Substation/Building's own name — see ResolveBatchContainers'
		// res.Attributes and the 2026-07-19 fix wiring it through to the
		// sink) round-trips through the same generic File.Attributes
		// channel Fachmodell files already use for hand-authored
		// container attributes (MaLo/MeLo etc.) — no dedicated field
		// needed. Equipment-only keys (AttributeKeyClass/
		// AttributeKeySatelliteClass) never apply to a container, so no
		// exclusion is needed here unlike buildAttributes' skipClass.
		f.Attributes = buildAttributes(s, root.ID, false)
		if g, ok := s.GeometryByOwner[root.ID]; ok {
			f.Geometry = &importhjson.GeometryPoint{Lat: g.Lat, Lon: g.Lon}
		}

		switch root.Type {
		case common.ContainerTypeSubstation, common.ContainerTypeDistributionBox:
			buildStation(s, root.ID, &f, stationACLines[root.ID])
		case common.ContainerTypeHouse:
			for _, eq := range s.EquipmentByContainer[root.ID] {
				f.Equipment = append(f.Equipment, buildEquipment(s, root.ID, eq.ID, nil))
			}
		case common.ContainerTypeACLine:
			for _, eq := range s.EquipmentByContainer[root.ID] {
				f.Segments = append(f.Segments, buildSegment(s, root.ID, eq.ID, nil))
			}
		}

		outputs = append(outputs, FileOutput{
			Netzregion: region,
			Dir:        dirForType[root.Type],
			ID:         root.ID,
			File:       f,
		})
	}
	return outputs, nil
}

// containerRootOf walks up containerID's parent chain to find its
// top-level root container (Substation/KVS/House/ACLine — see
// dirForType). Used only by classifyInternalACLines to identify which
// station (if any) a node's OTHER connecting Equipment ultimately belongs
// to.
func containerRootOf(s *Snapshot, containerID string) string {
	seen := map[string]bool{}
	cur := containerID
	for cur != "" && !seen[cur] {
		seen[cur] = true
		c, ok := s.Containers[cur]
		if !ok {
			return ""
		}
		if _, isRoot := dirForType[c.Type]; isRoot {
			return c.ID
		}
		cur = c.ParentID
	}
	return ""
}

// classifyInternalACLines finds every ACLine root container whose
// segments' both ends are wired only to a single Substation/
// DistributionBox's own equipment (its root, a Bay, or another such
// station-internal ACLineSegment) — a short in-station jumper cable
// (e.g. examples/nsc's O-5 station: TRAF-4-ISEG links the Transformer's
// Fuse to the busbar node CN3 as its own ACLineSegment) rather than a real
// route leaving the station. Such an ACLine gets no separate "Kabel" file
// at all — its Segments are instead folded straight into that station's
// own file (see buildStation) — an explicit user decision (2026-07-20): a
// purely station-internal cable is more useful embedded in its owning
// station's file than split into an easy-to-miss separate file, purely
// because CIM/CGMES/NSC happens to model even short in-station jumpers as
// their own ACLineSegment. An ACLineSegment touching two different
// stations, a House, or nothing else at all is left as its own ordinary
// Kabel file, unchanged.
func classifyInternalACLines(s *Snapshot, roots []coremodel.Container, nodeEquipment map[string][]string) map[string]string {
	owner := map[string]string{}
	for _, root := range roots {
		if root.Type != common.ContainerTypeACLine {
			continue
		}
		ownEqs := map[string]bool{}
		for _, eq := range s.EquipmentByContainer[root.ID] {
			ownEqs[eq.ID] = true
		}
		if len(ownEqs) == 0 {
			continue
		}
		var found string
		ok := true
		for _, eq := range s.EquipmentByContainer[root.ID] {
			edge, has := s.Edges[eq.ID]
			if !has {
				continue
			}
			for _, n := range []string{edge.Terminal1NodeID, edge.Terminal2NodeID} {
				if n == "" || n == gndToken {
					continue
				}
				for _, otherID := range nodeEquipment[n] {
					if ownEqs[otherID] {
						continue // another segment of this same ACLine
					}
					otherEq, has := s.Equipment[otherID]
					if !has {
						ok = false
						continue
					}
					r := containerRootOf(s, otherEq.ContainerID)
					rc, known := s.Containers[r]
					if r == "" || !known || (rc.Type != common.ContainerTypeSubstation && rc.Type != common.ContainerTypeDistributionBox) {
						ok = false
						continue
					}
					if found == "" {
						found = r
					} else if found != r {
						ok = false
					}
				}
			}
		}
		if ok && found != "" {
			owner[root.ID] = found
		}
	}
	return owner
}

// buildStation fills f's Busbars/Bays from every child container of
// rootID (busbar and bay/feeder containers — see container.go's
// BuildContainers), plus any Equipment attached directly to the station's
// own root container instead of a child Bay (e.g. an incomplete/orphaned
// PowerTransformer stub with no proper Feeder/Bay assignment — observed in
// examples/nsc's "...ohne_Trafo..." dataset). Such equipment previously
// existed in the model (s.EquipmentByContainer[rootID]) but was silently
// dropped from export since only child containers were walked; it's now
// rendered via the station file's own top-level "equipment" list (the
// same File.Equipment field already used for House files) so the station
// layout as-imported can be fully seen/maintained in the .hjson file.
//
// Busbar node identification (decided with the user 2026-07-20, see this
// package's history): the persisted model (model_node/model_edge) does
// NOT retain any mapping from a BusbarSection Equipment's own ID to the
// real, canonical electrical Node it was merged into during Phase 2
// (MergeBusbarSectionNodes) — the BusbarSection's own ID simply vanishes
// from the graph, only the ConnectivityNode it shares with its real
// electrical neighbors survives. So there is no way to look up "which
// Node does this BusbarSection correspond to". Instead, hjson2 derives
// the answer the other way around, using only data already available in
// a Snapshot (no shared-code change needed): within a station, the real
// electrical node belonging to a Busbar is the node where equipment from
// two or more different "branches" (a Bay, or the station's own
// directly-attached root equipment) meet. A plain in-line chain (e.g.
// Transformer -> Fuse -> internal cable segment) never produces such a
// convergence on its own — only when a second, independent branch (a Bay,
// or another root-level chain) also terminates at the very same node does
// it qualify. This matches every real busbar in examples/nsc's dataset:
// O-5's CN3 (Transformer chain + Feeder FEED-1's Meter both end there),
// A-9's CN9, C-17's CN16 — see the busbar-topology checkpoint history.
//
// Once a station's busbar node(s) are found, each is assigned a short,
// per-station synthetic ID ("BB-1", "BB-2", ...), independent of any
// original CIM object ID (busbar containers/BusbarSection Equipment IDs
// are container-hierarchy artifacts, not electrical identity — see
// container.go's "busbar:"-prefix doc comment). Every piece of Equipment
// that is actually wired to that node (regardless of which Bay or the
// root it lives in) gets its own "Section" under that Busbar (a per-
// connection slot, NOT a distinct electrical point — all Sections of one
// Busbar are, by definition, the very same node, confirmed explicitly by
// the user: "eine Busbar und alle ihre BusbarSections haben immer
// DIESELBE Spannungsebene"). The connecting Equipment's own "connects"
// entry is rewritten from the raw node ID to this Section's long ID
// ("BB-1-1"), so a human reading the file sees "connects: [BB-1-1]"
// instead of an opaque "CN3". Any equipment NOT wired to a busbar node
// keeps its ordinary (shortened) node ID unchanged.
func buildStation(s *Snapshot, rootID string, f *importhjson.File, ownedACLines []coremodel.Container) {
	rootEqs := append([]coremodel.Equipment(nil), s.EquipmentByContainer[rootID]...)
	sort.Slice(rootEqs, func(i, j int) bool { return rootEqs[i].ID < rootEqs[j].ID })

	children := append([]coremodel.Container(nil), s.ChildrenByParent[rootID]...)
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })

	var busbarContainers []coremodel.Container
	var bayContainers []coremodel.Container
	for _, child := range children {
		switch child.Type {
		case common.ContainerTypeBusbar:
			busbarContainers = append(busbarContainers, child)
		case common.ContainerTypeBay:
			bayContainers = append(bayContainers, child)
		}
	}

	overrides := buildBusbarSections(s, rootID, rootEqs, bayContainers, ownedACLines, busbarContainers, f)

	for _, eq := range rootEqs {
		f.Equipment = append(f.Equipment, buildEquipment(s, rootID, eq.ID, overrides))
	}

	for _, child := range bayContainers {
		bay := importhjson.Bay{ID: shortenID(rootID, child.ID)}
		eqs := append([]coremodel.Equipment(nil), s.EquipmentByContainer[child.ID]...)
		sort.Slice(eqs, func(i, j int) bool { return eqs[i].ID < eqs[j].ID })
		for _, eq := range eqs {
			bay.Equipment = append(bay.Equipment, buildEquipment(s, rootID, eq.ID, overrides))
		}
		f.Bays = append(f.Bays, bay)
	}

	// ownedACLines: station-internal ACLineSegments folded straight into
	// this file's own Segments list instead of a separate Kabel file (see
	// classifyInternalACLines) — overrides applies here too, so an inline
	// jumper cable ending at a busbar node (e.g. O-5's TRAF-4-ISEG ->
	// CN3) gets its own "connects"-equivalent (From/To) rewritten to that
	// Busbar Section's ID exactly like ordinary Equipment does.
	for _, ac := range ownedACLines {
		eqs := append([]coremodel.Equipment(nil), s.EquipmentByContainer[ac.ID]...)
		sort.Slice(eqs, func(i, j int) bool { return eqs[i].ID < eqs[j].ID })
		for _, eq := range eqs {
			f.Segments = append(f.Segments, buildSegment(s, rootID, eq.ID, overrides))
		}
	}
}

// buildBusbarSections implements this station's busbar-node convergence
// detection (see buildStation's doc comment) and appends one
// importhjson.Busbar per busbarContainers entry to f.Busbars. It returns
// the "connects" rewrite table: overrides[eqID][nodeID] = the Section long
// ID that eqID's connects entry for nodeID must be rewritten to (e.g.
// overrides["MD-2"]["O-5-CN3"] = "BB-1-2").
//
// busbarContainers and the found convergence nodes are paired up in
// sorted order (busbar container ID ascending <-> node ID ascending) —
// this station's dataset only ever has one busbar container per station,
// so this pairing is unambiguous there; a station with genuinely multiple
// physically separate busbars would need this pairing to be exact, which
// cannot be fully guaranteed from convergence alone, but no such example
// exists in this project's current fixtures (see Konzept.md's Topologie
// section: double-busbar arrangements stay linked via a real coupler
// Equipment and would need further, not-yet-observed handling).
func buildBusbarSections(
	s *Snapshot,
	rootID string,
	rootEqs []coremodel.Equipment,
	bayContainers []coremodel.Container,
	ownedACLines []coremodel.Container,
	busbarContainers []coremodel.Container,
	f *importhjson.File,
) map[string]map[string]string {
	if len(busbarContainers) == 0 {
		return nil
	}

	// branches: one entry for the station's own root-level equipment
	// (including any station-internal ACLineSegment folded into this
	// file, see classifyInternalACLines/buildStation — a jumper cable
	// simply continuing the root's own series chain is part of the same
	// "root" branch, NOT an independent one), plus one further entry per
	// Bay — each branch's member IDs are used only to decide which nodes
	// are touched by 2+ *genuinely different* branches. Bays are always
	// their own branch since a Bay/Feeder is by definition an
	// independently-switched feed; an owned ACLine is deliberately merged
	// into "__root__" rather than given its own branch key, since
	// otherwise a plain degree-2 pass-through point (e.g. O-5's CN2,
	// where the Transformer's Fuse simply hands off to the jumper cable
	// TRAF-4-ISEG in series, no real fork) would wrongly look like a
	// 2-branch convergence purely because the same physical wiring
	// happens to cross a container-type boundary (root Equipment ->
	// ACLineSegment). Only where that cable's OTHER end also meets a
	// genuinely independent branch (a Bay, in O-5's case FEED-1) does a
	// real busbar convergence exist (O-5's CN3).
	type branch struct {
		key string
		eqs []coremodel.Equipment
	}
	rootBranchEqs := append([]coremodel.Equipment(nil), rootEqs...)
	for _, ac := range ownedACLines {
		rootBranchEqs = append(rootBranchEqs, s.EquipmentByContainer[ac.ID]...)
	}
	branches := []branch{{key: "__root__", eqs: rootBranchEqs}}
	for _, bay := range bayContainers {
		branches = append(branches, branch{key: bay.ID, eqs: s.EquipmentByContainer[bay.ID]})
	}

	nodeBranches := map[string]map[string]bool{} // nodeID -> set of branch keys touching it
	for _, br := range branches {
		for _, eq := range br.eqs {
			edge, ok := s.Edges[eq.ID]
			if !ok {
				continue
			}
			for _, n := range []string{edge.Terminal1NodeID, edge.Terminal2NodeID} {
				if n == "" || n == gndToken {
					continue
				}
				if nodeBranches[n] == nil {
					nodeBranches[n] = map[string]bool{}
				}
				nodeBranches[n][br.key] = true
			}
		}
	}

	var candidateNodes []string
	for n, brs := range nodeBranches {
		if len(brs) >= 2 {
			candidateNodes = append(candidateNodes, n)
		}
	}
	sort.Strings(candidateNodes)

	overrides := map[string]map[string]string{}
	addOverride := func(eqID, nodeID, sectionLongID string) {
		if overrides[eqID] == nil {
			overrides[eqID] = map[string]string{}
		}
		overrides[eqID][nodeID] = sectionLongID
	}

	for k, bbContainer := range busbarContainers {
		bb := importhjson.Busbar{ID: fmt.Sprintf("BB-%d", k+1)}
		if k >= len(candidateNodes) {
			// No convergence node found for this busbar container (e.g. a
			// station with only a single branch) — render the busbar
			// with no sections rather than guessing.
			f.Busbars = append(f.Busbars, bb)
			continue
		}
		node := candidateNodes[k]

		// Original BusbarSection Equipment objects for this busbar
		// container — used only as a best-effort source of per-section
		// Attributes/Satellites/Geometry (see buildStation's doc comment:
		// there is no reliable way to know which original BusbarSection
		// corresponded to which connecting Equipment, so original
		// sections are paired index-wise, in ID order, with the
		// connecting Equipment found below).
		origSections := append([]coremodel.Equipment(nil), s.EquipmentByContainer[bbContainer.ID]...)
		sort.Slice(origSections, func(i, j int) bool { return origSections[i].ID < origSections[j].ID })

		// Find every piece of Equipment (station-wide) actually wired to
		// this node, sorted by ID for determinism, and assign each its
		// own Section.
		var connecting []coremodel.Equipment
		for _, br := range branches {
			for _, eq := range br.eqs {
				edge, ok := s.Edges[eq.ID]
				if !ok {
					continue
				}
				if edge.Terminal1NodeID == node || edge.Terminal2NodeID == node {
					connecting = append(connecting, eq)
				}
			}
		}
		sort.Slice(connecting, func(i, j int) bool { return connecting[i].ID < connecting[j].ID })

		for i, eq := range connecting {
			sectionShortID := strconv.Itoa(i + 1)
			sectionLongID := fmt.Sprintf("%s-%d", bb.ID, i+1)
			addOverride(eq.ID, node, sectionLongID)

			sec := importhjson.BusbarSectionEntry{ID: sectionShortID}
			if i < len(origSections) {
				orig := origSections[i].ID
				sec.Attributes = buildAttributes(s, orig, true)
				sec.Satellites = buildSatellites(s, orig)
				sec.Geometry = firstGeometryPoint(buildGeometryPath(s, orig))
			}
			bb.Sections = append(bb.Sections, sec)
		}
		f.Busbars = append(f.Busbars, bb)
	}
	return overrides
}

// buildEquipment reconstructs one ordinary (non-BusbarSection,
// non-ACLineSegment) Equipment entry: its class (from the "cim_class"
// Sachdaten attribute — see AttributeKeyClass's doc comment in
// internal/impl/common/attributekeys.go for why this round-trips through
// Sachdaten instead of a dedicated field), its connects (from its own
// Edge, omitting GND for single-terminal source/sink equipment per the
// auto-GND-wiring decision, and rewritten to a Busbar Section's long ID
// per overrides — see buildBusbarSections), and its remaining literal
// attributes.
func buildEquipment(s *Snapshot, rootID, eqID string, overrides map[string]map[string]string) importhjson.Equipment {
	eq := importhjson.Equipment{ID: shortenID(rootID, eqID)}
	attrs := s.AttributesByOwner[eqID]
	for _, a := range attrs {
		if a.Key == common.AttributeKeyClass {
			eq.Class = fmt.Sprintf("%v", a.Value)
			break
		}
	}
	if edge, ok := s.Edges[eqID]; ok {
		n1 := resolveConnectTarget(rootID, eqID, edge.Terminal1NodeID, overrides)
		if edge.Terminal2NodeID == gndToken {
			eq.Connects = []string{n1}
		} else {
			eq.Connects = []string{n1, resolveConnectTarget(rootID, eqID, edge.Terminal2NodeID, overrides)}
		}
	}
	eq.Attributes = buildAttributes(s, eqID, true)
	eq.Satellites = buildSatellites(s, eqID)
	eq.Geometry = firstGeometryPoint(buildGeometryPath(s, eqID))
	if eq.Class == "Meter" {
		extractMeterSchedules(&eq)
	}
	extractDiscreteControlLimits(&eq)
	return eq
}

// resolveConnectTarget renders one connects entry: nodeID is replaced by
// its Busbar Section's long ID if eqID has an override for it (see
// buildBusbarSections), otherwise it falls back to the ordinary shortened
// node ID.
func resolveConnectTarget(rootID, eqID, nodeID string, overrides map[string]map[string]string) string {
	if m, ok := overrides[eqID]; ok {
		if repl, ok := m[nodeID]; ok {
			return repl
		}
	}
	return shortenID(rootID, nodeID)
}

// extractDiscreteControlLimits implements Equipment.Steps' compaction (see
// types.go's doc comment): if every "DiscreteControlLimit" satellite on eq
// has a parsable DiscreteControlLimit.sequenceNumber/DiscreteControlLimit.value
// and those sequence numbers are exactly 1..N with no gaps/duplicates, they
// are removed from eq.Satellites and replaced by a single ascending Steps
// slice (Steps[i] == sequenceNumber i+1's value). Any other shape is left
// completely untouched.
func extractDiscreteControlLimits(eq *importhjson.Equipment) {
	var limits []importhjson.Satellite
	var rest []importhjson.Satellite
	for _, sat := range eq.Satellites {
		if sat.Class == "DiscreteControlLimit" {
			limits = append(limits, sat)
		} else {
			rest = append(rest, sat)
		}
	}
	if len(limits) == 0 {
		return
	}
	steps := make([]float64, len(limits))
	seen := make([]bool, len(limits))
	for _, sat := range limits {
		seqStr, ok := sat.Attributes["DiscreteControlLimit.sequenceNumber"]
		if !ok {
			return
		}
		seq, err := strconv.Atoi(fmt.Sprintf("%v", seqStr))
		if err != nil || seq < 1 || seq > len(limits) || seen[seq-1] {
			return
		}
		valStr, ok := sat.Attributes["DiscreteControlLimit.value"]
		if !ok {
			return
		}
		val, err := strconv.ParseFloat(fmt.Sprintf("%v", valStr), 64)
		if err != nil {
			return
		}
		steps[seq-1] = val
		seen[seq-1] = true
	}
	eq.Steps = steps
	eq.Satellites = rest
}

// extractMeterSchedules implements Equipment.Measuring/Transmission's
// positional heuristic (see types.go's doc comment on those fields): if
// eq (already known to be class "Meter") has exactly two "TimeSchedule"
// satellites — regardless of any other, non-TimeSchedule satellites also
// present (e.g. a UsagePoint) — the first TimeSchedule becomes Measuring
// and the second Transmission; both are removed from eq.Satellites, any
// other satellites are left in place. Any other shape (0, 1, or 3+
// TimeSchedule satellites) is left completely untouched, falling back to
// the generic "satellites" rendering — this heuristic only ever fires for
// the exact shape it was designed for, never guesses.
func extractMeterSchedules(eq *importhjson.Equipment) {
	var schedules []importhjson.Satellite
	var rest []importhjson.Satellite
	for _, sat := range eq.Satellites {
		if sat.Class == "TimeSchedule" {
			schedules = append(schedules, sat)
		} else {
			rest = append(rest, sat)
		}
	}
	if len(schedules) != 2 {
		return
	}
	eq.Measuring = schedules[0].Attributes
	eq.Transmission = schedules[1].Attributes
	eq.Satellites = rest
}

// buildSegment reconstructs one ACLineSegment as a Segment entry (From/To
// instead of Connects — see internal/importer/hjson.Segment). overrides
// (see buildBusbarSections) rewrites From/To exactly like buildEquipment
// does for Connects — relevant for a station-internal ACLine folded into
// its owning station's own file (see classifyInternalACLines) whose
// From/To lands on that station's busbar node.
func buildSegment(s *Snapshot, rootID, eqID string, overrides map[string]map[string]string) importhjson.Segment {
	seg := importhjson.Segment{ID: shortenID(rootID, eqID)}
	if edge, ok := s.Edges[eqID]; ok {
		seg.From = resolveConnectTarget(rootID, eqID, edge.Terminal1NodeID, overrides)
		seg.To = resolveConnectTarget(rootID, eqID, edge.Terminal2NodeID, overrides)
	}
	// Like BusbarSectionEntry, Segment has no dedicated Class field — its
	// class is always implicitly ACLineSegment — so skip re-surfacing the
	// internal-only cim_class Sachdaten key as a regular attribute here too
	// (consistent with buildEquipment/the busbar-section case above).
	seg.Attributes = buildAttributes(s, eqID, true)
	seg.Satellites = buildSatellites(s, eqID)
	seg.Geometry = buildGeometryPath(s, eqID)
	return seg
}

// buildAttributes renders ownerID's Sachdaten as the map[string]interface{}
// shape internal/importer/hjson.Equipment/Segment/BusbarSectionEntry
// expect, excluding the internal-only AttributeKeyClass key (already
// surfaced separately as Equipment.Class when skipClass is true) and
// reversing the kW/kVA <-> MW/MVA curated-key conversion.
//
// Multi-value keys (coremodel.Attribute's doc comment: "Multi-value keys
// produce multiple Attribute rows sharing the same OwnerID+Key") are
// rendered as a JSON/HJSON array under that one key, rather than
// collapsing to a single (arbitrary, last-write-wins) scalar value — a
// real data-loss bug found 2026-07-18 while exporting a house whose
// PowerElectronicsConnection had a Wallbox satellite: the Wallbox's own
// IdentifiedObject.name ("STEU-24") shared the "IdentifiedObject.name" key
// with the PEC's own name and four DiscreteControlLimit satellites' names,
// and only the last one survived a plain map assignment. A single-value
// key still renders as a plain scalar (not a one-element array), so
// existing hand-authored fixtures/output for ordinary Equipment are
// unaffected. See internal/importer/hjson's addAttributes for the
// corresponding import-side array handling.
func buildAttributes(s *Snapshot, ownerID string, skipClass bool) map[string]interface{} {
	attrs := s.AttributesByOwner[ownerID]
	if len(attrs) == 0 {
		return nil
	}
	order := make([]string, 0, len(attrs))
	grouped := map[string][]interface{}{}
	for _, a := range attrs {
		if skipClass && a.Key == common.AttributeKeyClass {
			continue
		}
		if a.Key == common.AttributeKeySatellite {
			continue // rendered separately, see buildSatellites
		}
		key := string(a.Key)
		val := a.Value
		if kwToMWKeys[key] {
			if f, ok := val.(float64); ok {
				val = f * 1000
			}
		}
		if _, seen := grouped[key]; !seen {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], val)
	}
	if len(grouped) == 0 {
		return nil
	}
	out := map[string]interface{}{}
	for _, key := range order {
		vals := grouped[key]
		if len(vals) == 1 {
			out[key] = vals[0]
		} else {
			out[key] = vals
		}
	}
	return out
}

// buildSatellites decodes ownerID's AttributeKeySatellite entries (see
// that key's doc comment and sachdaten.go's satelliteValue) back into the
// importhjson.Satellite list Equipment/Segment/BusbarSectionEntry expect.
// Each entry is already a self-contained {"class", "attributes"} object —
// decoded straight from JSON via ModelStore's json.Unmarshal into `any`,
// so "attributes" comes back as map[string]interface{} and can be assigned
// directly, no further reshaping needed (unlike buildAttributes, which has
// to regroup a whole owner's flat rows).
func buildSatellites(s *Snapshot, ownerID string) []importhjson.Satellite {
	var out []importhjson.Satellite
	for _, a := range s.AttributesByOwner[ownerID] {
		if a.Key != common.AttributeKeySatellite {
			continue
		}
		obj, ok := a.Value.(map[string]interface{})
		if !ok {
			continue // malformed/unexpected shape; skip rather than fail the whole export
		}
		class, _ := obj["class"].(string)
		if isGeometrySatelliteClass(class) {
			continue // rendered separately as Geometry, see buildGeometryPath
		}
		sat := importhjson.Satellite{Class: class}
		if attrs, ok := obj["attributes"].(map[string]interface{}); ok {
			sat.Attributes = attrs
		}
		out = append(out, sat)
	}
	return out
}

// geometryLocationClasses lists every raw CIM class this dataset set has
// been observed to use for the "Location" role feeding a
// PowerSystemResource.Location reference — not just the plain CIM
// "Location" base class, but also its subclasses. CIM lets a more
// specific Location subtype (e.g. "UsagePointLocation", normally used via
// UsagePoint.UsagePointLocation) be reused as an ordinary
// PowerSystemResource.Location target too — observed 2026-07-20 in the
// NSC example dataset, where a Junction's (Muffe) own Location object is
// modeled as a UsagePointLocation, not a plain Location. Structurally it's
// the exact same GL-profile role (Location -> PositionPoint), so it must
// be excluded from "satellites" exactly like "Location" is. Extend this
// set if a future dataset uses yet another Location subclass this way.
var geometryLocationClasses = map[string]bool{
	"Location":           true,
	"UsagePointLocation": true,
}

// isGeometrySatelliteClass reports whether class is one of the raw CIM GL
// profile classes (Location/PositionPoint, or a Location subclass — see
// geometryLocationClasses) that build.go's Geometry reconstruction
// (buildGeometryPath) already accounts for — these must never also show
// up as a plain "satellites" entry (that would just duplicate the same
// data in two different shapes in the exported file).
func isGeometrySatelliteClass(class string) bool {
	return class == "PositionPoint" || geometryLocationClasses[class]
}

// buildGeometryPath reconstructs ownerID's full WGS84 route from its raw
// "PositionPoint" satellites (see sachdaten.go's satellite walk — neither
// "Location" nor "PositionPoint" is excluded from the generic walk there,
// since internal/impl/common/geometry.go's BuildGeometry already reduces
// them to a single owner-level Geometry point independently and doesn't
// touch the Sachdaten/satellite pipeline at all), sorted ascending by
// PositionPoint.sequenceNumber (ties broken by encounter order — real CIM
// data is expected to have distinct sequence numbers). This is hjson2's
// own, more user-friendly replacement for exporting each PositionPoint as
// a separate raw "{class: \"PositionPoint\", attributes: {...}}" satellite
// block (see build.go's isGeometrySatelliteClass): the caller renders the
// result either as a single {lat, lon} object (Equipment/BusbarSectionEntry
// — see firstGeometryPoint) or as the full ordered array (Segment/
// ACLineSegment, which alone represents a route rather than a single
// point).
func buildGeometryPath(s *Snapshot, ownerID string) []importhjson.GeometryPoint {
	type point struct {
		seq      int
		hasSeq   bool
		lat, lon float64
		hasLat   bool
		hasLon   bool
	}
	var points []point
	for _, a := range s.AttributesByOwner[ownerID] {
		if a.Key != common.AttributeKeySatellite {
			continue
		}
		obj, ok := a.Value.(map[string]interface{})
		if !ok {
			continue
		}
		class, _ := obj["class"].(string)
		if class != "PositionPoint" {
			continue
		}
		attrs, _ := obj["attributes"].(map[string]interface{})
		var p point
		if v, ok := attrs["PositionPoint.sequenceNumber"]; ok {
			p.seq, p.hasSeq = parseAttrInt(v)
		}
		if v, ok := attrs["PositionPoint.xPosition"]; ok {
			p.lon, p.hasLon = parseAttrFloat(v)
		}
		if v, ok := attrs["PositionPoint.yPosition"]; ok {
			p.lat, p.hasLat = parseAttrFloat(v)
		}
		if !p.hasLat || !p.hasLon {
			continue // incomplete PositionPoint, skip rather than emit a bogus 0/0 point
		}
		points = append(points, p)
	}
	if len(points) == 0 {
		return nil
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].seq < points[j].seq })
	out := make([]importhjson.GeometryPoint, len(points))
	for i, p := range points {
		out[i] = importhjson.GeometryPoint{Lat: p.lat, Lon: p.lon}
	}
	return out
}

// firstGeometryPoint returns points' first (lowest-sequenceNumber) entry,
// or nil if empty — used wherever only a single point is wanted
// (Equipment/BusbarSectionEntry), consistent with Konzept.md's Geometrie
// decision that a Node/Equipment's own Geometry is always a single point,
// never a path (only a Segment/ACLine's route is a path, see
// buildGeometryPath's own doc comment).
func firstGeometryPoint(points []importhjson.GeometryPoint) *importhjson.GeometryPoint {
	if len(points) == 0 {
		return nil
	}
	p := points[0]
	return &p
}

// parseAttrInt/parseAttrFloat parse an hjson2-decoded satellite attribute
// value (string, per model.StagingRecord.Value's plain-string convention —
// see satelliteValue's doc comment in sachdaten.go) as int/float64,
// reporting whether parsing succeeded.
func parseAttrInt(v interface{}) (int, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func parseAttrFloat(v interface{}) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// regionOf looks up ownerID's "region" Sachdaten attribute, falling back
// to defaultNetzregion if absent (see this package's doc comment).
func regionOf(s *Snapshot, ownerID, defaultNetzregion string) string {
	for _, a := range s.AttributesByOwner[ownerID] {
		if string(a.Key) == "region" {
			if str, ok := a.Value.(string); ok && str != "" {
				return str
			}
		}
	}
	return defaultNetzregion
}

// shortenID strips a "<rootID>-" prefix if present (the Fachmodell
// importer's own ID-prefixing scheme — see internal/importer/hjson's
// resolveID); GND and IDs without that prefix (e.g. raw CIM/CGMES/NSC
// mRIDs, or cross-file references into a different root) are returned
// unchanged.
func shortenID(rootID, id string) string {
	if id == gndToken {
		return gndToken
	}
	if strings.HasPrefix(id, rootID+"-") {
		return strings.TrimPrefix(id, rootID+"-")
	}
	return id
}
