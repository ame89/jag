// Package batch provides a bounded-memory bridge between a streaming
// Phase 1 parser (which emits one model.StagingRecord at a time) and a
// bulk-oriented staging.Store. It exists so parsers never have to build a
// full in-memory slice of a model's records — memory use is governed
// solely by the configured batch size, independent of model size (see
// Idee.md's streaming-import / RAM-bound requirement).
package batch

import (
	"fmt"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// DefaultSize is the default number of records buffered before a flush to
// the store. Chosen as a pragmatic middle ground (a few thousand records is
// a small, bounded amount of RAM but still amortizes per-transaction
// overhead well) — revisit with real benchmarks once available.
const DefaultSize = 2000

// Writer buffers StagingRecords in memory up to Size records, then flushes
// them to Store in one bulk InsertBatch call. It also stamps every record
// with Version, so parsers themselves stay run-agnostic (see
// model.StagingRecord).
type Writer struct {
	Store   staging.Store
	Version uint64
	Size    int // 0 means DefaultSize

	buf []model.StagingRecord
}

// Emit adds one record to the buffer, stamping it with w.Version, and
// flushes automatically once the buffer reaches its configured size. Emit
// has the signature required by cgmes.ParseSAXStream's emit callback.
func (w *Writer) Emit(r model.StagingRecord) error {
	r.Version = w.Version
	w.buf = append(w.buf, r)

	if len(w.buf) >= w.size() {
		return w.Flush()
	}
	return nil
}

// Flush writes any buffered records to the store and clears the buffer.
// Safe to call when the buffer is empty (no-op). Callers MUST call Flush
// after the last Emit to persist a final partial batch.
//
// Any error returned here is a *WriteError — an infrastructure/store
// failure, as opposed to a parser-level error. Orchestrators running
// multiple files (see internal/importer/phase1) treat WriteError as fatal
// (abort the whole run immediately), since there is no safe way to keep
// writing once the store itself is failing.
func (w *Writer) Flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	if err := w.Store.InsertBatch(w.buf); err != nil {
		return &WriteError{err: err}
	}
	// Reset length but keep the underlying array's capacity to avoid
	// reallocating every batch.
	w.buf = w.buf[:0]
	return nil
}

func (w *Writer) size() int {
	if w.Size <= 0 {
		return DefaultSize
	}
	return w.Size
}

// WriteError wraps a failure from the underlying Store. See Writer.Flush.
type WriteError struct {
	err error
}

func (e *WriteError) Error() string { return fmt.Sprintf("batch: store write failed: %v", e.err) }
func (e *WriteError) Unwrap() error { return e.err }
