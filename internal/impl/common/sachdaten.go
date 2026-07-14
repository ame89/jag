// Package common — this file builds Sachdaten (model.Attribute) for each
// resolved Equipment: its own literal attributes plus those of any
// "Anhängsel" (satellite objects, e.g. GeneratingUnit/FossilFuel/
// RegulatingControl) reachable from it via non-topology references, in
// either direction.
package common

import (
	"fmt"
	"sort"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
	"gitlab.com/openk-nsc/jag/internal/progress"
)

// structuralClasses are never walked into as a satellite — they are handled
// by their own dedicated resolution (topology: Terminal/ConnectivityNode;
// hierarchy: Substation/VoltageLevel/Bay/Line), or are shared/many-to-one
// grouping objects too broad to fold into a single Equipment's Sachdaten
// (ControlArea).
var structuralClasses = map[string]bool{
	"Terminal":         true,
	"ConnectivityNode": true,
	"Substation":       true,
	"VoltageLevel":     true,
	"Bay":              true,
	"Feeder":           true, // NSC dialect's name for the same container role as Bay, see container.go
	"Building":         true, // JAG's "house" container type, see container.go/containertype.go
	"Line":             true,
	"ControlArea":      true,
	"BaseVoltage":      true, // shared many-to-one hub (e.g. all 220kV equipment points to the same BaseVoltage object) — must not bridge unrelated equipment together
	"InstanceSet":      true, // shared many-to-one hub: CIM's dataset/model-version envelope object (IdentifiedObject.InstanceSet), referenced by essentially every object in a file (observed in lasttest-200-10-10 load test: a single InstanceSet fanned out the reverse-reference walk to the whole model, causing multi-GB RAM use and an apparent stall in the sachdaten phase) — must not bridge unrelated equipment together, same reasoning as BaseVoltage above.
	"CoordinateSystem": true, // shared many-to-one hub: every Location.CoordinateSystem in a file typically points at the same single CoordinateSystem object (e.g. "wgs84"). Location itself is not a resolved Equipment, so its reverse-reference walk isn't filtered by the resolved[objID] check — reaching CoordinateSystem from one owner's Location bridges in every OTHER Location in the model too (observed in lasttest-200-10-10: this was the second, larger hub found after fixing InstanceSet, responsible for a jump to ~60M visited records / ~9.4GB RAM in a single batch).
	"PerLengthSequenceImpedance": true, // shared many-to-one hub: a cable-type/impedance catalog object (e.g. "NYY_4x70mm") referenced by every ACLineSegment using that cable type — thousands of segments across unrelated ACLines all point at the same object (observed in lasttest-200-10-10: fanin ~20000), which would otherwise bridge unrelated equipment together exactly like BaseVoltage/InstanceSet/CoordinateSystem above.
	"PSRType":                    true, // shared many-to-one hub: a classification/category object (e.g. "PSR-TRAN") reused across many otherwise-unrelated equipment of the same kind (observed in lasttest-200-10-10: fanin ~200) — same bridging risk as the other hubs above.
	"GeographicalRegion":         true, // shared many-to-one hub: CIM's Substation.Region -> SubGeographicalRegion -> GeographicalRegion chain — one region object typically covers the entire grid/file (observed in lasttest-500-10-10).
	"SubGeographicalRegion":      true, // shared many-to-one hub: every Substation in a region points at the same SubGeographicalRegion object (observed in lasttest-500-10-10: fanin ~1500) — same bridging risk as the other hubs above.

	// The following are all "catalog"/type-data objects (confirmed with
	// the user 2026-07-14): each represents a reusable type/category
	// shared across many otherwise-unrelated equipment (analogous to
	// PSRType/BaseVoltage/PerLengthSequenceImpedance above), not data
	// belonging to one specific owner. Not all were observed with a huge
	// fanin in the current example datasets, but the classification is by
	// CIM semantics (catalog vs. instance data), not just measured fanin,
	// since a small/synthetic example dataset can under-represent a
	// catalog's real-world sharing factor.
	"NameType":                   true, // catalog: classifies Name objects (e.g. "IEC61970 CGMES Sensor Alias"), shared across many IdentifiedObjects' Name satellites.
	"NameTypeAuthority":          true, // catalog: the naming authority owning a NameType, shared across all NameTypes it defines.
	"OperationalLimitType":       true, // catalog: a reusable limit kind (e.g. "PATL"/"TATL"), shared across many OperationalLimitSet/OperationalLimit objects.
	"EnergySchedulingType":       true, // catalog: a reusable scheduling/energy-source category, shared across many GeneratingUnit/TieFlow objects.
	"RatioTapChangerTable":       true, // catalog: a reusable tap-ratio table, potentially shared by several RatioTapChangers of the same transformer type.
	"RatioTapChangerTablePoint":  true, // catalog: individual rows of a shared RatioTapChangerTable, not owner-specific data.
	"PhaseTapChangerTable":       true, // catalog: a reusable phase-tap-ratio table, potentially shared by several PhaseTapChangers of the same transformer type.
	"PhaseTapChangerTablePoint":  true, // catalog: individual rows of a shared PhaseTapChangerTable, not owner-specific data.
	"TopologicalIsland":          true, // catalog/derived: a CGMES SV-profile grouping object referenced by every TopologicalNode in its island — bridges unrelated equipment together just like the hubs above.
	"LoadArea":                   true, // catalog/grouping: a geographic load-area object shared across many SubLoadArea/ConformLoadGroup objects.
	"SubLoadArea":                true, // catalog/grouping: shared across many ConformLoadGroup objects within the same load area.
	"BasePower":                  true, // catalog: a shared per-unit base-power reference value, analogous to BaseVoltage.
	"LoadResponseCharacteristic": true, // catalog: a reusable load voltage-dependency model, shared across many ConformLoad/ConformLoadGroup objects.
	"DiagramObjectStyle":         true, // catalog: a reusable diagram presentation style, shared across many DiagramObjects.
	"CommunicationLink":          true, // catalog/out-of-scope: SCADA communication-link metadata, shared across many RemoteUnit/RemoteSource objects; not modeled by JAG (no live telemetry, see Idee.md).
	"RemoteUnit":                 true, // out-of-scope: SCADA remote-terminal-unit metadata, not modeled by JAG (no live telemetry).
	"RemoteSource":                true, // out-of-scope: SCADA measurement-source metadata, not modeled by JAG (no live telemetry).
}

// irrelevantClasses are classes outside JAG's domain entirely — never
// emitted as Sachdaten and never walked into as a satellite, regardless of
// how they're reached. Currently this is the CGMES DL (Diagram Layout)
// profile: Diagram/DiagramObject/DiagramObjectPoint are one-line-diagram
// rendering hints (symbol position/rotation on a schematic canvas) with no
// electrical or geographic meaning — not the same as the GL profile's real
// WGS84 Location/PositionPoint, which JAG does model via Geometry. JAG has
// no diagram-rendering use case, so this data is pure noise here.
var irrelevantClasses = map[string]bool{
	"Diagram":            true,
	"DiagramObject":      true,
	"DiagramObjectPoint": true,
}

// topologyAttributes are reference attributes already fully consumed by
// Terminal/ConnectivityNode/Container resolution elsewhere — never
// re-emitted as Sachdaten, and never walked as a satellite edge.
var topologyAttributes = map[string]bool{
	"Terminal.ConductingEquipment":               true,
	"Terminal.ConnectivityNode":                  true,
	"ConnectivityNode.ConnectivityNodeContainer": true,
	"Equipment.EquipmentContainer":               true,
	"Bay.VoltageLevel":                           true,
}

// sachdatenBatchSize is how many Equipment are processed together per
// outer batch of BuildAttributes — deliberately generous ("lieber etwas
// zuviel laden als zu wenig"): the goal is few, large batched DB calls
// instead of many small ones (see Idee.md's bulk-operations mandate), not
// a tight memory bound. Only the current batch's satellite subgraphs are
// held in memory at once, not the whole model.
const sachdatenBatchSize = 5000

// DisableSatelliteWalk is a diagnostic/experiment switch (off by default):
// when true, BuildAttributes emits only each Equipment's own literal
// attributes and never walks into any Anhängsel/satellite object at all
// (forward or backward), regardless of class. This exists purely to
// measure the Sachdaten phase's baseline duration/RAM without any
// many-to-one hub risk, as a reference point while hunting hub classes
// (see structuralClasses) — not a supported/permanent mode, since real
// Sachdaten (e.g. GeneratingUnit/RegulatingControl/PerLengthSequenceImpedance
// attributes) would be silently dropped.
var DisableSatelliteWalk = false

// BuildAttributes resolves Sachdaten for every Equipment in resolved: its
// own literal attributes plus those of any satellite object reachable via
// non-topology references (forward or backward), excluding structural
// classes. See Konzept.md's Sachdaten section (EAV, global key enum — the
// AttributeKey values used here are the raw CIM attribute names, since the
// final global enum isn't decided yet; expect these keys to be remapped
// once it is).
//
// The Sachdaten/Anhängsel walk is inherently bidirectional and can reach any
// object anywhere in the model. Instead of one DB round-trip per visited
// object (the original design, which turned into the dominant Phase 2
// bottleneck at real scale — see Idee.md's bulk-operations mandate: "Hier
// sind Bulk- oder Massenoperationen zu erwarten, statt Abarbeitung von
// Einzelabfragen, Einzelrecords. Das wäre viel zu ineffizient."), Equipment
// are processed in batches (sachdatenBatchSize), and within a batch the
// walk proceeds in synchronized waves: one round-trip fetches records for
// every ID any Equipment in the batch still needs to look at (forward via
// store.GetByIDs, backward via store.GetReferencesToAny), for all of them
// at once, then the next wave's IDs are determined from that, and so on
// until no Equipment in the batch has anything left to look up. Memory
// stays bounded to one batch's satellite subgraphs, not the whole model;
// DB round-trips are bounded by the satellite graph's depth (typically
// small), not by the number of objects visited.
//
// Each batch's result is flushed through sink immediately and then
// dropped — BuildAttributes itself never accumulates the whole model's
// Attributes into one slice (see sink.go's doc comment: this is what
// keeps RAM bounded to batch size regardless of total model size, instead
// of scaling with how much Equipment the caller/station-worker was
// assigned).
func BuildAttributes(store staging.Store, version uint64, chunkSize int, resolved map[string]EquipmentTerminals, sink Sink) error {
	var equipmentIDs []string
	for eqID := range resolved {
		equipmentIDs = append(equipmentIDs, eqID)
	}
	sort.Strings(equipmentIDs)

	p := newProgress("sachdaten")
	defer p.Done()
	for start := 0; start < len(equipmentIDs); start += sachdatenBatchSize {
		end := min(start+sachdatenBatchSize, len(equipmentIDs))
		batch := equipmentIDs[start:end]
		batchAttrs, err := collectAttributesBatch(store, version, resolved, batch, p)
		if err != nil {
			return fmt.Errorf("common: collecting attributes for batch starting at %s: %w", batch[0], err)
		}
		if len(batchAttrs) > 0 {
			if err := sink.WriteAttributes(batchAttrs); err != nil {
				return fmt.Errorf("common: writing attribute batch: %w", err)
			}
		}
		p.Tick(len(batch))
	}
	return nil
}

// ownerWalk tracks one Equipment's satellite walk across waves within a
// collectAttributesBatch call.
type ownerWalk struct {
	ownerID  string
	visited  map[string]bool
	frontier []string // IDs to process in the current wave
	attrs    []coremodel.Attribute
}

// collectAttributesBatch runs the wave-based satellite walk (see
// BuildAttributes) for one batch of Equipment IDs at once. p receives a
// Tick per wave (not just once per whole batch) so a batch whose satellite
// graph needs many waves still produces visible "phase progress" log lines
// instead of going silent for the batch's whole duration — see
// internal/progress's doc comment on why silence vs. stuck must stay
// distinguishable.
func collectAttributesBatch(store staging.Store, version uint64, resolved map[string]EquipmentTerminals, equipmentIDs []string, p *progress.Reporter) ([]coremodel.Attribute, error) {
	walks := make([]*ownerWalk, len(equipmentIDs))
	rootIDs := make(map[string]bool, len(equipmentIDs))
	for i, eqID := range equipmentIDs {
		walks[i] = &ownerWalk{ownerID: eqID, visited: map[string]bool{eqID: true}, frontier: []string{eqID}}
		rootIDs[eqID] = true
	}

	for {
		var allIDs []string
		for _, w := range walks {
			allIDs = append(allIDs, w.frontier...)
		}
		if len(allIDs) == 0 {
			break
		}
		p.Tick(len(allIDs))

		byID, err := getByIDsIndexed(store, version, allIDs)
		if err != nil {
			return nil, fmt.Errorf("common: fetching wave records: %w", err)
		}
		// The backward-reference scan (GetReferencesToAny) is only needed
		// to discover satellite neighbors reachable via incoming
		// references; when the satellite walk is disabled its result is
		// discarded entirely below, so skip the (expensive, table-wide)
		// query altogether rather than computing and throwing it away.
		//
		// Additionally, an ID whose class is a known hub/structural class
		// (e.g. InstanceSet, BaseVoltage, GeographicalRegion — objects
		// referenced from thousands/millions of unrelated equipments) is
		// never expanded into below regardless of its incoming references,
		// so querying its (potentially huge) incoming-reference set here
		// would be pure wasted work/RAM. Filter such IDs out of the
		// backward-reference query up front instead of discarding the
		// result afterwards. Root (owner) IDs are always queried, since the
		// structural-class skip never applies to them.
		seen := make(map[string]bool, len(allIDs))
		backwardIDs := allIDs[:0:0]
		for _, id := range allIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			recs := byID[id]
			if len(recs) == 0 {
				continue // dangling reference; nothing incoming matters either
			}
			if !rootIDs[id] {
				class := recs[0].Class
				if structuralClasses[class] || irrelevantClasses[class] {
					continue // hub/structural class: never expanded into, skip its (huge) fan-in
				}
			}
			backwardIDs = append(backwardIDs, id)
		}
		var referencing map[string][]model.StagingRecord
		if !DisableSatelliteWalk && len(backwardIDs) > 0 {
			referencing, err = getReferencesToAnyIndexed(store, version, backwardIDs)
			if err != nil {
				return nil, fmt.Errorf("common: fetching wave reverse references: %w", err)
			}
		}

		for _, w := range walks {
			var nextFrontier []string
			for _, objID := range w.frontier {
				records := byID[objID]
				if len(records) == 0 {
					// Dangling/external reference (e.g. BaseVoltage from a
					// missing CGMES boundary profile).
					continue
				}
				class := records[0].Class
				isRoot := objID == w.ownerID
				if !isRoot {
					if structuralClasses[class] || irrelevantClasses[class] {
						continue // never walked into as a satellite
					}
					if _, isOtherEquipment := resolved[objID]; isOtherEquipment {
						continue // belongs to its own Equipment, not a satellite of ownerID
					}
				}

				out, neighbors := attributesAndNeighbors(w.ownerID, objID, records, referencing[objID])
				w.attrs = append(w.attrs, out...)
				if DisableSatelliteWalk {
					continue // experiment: never expand past the root object at all
				}
				for _, n := range neighbors {
					if w.visited[n] {
						continue
					}
					w.visited[n] = true
					nextFrontier = append(nextFrontier, n)
				}
			}
			w.frontier = nextFrontier
		}
	}

	var attrs []coremodel.Attribute
	for _, w := range walks {
		attrs = append(attrs, w.attrs...)
	}
	return attrs, nil
}

// attributesAndNeighbors emits objID's own literal attributes (attributed to
// ownerID) plus the sorted set of neighbor IDs reachable from objID via
// non-topology references, forward (objID's own reference attributes, from
// records) and backward (incoming, objID's reverse references).
func attributesAndNeighbors(ownerID, objID string, records, incoming []model.StagingRecord) ([]coremodel.Attribute, []string) {
	var out []coremodel.Attribute
	var neighbors []string
	for _, r := range records {
		if !r.IsReference {
			out = append(out, coremodel.Attribute{OwnerID: ownerID, Key: coremodel.AttributeKey(r.Attribute), Value: r.Value})
			continue
		}
		if !topologyAttributes[r.Attribute] {
			neighbors = append(neighbors, r.Value)
		}
	}
	for _, r := range incoming {
		if !topologyAttributes[r.Attribute] {
			neighbors = append(neighbors, r.ID)
		}
	}
	sort.Strings(neighbors)
	return out, neighbors
}
