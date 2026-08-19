package phase1

import (
	"fmt"

	"github.com/ame89/jag/pkg/core/staging"
	"github.com/ame89/jag/pkg/importer/batch"
	hjsondeprecated "github.com/ame89/jag/internal/importer/hjson-deprecated"
)

// RunHJSONDeprecatedFiles parses every Fachmodell *.hjson file found under
// root using the deprecated v1 HJSON Fachmodell dialect (see
// hjsondeprecated.FindFiles/hjsondeprecated.Emit) and writes the resulting
// StagingRecords into store under a freshly allocated version. Deprecated:
// hjson2 (internal/exporter/hjson, pkg/importer/hjson,
// RunHJSONFiles) is the current, authoritative HJSON Fachmodell dialect;
// this v1 entrypoint is kept only for backward compatibility, not actively
// developed further. Unlike RunCGMESFiles/RunNSCFiles (one file -> one
// streaming parse call each), the Fachmodell dialect's ID-prefixing scheme
// needs to see every file's top-level container ID up front (see
// hjsondeprecated.Emit's two-pass resolution), so parsing isn't truly
// streaming per file — acceptable for this dialect, since it is
// explicitly the small, hand-authorable format (one file per
// Substation/KVS/ACLine/House), not the multi-GB CIM XML case streaming
// was built for.
//
// Error handling mirrors RunCGMESFiles: parse/semantic errors (duplicate
// container IDs, malformed connects, unknown directory names, ...) are
// recorded as model.StagingError and do NOT abort the run; only store
// infrastructure failures are fatal.
func RunHJSONDeprecatedFiles(store staging.Store, root string) (Result, error) {
	version, err := store.NextVersion()
	if err != nil {
		return Result{}, fmt.Errorf("phase1: allocating version: %w", err)
	}

	records, stagingErrs, err := hjsondeprecated.Emit(version, root)
	if err != nil {
		return Result{Version: version}, fmt.Errorf("phase1: hjson emit: %w", err)
	}

	w := &batch.Writer{Store: store, Version: version}
	p := newProgress("phase1-import-hjson-deprecated")
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
