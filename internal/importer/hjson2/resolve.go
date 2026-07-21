package hjson

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// gndToken is the reserved literal used in connects/from/to to mean the
// explicit virtual ground node (see Konzept.md: a GroundDisconnector
// deliberately connecting a real node to ground). It is never prefixed and
// never registered as a local name.
const gndToken = "GND"

// kwToMWKeys are curated Fachmodell attribute keys documented as being
// authored in kW/kvar (matching how a Netzbetreiber employee thinks about
// small LV loads/generators) that must be converted to CIM-canonical
// MW/MVAr at Phase 1 parse time (see Konzept.md's unit-conversion
// decision), so no downstream phase needs dialect-specific unit handling.
// This is a deliberately small, extensible seed list, not an exhaustive
// catalog of every possible power attribute.
var kwToMWKeys = map[string]bool{
	"EnergyConsumer.p":                  true,
	"EnergyConsumer.q":                  true,
	"PowerElectronicsConnection.p":      true,
	"PowerElectronicsConnection.q":      true,
	"PowerElectronicsConnection.ratedS": true,
}

// FindFiles walks root and returns the classified location of every
// *.hjson file found beneath it (see ClassifyPath).
func FindFiles(root string) ([]FileInfo, error) {
	var infos []FileInfo
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || !strings.HasSuffix(path, ".hjson") {
			return nil
		}
		info, err := ClassifyPath(root, path)
		if err != nil {
			return err
		}
		infos = append(infos, info)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

// Emit parses every Fachmodell file found under root and translates them
// into dialect-neutral model.StagingRecord values (plus any
// model.StagingError encountered), ready for staging.Store.InsertRecords/
// InsertErrors exactly like the CGMES/NSC parsers (see
// internal/importer/cgmes/parser.go).
//
// Container-level custom Attributes (e.g. a Substation's "region", or a
// House's MaLo/MeLo) are parsed into File.Attributes and emitted as
// ordinary Sachdaten owned by the container's own ID (see emitStation/
// emitHouse). This previously round-tripped only halfway: the shared
// Phase 2 Sachdaten extraction (internal/impl/common/sachdaten.go) only
// ever processed the caller-supplied Equipment ID list, never a batch's
// own Substation/Building root IDs, so ResolveBatchContainers' own
// res.Attributes (built from these very records) was computed but never
// flushed to the sink. Fixed 2026-07-19 in ProcessStationBatch
// (pass_a_pipeline.go) — no further change was needed here or in the
// exporter, since both already used the generic, OwnerID-keyed Attribute
// channel.
func Emit(version uint64, root string) ([]model.StagingRecord, []model.StagingError, error) {
	infos, err := FindFiles(root)
	if err != nil {
		return nil, nil, fmt.Errorf("hjson: walking %s: %w", root, err)
	}

	// Pass 1: check for duplicate top-level container IDs across files
	// (a genuine authoring error — two files claiming the same station/
	// house/ACLine root ID).
	seen := map[string]FileInfo{}
	var errs []model.StagingError
	for _, fi := range infos {
		if prev, dup := seen[fi.ContainerID]; dup {
			errs = append(errs, model.StagingError{
				Version:    version,
				SourceFile: fi.Path,
				Message:    fmt.Sprintf("duplicate top-level container ID %q (already used by %s)", fi.ContainerID, prev.Path),
			})
			continue
		}
		seen[fi.ContainerID] = fi
	}

	var records []model.StagingRecord
	for _, fi := range infos {
		if _, ok := seen[fi.ContainerID]; !ok {
			continue // duplicate, already reported above
		}
		f, err := ParseFile(fi.Path)
		if err != nil {
			errs = append(errs, model.StagingError{Version: version, SourceFile: fi.Path, Message: err.Error()})
			continue
		}
		recs, fileErrs := emitFile(version, fi, f)
		records = append(records, recs...)
		errs = append(errs, fileErrs...)
	}
	return records, errs, nil
}

// localIDPrefix marks a name occurring in a Fachmodell file as a local ID
// (only unique within that one file) rather than an already-global ID —
// see Konzept.md's ID-prefixing decision (2026-07-20 revision): a name is
// local if and only if it starts with "@"; anything else is global and
// used verbatim, in and outside the file, unchanged.
const localIDPrefix = "@"

// resolveID translates a name occurring in the given file (either an
// entity's own declared ID, or a connects/from/to token) into its final
// global ID: GND stays GND; a name starting with "@" is local to this
// file and expands to "<fileContainerID>-<name without @>"; anything else
// is already a global ID and is used verbatim.
func resolveID(fileContainerID, name string) string {
	if name == gndToken {
		return gndToken
	}
	if strings.HasPrefix(name, localIDPrefix) {
		return fileContainerID + "-" + strings.TrimPrefix(name, localIDPrefix)
	}
	return name
}

// r is a tiny per-file record-emission helper carrying the shared context
// (version, file info) so individual emit* helpers don't need to thread
// several parameters each.
type r struct {
	version uint64
	fi      FileInfo
	recs    []model.StagingRecord
	errs    []model.StagingError
}

func (e *r) add(id, class, attr, value string, isRef bool, seq int) {
	e.recs = append(e.recs, model.StagingRecord{
		Version: e.version, ID: id, Class: class, Attribute: attr, Value: value, IsReference: isRef, Seq: seq,
	})
}

func (e *r) resolve(name string) string { return resolveID(e.fi.ContainerID, name) }

// addGeometry synthesizes the minimal CIM GL-profile shape
// (PowerSystemResource.Location -> Location -> PositionPoint) BuildGeometry
// (internal/impl/common/geometry.go) already knows how to resolve, so a
// container's own Geometry (added 2026-07-19) round-trips through Phase 2
// exactly like real CIM/CGMES Location data, with no changes needed to
// that already-load-tested pipeline. ownerID is the container's own ID
// (Substation/Building); the synthesized Location/PositionPoint IDs are
// derived from it and are never referenced from anywhere else.
func (e *r) addGeometry(ownerID, class string, geo *GeometryPoint) {
	if geo == nil {
		return
	}
	locID := ownerID + "-LOC"
	ppID := ownerID + "-LOC-PP1"
	e.add(ownerID, class, "PowerSystemResource.Location", locID, true, 0)
	e.add(ppID, "PositionPoint", "PositionPoint.Location", locID, true, 0)
	e.add(ppID, "PositionPoint", "PositionPoint.sequenceNumber", "1", false, 0)
	e.add(ppID, "PositionPoint", "PositionPoint.xPosition", strconv.FormatFloat(geo.Lon, 'g', -1, 64), false, 0)
	e.add(ppID, "PositionPoint", "PositionPoint.yPosition", strconv.FormatFloat(geo.Lat, 'g', -1, 64), false, 0)
}

// addGeometryPath is addGeometry's multi-point counterpart (hjson2 only):
// it synthesizes one shared Location plus one PositionPoint per entry of
// points, numbered 1..len(points) in the given order (the exporter's
// build.buildGeometryPath already sorted them ascending by
// PositionPoint.sequenceNumber, first entry = sequenceNumber 1 — see
// Segment.Geometry's doc comment), so a Segment/ACLineSegment's full route
// round-trips through Phase 2's existing BuildGeometry exactly like real
// CIM/CGMES multi-point Location data, and — via the ordinary satellite
// walk (internal/impl/common/sachdaten.go) — every individual PositionPoint
// is rediscovered as its own satellite too, symmetric with what the
// exporter's buildGeometryPath read back out.
func (e *r) addGeometryPath(ownerID, class string, points []GeometryPoint) {
	if len(points) == 0 {
		return
	}
	locID := ownerID + "-LOC"
	e.add(ownerID, class, "PowerSystemResource.Location", locID, true, 0)
	for i, p := range points {
		seq := i + 1
		ppID := fmt.Sprintf("%s-LOC-PP%d", ownerID, seq)
		e.add(ppID, "PositionPoint", "PositionPoint.Location", locID, true, 0)
		e.add(ppID, "PositionPoint", "PositionPoint.sequenceNumber", strconv.Itoa(seq), false, 0)
		e.add(ppID, "PositionPoint", "PositionPoint.xPosition", strconv.FormatFloat(p.Lon, 'g', -1, 64), false, 0)
		e.add(ppID, "PositionPoint", "PositionPoint.yPosition", strconv.FormatFloat(p.Lat, 'g', -1, 64), false, 0)
	}
}

// denormalizeAttrKey maps an hjson2-simplified attribute key (as written
// by internal/exporter/hjson2's writeAttributesBlock for an
// Equipment/Segment/BusbarSection/Satellite — never for a top-level
// container's own f.Attributes, which uses addAttributes below unchanged)
// back to the raw CIM "Class.attribute" key it stands for, given ownClass
// (the very object's own concrete CIM class, e.g. "Fuse" for a Fuse):
//   - bare "name" -> "IdentifiedObject.name" (attributesLeadKeyAlias).
//   - a key with no "." at all: the exporter strips a "Class." prefix
//     whenever that suffix wasn't ambiguous *within the one attrs map it
//     was rendering* (see writeNamedBlock's doc comment) — a purely local
//     decision the importer, seeing only this one already-stripped key,
//     cannot re-derive on its own. So reconstruction here is a best-effort
//     two-step guess: first UniqueAttrClass (a small curated table of
//     keys known, from real example datasets, to belong to some *other*
//     class than the object's own — e.g. "normallyInService" always means
//     "Equipment.normallyInService", never "<ownClass>.normallyInService"
//     for any concrete class actually seen so far); otherwise fall back to
//     "<ownClass>.<key>", which is exact whenever the exporter's own-class
//     case applied. Both are correct for every case actually produced by
//     this package's own exporter; only a hand-authored file using some
//     other class's short key not yet in UniqueAttrClass could still be
//     ambiguous — accepted, documented limitation of a table-based guess.
//   - anything else (already a full "Class.attribute" key, e.g. an
//     inherited "Equipment.normallyInService" or "Switch.normalOpen") is
//     returned unchanged.
func denormalizeAttrKey(k, ownClass string) string {
	if k == "name" {
		return "IdentifiedObject.name"
	}
	if !strings.Contains(k, ".") {
		// State-Variable (SV-profile) satellite classes (SvStatus,
		// SvSwitch, SvTapStep, SvVoltage, ...) always carry their own
		// base-class attributes under their own concrete class name — a
		// bare "open"/"inService" on such a satellite means
		// "SvSwitch.open"/"SvStatus.inService", never
		// "Switch.open"/"Equipment.inService". ownClass already IS the
		// exact right class in that case, so skip the UniqueAttrClass
		// table entirely for Sv* owners and fall straight through to
		// "<ownClass>.<key>". This is what makes it safe for
		// UniqueAttrClass to map "open"->"Switch" and
		// "inService"->"Equipment" despite SvSwitch.open/SvStatus.inService
		// also occurring in real data (see uniqueattrs.go) — the two
		// contexts are disambiguated by ownClass, not by attribute name
		// alone. Found 2026-07-21 while root-causing a busbar/circuit
		// topology divergence in the ReliCapGrid_Espheim round-trip:
		// without this guard, "open" couldn't be added to UniqueAttrClass,
		// so a Switch's own "open" survived only as "Switch.normalOpen"
		// after reimport (its live SSH-profile open/closed state was
		// silently lost, falling back to the design-time default), which
		// materially changed BuildCircuits' zero-ohm reduction results.
		if !strings.HasPrefix(ownClass, "Sv") {
			if cls, ok := UniqueAttrClass[k]; ok {
				return cls + "." + k
			}
		}
		return ownClass + "." + k
	}
	return k
}

// addAttributes emits every entry of attrs as a literal (non-reference)
// StagingRecord attribute owned by id/class verbatim, with no key
// remapping — used only for a top-level container's own f.Attributes
// (Substation/Building), whose keys (e.g. "name"/"region",
// common.AttributeKeyName/AttributeKeyRegion) are already JAG-native and
// never abbreviated CIM keys in the first place. See addEntityAttributes
// for the Equipment/Segment/BusbarSection/Satellite counterpart that does
// apply denormalizeAttrKey.
func (e *r) addAttributes(id, class string, attrs map[string]interface{}) {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := attrs[k]
		if arr, ok := v.([]interface{}); ok {
			for i, elem := range arr {
				e.add(id, class, k, formatAttrValue(k, elem), false, i)
			}
			continue
		}
		e.add(id, class, k, formatAttrValue(k, v), false, 0)
	}
}

// addEntityAttributes is addAttributes' counterpart for an
// Equipment/Segment/BusbarSection/Satellite's own attrs map: every key is
// first passed through denormalizeAttrKey(k, class) to restore the "name"
// alias and any own-class-stripped "Class." prefix hjson2's exporter may
// have applied, before being emitted exactly like addAttributes would.
func (e *r) addEntityAttributes(id, class string, attrs map[string]interface{}) {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		v := attrs[rawKey]
		k := denormalizeAttrKey(rawKey, class)
		if arr, ok := v.([]interface{}); ok {
			for i, elem := range arr {
				e.add(id, class, k, formatAttrValue(k, elem), false, i)
			}
			continue
		}
		e.add(id, class, k, formatAttrValue(k, v), false, 0)
	}
}

// satelliteRefAttribute is a synthetic, JAG-only reference key (never a
// real CIM attribute) used purely to tie a re-imported satellite object
// back to its owner. It is deliberately NOT one of sachdaten.go's
// topologyAttributes, so internal/impl/common's satellite walk discovers
// it exactly like it would a real, dialect-specific CIM back-reference
// (e.g. PowerElectronicsUnit.PowerElectronicsConnection) — no special
// casing needed there for HJSON-authored satellites vs. real CIM ones.
const satelliteRefAttribute = "Satellite.Of"

// addSatellites emits one synthetic StagingRecord object per Satellite
// entry (see internal/importer/hjson.Satellite and
// internal/impl/common.AttributeKeySatellite's doc comment): its own
// literal attributes (addAttributes) plus one satelliteRefAttribute
// reference back to ownerID. This makes a re-imported satellite
// indistinguishable, from the satellite walk's point of view, from a real
// folded CIM satellite object — the walk discovers it via the reference
// (backward from ownerID) and folds it back into ownerID's Sachdaten under
// AttributeKeySatellite exactly as it would on first import from a real
// CIM/CGMES/NSC file, giving full export<->import symmetry with no
// dedicated satellite-merging logic anywhere.
func (e *r) addSatellites(ownerID string, satellites []Satellite) {
	for i, sat := range satellites {
		satID := fmt.Sprintf("%s-SAT%d", ownerID, i)
		e.add(satID, sat.Class, satelliteRefAttribute, ownerID, true, 0)
		e.addEntityAttributes(satID, sat.Class, sat.Attributes)
	}
}

// addTerminals emits the Terminal/ConnectivityNode records for one
// Zweipol/single-terminal Equipment's connects list. A single connects
// entry is a single-terminal source/sink (Terminal 2 = GND is implied
// automatically by BuildNodesAndEdges, per the auto-GND-wiring decision —
// no Terminal is synthesized for it here). Two entries are an ordinary
// Zweipol. Zero entries means the Equipment has no electrical
// representation at all (e.g. a multi-winding/3+-winding PowerTransformer,
// which terminals.go's classifyAll rejects as an Anomaly — >2 Terminals —
// rather than a hard import failure, see Konzept.md's Transformer
// section): the exporter (build.go's buildEquipment) then has no Edge to
// render a connects list from and simply omits it. Tolerating 0 here
// instead of erroring keeps hjson2 symmetric with that existing,
// pre-hjson2 Anomaly behavior — the Equipment round-trips as an
// Attribute/Satellite-only object with no Terminal, exactly as it already
// existed before export, rather than crashing the reimport over a case
// the raw importer itself already accepts. Anything above 2 is still a
// parse-time error, since that shape (3+ explicit connects) can only come
// from a corrupt/hand-edited .hjson file, never from hjson2's own
// exporter.
func (e *r) addTerminals(equipmentID string, connects []string) {
	if len(connects) == 0 {
		return
	}
	if len(connects) > 2 {
		e.errs = append(e.errs, model.StagingError{
			Version: e.version, SourceFile: e.fi.Path,
			Message: fmt.Sprintf("%s: connects must have 1 or 2 entries, got %d", equipmentID, len(connects)),
		})
		return
	}
	for i, node := range connects {
		seq := i + 1
		termID := fmt.Sprintf("%s-T%d", equipmentID, seq)
		e.add(termID, "Terminal", "Terminal.ConductingEquipment", equipmentID, true, 0)
		e.add(termID, "Terminal", "Terminal.ConnectivityNode", e.resolve(node), true, 0)
		e.add(termID, "Terminal", "ACDCTerminal.sequenceNumber", strconv.Itoa(seq), false, 0)
	}
}

// emitFile translates one parsed Fachmodell file into StagingRecords,
// dispatching on its classified top-level type.
func emitFile(version uint64, fi FileInfo, f *File) ([]model.StagingRecord, []model.StagingError) {
	e := &r{version: version, fi: fi}
	switch fi.Type {
	case TopLevelSubstation, TopLevelDistributionBox:
		e.emitStation(f)
	case TopLevelACLine:
		e.emitACLine(f)
	case TopLevelHouse:
		e.emitHouse(f)
	case TopLevelBoundary:
		e.emitBoundary(f)
	}
	return e.recs, e.errs
}

// emitStation handles both ONS ("Substation") and KVS
// ("distribution-box") files. KVS files are deliberately staged as CIM
// class "Substation" too (documented pre-existing gap: neither
// container.go's BuildContainers nor pass_a.go's RunPassA batch-root
// scanning has any support for a dedicated "distribution-box" staging
// class — see Konzept.md's Container section, "structurally identical to
// the station structure") — this reuses the fully-supported Substation
// path rather than inventing new Phase 2 machinery.
//
// Each named Busbar becomes a synthetic VoltageLevel (Konzept.md's Busbar
// grouping is per named Busbar, but container.go's Substation-direct
// BusbarSection grouping — used when there's no VoltageLevel at all, e.g.
// plain NSC data — merges ALL of a Substation's BusbarSections into ONE
// busbar container; synthesizing one VoltageLevel per named Busbar instead
// reuses container.go's existing, already-correct per-VoltageLevel busbar
// grouping to keep multiple named Busbars in one Substation properly
// distinct, with no Phase 2 code changes needed).
//
// Each Bay becomes a Feeder (NSC dialect's Bay-equivalent, chosen because
// Feeder.NormalEnergizingSubstation references the Substation directly —
// no VoltageLevel indirection needed, matching the Fachmodell format's flat
// Bay-under-Substation structure).
func (e *r) emitStation(f *File) {
	subID := e.fi.ContainerID
	e.add(subID, "Substation", "IdentifiedObject.name", subID, false, 0)
	// Container-level Sachdaten (e.g. AttributeKeyName "name", "region") —
	// see this package's doc comment on Emit's former "known current
	// limitation": these used to be parsed into f.Attributes and then
	// silently dropped, since the shared Phase 2 Sachdaten extraction
	// only ever looked at Equipment IDs, never at the batch's own
	// Substation/Building root IDs. Fixed 2026-07-19 by having
	// ProcessStationBatch flush ResolveBatchContainers' own
	// res.Attributes (previously computed but never sent to the sink) —
	// this addAttributes call is the counterpart import-side half of that
	// fix, giving these attributes somewhere to land as ordinary
	// StagingRecords owned by subID.
	e.addAttributes(subID, "Substation", f.Attributes)
	e.addGeometry(subID, "Substation", f.Geometry)

	for _, bb := range f.Busbars {
		// bb.ID is the shortened form of the Busbar's one real physical
		// electrical Node (see hjson2/build.go's buildBusbarSections —
		// e.g. "@CN3", the actual CIM ConnectivityNode ID, shortened),
		// NOT an arbitrary synthetic name. Resolving it therefore yields
		// exactly the same global node ID any external Kabel/ACLine file
		// would use to reference this same busbar (via the ordinary
		// shortenID fallback — see resolveConnectTarget), with no
		// import-side alias/rewrite table needed for the two to agree.
		nodeID := e.resolve(bb.ID)
		// vlID (the VoltageLevel object) must be a distinct string from
		// nodeID (the ConnectivityNode/Node) — "-VL" keeps them from
		// colliding as two different CIM objects sharing one ID.
		vlID := nodeID + "-VL"
		e.add(vlID, "VoltageLevel", "VoltageLevel.Substation", subID, true, 0)
		e.add(vlID, "VoltageLevel", "IdentifiedObject.name", bb.ID, false, 0)
		for _, sec := range bb.Sections {
			// secID is the Section's own Equipment ID, used only for its
			// Attributes/Satellites/Geometry — a Section is a purely
			// logical/organizational slot, never a distinct electrical
			// point (a Busbar Container is, physically, exactly one
			// Node — see MergeBusbarSectionNodes's doc comment). Its
			// Terminal's ConnectivityNode is therefore wired directly to
			// the Busbar's one shared nodeID, not to secID itself — this
			// makes every Section reconverge on the same real Node from
			// the start, with no post-hoc merge step needed here.
			secID := e.resolve(bb.ID + "-" + sec.ID)
			e.add(secID, "BusbarSection", "Equipment.EquipmentContainer", vlID, true, 0)
			// A Section's own Attributes take precedence; any key hoisted
			// onto the Busbar (see build.go's hoistCommonSectionName,
			// currently only ever "IdentifiedObject.name") only fills in
			// where the Section itself doesn't already have that key —
			// this is what makes the export-side hoist round-trip safe.
			secAttrs := sec.Attributes
			if len(bb.Attributes) > 0 {
				merged := make(map[string]interface{}, len(bb.Attributes)+len(secAttrs))
				for k, v := range bb.Attributes {
					merged[k] = v
				}
				for k, v := range secAttrs {
					merged[k] = v
				}
				secAttrs = merged
			}
			e.addEntityAttributes(secID, "BusbarSection", secAttrs)
			e.addSatellites(secID, sec.Satellites)
			e.addGeometry(secID, "BusbarSection", sec.Geometry)
			// BusbarSection is its own Node-role Equipment (nodeRoleClasses,
			// see terminals.go), but its Terminal points at the Busbar's
			// shared nodeID rather than self-referencing secID — every
			// Section of one Busbar shares the very same node.
			termID := secID + "-T1"
			e.add(termID, "Terminal", "Terminal.ConductingEquipment", secID, true, 0)
			e.add(termID, "Terminal", "Terminal.ConnectivityNode", nodeID, true, 0)
			e.add(termID, "Terminal", "ACDCTerminal.sequenceNumber", "1", false, 0)
		}
	}

	for _, bay := range f.Bays {
		bayID := e.resolve(bay.ID)
		e.add(bayID, "Feeder", "Feeder.NormalEnergizingSubstation", subID, true, 0)
		// Prefer the Bay container's own captured "name" Sachdaten
		// (bay.Attributes, added 2026-07-21) over defaulting to the
		// Bay's own ID — see Bay.Attributes' doc comment in types.go.
		// Uses the literal "name" key (== impl/common.AttributeKeyName)
		// rather than importing impl/common here, to avoid an import
		// cycle (impl/common's tests import importer/phase1, which
		// imports importer/hjson2).
		name := bay.ID
		if v, ok := bay.Attributes["name"]; ok {
			if s, ok := v.(string); ok && s != "" {
				name = s
			}
		}
		e.add(bayID, "Feeder", "IdentifiedObject.name", name, false, 0)
		for _, eq := range bay.Equipment {
			e.emitEquipment(eq, bayID)
		}
	}

	// Equipment attached directly to the station's own root container
	// instead of a child Bay/Feeder — see build.go's buildStation doc
	// comment (an incomplete/orphaned equipment stub with no proper Bay
	// assignment, e.g. examples/nsc's "...ohne_Trafo..." dataset's
	// single-ended PowerTransformer). Its EquipmentContainer is subID
	// itself, exactly mirroring how the exporter read it back.
	for _, eq := range f.Equipment {
		e.emitEquipment(eq, subID)
	}

	// Station-internal ACLineSegments folded straight into this file
	// instead of their own separate Kabel file (see build.go's
	// classifyInternalACLines/buildStation) — emitted exactly like an
	// ordinary Kabel file's Segments (emitSegments), so Pass B's
	// buildACLineChains resolves their ACLine container the same way
	// either way, purely from topology.
	e.emitSegments(f.Segments)
}

// emitEquipment emits one piece of Equipment attached to containerID
// (a Bay/Feeder ID for station-internal equipment, or a House ID for
// standalone consumer/producer equipment).
// emitEquipment stages one Equipment entry. containerID is the owning
// container's ID, or "" for a genuinely containerless equipment (see
// emitBoundary) — in that case no "Equipment.EquipmentContainer"
// reference is emitted at all, matching Konzept.md's decision that some
// equipment (boundary EquivalentInjection) legitimately has no Container
// membership rather than falling back to some synthetic container.
func (e *r) emitEquipment(eq Equipment, containerID string) {
	if eq.Class == "" {
		e.errs = append(e.errs, model.StagingError{
			Version: e.version, SourceFile: e.fi.Path,
			Message: fmt.Sprintf("equipment %q: missing class", eq.ID),
		})
		return
	}
	eqID := e.resolve(eq.ID)
	if containerID != "" {
		e.add(eqID, eq.Class, "Equipment.EquipmentContainer", containerID, true, 0)
	}
	e.addEntityAttributes(eqID, eq.Class, eq.Attributes)
	e.addSatellites(eqID, eq.Satellites)
	e.addMeterSchedules(eqID, eq)
	e.addDiscreteControlLimits(eqID, eq)
	e.addGeometry(eqID, eq.Class, eq.Geometry)
	e.addTerminals(eqID, eq.Connects)
}

// addMeterSchedules is extractMeterSchedules' import-side counterpart
// (see hjson2 exporter's build.go and types.go's Equipment.Measuring/
// Transmission doc comment): re-expands eq's Measuring/Transmission maps
// (if any) back into two ordinary synthetic "TimeSchedule" satellites,
// Measuring first, so the shared satellite-fold pipeline
// (internal/impl/common/sachdaten.go) rediscovers them exactly as if they
// had been two plain "satellites" entries in the file. A no-op when
// neither field is set (the common case for every non-Meter class, and
// for a Meter whose satellites didn't fit the exact two-TimeSchedule
// shape the exporter requires before applying this simplification).
func (e *r) addMeterSchedules(eqID string, eq Equipment) {
	var sats []Satellite
	if eq.Measuring != nil {
		sats = append(sats, Satellite{Class: "TimeSchedule", Attributes: withFallbackScheduleName(eq.Measuring, eq.Attributes)})
	}
	if eq.Transmission != nil {
		sats = append(sats, Satellite{Class: "TimeSchedule", Attributes: withFallbackScheduleName(eq.Transmission, eq.Attributes)})
	}
	if len(sats) == 0 {
		return
	}
	e.addSatellites(eqID, sats)
}

// withFallbackScheduleName is dropRedundantScheduleName's import-side
// counterpart (see hjson2 exporter's build.go): a measuring/transmission
// TimeSchedule map missing its own "name" (the exporter's stripped alias
// for "IdentifiedObject.name" — see this package's denormalizeAttrKey)
// inherits the owning Equipment's own "name" instead, reconstructing the
// exact value the exporter dropped. Returns schedule unchanged (same map,
// no copy) when it already has its own name or the owner has none —
// avoiding an allocation on the overwhelmingly common no-op path.
func withFallbackScheduleName(schedule, ownerAttrs map[string]interface{}) map[string]interface{} {
	if _, ok := schedule["name"]; ok {
		return schedule
	}
	name, ok := ownerAttrs["name"]
	if !ok {
		return schedule
	}
	merged := make(map[string]interface{}, len(schedule)+1)
	for k, v := range schedule {
		merged[k] = v
	}
	merged["name"] = name
	return merged
}

// addDiscreteControlLimits is extractDiscreteControlLimits' import-side
// counterpart (see hjson2 exporter's build.go and types.go's
// Equipment.Steps doc comment): re-expands eq.Steps back into ordinary
// synthetic "DiscreteControlLimit" satellites, one per entry
// (sequenceNumber = index+1, value = the step's number, name =
// "<eqID>-<sequenceNumber>" — a hjson2-only approximation of the original
// name, see Steps' doc comment). A no-op when eq.Steps is empty.
func (e *r) addDiscreteControlLimits(eqID string, eq Equipment) {
	if len(eq.Steps) == 0 {
		return
	}
	sats := make([]Satellite, 0, len(eq.Steps))
	for i, v := range eq.Steps {
		seq := i + 1
		sats = append(sats, Satellite{
			Class: "DiscreteControlLimit",
			Attributes: map[string]interface{}{
				"IdentifiedObject.name":               fmt.Sprintf("%s-%d", eqID, seq),
				"DiscreteControlLimit.sequenceNumber": strconv.Itoa(seq),
				"DiscreteControlLimit.value":          strconv.FormatFloat(v, 'g', -1, 64),
			},
		})
	}
	e.addSatellites(eqID, sats)
}

// emitSegments emits every Segment in segs the same way regardless of
// which file declared it — straight into ACLineSegment Equipment +
// Terminals, no container record (see emitACLine's doc comment: Pass B's
// buildACLineChains derives the ACLine container automatically from the
// resolved segment topology; ACLineSegment carries no
// Equipment.EquipmentContainer of its own). Shared by emitACLine (an
// ordinary Kabel file) and emitStation (a station-internal ACLineSegment
// folded straight into its owning station's own file — see build.go's
// classifyInternalACLines/buildStation).
func (e *r) emitSegments(segs []Segment) {
	for _, seg := range segs {
		segID := e.resolve(seg.ID)
		e.addEntityAttributes(segID, "ACLineSegment", seg.Attributes)
		e.addSatellites(segID, seg.Satellites)
		e.addGeometryPath(segID, "ACLineSegment", seg.Geometry)
		e.addTerminals(segID, []string{seg.From, seg.To})
	}
}

// emitACLine handles a Kabel/ACLine file: only Segments, translated
// straight into ACLineSegment Equipment + Terminals. No container record is
// emitted at all — Pass B's buildACLineChains derives the ACLine container
// automatically from the resolved segment topology (see Konzept.md's
// "ACLine boundary is topological" decision; confirmed in container.go/
// pass_b.go: ACLineSegment carries no Equipment.EquipmentContainer of its
// own).
func (e *r) emitACLine(f *File) {
	e.emitSegments(f.Segments)
}

// emitHouse handles a Haushalte/House file: a Building container plus its
// (usually single-terminal source/sink) Equipment.
func (e *r) emitHouse(f *File) {
	houseID := e.fi.ContainerID
	e.add(houseID, "Building", "IdentifiedObject.name", houseID, false, 0)
	// See emitStation's matching comment — same container-level Sachdaten
	// fix applies to House files.
	e.addAttributes(houseID, "Building", f.Attributes)
	e.addGeometry(houseID, "Building", f.Geometry)
	for _, eq := range f.Equipment {
		e.emitEquipment(eq, houseID)
	}
}

// emitBoundary handles a Grenzknoten/Boundary file: a flat list of
// Equipment with no owning container at all (containerID = "", see
// emitEquipment's doc comment). No container record (Building/Substation)
// is emitted here — unlike emitHouse/emitStation, a Boundary file
// describes no container object, only free-standing equipment.
func (e *r) emitBoundary(f *File) {
	for _, eq := range f.Equipment {
		e.emitEquipment(eq, "")
	}
}

// formatAttrValue renders an hjson-decoded attribute value (string,
// float64, or bool) as the plain string StagingRecord.Value expects,
// applying the kW/kVA -> MW/MVA curated-key conversion where applicable.
func formatAttrValue(key string, v interface{}) string {
	switch val := v.(type) {
	case float64:
		if kwToMWKeys[key] {
			val = val / 1000
		}
		return strconv.FormatFloat(val, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}
