// Package phase1 orchestrates the Phase 1 "Rohimport" for a set of source
// files (see Konzept.md, "Die Import-Pipeline"): allocate a fresh staging
// version, stream-parse each file into the staging store via a batched
// Writer, and collect per-file parse errors instead of aborting the whole
// run (see Idee.md "Implementierungshinweise": a phase runs to completion,
// all errors are gathered and reported, not just the first one).
//
// Currently CGMES-only — CIM/NSC parsers don't exist yet (see Impl.md,
// dialect-implementation order: CIM first, then NSC/CGMES). This package
// will grow a dialect parameter/dispatch once those exist; deliberately not
// abstracted prematurely.
package phase1

import (
	"errors"
	"fmt"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/batch"
	"gitlab.com/openk-nsc/jag/internal/importer/cgmes"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// Result summarizes one Phase 1 run.
type Result struct {
	Version     uint64
	RecordCount int
	Errors      []model.StagingError
}

// RunCGMESFiles parses each of the given CGMES files (profile auto-detected
// from the filename) and writes the resulting StagingRecords into store
// under a freshly allocated version (see staging.Store.NextVersion).
//
// A parse error on one file is recorded as a model.StagingError and does
// NOT abort the run — remaining files are still processed. Errors from the
// store itself (infrastructure failures, e.g. a broken DB connection) DO
// abort immediately, since there is no safe way to continue writing.
//
// Once Phase 1 completes without a fatal (store) error, any collected
// parse errors are persisted via store.InsertErrors. The caller decides
// what happens next: with zero errors, Phase 2 can proceed to consume this
// version's staging data; with errors, per Idee.md's Phase 4, the import is
// reported as failed and Phase 2 must not run for this version. Either way,
// once the import has fully finished (successfully processed by Phase 2, or
// abandoned after failure), the caller is expected to call
// store.DeleteVersion(result.Version) — staging is a transient scratch
// area, not permanent storage.
func RunCGMESFiles(store staging.Store, files []string) (Result, error) {
	version, err := store.NextVersion()
	if err != nil {
		return Result{}, fmt.Errorf("phase1: allocating version: %w", err)
	}

	w := &batch.Writer{Store: store, Version: version}
	var stagingErrs []model.StagingError
	recordCount := 0

	for _, path := range files {
		profile := cgmes.DetectProfile(path)

		err := cgmes.ParseFileSAXStream(path, profile, func(rec model.StagingRecord) error {
			recordCount++
			return w.Emit(rec)
		})
		if err == nil {
			continue
		}

		var writeErr *batch.WriteError
		if errors.As(err, &writeErr) {
			// Infrastructure failure — no safe way to keep going.
			return Result{Version: version, RecordCount: recordCount}, fmt.Errorf("phase1: fatal store error while parsing %s: %w", path, writeErr)
		}

		var parseErr *cgmes.ParseError
		var offset, line int64
		if errors.As(err, &parseErr) {
			offset = parseErr.Offset
			line = parseErr.Line
		}
		stagingErrs = append(stagingErrs, model.StagingError{
			Version:    version,
			SourceFile: path,
			Line:       line,
			ByteOffset: offset,
			Message:    err.Error(),
		})
		// Deliberately continue with the next file — a malformed file
		// must not abort the whole run (Idee.md Phase 4).
	}

	if err := w.Flush(); err != nil {
		return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: final flush: %w", err)
	}

	// Build read-side indexes once, now that all rows are in — cheaper
	// than maintaining them incrementally during the inserts above (see
	// staging.Store.EnsureIndexes), and must happen before Phase 2 reads
	// this version's data.
	if err := store.EnsureIndexes(); err != nil {
		return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: building indexes: %w", err)
	}

	if len(stagingErrs) > 0 {
		if err := store.InsertErrors(stagingErrs); err != nil {
			return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: inserting staging errors: %w", err)
		}
	}

	return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, nil
}
