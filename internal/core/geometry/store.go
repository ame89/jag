// Package geometry defines the storage abstraction for Geometry (2D WGS84
// positions composed onto an Equipment or a Container, see Konzept.md,
// "Geometrie"). Pure interface + data only — no domain/business logic lives
// here (see Impl.md, Ports & Adapters).
package geometry

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Store persists coremodel.Geometry values, at most one per owner
// (OwnerID+OwnerKind). There is no inherited/derived Geometry stored here —
// a missing entry simply means "not separately located"; any Container-tree
// fallback lookup (nearest ancestor with a Geometry) is pure query logic on
// top of this Store, not part of the stored data itself.
type Store interface {
	// GetByIDs returns Geometry entries for the given owner IDs
	// (Equipment or Container IDs). Owners with no Geometry are silently
	// omitted from the result (not an error).
	GetByIDs(ownerIDs []string) ([]coremodel.Geometry, error)
	// InBoundingBox returns every Geometry entry whose Lat/Lon falls
	// within the given WGS84 bounding box (minLat<=lat<=maxLat,
	// minLon<=lon<=maxLon) — a spatial range query for region-based
	// usecases (see Usecases.md UC3), computed DB-side rather than
	// filtering a full in-memory scan.
	InBoundingBox(minLat, minLon, maxLat, maxLon float64) ([]coremodel.Geometry, error)
	// Upsert inserts or replaces Geometry entries in bulk.
	Upsert(geometries []coremodel.Geometry) error
}
