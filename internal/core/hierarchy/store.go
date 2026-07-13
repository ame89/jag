// Package hierarchy defines the storage abstraction for the Container tree
// (see Konzept.md, "Container / Hierarchie"). No logic lives here — this is
// a pure storage interface; validation of path templates and tree rules is
// business logic living in /internal/impl.
package hierarchy

import (
	"gitlab.com/openk-nsc/jag/internal/core/model"
)

// Store is the bulk-oriented storage abstraction for Container. Backends
// (sqlite, postgres, file) each implement this interface.
type Store interface {
	// GetByIDs returns the current version of each requested Container.
	GetByIDs(ids []string) ([]model.Container, error)

	// GetChildren returns the direct child containers of the given parent
	// container IDs (current version only).
	GetChildren(parentIDs []string) ([]model.Container, error)

	// GetDescendants returns the full recursive set of descendant
	// containers for the given root IDs (current version only). Backends
	// implement this DB-side (e.g. via WITH RECURSIVE), not via
	// hop-by-hop Go-side recursion.
	GetDescendants(rootIDs []string) ([]model.Container, error)

	// Upsert bulk-inserts/updates the given containers. Historisation was
	// dropped entirely — Upsert overwrites an existing Container
	// directly, there is no versioned history to preserve.
	Upsert(containers []model.Container) error
}
