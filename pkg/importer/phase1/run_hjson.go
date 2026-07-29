package phase1

import (
	"fmt"

	"github.com/ame89/jag/pkg/core/staging"
	"github.com/ame89/jag/pkg/importer/batch"
	"github.com/ame89/jag/pkg/importer/hjson"
)

// RunHJSONFiles parses every Fachmodell *.hjson file found under root using
// the current, authoritative HJSON Fachmodell dialect (see
// hjson.FindFiles/hjson.Emit; internal/exporter/hjson is its export
// counterpart). Structurally identical to the deprecated
// RunHJSONDeprecatedFiles: parse/semantic errors are recorded as
// model.StagingError and do NOT abort the run; only store infrastructure
// failures are fatal.
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
