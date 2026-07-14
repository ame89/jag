// Package common — this file builds Geometry (see Konzept.md, "Geometrie"):
// 2D WGS84 coordinates composed onto Equipment or Container owners, from
// CIM's GL profile (Location/PositionPoint, linked via
// PowerSystemResource.Location).
//
// NOTE: Espheim (this session's validation fixture) ships no GL profile at
// all, so this code is written against the documented CGMES GL structure
// but has NOT been validated end-to-end against real coordinate data —
// unlike the rest of Phase 2, treat this as unverified until a dataset with
// an actual GL profile is available.
package common

import (
	"fmt"
	"sort"
	"strconv"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// BuildGeometry resolves a Geometry for every Equipment/Container owner
// (from equipmentIDs/containerIDs — the sets of already-resolved
// Equipment/Container IDs, from ResolveTerminals/BuildContainers) that
// carries a PowerSystemResource.Location reference:
//
//   - Location -> PositionPoint (1:n, sequenceNumber-ordered) is the CIM
//     GL shape. Per Konzept.md, JAG's Geometry is a single 2D point, not a
//     path — for owners with several PositionPoints (e.g. a Line's route),
//     only the lowest sequenceNumber ("the first point") is kept as a
//     simplification; the full path isn't modeled. This is a deliberate,
//     documented simplification (not a full GIS replacement), consistent
//     with Konzept.md's Geometrie section.
//   - Location.CoordinateSystem is deliberately NOT read/checked — JAG
//     currently assumes every PositionPoint's xPosition/yPosition is
//     already WGS84 lon/lat (explicit decision), not a projected CRS. If a
//     future dataset uses a non-WGS84 CoordinateSystem, this will silently
//     misinterpret its coordinates; revisit if/when that's encountered.
//
// IMPORTANT (2026-07-14 fix): this used to scan store.ListClasses/
// store.GetByClass over the ENTIRE model (every class, every record) plus
// index every PositionPoint in the whole model up front, regardless of how
// small equipmentIDs/containerIDs was — i.e. its cost/RAM scaled with the
// whole model's size, not with the caller's actual workload. That is
// exactly the kind of "touches all data" code the project's hard resource
// target forbids (see Idee.md's Performance/Resource goals: "now" reads
// must never scale with model size) and, worse, meant every one of
// BuildSachdatenAndGeometryParallel's station workers redundantly re-scanned
// the whole model concurrently. Fixed to use the same targeted, indexed,
// batch-bounded lookup style as BuildAttributes/collectAttributesBatch
// (getByIDsIndexed/getReferencesToAnyIndexed, see batch.go): resolve only
// the given owners' own Location reference, then only the PositionPoints
// that reference those specific Locations, then only those PositionPoints'
// own attributes. Cost/RAM now scales with len(equipmentIDs)+len(containerIDs)
// (processed in bounded batches, see geometryBatchSize below), never with
// total model size.
func BuildGeometry(store staging.Store, version uint64, chunkSize int, equipmentIDs, containerIDs map[string]bool, sink Sink) error {
	p := newProgress("geometry")
	defer p.Done()

	ownerKind := make(map[string]coremodel.GeometryOwnerKind, len(equipmentIDs)+len(containerIDs))
	ownerIDs := make([]string, 0, len(equipmentIDs)+len(containerIDs))
	for id := range equipmentIDs {
		ownerIDs = append(ownerIDs, id)
		ownerKind[id] = coremodel.GeometryOwnerEquipment
	}
	for id := range containerIDs {
		ownerIDs = append(ownerIDs, id)
		ownerKind[id] = coremodel.GeometryOwnerContainer
	}
	sort.Strings(ownerIDs)

	for start := 0; start < len(ownerIDs); start += geometryBatchSize {
		end := min(start+geometryBatchSize, len(ownerIDs))
		batch := ownerIDs[start:end]
		if err := buildGeometryBatch(store, version, batch, ownerKind, sink); err != nil {
			return err
		}
		p.Tick(len(batch))
	}
	return nil
}

// geometryBatchSize bounds how many owners (Equipment/Container) are
// resolved together per batch — same rationale/order of magnitude as
// sachdatenBatchSize (sachdaten.go): keeps memory/IO bounded per batch
// regardless of how many owners the caller (or a single station worker)
// was assigned in total.
const geometryBatchSize = sachdatenBatchSize

// buildGeometryBatch resolves Geometry for one bounded batch of owner IDs
// via a targeted 3-hop indexed lookup (owner -> Location -> PositionPoint
// -> PositionPoint's own lat/lon/seq attributes), then flushes the result
// through sink — never touching any owner/Location/PositionPoint outside
// this batch.
func buildGeometryBatch(store staging.Store, version uint64, batch []string, ownerKind map[string]coremodel.GeometryOwnerKind, sink Sink) error {
	// Hop 1: each owner's own PowerSystemResource.Location reference (if
	// any) — owners without one simply have no Geometry.
	byOwner, err := getByIDsIndexed(store, version, batch)
	if err != nil {
		return fmt.Errorf("common: fetching geometry owner records: %w", err)
	}
	locationOf := make(map[string]string, len(batch)) // ownerID -> locationID
	var locationIDs []string
	for _, ownerID := range batch {
		for _, r := range byOwner[ownerID] {
			if r.Attribute == "PowerSystemResource.Location" && r.IsReference {
				locationOf[ownerID] = r.Value
				locationIDs = append(locationIDs, r.Value)
				break
			}
		}
	}
	if len(locationIDs) == 0 {
		return nil
	}

	// Hop 2: PositionPoints referencing any of this batch's Locations
	// (PositionPoint.Location -> locationID) — only candidates for THESE
	// Locations, never the whole PositionPoint class.
	refs, err := getReferencesToAnyIndexed(store, version, locationIDs)
	if err != nil {
		return fmt.Errorf("common: fetching PositionPoint references: %w", err)
	}
	ppCandidatesByLocation := map[string][]string{} // locationID -> []PositionPoint ID
	var ppIDs []string
	for _, locID := range locationIDs {
		for _, r := range refs[locID] {
			if r.Class == "PositionPoint" && r.Attribute == "PositionPoint.Location" {
				ppCandidatesByLocation[locID] = append(ppCandidatesByLocation[locID], r.ID)
				ppIDs = append(ppIDs, r.ID)
			}
		}
	}
	if len(ppIDs) == 0 {
		return nil
	}

	// Hop 3: only those candidate PositionPoints' own xPosition/yPosition/
	// sequenceNumber attributes.
	ppByID, err := getByIDsIndexed(store, version, ppIDs)
	if err != nil {
		return fmt.Errorf("common: fetching PositionPoint attributes: %w", err)
	}

	type point struct {
		seq      int
		lat, lon float64
	}
	bestForLocation := map[string]point{}
	for locID, candidates := range ppCandidatesByLocation {
		for _, ppID := range candidates {
			var seq int
			var lat, lon float64
			var haveLat, haveLon bool
			for _, r := range ppByID[ppID] {
				switch r.Attribute {
				case "PositionPoint.sequenceNumber":
					seq, _ = strconv.Atoi(r.Value)
				case "PositionPoint.xPosition":
					if v, convErr := strconv.ParseFloat(r.Value, 64); convErr == nil {
						lon, haveLon = v, true
					}
				case "PositionPoint.yPosition":
					if v, convErr := strconv.ParseFloat(r.Value, 64); convErr == nil {
						lat, haveLat = v, true
					}
				}
			}
			if !haveLat || !haveLon {
				continue
			}
			if cur, ok := bestForLocation[locID]; !ok || seq < cur.seq {
				bestForLocation[locID] = point{seq: seq, lat: lat, lon: lon}
			}
		}
	}

	var geoms []coremodel.Geometry
	for _, ownerID := range batch {
		locID, ok := locationOf[ownerID]
		if !ok {
			continue
		}
		pt, ok := bestForLocation[locID]
		if !ok {
			continue // Location without a resolvable PositionPoint
		}
		geoms = append(geoms, coremodel.Geometry{OwnerID: ownerID, OwnerKind: ownerKind[ownerID], Lat: pt.lat, Lon: pt.lon})
	}
	if len(geoms) > 0 {
		if err := sink.WriteGeometries(geoms); err != nil {
			return fmt.Errorf("common: writing geometry batch: %w", err)
		}
	}
	return nil
}
