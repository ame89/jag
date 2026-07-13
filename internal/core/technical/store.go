// Package technical defines the storage abstraction for Sachdaten
// (key-value attributes) and ParameterCatalog entries (see Konzept.md,
// "Sachdaten"). ParameterCatalog reuses the same Attribute mechanism/global
// enum, per the ParameterCatalog-Versionierung decision, so both live in one
// interface. No logic lives here — key metadata (type, single/multi-value)
// and per-kind schema mapping are business logic living in /internal/impl.
package technical

import (
	"gitlab.com/openk-nsc/jag/internal/core/model"
)

// Store is the bulk-oriented storage abstraction for Attribute and
// CatalogEntry. Backends (sqlite, postgres, file) each implement
// this interface.
type Store interface {
	// GetByOwnerIDs returns all current Attribute rows for the given
	// owner IDs (usually EquipmentIDs). Multi-value keys are represented
	// as multiple rows sharing the same OwnerID+Key.
	GetByOwnerIDs(ownerIDs []string) ([]model.Attribute, error)

	// Upsert bulk-inserts/updates the given attributes.
	Upsert(attributes []model.Attribute) error

	// GetCatalogEntryByIDs returns each requested CatalogEntry.
	GetCatalogEntryByIDs(ids []string) ([]model.CatalogEntry, error)

	// UpsertCatalogEntry bulk-inserts/updates the given catalog entries.
	// Historisation was dropped entirely — this overwrites an existing
	// entry directly, there is no versioned history to preserve.
	UpsertCatalogEntry(entries []model.CatalogEntry) error
}
