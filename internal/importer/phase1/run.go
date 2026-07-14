// Package phase1 orchestrates the Phase 1 "Rohimport" for a set of source
// files (see Konzept.md, "Die Import-Pipeline"): allocate a fresh staging
// version, stream-parse each file into the staging store via a batched
// Writer, and collect per-file parse errors instead of aborting the whole
// run (see Idee.md "Implementierungshinweise": a phase runs to completion,
// all errors are gathered and reported, not just the first one).
//
// Currently supports CGMES (RunCGMESFiles) and NSC (RunNSCFiles) — CIM
// XML (non-CGMES, non-NSC) doesn't have a parser yet (see Impl.md).
package phase1

import (
	"errors"
	"fmt"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/batch"
	"gitlab.com/openk-nsc/jag/internal/importer/cgmes"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
	"gitlab.com/openk-nsc/jag/internal/importer/nsc"
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
	p := newProgress("phase1-import-cgmes")

	for _, path := range files {
		profile := cgmes.DetectProfile(path)

		err := cgmes.ParseFileSAXStream(path, profile, func(rec model.StagingRecord) error {
			recordCount++
			p.Tick(1)
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
	p.Done()

	if err := w.Flush(); err != nil {
		return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: final flush: %w", err)
	}

	// Build read-side indexes once, now that all rows are in — cheaper
	// than maintaining them incrementally during the inserts above (see
	// staging.Store.EnsureIndexes), and must happen before Phase 2 reads
	// this version's data. Wrapped in its own progress phase: previously
	// this ran silently between "phase1-import-cgmes" done and the next
	// phase's "started" log, making a real (observed: tens of seconds on
	// a multi-GB staging table) chunk of wall-clock time invisible.
	pIdx := newProgress("phase1-build-indexes")
	if err := store.EnsureIndexes(); err != nil {
		pIdx.Done()
		return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: building indexes: %w", err)
	}
	pIdx.Done()

	if len(stagingErrs) > 0 {
		if err := store.InsertErrors(stagingErrs); err != nil {
			return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: inserting staging errors: %w", err)
		}
	}

	return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, nil
}

// RunNSCFiles parses each of the given NSC-dialect files and writes the
// resulting StagingRecords into store under a freshly allocated version.
// The raw parsing itself reuses the exact same dialect-neutral RDF/XML
// parser as RunCGMESFiles (cgmes.ParseFileSAXStream — see that package's
// doc comment: it has no CGMES-specific structural logic at all).
// nsc.StreamFile wraps that parser with two bounded-memory passes per file
// to apply NSC's dialect-specific fixes (0-based Terminal sequence
// numbers, multi-Terminal BusbarSection splitting) — see that package's
// doc comment for why a per-file, two-pass streaming design is used
// instead of buffering a file's records in memory (an actual ~1GB NSC
// load-test file made that balloon to several GB of RAM).
//
// Error handling mirrors RunCGMESFiles for parse errors (recorded, does
// not abort the run) and store errors (fatal, aborts immediately). One
// additional check is fatal too and NOT merely recorded: NSC files are
// independent, standalone scenarios (see the package's example data doc in
// spec/Idee.md — the ~20 ".rdf" scenario files each show a self-contained
// House-connection variant, not fragments of one shared model) and, unlike
// CGMES's profile split, have no model-metadata header to say whether two
// files are ever meant to share an object ID (see Konzept.md's "NSC
// dialect has no model-metadata header" decision). So if the same object
// ID appears in more than one file passed to a single RunNSCFiles call,
// that is treated as an error (nsc.DuplicateIDError) and the whole run
// aborts immediately — not a per-file parse error, since guessing which
// file "wins" or silently merging would corrupt both scenarios (this is
// exactly the failure mode that produced a >1,000,000-row Sachdaten
// blow-up when 21 unrelated NSC files sharing base IDs were combined into
// one version during testing).
func RunNSCFiles(store staging.Store, files []string) (Result, error) {
	version, err := store.NextVersion()
	if err != nil {
		return Result{}, fmt.Errorf("phase1: allocating version: %w", err)
	}

	w := &batch.Writer{Store: store, Version: version}
	var stagingErrs []model.StagingError
	recordCount := 0
	idSourceFile := map[string]string{} // object ID -> file it was first seen in, shared across all files in this run
	p := newProgress("phase1-import-nsc")

	for _, path := range files {
		var dupErr *nsc.DuplicateIDError
		err := nsc.StreamFile(path, idSourceFile, func(rec model.StagingRecord) error {
			recordCount++
			p.Tick(1)
			return w.Emit(rec)
		})
		if err == nil {
			continue
		}
		if errors.As(err, &dupErr) {
			return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: %w", dupErr)
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
	p.Done()

	if err := w.Flush(); err != nil {
		return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: final flush: %w", err)
	}

	pIdx := newProgress("phase1-build-indexes")
	if err := store.EnsureIndexes(); err != nil {
		pIdx.Done()
		return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: building indexes: %w", err)
	}
	pIdx.Done()

	if len(stagingErrs) > 0 {
		if err := store.InsertErrors(stagingErrs); err != nil {
			return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, fmt.Errorf("phase1: inserting staging errors: %w", err)
		}
	}

	return Result{Version: version, RecordCount: recordCount, Errors: stagingErrs}, nil
}
