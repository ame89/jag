// Package staging defines the storage abstraction for Phase 1 raw import
// records (see Konzept.md, "Die Import-Pipeline", Phase 1 "Rohimport"). No
// logic lives here — this is a pure storage interface; parsing (producing
// StagingRecord/StagingError values) lives in /internal/importer/*, and
// Phase 2 (resolving references, building the node-edge model) lives in
// /internal/impl.
package staging

import "gitlab.com/openk-nsc/jag/internal/importer/model"

// Store is the bulk-oriented storage abstraction for Phase 1 staging data.
// Only batch operations are exposed — Phase 1 never processes single
// records at a time (see Idee.md's bulk-operations mandate). Backends
// (sqlite, postgres) each implement this interface; the file backend
// (/internal/file) does not use a staging table at all — it writes parsed
// records directly as HJSON/JSON per source file (see Impl.md).
type Store interface {
	// NextVersion allocates and returns a fresh, monotonically increasing
	// version number for a new import run. Every import run gets its own
	// version so repeated imports never collide in the staging tables.
	NextVersion() (uint64, error)

	// InsertBatch bulk-inserts the given staging records. Callers are
	// expected to batch records themselves (e.g. every few thousand) so
	// memory use stays bounded independent of model size — this method
	// does not buffer or split large batches internally (though backends
	// are free to chunk internally for their own statement-size limits).
	InsertBatch(records []model.StagingRecord) error

	// EnsureIndexes (re-)creates the secondary indexes staging_records
	// reads depend on (GetByID/GetByClass/GetReferencesTo). Callers are
	// expected to call this once, after all InsertBatch calls for a run
	// have completed and before any Phase 2 reads happen — building an
	// index once over already-inserted rows is far cheaper than
	// maintaining it incrementally during bulk insert (see Konzept.md's
	// performance-goals section: reads stay fast, writes are not
	// penalized by read-side index maintenance). Safe/cheap to call
	// again on an already-indexed store (a no-op).
	EnsureIndexes() error

	// InsertErrors bulk-inserts Phase 1 parse errors. A parse error does
	// not abort the run (see Idee.md "Implementierungshinweise") — errors
	// are collected here and reported once the phase has run to
	// completion.
	InsertErrors(errs []model.StagingError) error

	// GetByID returns all staging records for one object ID within an
	// import version (across all profiles) — the primary Phase 2 read
	// access pattern (resolving one object's full raw data).
	GetByID(version uint64, id string) ([]model.StagingRecord, error)

	// ListClasses returns the distinct classes present in the given
	// import version — used by Phase 2 to iterate class-by-class (e.g.
	// scanning "Terminal" for reference resolution) instead of loading
	// the whole version into memory at once (see Konzept.md, Phase 2
	// resolution: chunked, class-scoped processing).
	ListClasses(version uint64) ([]string, error)

	// GetByClass returns a chunk of staging records for objects of the
	// given class within an import version, ordered by ID. It returns
	// records for at most limit distinct object IDs whose ID is greater
	// than afterID (cursor-based pagination — never OFFSET-based, so
	// performance doesn't degrade on later pages). All rows for each
	// included object ID are returned together — a batch never ends in
	// the middle of an object's attribute rows. Callers loop, passing the
	// last-seen object ID back in as afterID, until a call returns fewer
	// than limit distinct object IDs (including zero), signalling the
	// end of the class. Start with afterID = "".
	GetByClass(version uint64, class string, afterID string, limit int) ([]model.StagingRecord, error)

	// GetReferencesTo returns every staging record whose value is a
	// reference to targetID within the given import version — i.e. "who
	// points at this object". Backed by an index on the value column, so
	// this is a bounded, indexed lookup rather than a full-table scan.
	// This is what lets Phase 2's bidirectional Sachdaten/Anhängsel walk
	// (see internal/impl/common/sachdaten.go) resolve one Equipment at a
	// time against the store, instead of preloading a reverse-reference
	// index for the whole model into memory.
	GetReferencesTo(version uint64, targetID string) ([]model.StagingRecord, error)

	// GetByIDs is GetByID for many IDs at once (WHERE id IN (...)).
	// Returned records are in no particular order — callers needing a
	// specific order must sort the (small, already-filtered) result
	// themselves rather than relying on an ORDER BY here, which would
	// force backends to give up an otherwise-selective index (see
	// GetReferencesToAny's doc for the concrete regression this caused).
	GetByIDs(version uint64, ids []string) ([]model.StagingRecord, error)

	// GetReferencesToAny is GetReferencesTo for many target IDs at once
	// (WHERE value IN (...) AND is_reference = 1). Returned records are
	// in no particular order for the same reason as GetByIDs — measured
	// on a ~2.7M-row SQLite staging table, an ORDER BY here made the
	// query planner switch from an index seek on (version, value,
	// is_reference) to a full-table scan ordered by id, turning a ~5ms
	// query into several seconds per 500-ID chunk (minutes overall for
	// Phase 3's checkUnreferencedNodes). Sort in Go if a caller ever
	// needs deterministic order.
	GetReferencesToAny(version uint64, targetIDs []string) ([]model.StagingRecord, error)

	// CountByVersion returns the number of staging records currently
	// stored for the given import version (mainly for reporting/
	// diagnostics).
	CountByVersion(version uint64) (int, error)

	// CountErrorsByVersion returns the number of staging errors currently
	// stored for the given import version (mainly for reporting/
	// diagnostics).
	CountErrorsByVersion(version uint64) (int, error)

	// DeleteVersion removes all staging records and errors for the given
	// import version. Called once an import has fully completed (its
	// staging data has been consumed into the node-edge model in Phase 2)
	// — staging is a transient scratch area, not permanent storage.
	DeleteVersion(version uint64) error
}
