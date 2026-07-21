package hjson

import (
	"fmt"
	"sort"
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	importhjson "gitlab.com/openk-nsc/jag/internal/importer/hjson"
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

	var outputs []FileOutput
	for _, root := range roots {
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
			buildStation(s, root.ID, &f)
		case common.ContainerTypeHouse:
			for _, eq := range s.EquipmentByContainer[root.ID] {
				f.Equipment = append(f.Equipment, buildEquipment(s, root.ID, eq.ID))
			}
		case common.ContainerTypeACLine:
			for _, eq := range s.EquipmentByContainer[root.ID] {
				f.Segments = append(f.Segments, buildSegment(s, root.ID, eq.ID))
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

// buildStation fills f's Busbars/Bays from every child container of
// rootID (busbar and bay/feeder containers — see container.go's
// BuildContainers).
func buildStation(s *Snapshot, rootID string, f *importhjson.File) {
	children := append([]coremodel.Container(nil), s.ChildrenByParent[rootID]...)
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })

	for _, child := range children {
		switch child.Type {
		case common.ContainerTypeBusbar:
			bb := importhjson.Busbar{ID: shortenID(rootID, child.ID)}
			sections := append([]coremodel.Equipment(nil), s.EquipmentByContainer[child.ID]...)
			sort.Slice(sections, func(i, j int) bool { return sections[i].ID < sections[j].ID })
			for _, sec := range sections {
				bb.Sections = append(bb.Sections, importhjson.BusbarSectionEntry{
					ID:         shortenID(rootID, sec.ID),
					Attributes: buildAttributes(s, sec.ID, true),
					Satellites: buildSatellites(s, sec.ID),
				})
			}
			f.Busbars = append(f.Busbars, bb)
		case common.ContainerTypeBay:
			bay := importhjson.Bay{ID: shortenID(rootID, child.ID)}
			eqs := append([]coremodel.Equipment(nil), s.EquipmentByContainer[child.ID]...)
			sort.Slice(eqs, func(i, j int) bool { return eqs[i].ID < eqs[j].ID })
			for _, eq := range eqs {
				bay.Equipment = append(bay.Equipment, buildEquipment(s, rootID, eq.ID))
			}
			f.Bays = append(f.Bays, bay)
		}
	}
}

// buildEquipment reconstructs one ordinary (non-BusbarSection,
// non-ACLineSegment) Equipment entry: its class (from the "cim_class"
// Sachdaten attribute — see AttributeKeyClass's doc comment in
// internal/impl/common/attributekeys.go for why this round-trips through
// Sachdaten instead of a dedicated field), its connects (from its own
// Edge, omitting GND for single-terminal source/sink equipment per the
// auto-GND-wiring decision), and its remaining literal attributes.
func buildEquipment(s *Snapshot, rootID, eqID string) importhjson.Equipment {
	eq := importhjson.Equipment{ID: shortenID(rootID, eqID)}
	attrs := s.AttributesByOwner[eqID]
	for _, a := range attrs {
		if a.Key == common.AttributeKeyClass {
			eq.Class = fmt.Sprintf("%v", a.Value)
			break
		}
	}
	if edge, ok := s.Edges[eqID]; ok {
		n1 := shortenID(rootID, edge.Terminal1NodeID)
		if edge.Terminal2NodeID == gndToken {
			eq.Connects = []string{n1}
		} else {
			eq.Connects = []string{n1, shortenID(rootID, edge.Terminal2NodeID)}
		}
	}
	eq.Attributes = buildAttributes(s, eqID, true)
	eq.Satellites = buildSatellites(s, eqID)
	return eq
}

// buildSegment reconstructs one ACLineSegment as a Segment entry (From/To
// instead of Connects — see internal/importer/hjson.Segment).
func buildSegment(s *Snapshot, rootID, eqID string) importhjson.Segment {
	seg := importhjson.Segment{ID: shortenID(rootID, eqID)}
	if edge, ok := s.Edges[eqID]; ok {
		seg.From = shortenID(rootID, edge.Terminal1NodeID)
		seg.To = shortenID(rootID, edge.Terminal2NodeID)
	}
	// Like BusbarSectionEntry, Segment has no dedicated Class field — its
	// class is always implicitly ACLineSegment — so skip re-surfacing the
	// internal-only cim_class Sachdaten key as a regular attribute here too
	// (consistent with buildEquipment/the busbar-section case above).
	seg.Attributes = buildAttributes(s, eqID, true)
	seg.Satellites = buildSatellites(s, eqID)
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
		if a.Key == common.AttributeKeyBusbarNode {
			continue // hjson2-only internal bookkeeping (see that key's doc comment), never a visible Sachdaten value
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
		sat := importhjson.Satellite{}
		if class, ok := obj["class"].(string); ok {
			sat.Class = class
		}
		if attrs, ok := obj["attributes"].(map[string]interface{}); ok {
			sat.Attributes = attrs
		}
		out = append(out, sat)
	}
	return out
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
