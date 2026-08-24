// Package metadata defines the storage abstraction for JAG's single,
// global Metadata record — a small, permanent-but-current-state-only
// record describing "the last successful import that produced the
// current overall model", not a history/log (Historisierung was dropped
// entirely, see Konzept.md: there is exactly one Metadata record per
// model, fully overwritten on every successful import, never a growing
// list of past versions).
package metadata

import "time"

// Metadata is the single global record. There is exactly one of these per
// model/database — never one per partial model/import source.
type Metadata struct {
	// Number is an independent, monotonically increasing counter,
	// allocated by Store.Record. It is deliberately NOT the same counter
	// as staging.Store.NextVersion (which allocates a fresh number on
	// every Phase 1 run regardless of outcome, purely as transient
	// import-time scratch bookkeeping — see staging/store.go). Number
	// only advances when Record is actually called, i.e. once per
	// successful import.
	Number uint64
	// Timestamp is when Record was called (UTC).
	Timestamp time.Time
	// Label is an optional, free-text, caller-supplied description of
	// the import that produced the current state (e.g. "reimport after
	// bugfix X"). If the caller passes an empty label to Record, it
	// defaults to "v"+Number (e.g. "v3") instead of staying empty — see
	// Store.Record's doc comment.
	Label string
}

// Store is the storage abstraction for the single global Metadata record.
// Backends (sqlite, postgres) each implement this on top of a single-row
// table, the same pattern as staging.Store.NextVersion's counter table.
type Store interface {
	// Get returns the current Metadata record. ok is false if no import
	// has ever called Record yet (e.g. a brand-new/empty database).
	Get() (m Metadata, ok bool, err error)

	// Record atomically allocates the next Number, sets Timestamp to the
	// current time (UTC), and overwrites the single global Metadata
	// record with (Number, Timestamp, label). If label is empty, it
	// defaults to "v"+Number (e.g. "v3") instead of staying empty.
	// Intended to be called exactly once per successful import, after
	// Phase 1-4 have all completed without a fatal error (see
	// common.FinalizeImport, called from the same point in
	// cmd/phase2check and pkg/impl/hjsonimport) — never mid-import, and
	// never after a failed/aborted one.
	Record(label string) (Metadata, error)

	// Set overwrites the single global Metadata record with the exact
	// given values, WITHOUT allocating a new Number (unlike Record). Used
	// only to restore a previously exported metadata.hjson (see
	// pkg/exporter/hjson.WriteMetadata) into a brand-new, not-yet-recorded
	// database (see pkg/impl/hjsonimport) — ordinary import completion
	// should call Record instead.
	Set(m Metadata) error
}
