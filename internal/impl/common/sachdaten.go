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
	"Line":             true,
	"ControlArea":      true,
	"BaseVoltage":      true, // shared many-to-one hub (e.g. all 220kV equipment points to the same BaseVoltage object) — must not bridge unrelated equipment together
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

// BuildAttributes resolves Sachdaten for every Equipment in resolved: its
// own literal attributes plus those of any satellite object reachable via
// non-topology references (forward or backward), excluding structural
// classes. See Konzept.md's Sachdaten section (EAV, global key enum — the
// AttributeKey values used here are the raw CIM attribute names, since the
// final global enum isn't decided yet; expect these keys to be remapped
// once it is).
//
// Unlike Terminal/Container/Geometry resolution (which each scan a handful
// of small or medium classes into memory), the Sachdaten/Anhängsel walk is
// inherently bidirectional and can reach any object anywhere in the model —
// so it does NOT preload a whole-model index. Instead it resolves one
// Equipment at a time, walking outward via store.GetByID (forward: an
// object's own reference attributes) and store.GetReferencesTo (backward:
// who points at this object, backed by a DB index — see
// internal/core/staging/store.go and internal/sqlite/staging.go). Peak
// memory is therefore bounded by one Equipment's satellite subgraph, not by
// the whole model, at the cost of more (smaller) DB round-trips — the
// resource-goal trade-off called for in Konzept.md ("now" reads must not
// scale linearly with model size).
func BuildAttributes(store staging.Store, version uint64, chunkSize int, resolved map[string]EquipmentTerminals) ([]coremodel.Attribute, error) {
	var equipmentIDs []string
	for eqID := range resolved {
		equipmentIDs = append(equipmentIDs, eqID)
	}
	sort.Strings(equipmentIDs)

	var attrs []coremodel.Attribute
	for _, eqID := range equipmentIDs {
		visited := map[string]bool{eqID: true}
		a, err := collectAttributes(store, version, resolved, eqID, eqID, visited)
		if err != nil {
			return nil, fmt.Errorf("common: collecting attributes for %s: %w", eqID, err)
		}
		attrs = append(attrs, a...)
	}
	return attrs, nil
}

// collectAttributes emits ownerID's own literal attributes, then walks
// objID's references (forward and backward) to find and recurse into
// satellite objects, still attributing everything to ownerID. objID's own
// records are fetched once via store.GetByID; backward neighbors come from
// store.GetReferencesTo, an indexed reverse lookup — so no whole-model
// index is ever built.
func collectAttributes(store staging.Store, version uint64, resolved map[string]EquipmentTerminals, ownerID, objID string, visited map[string]bool) ([]coremodel.Attribute, error) {
	records, err := store.GetByID(version, objID)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		// Dangling/external reference (e.g. BaseVoltage from a missing
		// CGMES boundary profile): has no attributes of its own, and
		// walking into it would only expose whatever else happens to
		// reference the same external ID as a false "hub".
		return nil, nil
	}
	class := records[0].Class

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

	incoming, err := store.GetReferencesTo(version, objID)
	if err != nil {
		return nil, err
	}
	for _, r := range incoming {
		if !topologyAttributes[r.Attribute] {
			neighbors = append(neighbors, r.ID)
		}
	}

	sort.Strings(neighbors)
	if structuralClasses[class] && objID != ownerID {
		// A structural object reached as a neighbor (shouldn't normally
		// happen, since callers already filter structuralClasses before
		// recursing, but guards against walking further from it).
		return out, nil
	}
	for _, n := range neighbors {
		if visited[n] {
			continue
		}
		visited[n] = true
		nRecords, err := store.GetByID(version, n)
		if err != nil {
			return nil, err
		}
		if len(nRecords) == 0 {
			continue // dangling/external reference, see above
		}
		if structuralClasses[nRecords[0].Class] {
			continue
		}
		if n != ownerID {
			if _, isOtherEquipment := resolved[n]; isOtherEquipment {
				continue // belongs to its own Equipment, not a satellite of ownerID
			}
		}
		sub, err := collectAttributesFromRecords(store, version, resolved, ownerID, n, nRecords, visited)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

// collectAttributesFromRecords is collectAttributes, but reuses records
// already fetched by the caller (which needed them anyway to check
// class/existence) instead of querying store.GetByID again.
func collectAttributesFromRecords(store staging.Store, version uint64, resolved map[string]EquipmentTerminals, ownerID, objID string, records []model.StagingRecord, visited map[string]bool) ([]coremodel.Attribute, error) {
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

	incoming, err := store.GetReferencesTo(version, objID)
	if err != nil {
		return nil, err
	}
	for _, r := range incoming {
		if !topologyAttributes[r.Attribute] {
			neighbors = append(neighbors, r.ID)
		}
	}

	sort.Strings(neighbors)
	for _, n := range neighbors {
		if visited[n] {
			continue
		}
		visited[n] = true
		nRecords, err := store.GetByID(version, n)
		if err != nil {
			return nil, err
		}
		if len(nRecords) == 0 {
			continue
		}
		if structuralClasses[nRecords[0].Class] {
			continue
		}
		if n != ownerID {
			if _, isOtherEquipment := resolved[n]; isOtherEquipment {
				continue
			}
		}
		sub, err := collectAttributesFromRecords(store, version, resolved, ownerID, n, nRecords, visited)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}
