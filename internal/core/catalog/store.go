// Package catalog defines the storage abstraction for ParameterCatalog
// entries (see Konzept.md's Sachdaten section). Pure interface + data only —
// no domain/business logic lives here (see Impl.md, Ports & Adapters).
package catalog

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Store persists coremodel.CatalogEntry values. Catalog entries are not
// versioned (see coremodel.CatalogEntry's doc comment) — Upsert overwrites
// an existing entry with the same ID directly.
type Store interface {
	// Upsert inserts or replaces catalog entries in bulk, consistent with
	// the bulk-oriented API convention used elsewhere (see Impl.md).
	Upsert(entries []coremodel.CatalogEntry) error
	// GetByIDs returns catalog entries for the given IDs. IDs with no
	// matching entry are silently omitted from the result (not an error).
	GetByIDs(ids []string) ([]coremodel.CatalogEntry, error)
	// GetAll returns every catalog entry currently stored. Intended for
	// small/medium catalogs (hundreds to low thousands of entries) — not
	// bounded/paginated, unlike the staging.Store class-scan methods.
	GetAll() ([]coremodel.CatalogEntry, error)
}
