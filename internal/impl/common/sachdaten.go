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
func BuildAttributes(store staging.Store, version uint64, chunkSize int, resolved map[string]EquipmentTerminals) ([]coremodel.Attribute, error) {
	var equipmentIDs []string
	for eqID := range resolved {
		equipmentIDs = append(equipmentIDs, eqID)
	}
	sort.Strings(equipmentIDs)

	p := newProgress("sachdaten")
	var attrs []coremodel.Attribute
	for start := 0; start < len(equipmentIDs); start += sachdatenBatchSize {
		end := min(start+sachdatenBatchSize, len(equipmentIDs))
		batch := equipmentIDs[start:end]
		batchAttrs, err := collectAttributesBatch(store, version, resolved, batch, p)
		if err != nil {
			return nil, fmt.Errorf("common: collecting attributes for batch starting at %s: %w", batch[0], err)
		}
		attrs = append(attrs, batchAttrs...)
		p.Tick(len(batch))
	}
	p.Done()
	return attrs, nil
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
	for i, eqID := range equipmentIDs {
		walks[i] = &ownerWalk{ownerID: eqID, visited: map[string]bool{eqID: true}, frontier: []string{eqID}}
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
		referencing, err := getReferencesToAnyIndexed(store, version, allIDs)
		if err != nil {
			return nil, fmt.Errorf("common: fetching wave reverse references: %w", err)
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
