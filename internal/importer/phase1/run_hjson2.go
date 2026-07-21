package phase1

import (
	"fmt"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/batch"
	hjson2 "gitlab.com/openk-nsc/jag/internal/importer/hjson2"
)

// RunHJSON2Files is RunHJSONFiles' counterpart for the newer, more compact
// hjson2 "Fachmodell" dialect (internal/exporter/hjson2's export
// counterpart, internal/importer/hjson2's parse/resolve side) — added
// 2026-07-21 to close the gap that hjson2 previously had no full Phase 1
// entrypoint of its own (only internal/exporter/hjson2/roundtrip_test.go
// called importhjson2.Emit directly, without wiring it through
// batch.Writer into a staging.Store). Structurally identical to
// RunHJSONFiles: parse/semantic errors are recorded as model.StagingError
// and do NOT abort the run; only store infrastructure failures are fatal.
func RunHJSON2Files(store staging.Store, root string) (Result, error) {
	version, err := store.NextVersion()
	if err != nil {
		return Result{}, fmt.Errorf("phase1: allocating version: %w", err)
	}

	records, stagingErrs, err := hjson2.Emit(version, root)
	if err != nil {
		return Result{Version: version}, fmt.Errorf("phase1: hjson2 emit: %w", err)
	}

	w := &batch.Writer{Store: store, Version: version}
	p := newProgress("phase1-import-hjson2")
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
