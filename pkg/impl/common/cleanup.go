package common

import (
	"fmt"

	"github.com/ame89/jag/pkg/core/staging"
)

// FinalizeImport clears an import version's ephemeral, import-time-only
// bookkeeping once Pass A/B (and, if run, CheckInvariantsFlagged) have
// finished without a fatal error: the import_flag rows (via
// flags.ClearFlags) and, unless keepStaging is true, the raw Phase 1
// staging_records/staging_errors rows (via store.DeleteVersion) — see
// staging.Store.DeleteVersion's doc comment: staging is "a transient
// scratch area, not permanent storage", meant to be deleted once its
// version has been fully consumed into the model_* tables.
//
// This is a single shared entry point (used by both cmd/phase2check and
// pkg/impl/hjsonimport, and available to any external caller driving the
// Pass A/B pipeline directly via this package) specifically so the
// cleanup happens automatically wherever the pipeline is used, instead of
// depending on each individual driver remembering to call ClearFlags/
// DeleteVersion itself.
//
// keepStaging exists because internal/jag2nsc's optional, Postgres-only
// NSC_SUPPORT feature (BuildTopology/BuildNetworkGroup/BuildCircuits)
// reads staging_records directly for raw CIM Terminal/SubGeographicalRegion/
// PowerTransformerEnd data that JAG's own model never retains (see
// internal/jag2nsc/topology_tables.sql's doc comment for the accepted
// limitation this causes once staging_records is gone). A caller whose
// database will also be used with jag2nsc's NSC_SUPPORT feature must pass
// keepStaging=true (e.g. driven by a JAG_KEEP_STAGING=1 environment
// variable) or BuildTopology/BuildCircuits will silently produce empty/
// incomplete results after the next import.
//
// skipVacuum controls whether store.Vacuum() also runs at the end (see
// staging.Store.Vacuum's doc comment): by default (skipVacuum=false) it
// does, since a measurement against the lasttest-200 fixture found ~76%
// of the on-disk SQLite file was unused freelist pages left behind by
// DeleteVersion and Pass A/B's delete-then-insert re-upsert patterns — a
// far bigger space-reclaiming lever than keeping staging around, so this
// runs unconditionally unless a caller explicitly opts out (e.g. driven
// by a JAG_SKIP_VACUUM=1 environment variable) — useful for very large
// databases where VACUUM's own runtime/temporary-disk-space cost (it
// rewrites the entire file) is undesirable for a given import.
//
// Callers should only call FinalizeImport once they consider the import
// itself successful — do not call it after a Phase 1 parse error or a
// Pass A/B error, so the raw staging data remains available for
// diagnosis.
func FinalizeImport(store staging.Store, flags FlagStore, version uint64, keepStaging bool, skipVacuum bool) error {
	if flags != nil {
		if err := flags.ClearFlags(version); err != nil {
			return fmt.Errorf("common: clearing import flags for version %d: %w", version, err)
		}
	}
	if !keepStaging {
		if err := store.DeleteVersion(version); err != nil {
			return fmt.Errorf("common: deleting staging version %d: %w", version, err)
		}
	}
	if !skipVacuum {
		if err := store.Vacuum(); err != nil {
			return fmt.Errorf("common: vacuuming database: %w", err)
		}
	}
	return nil
}
