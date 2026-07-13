// Package geometry defines the storage abstraction for 2D WGS 84 positions
// composed onto Equipment (see Konzept.md, "Geometrie"). No logic lives
// here — inheritance/fallback-to-container-coordinate at query time is
// business logic living in /internal/impl, not this package.
package geometry

import "gitlab.com/openk-nsc/jag/internal/core/model"

// Store is the bulk-oriented storage abstraction for Geometry. Backends
// (sqlite, postgres, file) each implement this interface.
type Store interface {
	// GetByIDs returns the current Geometry for each requested Equipment
	// ID. Equipment without a Geometry are simply absent from the result.
	GetByIDs(equipmentIDs []string) ([]model.Geometry, error)

	// Upsert bulk-inserts/updates the given geometries.
	Upsert(geometries []model.Geometry) error
}
