// Package technical defines the storage abstraction for Sachdaten
// (instance-specific attribute key-value data, see Konzept.md, "Sachdaten").
// Pure interface + data only — no domain/business logic lives here (see
// Impl.md, Ports & Adapters). ParameterCatalog entries use a separate
// storage interface (see /internal/core/catalog) despite sharing the same
// underlying Attribute mechanism/global key enum (see Konzept.md's
// explicit "kein eigenes Storage-Interface für ParameterCatalog"
// decision — catalog entries are addressed by their own entry ID, not an
// OwnerID from the node-edge model, which is why they still get their own
// interface here).
package technical

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Store persists coremodel.Attribute values (Sachdaten), keyed by OwnerID
// (usually an EquipmentID, but also used for Container-owned Sachdaten).
// Multi-value keys are represented as multiple Attribute rows sharing the
// same OwnerID+Key (see coremodel.Attribute's doc comment) — Upsert must
// preserve that (not silently collapse multiple values into one row).
type Store interface {
	// GetByOwnerIDs returns every Attribute for the given owner IDs. Owners
	// with no Attributes are silently omitted from the result (not an
	// error).
	GetByOwnerIDs(ownerIDs []string) ([]coremodel.Attribute, error)
	// Upsert inserts or replaces Attributes in bulk.
	Upsert(attributes []coremodel.Attribute) error
}
