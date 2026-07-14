// Package hierarchy defines the storage abstraction for the Container tree
// (see Konzept.md, "Container / Hierarchie"). Pure interface + data only —
// no domain/business logic lives here (see Impl.md, Ports & Adapters). Path
// template validation, the closed ContainerType enum, and depth rules are
// business logic and live in /internal/impl, not here.
package hierarchy

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Store persists coremodel.Container values. Historisation was dropped
// entirely (see Konzept.md) — Upsert overwrites an existing container with
// the same ID directly, there is no valid_from/version tracking.
type Store interface {
	// GetByIDs returns containers for the given IDs. IDs with no matching
	// container are silently omitted from the result (not an error).
	GetByIDs(ids []string) ([]coremodel.Container, error)
	// GetChildren returns the direct (one level down) children of the
	// given parent container IDs.
	GetChildren(parentIDs []string) ([]coremodel.Container, error)
	// GetDescendants returns the full recursive descendant set of the
	// given root container IDs (all levels down), computed DB-side (e.g.
	// via a recursive CTE) — not via Go-side recursive traversal (see
	// Idee.md's "avoid recursion in Go application code" rule).
	GetDescendants(rootIDs []string) ([]coremodel.Container, error)
	// Upsert inserts or replaces containers in bulk, consistent with the
	// bulk-oriented API convention used elsewhere (see Impl.md).
	Upsert(containers []coremodel.Container) error
	// CountByType returns, for every distinct Container type currently
	// persisted, how many containers exist of that type — computed
	// DB-side (a single GROUP BY query), for aggregation/reporting
	// usecases (see Usecases.md UC12) without loading every Container
	// into memory first.
	CountByType() (map[string]int, error)
}

// EquipmentStore persists coremodel.Equipment values — specifically, each
// Equipment's own ContainerID assignment (its membership in the Container
// tree). Kept in this package (not a separate one) because "which
// container does this equipment belong to" is fundamentally a hierarchy
// concern, even though Equipment itself is a distinct core.model type from
// Container.
type EquipmentStore interface {
	// GetByIDs returns Equipment entries for the given IDs. IDs with no
	// matching Equipment are silently omitted from the result (not an
	// error).
	GetByIDs(ids []string) ([]coremodel.Equipment, error)
	// GetByContainerIDs returns every Equipment directly assigned to any of
	// the given Container IDs (does not recurse into child containers —
	// combine with Store.GetDescendants first if the full subtree is
	// wanted, see UC1 "ONS-Aufbau" in Usecases.md).
	GetByContainerIDs(containerIDs []string) ([]coremodel.Equipment, error)
	// Upsert inserts or replaces Equipment entries in bulk.
	Upsert(equipment []coremodel.Equipment) error
}
