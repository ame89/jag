// Package common — Sink lets BuildAttributes/BuildGeometry (and
// BuildSachdatenAndGeometryParallel's station workers) hand off each
// already-bounded internal batch as it is produced, instead of
// accumulating the whole model's result into one big slice and returning
// it wholesale. See Idee.md's hard resource target: builds must not scale
// with model size — a caller that still wants "everything in one slice"
// (e.g. a small diagnostic run) can implement a Sink that appends to a
// slice itself, but the production path is expected to flush/persist each
// batch and drop it, keeping RAM bounded to batch size regardless of total
// model size.
package common

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Sink receives Attribute/Geometry rows in bounded batches. Implementations
// MUST be safe for concurrent calls from multiple goroutines —
// BuildSachdatenAndGeometryParallel invokes the same Sink from every
// station worker concurrently (e.g. guard shared state with a mutex, or
// write straight through to a store whose driver already serializes
// writes).
type Sink interface {
	WriteAttributes(batch []coremodel.Attribute) error
	WriteGeometries(batch []coremodel.Geometry) error
}
