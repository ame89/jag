package phase1

import (
	"fmt"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/batch"
	"gitlab.com/openk-nsc/jag/internal/importer/hjson"
)

// RunHJSONFiles parses every Fachmodell *.hjson file found under root (see
// hjson.FindFiles/hjson.Emit) and writes the resulting StagingRecords into
// store under a freshly allocated version. Unlike RunCGMESFiles/
// RunNSCFiles (one file -> one streaming parse call each), the Fachmodell
// dialect's ID-prefixing scheme needs to see every file's top-level
// container ID up front (see hjson.Emit's two-pass resolution), so parsing
// isn't truly streaming per file — acceptable for this dialect, since it
// is explicitly the small, hand-authorable format (one file per
// Substation/KVS/ACLine/House), not the multi-GB CIM XML case streaming
// was built for.
//
// Error handling mirrors RunCGMESFiles: parse/semantic errors (duplicate
// container IDs, malformed connects, unknown directory names, ...) are
// recorded as model.StagingError and do NOT abort the run; only store
// infrastructure failures are fatal.
func RunHJSONFiles(store staging.Store, root string) (Result, error) {
	version, err := store.NextVersion()
	if err != nil {
		return Result{}, fmt.Errorf("phase1: allocating version: %w", err)
	}

	records, stagingErrs, err := hjson.Emit(version, root)
	if err != nil {
		return Result{Version: version}, fmt.Errorf("phase1: hjson emit: %w", err)
	}

	w := &batch.Writer{Store: store, Version: version}
	p := newProgress("phase1-import-hjson")
	for _, rec := range records {
		p.Tick(1)
		if err := w.Emit(rec); err != nil {
			return Result{Version: version, RecordCount: len(records), Errors: stagingErrs}, fmt.Errorf("phase1: fatal store error: %w", err)
		}
	}
	p.Done()

	if err := w.Flush(); err != nil {
		return Result{Version: version, RecordCount: len(records), Errors: stagingErrs}, fmt.Errorf("phase1: final flush: %w", err)
	}

	pIdx := newProgress("phase1-build-indexes")
	if err := store.EnsureIndexes(); err != nil {
		pIdx.Done()
		return Result{Version: version, RecordCount: len(records), Errors: stagingErrs}, fmt.Errorf("phase1: building indexes: %w", err)
	}
	pIdx.Done()

	if len(stagingErrs) > 0 {
		if err := store.InsertErrors(stagingErrs); err != nil {
			return Result{Version: version, RecordCount: len(records), Errors: stagingErrs}, fmt.Errorf("phase1: inserting staging errors: %w", err)
		}
	}

	return Result{Version: version, RecordCount: len(records), Errors: stagingErrs}, nil
}
