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
	"strconv"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
)

// BuildGeometry resolves a Geometry for every Equipment/Container owner
// that carries a PowerSystemResource.Location reference:
//
//   - Location -> PositionPoint (1:n, sequenceNumber-ordered) is the CIM
//     GL shape. Per Konzept.md, JAG's Geometry is a single 2D point, not a
//     path — for owners with several PositionPoints (e.g. a Line's route),
//     only the lowest sequenceNumber ("the first point") is kept as a
//     simplification; the full path isn't modeled. This is a deliberate,
//     documented simplification (not a full GIS replacement), consistent
//     with Konzept.md's Geometrie section.
//   - equipmentIDs/containerIDs are the sets of already-resolved
//     Equipment/Container IDs (from ResolveTerminals/BuildContainers) —
//     only objects found there get a Geometry; anything else pointing at
//     a Location is ignored (it isn't part of the node-edge/container model
//     anyway).
//   - Location.CoordinateSystem is deliberately NOT read/checked — JAG
//     currently assumes every PositionPoint's xPosition/yPosition is
//     already WGS84 lon/lat (explicit decision), not a projected CRS. If a
//     future dataset uses a non-WGS84 CoordinateSystem, this will silently
//     misinterpret its coordinates; revisit if/when that's encountered.
func BuildGeometry(store staging.Store, version uint64, chunkSize int, equipmentIDs, containerIDs map[string]bool) ([]coremodel.Geometry, error) {
	_, ppIdx, err := scanClass(store, version, chunkSize, "PositionPoint")
	if err != nil {
		return nil, err
	}

	// Location -> best (lowest sequenceNumber) PositionPoint's lat/lon.
	type point struct {
		seq      int
		lat, lon float64
	}
	best := map[string]point{}
	for _, ppID := range ppIdx.IDsOfClass("PositionPoint") {
		loc := ppIdx.Ref(ppID, "PositionPoint.Location")
		if loc == "" {
			continue
		}
		seq, _ := strconv.Atoi(ppIdx.Attr(ppID, "PositionPoint.sequenceNumber"))
		lon, errLon := strconv.ParseFloat(ppIdx.Attr(ppID, "PositionPoint.xPosition"), 64)
		lat, errLat := strconv.ParseFloat(ppIdx.Attr(ppID, "PositionPoint.yPosition"), 64)
		if errLon != nil || errLat != nil {
			continue
		}
		if cur, ok := best[loc]; !ok || seq < cur.seq {
			best[loc] = point{seq: seq, lat: lat, lon: lon}
		}
	}

	// Every class may carry PowerSystemResource.Location; scan generically
	// across all classes (like the Bay/VoltageLevel fallback in
	// BuildContainers) rather than hardcoding a class list, since both
	// Equipment subclasses and Container-backing classes (Substation,
	// Line, ...) can be Location owners.
	classes, err := store.ListClasses(version)
	if err != nil {
		return nil, err
	}
	var geometries []coremodel.Geometry
	for _, class := range classes {
		if class == "PositionPoint" || class == "Location" || class == "CoordinateSystem" {
			continue
		}
		afterID := ""
		for {
			records, err := store.GetByClass(version, class, afterID, chunkSize)
			if err != nil {
				return nil, err
			}
			if len(records) == 0 {
				break
			}
			idx := BuildObjectIndex(records)
			ids := distinctIDsInOrder(records)
			for _, id := range ids {
				loc := idx.Ref(id, "PowerSystemResource.Location")
				if loc == "" {
					continue
				}
				pt, ok := best[loc]
				if !ok {
					continue // Location without a resolvable PositionPoint
				}
				switch {
				case equipmentIDs[id]:
					geometries = append(geometries, coremodel.Geometry{OwnerID: id, OwnerKind: coremodel.GeometryOwnerEquipment, Lat: pt.lat, Lon: pt.lon})
				case containerIDs[id]:
					geometries = append(geometries, coremodel.Geometry{OwnerID: id, OwnerKind: coremodel.GeometryOwnerContainer, Lat: pt.lat, Lon: pt.lon})
				}
			}
			afterID = ids[len(ids)-1]
			if len(ids) < chunkSize {
				break
			}
		}
	}
	return geometries, nil
}
