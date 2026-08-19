package hjson

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/impl/common"
	importhjson "github.com/ame89/jag/pkg/importer/hjson"
)

// gndToken mirrors pkg/importer/hjson's gndToken (unexported there) —
// duplicated rather than exported across the import/export package
// boundary, since it's a single reserved literal, not shared logic.
const gndToken = "GND"

// kwToMWKeys mirrors pkg/importer/hjson's own (unexported) unit
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
// (reusing pkg/importer/hjson's File/Busbar/Bay/Equipment/Segment
// types, so the export side is structurally guaranteed to stay in sync
// with whatever the importer accepts).
type FileOutput struct {
	Netzregion string
	// SubNetzregion is the optional second-level Netzregion path segment
	// (see importhjson.FileInfo.SubNetzregion / AttributeKeySubRegion's
	// doc comment). Empty means the file exports directly under its
	// Netzregion, matching the original 3-segment layout.
	SubNetzregion string
	Dir           string // "ONS", "KVS", "Kabel", "Haushalte"
	ID            string // container ID, becomes the filename (before .hjson)
	File          importhjson.File
}

// dirForType maps a container type to its Fachmodell top-level directory
// name (see pkg/importer/hjson/toplevel.go's dirNameToType, reversed).
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

	nodeKabelFile, nodeStationFile := buildCrossFileRefs(s, acLineOwner, nodeEquipment, defaultNetzregion)

	var outputs []FileOutput
	for _, root := range roots {
		if _, owned := acLineOwner[root.ID]; owned {
			continue // folded into its owning station's file instead of its own Kabel file — see classifyInternalACLines
		}
		region, subregion := regionSubregionFor(s, root.ID, root.Type, nodeEquipment, defaultNetzregion)
		fromDirPath := dirPathOf(region, subregion, dirForType[root.Type])
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

		fileID := root.ID
		switch root.Type {
		case common.ContainerTypeSubstation, common.ContainerTypeDistributionBox:
			buildStation(s, root.ID, &f, stationACLines[root.ID], fromDirPath, nodeKabelFile)
		case common.ContainerTypeHouse:
			for _, eq := range s.EquipmentByContainer[root.ID] {
				f.Equipment = append(f.Equipment, buildEquipment(s, root.ID, eq.ID, fromDirPath, nodeKabelFile))
			}
		case common.ContainerTypeACLine:
			for _, eq := range s.EquipmentByContainer[root.ID] {
				f.Segments = append(f.Segments, buildSegment(s, root.ID, eq.ID, fromDirPath, nodeStationFile))
			}
			// The "acline:" prefix (see acline_streaming.go's
			// buildACLineComponentResult) is an internal disambiguation
			// marker only needed to keep container IDs distinct from
			// Equipment/Node IDs elsewhere in the model — it has no
			// value in a human-facing filename, which is otherwise
			// exactly "<firstACLineSegmentID>_<lastACLineSegmentID>"
			// (colon already turned into "_" by sanitizeSegment).
			fileID = strings.TrimPrefix(root.ID, "acline:")
		}

		outputs = append(outputs, FileOutput{
			Netzregion:    region,
			SubNetzregion: subregion,
			Dir:           dirForType[root.Type],
			ID:            fileID,
			File:          f,
		})
	}

	if boundary := buildBoundaryEquipment(s, defaultNetzregion); len(boundary) > 0 {
		outputs = append(outputs, boundary...)
	}
	return outputs, nil
}

// boundaryDir is the top-level directory name for containerless equipment
// (see importhjson.TopLevelBoundary's doc comment) — not part of
// dirForType since it has no associated coremodel.ContainerType (no
// container is ever created for these).
const boundaryDir = "Grenzknoten"

// buildBoundaryEquipment collects every Equipment that has an Edge (i.e.
// is part of the node-edge model) but no Equipment/Container membership
// at all — currently only boundary EquivalentInjection (see Konzept.md's
// resolveBoundaryEquivalents doc comment: some CIM EquivalentInjection
// attach only to a boundary "Line" object that may not even be imported,
// so Pass B deliberately leaves them containerless rather than inventing
// a synthetic parent). Grouped into one "Boundary.hjson" file per
// Netzregion (added 2026-07-21 on user request, so these otherwise-
// invisible-to-hjson2 equipment round-trip through export/import too).
func buildBoundaryEquipment(s *Snapshot, defaultNetzregion string) []FileOutput {
	var orphanIDs []string
	for eqID := range s.Edges {
		if _, hasContainer := s.Equipment[eqID]; !hasContainer {
			orphanIDs = append(orphanIDs, eqID)
		}
	}
	if len(orphanIDs) == 0 {
		return nil
	}
	sort.Strings(orphanIDs)

	// regionKey bundles region+subregion so boundary equipment in
	// different Subnetzregionen (even under the same Netzregion) land in
	// separate Boundary.hjson files, matching every other top-level file.
	type regionKey struct{ region, subregion string }
	byRegion := map[regionKey][]string{}
	for _, eqID := range orphanIDs {
		key := regionKey{regionOf(s, eqID, defaultNetzregion), resolvedSubregion(s, eqID)}
		byRegion[key] = append(byRegion[key], eqID)
	}

	keys := make([]regionKey, 0, len(byRegion))
	for key := range byRegion {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].region != keys[j].region {
			return keys[i].region < keys[j].region
		}
		return keys[i].subregion < keys[j].subregion
	})

	var outputs []FileOutput
	for _, key := range keys {
		fromDirPath := dirPathOf(key.region, key.subregion, boundaryDir)
		f := importhjson.File{}
		for _, eqID := range byRegion[key] {
			// rootID = "" — boundary equipment IDs are raw CIM mRIDs with
			// no shared station prefix to strip (see shortenID: an ID
			// without the "<rootID>-" prefix is returned unchanged), and
			// there is no cross-file node ref tracking for boundary
			// equipment's own nodes (nil map).
			f.Equipment = append(f.Equipment, buildEquipment(s, "", eqID, fromDirPath, nil))
		}
		outputs = append(outputs, FileOutput{
			Netzregion:    key.region,
			SubNetzregion: key.subregion,
			Dir:           boundaryDir,
			ID:            "Boundary",
			File:          f,
		})
	}
	return outputs
}

// fileRef names one exported file (its full export directory path plus
// filename, matching FileOutput's own fields) — used only to compute the
// relative-path cross-reference comments below (see relativeFileRef).
type fileRef struct {
	DirPath string // e.g. "Nord/ONS" or, with a Subnetzregion, "Nord/West/ONS"
	ID      string // fileID, exactly as used for that file's own filename
}

// buildCrossFileRefs scans every Equipment's Edge once and, for every
// node touched by more than one top-level file's own equipment, records
// which Kabel file(s) and which Substation/KVS/House file(s) touch it —
// added 2026-07-21 on user request, to let a reader immediately see, from
// a Station/House file, which external Kabel file continues at one of its
// nodes, and vice versa from a Kabel file which Station/House it ends at.
// An ACLineSegment folded into its owning station (see
// classifyInternalACLines) is redirected to that owning station's own
// file identity here (effRoot), since it has no separate Kabel file of
// its own to reference.
//
// Returns two maps keyed by raw (unshortened) node ID: nodeKabelRefs (the
// Kabel file(s) touching that node, for annotating Station/House files)
// and nodeStationRefs (the Station/House file(s) touching that node, for
// annotating Kabel files). A node only appears in a returned map if it is
// touched by at least one file of that map's own kind — a purely
// station-internal or purely Kabel-internal node is absent from both,
// which is exactly the desired "no comment" behavior at render time (see
// write.go's writeEquipment/writeSegment).
func buildCrossFileRefs(s *Snapshot, acLineOwner map[string]string, nodeEquipment map[string][]string, defaultNetzregion string) (nodeKabelRefs, nodeStationRefs map[string][]fileRef) {
	nodeKabelRefs = map[string][]fileRef{}
	nodeStationRefs = map[string][]fileRef{}
	refCache := map[string]fileRef{}
	seen := map[string]map[string]bool{} // nodeID -> set of effRootID already recorded

	for eqID, edge := range s.Edges {
		eq, ok := s.Equipment[eqID]
		if !ok {
			continue
		}
		root := containerRootOf(s, eq.ContainerID)
		if root == "" {
			continue
		}
		rc, ok := s.Containers[root]
		if !ok {
			continue
		}
		effRoot, effType := root, rc.Type
		if rc.Type == common.ContainerTypeACLine {
			if owner, isOwned := acLineOwner[root]; isOwned {
				effRoot = owner
				if oc, ok2 := s.Containers[owner]; ok2 {
					effType = oc.Type
				}
			}
		}
		ref, cached := refCache[effRoot]
		if !cached {
			id := effRoot
			if effType == common.ContainerTypeACLine {
				id = strings.TrimPrefix(effRoot, "acline:")
			}
			region, subregion := regionSubregionFor(s, effRoot, effType, nodeEquipment, defaultNetzregion)
			dirPath := dirPathOf(region, subregion, dirForType[effType])
			ref = fileRef{DirPath: dirPath, ID: id}
			refCache[effRoot] = ref
		}
		for _, n := range []string{edge.Terminal1NodeID, edge.Terminal2NodeID} {
			if n == "" || n == gndToken {
				continue
			}
			if seen[n] == nil {
				seen[n] = map[string]bool{}
			}
			if seen[n][effRoot] {
				continue
			}
			seen[n][effRoot] = true
			if effType == common.ContainerTypeACLine {
				nodeKabelRefs[n] = append(nodeKabelRefs[n], ref)
			} else {
				nodeStationRefs[n] = append(nodeStationRefs[n], ref)
			}
		}
	}

	sortFileRefs := func(m map[string][]fileRef) {
		for n := range m {
			sort.Slice(m[n], func(i, j int) bool {
				if m[n][i].DirPath != m[n][j].DirPath {
					return m[n][i].DirPath < m[n][j].DirPath
				}
				return m[n][i].ID < m[n][j].ID
			})
		}
	}
	sortFileRefs(nodeKabelRefs)
	sortFileRefs(nodeStationRefs)
	return nodeKabelRefs, nodeStationRefs
}

// relativeFileRef renders r as a path relative to a file located directly
// under fromDirPath (see FileOutput.Netzregion/SubNetzregion/Dir and
// dirPathOf) by walking up to the deepest shared ancestor directory and
// back down into r's own directory — a straightforward generalization of
// the original fixed "one Netzregion level" logic (added 2026-07-21) that
// now also handles the optional Subnetzregion nesting level without
// special-casing it: "../<Dir>/<ID>.hjson" if r shares the same
// Netzregion+Subnetzregion, "../../<Subnetzregion-or-Dir>/.../<ID>.hjson"
// otherwise, however many levels actually differ.
func relativeFileRef(fromDirPath string, r fileRef) string {
	name := sanitizeSegment(r.ID) + ".hjson"
	fromParts := strings.Split(fromDirPath, "/")
	toParts := strings.Split(r.DirPath, "/")
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	parts := make([]string, 0, len(fromParts)-common+len(toParts)-common+1)
	for i := 0; i < len(fromParts)-common; i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, toParts[common:]...)
	parts = append(parts, name)
	return strings.Join(parts, "/")
}

// formatFileRefs joins every ref in refs (see buildCrossFileRefs) into one
// comma-separated comment string of relative paths (see relativeFileRef),
// or "" if refs is empty (the overwhelmingly common case: most nodes
// don't cross a file boundary at all).
func formatFileRefs(fromDirPath string, refs []fileRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, relativeFileRef(fromDirPath, r))
	}
	return strings.Join(parts, ", ")
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
// rendered via the station file's own top-level "equipments" list (the
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
// per-station synthetic local ID ("@BB-1", "@BB-2", ...; the "@" prefix
// marks it explicitly as file-local, see pkg/importer/hjson2's
// localIDPrefix/resolveID), independent of any original CIM object ID
// (busbar containers/BusbarSection Equipment IDs are container-hierarchy
// artifacts, not electrical identity — see container.go's "busbar:"-
// prefix doc comment). Every piece of Equipment that is actually wired to
// that node (regardless of which Bay or the root it lives in) gets its
// own "Section" under that Busbar (a per-connection slot, NOT a distinct
// electrical point — all Sections of one Busbar are, by definition, the
// very same node, confirmed explicitly by the user: "eine Busbar und alle
// ihre BusbarSections haben immer DIESELBE Spannungsebene"). The
// connecting Equipment's own "connects" entry is rewritten from the raw
// node ID to this Section's long local ID ("@BB-1-1"), so a human reading
// the file sees "connects: [@BB-1-1]" instead of an opaque "CN3". Any
// equipment NOT wired to a busbar node keeps its ordinary (shortened)
// node ID unchanged.
func buildStation(s *Snapshot, rootID string, f *importhjson.File, ownedACLines []coremodel.Container, fromDirPath string, nodeKabelRefs map[string][]fileRef) {
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

	buildBusbarSections(s, rootID, rootEqs, bayContainers, ownedACLines, busbarContainers, f)

	for _, eq := range rootEqs {
		f.Equipment = append(f.Equipment, buildEquipment(s, rootID, eq.ID, fromDirPath, nodeKabelRefs))
	}

	for _, child := range bayContainers {
		bay := importhjson.Bay{ID: shortenID(rootID, child.ID)}
		// Bay's own container-level Sachdaten (currently just "name") —
		// see Bay.Attributes' doc comment in
		// pkg/importer/hjson2/types.go for why this is needed at
		// all (previously silently dropped, defaulting to the Bay's own
		// ID on reimport).
		bay.Attributes = buildAttributes(s, child.ID, false)
		eqs := append([]coremodel.Equipment(nil), s.EquipmentByContainer[child.ID]...)
		sort.Slice(eqs, func(i, j int) bool { return eqs[i].ID < eqs[j].ID })
		for _, eq := range eqs {
			bay.Equipment = append(bay.Equipment, buildEquipment(s, rootID, eq.ID, fromDirPath, nodeKabelRefs))
		}
		f.Bays = append(f.Bays, bay)
	}

	// ownedACLines: station-internal ACLineSegments folded straight into
	// this file's own Segments list instead of a separate Kabel file (see
	// classifyInternalACLines) — an inline jumper cable ending at a
	// busbar node (e.g. O-5's TRAF-4-ISEG -> CN3) renders its From/To for
	// that node exactly like ordinary Equipment does (see
	// resolveConnectTarget/shortenID — the busbar's own ID already IS
	// that node's shortened form, so no special-casing is needed here).
	for _, ac := range ownedACLines {
		eqs := append([]coremodel.Equipment(nil), s.EquipmentByContainer[ac.ID]...)
		sort.Slice(eqs, func(i, j int) bool { return eqs[i].ID < eqs[j].ID })
		for _, eq := range eqs {
			f.Segments = append(f.Segments, buildSegment(s, rootID, eq.ID, fromDirPath, nodeKabelRefs))
		}
	}
}

// buildBusbarSections finds each busbar container's real electrical Node
// directly from the AttributeKeyBusbarNode bookkeeping written by
// pkg/impl/common's Pass A (ProcessStationBatch, see that key's doc
// comment) — one row per original BusbarSection Equipment ID, holding the
// canonical Node ID it was merged into by MergeBusbarSectionNodes. This
// replaced an earlier "2+ independent branches converge on this node"
// guessing heuristic (found 2026-07-21, ReliCapGrid_Espheim round-trip
// investigation, to misidentify ordinary series pass-through points as
// spurious busbars whenever a station's busbar-adjacent disconnectors hang
// directly off the station root instead of a dedicated Bay, silently
// turning 48 real Circuits into 303 after an export/reimport cycle) — the
// persisted attribute is exact, no guessing needed.
//
// Busbar.ID is simply the shortened form of that real Node ID (e.g.
// "@CN3" — see shortenID), NOT an arbitrary synthetic "@BB-1" name.
// Consequence (decided with the user 2026-07-21, "Selbst die IDs aus dem
// CIM kann man korrekt durchreichen"): every connecting Equipment's own
// "connects"/From/To entry for this node is left completely untouched —
// it already renders as this exact same shortened ID via the ordinary
// resolveConnectTarget fallback (shortenID(rootID, nodeID)), since that's
// literally the same node. No connects-rewrite/override table is needed
// at all: a Busbar Section is purely an informational attribute-holder
// (e.g. per-section ipMax), never a distinct connects target — import and
// export are symmetric by construction, with no post-hoc node-merging
// step required on reimport.
//
// It appends one importhjson.Busbar per busbarContainers entry to f.Busbars.
func buildBusbarSections(
	s *Snapshot,
	rootID string,
	rootEqs []coremodel.Equipment,
	bayContainers []coremodel.Container,
	ownedACLines []coremodel.Container,
	busbarContainers []coremodel.Container,
	f *importhjson.File,
) {
	if len(busbarContainers) == 0 {
		return
	}

	// busbarNodeOf looks up eqID's AttributeKeyBusbarNode value (see that
	// key's doc comment) — the exact canonical Node ID a BusbarSection
	// Equipment was merged into during Pass A, no guessing involved.
	busbarNodeOf := func(eqID string) string {
		for _, a := range s.AttributesByOwner[eqID] {
			if a.Key == common.AttributeKeyBusbarNode {
				if v, ok := a.Value.(string); ok {
					return v
				}
			}
		}
		return ""
	}

	for _, bbContainer := range busbarContainers {
		// Original BusbarSection Equipment objects for this busbar
		// container — the source of both the container's real Node (via
		// AttributeKeyBusbarNode) AND the Sections themselves.
		//
		// 2026-08-05 change: this used to instead enumerate every piece
		// of Equipment station-wide actually wired to the busbar's Node
		// (any Bay/root-level branch, not just this container's own
		// children) and emit one Section per *connecting* Equipment,
		// index-paired best-effort with these origSections for their
		// Attributes/Satellites/Geometry. That inflated the Section
		// count far beyond the original CIM BusbarSection count whenever
		// several Equipment (cables, transformers, switches, ...)
		// happened to connect directly to one busbar Node with only a
		// single real BusbarSection object (confirmed via a MiniGrid
		// CGMES3 export/reimport round-trip: 11 real BusbarSection
		// equipment across 10 busbars became 39 synthetic Sections).
		// It's unnecessary: a connecting Equipment's own connects/From/To
		// entry is resolved directly against the busbar's shortened Node
		// ID by resolveConnectTarget/shortenID, never against a Section
		// — Sections are purely informational (Attributes/Satellites/
		// Geometry), see this type's doc comment in
		// pkg/importer/hjson/types.go. So this now emits exactly
		// one Section per *original* BusbarSection Equipment, keeping
		// the exported Section count equal to the real one, with no
		// connects-resolution impact at all.
		origSections := append([]coremodel.Equipment(nil), s.EquipmentByContainer[bbContainer.ID]...)
		sort.Slice(origSections, func(i, j int) bool { return origSections[i].ID < origSections[j].ID })

		var node string
		for _, orig := range origSections {
			if n := busbarNodeOf(orig.ID); n != "" {
				node = n
				break
			}
		}
		if node == "" {
			// No AttributeKeyBusbarNode bookkeeping found for this
			// container (e.g. no BusbarSection equipment at all, or a
			// pre-2026-07-21 model reimported without going through the
			// updated Pass A) — skip rather than guessing.
			continue
		}

		bb := importhjson.Busbar{ID: shortenID(rootID, node)}

		// Container-level geometry: Phase 2's BuildGeometry associates a
		// busbar's Location with the busbar CONTAINER's own ID (see
		// geometry.go's containerIDs param), not with any individual
		// BusbarSection Equipment ID — so look it up via bbContainer.ID,
		// not orig.ID. Fixed 2026-08-05 (pf_cim_beispiel_ortsnetz
		// round-trip regression: busbar geometry silently vanished on
		// reimport because ownerGeometryPoint(s, orig.ID) never matched
		// any GeometryByOwner entry for a container-owned point). Every
		// Section shares this same point, mirroring hoistCommonSectionName's
		// "one physical busbar" reasoning for names.
		bbGeom := ownerGeometryPoint(s, bbContainer.ID)

		for i, orig := range origSections {
			sectionShortID := strconv.Itoa(i + 1)
			sec := importhjson.BusbarSectionEntry{
				ID:         sectionShortID,
				Attributes: buildAttributes(s, orig.ID, true),
				Satellites: buildSatellites(s, orig.ID),
				Geometry:   bbGeom,
			}
			bb.Sections = append(bb.Sections, sec)
		}
		hoistCommonSectionName(&bb)
		f.Busbars = append(f.Busbars, bb)
	}
}

// hoistCommonSectionName lifts a Busbar's "IdentifiedObject.name" Sachdaten
// attribute out of every one of its Sections up onto the Busbar itself,
// but only when EVERY Section shares the exact same name — the
// overwhelmingly common case, since hjson2's Sections are just individual
// electrical connection slots of ONE physical busbar (see this file's
// Konzept.md-documented busbar-redesign), so their raw CIM
// BusbarSection.name values are typically identical copies of the
// busbar's own name. Added 2026-07-21 as a compaction: without this, an
// N-section busbar repeats the same name string N times. A Section whose
// name legitimately differs (or is entirely missing) prevents ANY hoist
// for that busbar — no partial/best-effort hoisting, to avoid silently
// dropping a genuinely distinct Section name. See
// pkg/importer/hjson2/resolve.go's emitStation for the corresponding
// import-side fallback (a Section without its own name inherits the
// Busbar's hoisted one), which keeps this fully round-trip safe.
func hoistCommonSectionName(bb *importhjson.Busbar) {
	if len(bb.Sections) < 2 {
		return
	}
	var name string
	for i, sec := range bb.Sections {
		v, ok := sec.Attributes[attributesLeadKey]
		if !ok {
			return
		}
		s, ok := v.(string)
		if !ok {
			return
		}
		if i == 0 {
			name = s
		} else if s != name {
			return
		}
	}
	bb.Attributes = map[string]interface{}{attributesLeadKey: name}
	for i := range bb.Sections {
		delete(bb.Sections[i].Attributes, attributesLeadKey)
	}
}

// buildEquipment reconstructs one ordinary (non-BusbarSection,
// non-ACLineSegment) Equipment entry: its class (from the "cim_class"
// Sachdaten attribute — see AttributeKeyClass's doc comment in
// pkg/impl/common/attributekeys.go for why this round-trips through
// Sachdaten instead of a dedicated field), its connects (from its own
// Edge, omitting GND for single-terminal source/sink equipment per the
// auto-GND-wiring decision), and its remaining literal attributes.
// region/nodeKabelRefs (see buildCrossFileRefs) populate eq.ConnectRefs
// with a relative-path cross-reference comment for any connects entry
// whose node is also touched by an external Kabel file. A connects entry
// landing on a Busbar's node needs no special handling here — see
// buildBusbarSections' doc comment: the Busbar's own ID already IS that
// node's shortened form, so the ordinary shortenID fallback in
// resolveConnectTarget already renders it correctly.
func buildEquipment(s *Snapshot, rootID, eqID string, fromDirPath string, nodeKabelRefs map[string][]fileRef) importhjson.Equipment {
	eq := importhjson.Equipment{ID: shortenID(rootID, eqID)}
	attrs := s.AttributesByOwner[eqID]
	for _, a := range attrs {
		if a.Key == common.AttributeKeyClass {
			eq.Class = fmt.Sprintf("%v", a.Value)
			break
		}
	}
	if edge, ok := s.Edges[eqID]; ok {
		n1, ref1 := resolveConnectTarget(rootID, edge.Terminal1NodeID, fromDirPath, nodeKabelRefs)
		if edge.Terminal2NodeID == gndToken {
			eq.Connects = []string{n1}
			eq.ConnectRefs = []string{ref1}
		} else {
			n2, ref2 := resolveConnectTarget(rootID, edge.Terminal2NodeID, fromDirPath, nodeKabelRefs)
			eq.Connects = []string{n1, n2}
			eq.ConnectRefs = []string{ref1, ref2}
		}
	}
	eq.Attributes = buildAttributes(s, eqID, true)
	eq.Satellites = buildSatellites(s, eqID)
	eq.Geometry = ownerGeometryPoint(s, eqID)
	if eq.Class == "Meter" {
		extractMeterSchedules(&eq)
	}
	extractDiscreteControlLimits(&eq)
	return eq
}

// resolveConnectTarget renders one connects entry as its shortened node ID
// (see shortenID) — this already renders a Busbar's node correctly, since
// a Busbar's own ID is exactly that same shortened form (see
// buildBusbarSections). The second return value is a relative-path
// cross-reference comment (see buildCrossFileRefs/formatFileRefs), or ""
// if nodeID isn't also touched by a file in xref.
func resolveConnectTarget(rootID, nodeID string, fromDirPath string, xref map[string][]fileRef) (string, string) {
	ref := formatFileRefs(fromDirPath, xref[nodeID])
	return shortenID(rootID, nodeID), ref
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
	dropRedundantScheduleName(eq.Measuring, eq.Attributes)
	dropRedundantScheduleName(eq.Transmission, eq.Attributes)
}

// dropRedundantScheduleName drops "IdentifiedObject.name" from a
// measuring/transmission TimeSchedule's own attrs when it is IDENTICAL to
// the owning Equipment's own name — added 2026-07-21 as a further
// compaction: a Meter's TimeSchedule satellites are, in every observed
// dataset, simply named after their own Meter (e.g. "Meter Feeder ONS 0"
// three times over: the Meter itself plus both its schedules), so
// repeating it verbatim in both blocks is pure noise. Left untouched
// (schedule keeps its own, presumably meaningful, name) if it differs.
// See pkg/importer/hjson2/resolve.go's addMeterSchedules for the
// corresponding import-side fallback (a schedule missing its own name
// inherits the owning Equipment's name), which keeps this round-trip
// safe.
func dropRedundantScheduleName(schedule, ownerAttrs map[string]interface{}) {
	if schedule == nil || ownerAttrs == nil {
		return
	}
	name, ok := schedule[attributesLeadKey]
	if !ok {
		return
	}
	ownerName, ok := ownerAttrs[attributesLeadKey]
	if !ok {
		return
	}
	if name == ownerName {
		delete(schedule, attributesLeadKey)
	}
}

// buildSegment reconstructs one ACLineSegment as a Segment entry (From/To
// instead of Connects — see pkg/importer/hjson.Segment). overrides
// (see buildBusbarSections) rewrites From/To exactly like buildEquipment
// does for Connects — relevant for a station-internal ACLine folded into
// its owning station's own file (see classifyInternalACLines) whose
// From/To lands on that station's busbar node. region/xref (see
// buildCrossFileRefs) populate seg.FromRef/ToRef with a relative-path
// cross-reference comment: xref is nodeStationRefs for a standalone Kabel
// file (naming which Station/House file each end connects into) or
// nodeKabelRefs for a station-internal folded segment (naming an
// external Kabel file, in the rare case such a jumper also happens to
// touch one — see buildStation's own call site).
func buildSegment(s *Snapshot, rootID, eqID string, fromDirPath string, xref map[string][]fileRef) importhjson.Segment {
	seg := importhjson.Segment{ID: shortenID(rootID, eqID)}
	if edge, ok := s.Edges[eqID]; ok {
		seg.From, seg.FromRef = resolveConnectTarget(rootID, edge.Terminal1NodeID, fromDirPath, xref)
		seg.To, seg.ToRef = resolveConnectTarget(rootID, edge.Terminal2NodeID, fromDirPath, xref)
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
// shape pkg/importer/hjson.Equipment/Segment/BusbarSectionEntry
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
// unaffected. See pkg/importer/hjson's addAttributes for the
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
		if a.Key == common.AttributeKeyBusbarNode {
			continue // internal bookkeeping (see that key's doc comment), never a visible Sachdaten value
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
// since pkg/impl/common/geometry.go's BuildGeometry already reduces
// them to a single owner-level Geometry point independently and doesn't
// touch the Sachdaten/satellite pipeline at all), sorted ascending by
// PositionPoint.sequenceNumber (ties broken by encounter order — real CIM
// data is expected to have distinct sequence numbers). This is hjson2's
// own, more user-friendly replacement for exporting each PositionPoint as
// a separate raw "{class: \"PositionPoint\", attributes: {...}}" satellite
// block (see build.go's isGeometrySatelliteClass). Only Segment/
// ACLineSegment (which represents a route, not a single point) uses this
// directly — Equipment/BusbarSectionEntry point geometry uses
// ownerGeometryPoint instead (see its doc comment for why: this function's
// multi-hop satellite walk can over-reach to a different object's own
// Location).
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

// ownerGeometryPoint returns ownerID's own single-point Geometry as
// determined by Phase 2 (pkg/impl/common/geometry.go's BuildGeometry,
// persisted in s.GeometryByOwner), or nil if Phase 2 found none.
//
// Equipment and BusbarSectionEntry geometry MUST be sourced from here, not
// from buildGeometryPath's satellite-walk reconstruction: buildGeometryPath
// finds any "PositionPoint" reachable via the generic, multi-hop Sachdaten
// satellite walk (sachdaten.go), which can reach a PositionPoint belonging
// to a genuinely different object several hops away (e.g. a Meter with no
// own Location, reached via EndDevice.UsagePoint -> UsagePoint's own
// UsagePointLocation -> PositionPoint) — broader than the direct/reverse
// 2-hop PowerSystemResource.Location association BuildGeometry requires.
// Using buildGeometryPath for point geometry therefore fabricated Geometry
// for equipment Phase 2 correctly determined has none, which reappeared as
// a spurious GEOM diff after an HJSON export/reimport round trip (observed
// 2026-08 on examples/nsc's Meter equipment). Segment/ACLineSegment routes
// still use buildGeometryPath directly since they need the full ordered
// multi-point path, which single-owner Geometry storage doesn't capture.
func ownerGeometryPoint(s *Snapshot, ownerID string) *importhjson.GeometryPoint {
	if g, ok := s.GeometryByOwner[ownerID]; ok {
		return &importhjson.GeometryPoint{Lat: g.Lat, Lon: g.Lon}
	}
	return nil
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

// regionSubregionFor resolves a top-level container's own export
// region/subregion. Ordinary containers (Substation/DistributionBox/
// House) simply read their own "region"/"subregion" Sachdaten (see
// regionOf/subregionOf) — resolved from CIM Substation.Region/
// SubGeographicalRegion in pass_a.go. A freestanding ACLine/Kabel root
// never gets those attributes itself (ACLineSegment/Line carries no
// resolved Netzregion in JAG's model, see Konzept.md's open point on
// CIM Line.Region), so it instead borrows the region/subregion of a
// Substation/DistributionBox/House it connects to (see stationRegionFor).
func regionSubregionFor(s *Snapshot, rootID string, rootType coremodel.ContainerType, nodeEquipment map[string][]string, defaultNetzregion string) (region, subregion string) {
	if rootType == common.ContainerTypeACLine {
		return stationRegionFor(s, rootID, nodeEquipment, defaultNetzregion)
	}
	return regionOf(s, rootID, defaultNetzregion), resolvedSubregion(s, rootID)
}

// stationRegionFor derives a freestanding ACLine/Kabel container's export
// region/subregion from the Substation/DistributionBox/House it connects
// to at its endpoint nodes, instead of resolving CIM Line.Region
// separately (a cable's own region trivially matches wherever it starts/
// ends). Per explicit user decision (2026-07-31/2026-07-30): every
// connected endpoint sharing the most-common region is considered, and
// the resulting subregion is the LONGEST COMMON PREFIX of their
// (possibly multi-level, see resolvedSubregion/AttributeKeySubRegionPath)
// subregion paths — e.g. a cable spanning two different Ortsteile of the
// same Ort collapses to just that Ort ("<Region>/<Ort>/Kabel"), never
// arbitrarily under just one of the two Ortsteile; a cable whose
// endpoints don't even share a subregion collapses further to a plain
// "<Region>/Kabel". If the cable touches no Substation/DistributionBox/
// House at all (e.g. runs entirely between two other Kabel/boundary
// points), defaultNetzregion is used with no subregion. Deterministic
// even when connected endpoints disagree on region entirely (rare,
// inconsistent source data): the region shared by the most endpoints
// wins, ties broken lexicographically.
func stationRegionFor(s *Snapshot, rootID string, nodeEquipment map[string][]string, defaultNetzregion string) (region, subregion string) {
	type candidate struct {
		region string
		segs   []string // resolvedSubregion split into path segments, possibly empty
	}
	seenRoot := map[string]bool{}
	var candidates []candidate
	for _, eq := range s.EquipmentByContainer[rootID] {
		edge, ok := s.Edges[eq.ID]
		if !ok {
			continue
		}
		for _, n := range []string{edge.Terminal1NodeID, edge.Terminal2NodeID} {
			if n == "" || n == gndToken {
				continue
			}
			for _, otherEqID := range nodeEquipment[n] {
				otherEq, ok := s.Equipment[otherEqID]
				if !ok {
					continue
				}
				otherRoot := containerRootOf(s, otherEq.ContainerID)
				if otherRoot == "" || otherRoot == rootID || seenRoot[otherRoot] {
					continue
				}
				rc, ok := s.Containers[otherRoot]
				if !ok || rc.Type == common.ContainerTypeACLine {
					continue // another cable endpoint, not a station/house — skip
				}
				seenRoot[otherRoot] = true
				r := regionOf(s, otherRoot, "")
				if r == "" {
					continue
				}
				var segs []string
				for _, part := range strings.Split(resolvedSubregion(s, otherRoot), "/") {
					if part != "" {
						segs = append(segs, part)
					}
				}
				candidates = append(candidates, candidate{r, segs})
			}
		}
	}
	if len(candidates) == 0 {
		return defaultNetzregion, ""
	}

	byRegion := map[string][]candidate{}
	for _, c := range candidates {
		byRegion[c.region] = append(byRegion[c.region], c)
	}
	regions := make([]string, 0, len(byRegion))
	for r := range byRegion {
		regions = append(regions, r)
	}
	sort.Slice(regions, func(i, j int) bool {
		if len(byRegion[regions[i]]) != len(byRegion[regions[j]]) {
			return len(byRegion[regions[i]]) > len(byRegion[regions[j]])
		}
		return regions[i] < regions[j]
	})
	chosenRegion := regions[0]

	group := byRegion[chosenRegion]
	var commonSegs []string
	for i, c := range group {
		if i == 0 {
			commonSegs = append([]string(nil), c.segs...)
			continue
		}
		commonSegs = commonSegPrefix(commonSegs, c.segs)
		if len(commonSegs) == 0 {
			break
		}
	}
	return chosenRegion, strings.Join(commonSegs, "/")
}

// commonSegPrefix returns the longest shared prefix of a and b, comparing
// segment by segment (not character by character) — used by
// stationRegionFor to collapse two connected endpoints' subregion paths
// down to their common ancestor directory (e.g. two Ortsteile of the same
// Ort share the "<Region>/<Ort>" prefix but not the Ortsteil segment
// itself).
func commonSegPrefix(a, b []string) []string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// regionOf looks up ownerID's "region" Sachdaten attribute, falling back
// to defaultNetzregion if absent (see this package's doc comment).
func regionOf(s *Snapshot, ownerID, defaultNetzregion string) string {
	for _, a := range s.AttributesByOwner[ownerID] {
		if a.Key == common.AttributeKeyRegion {
			if str, ok := a.Value.(string); ok && str != "" {
				return str
			}
		}
	}
	return defaultNetzregion
}

// subregionOf looks up ownerID's "subregion" Sachdaten attribute (see
// AttributeKeySubRegion's doc comment — CIM's SubGeographicalRegion,
// optional second-level Netzregion path segment). Returns "" if absent,
// unlike regionOf there is no default fallback: an absent subregion simply
// means the container is exported directly under its Netzregion, with no
// extra nesting level (see dirPathOf).
func subregionOf(s *Snapshot, ownerID string) string {
	for _, a := range s.AttributesByOwner[ownerID] {
		if a.Key == common.AttributeKeySubRegion {
			if str, ok := a.Value.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

// subregionPathOf looks up ownerID's "subregion_path" Sachdaten attribute
// (see AttributeKeySubRegionPath's doc comment) — an optional, HJSON-only,
// arbitrarily deep "/"-joined directory chain (e.g. PLZ/Ort/Ortsteil for a
// hand-organized large Netzregion) that a plain "subregion" attribute
// (always a single segment) cannot carry. Returns "" if absent.
func subregionPathOf(s *Snapshot, ownerID string) string {
	for _, a := range s.AttributesByOwner[ownerID] {
		if a.Key == common.AttributeKeySubRegionPath {
			if str, ok := a.Value.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

// resolvedSubregion returns ownerID's effective subregion directory chain
// for export placement: "subregion_path" if present (arbitrary depth,
// already "/"-joined), otherwise the plain single-level "subregion"
// attribute, or "" if neither is set. subregion_path always wins because
// it is a strict superset — a single-segment subregion_path and an
// equivalent plain subregion attribute are otherwise indistinguishable in
// the resulting export path anyway.
func resolvedSubregion(s *Snapshot, ownerID string) string {
	if p := subregionPathOf(s, ownerID); p != "" {
		return p
	}
	return subregionOf(s, ownerID)
}

// dirPathOf renders the full slash-separated export directory path (root-
// relative) for a container with the given region/subregion/top-level-dir,
// e.g. "Nord/ONS" (no subregion), "Nord/West/ONS" (one subregion level),
// or "Nord/West/12345/Musterstadt/ONS" (a multi-level "subregion_path" —
// see resolvedSubregion) — see Write's
// <root>/<Netzregion>/[<Subnetzregion...>/]<Dir>/<ID>.hjson layout.
// subregion may itself be a "/"-joined multi-segment chain; every
// resulting segment is sanitized individually (see sanitizeSegment),
// except dir, which is always one of dirForType's/boundaryDir's fixed,
// already-safe literals.
func dirPathOf(region, subregion, dir string) string {
	segs := make([]string, 0, 4)
	segs = append(segs, sanitizeSegment(region))
	for _, part := range strings.Split(subregion, "/") {
		if part == "" {
			continue
		}
		segs = append(segs, sanitizeSegment(part))
	}
	segs = append(segs, dir)
	return strings.Join(segs, "/")
}

// shortenID strips a "<rootID>-" prefix if present and marks the result
// as an explicitly local ID with the "@" prefix (see
// pkg/importer/hjson2's resolveID/localIDPrefix — a name is local to
// a file iff it starts with "@", global otherwise); GND and IDs without
// the "<rootID>-" prefix (e.g. raw CIM/CGMES/NSC mRIDs, or cross-file
// references into a different root — Kabel/ACLine files in particular
// rarely have IDs sharing any station-root prefix, so this rule fires
// there only rarely, staying global/unprefixed as expected) are returned
// unchanged.
func shortenID(rootID, id string) string {
	if id == gndToken {
		return gndToken
	}
	if strings.HasPrefix(id, rootID+"-") {
		return "@" + strings.TrimPrefix(id, rootID+"-")
	}
	return id
}
