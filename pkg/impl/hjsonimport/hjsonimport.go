// Package hjsonimport holds the reusable Pass A/B import pipeline that
// turns a directory tree of *.hjson files (produced by cmd/hjsonexport)
// into a fresh ModelStore SQLite file. It is the shared implementation
// behind cmd/hjsonimport (one-shot CLI) and cmd/hjsonwatch (file-watching
// daemon that re-runs the same pipeline whenever *.hjson files change).
package hjsonimport

import (
	"fmt"
	"os"
	"time"

	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/impl/common"
	"github.com/ame89/jag/pkg/importer/phase1"
	"github.com/ame89/jag/pkg/sqlite"
)

const persistChunkSize = 1000

// Options mirrors the JAG_* environment variables cmd/hjsonimport reads;
// zero values fall back to the same defaults RunPassA/RunPassB use.
type Options struct {
	ChunkSize      int
	BatchSize      int
	StationWorkers int
	PassBWorkers   int
	PassBBatchSize int
	// KeepStaging skips the automatic staging_records/staging_errors
	// cleanup that otherwise runs once the import completes without a
	// fatal error (see common.FinalizeImport's doc comment) — set this if
	// the resulting database will also be used with internal/jag2nsc's
	// Postgres-only NSC_SUPPORT feature, which reads staging_records
	// directly.
	KeepStaging bool
	// SkipVacuum skips the automatic VACUUM that otherwise runs by
	// default once the import completes without a fatal error (see
	// common.FinalizeImport's doc comment) — VACUUM reclaims freelist
	// pages left behind by DeleteVersion/Pass A/B's delete-then-insert
	// churn, but rewrites the entire database file.
	SkipVacuum bool
	// KeepExistingFile skips Run's default "os.Remove(dbPath) first"
	// behavior, so a pre-existing SQLite file at dbPath is opened and
	// built on top of (via the same Upsert* calls Run always makes)
	// instead of being discarded and rebuilt from scratch. Added for
	// callers (see jaggit's SPEC.md, "apply" command) that first remove
	// containers no longer present in the hjson source themselves — via
	// ModelStore.ListContainerIDs/DeleteContainers — and then want Run to
	// upsert the current source state on top of that already-cleaned
	// model, instead of Run's own unconditional full-file rebuild
	// clobbering that preparatory work. Existing callers (cmd/hjsonimport,
	// cmd/hjsonwatch) never set this, so their full-rebuild behavior is
	// completely unchanged.
	KeepExistingFile bool
	// OnFile, if set, is invoked with each Fachmodell file's path
	// immediately before phase1 parses it (see
	// pkg/importer/hjson.Emit's onFile parameter, which this is
	// forwarded to verbatim). Intended for verbose "which file is being
	// processed" reporting (e.g. jaggit's -verbose option); has no
	// effect on the import itself and is nil (no reporting) by default.
	OnFile func(path string)
}

// Summary is the aggregate result of one full import run, returned so
// callers (CLI, watcher) can print/log it however they like.
type Summary struct {
	Version                             int
	Containers, Equipment, Nodes, Edges int
	Attributes, Geometries              int
	Elapsed                             time.Duration
}

func chunkUpsert[T any](items []T, upsert func([]T) error) error {
	for i := 0; i < len(items); i += persistChunkSize {
		end := i + persistChunkSize
		if end > len(items) {
			end = len(items)
		}
		if err := upsert(items[i:end]); err != nil {
			return err
		}
	}
	return nil
}

// countingSink is common.Sink's minimal implementation: it persists
// straight into ModelStore and only keeps small running counts for the
// final summary, never a whole-model slice.
type countingSink struct {
	model     *sqlite.ModelStore
	attrCount int
	geomCount int
}

func (s *countingSink) WriteAttributes(batch []coremodel.Attribute) error {
	if err := s.model.UpsertAttributes(batch); err != nil {
		return fmt.Errorf("persisting attributes: %w", err)
	}
	s.attrCount += len(batch)
	return nil
}

func (s *countingSink) WriteGeometries(batch []coremodel.Geometry) error {
	if err := s.model.UpsertGeometry(batch); err != nil {
		return fmt.Errorf("persisting geometries: %w", err)
	}
	s.geomCount += len(batch)
	return nil
}

// Run parses the *.hjson tree under root and persists it into a SQLite
// file at dbPath. By default (opts.KeepExistingFile == false, matching
// every existing caller's behavior) any existing file at dbPath is
// deleted first, same as cmd/hjsonimport has always done — a full
// rebuild, not an incremental update. If opts.KeepExistingFile is set, an
// existing file is instead opened and built on top of via the same
// Upsert* calls (see Options.KeepExistingFile's doc comment for the
// intended use case). Progress lines are printed to stdout as the
// pipeline runs, matching cmd/hjsonimport's existing CLI output.
func Run(root, dbPath string, opts Options) (Summary, error) {
	var summary Summary

	if !opts.KeepExistingFile {
		os.Remove(dbPath)
	}

	overallStart := time.Now()
	store, err := sqlite.Open(dbPath)
	if err != nil {
		return summary, fmt.Errorf("opening store: %w", err)
	}
	defer store.Close()
	fmt.Printf("using sqlite file: %s\n", dbPath)
	modelStore := store.Model()
	flags := store.Flags()

	phase1Start := time.Now()
	result, err := phase1.RunHJSONFiles(store, root, opts.OnFile)
	if err != nil {
		return summary, fmt.Errorf("phase1: %w", err)
	}
	fmt.Printf("phase1: version=%d records=%d errors=%d (%s)\n", result.Version, result.RecordCount, len(result.Errors), time.Since(phase1Start))
	for _, e := range result.Errors {
		fmt.Printf("  parse error: %s: %s\n", e.SourceFile, e.Message)
	}
	if len(result.Errors) > 0 {
		return summary, fmt.Errorf("phase1 reported %d error(s), aborting before phase 2", len(result.Errors))
	}
	summary.Version = int(result.Version)

	sink := &countingSink{model: modelStore}
	var containerCount, equipmentCount, nodeCount, edgeCount int

	passAStart := time.Now()
	err = common.RunPassA(store, result.Version, opts.ChunkSize, opts.BatchSize, opts.StationWorkers, sink, flags, false, func(b *common.BatchResult) error {
		if err := chunkUpsert(b.Containers, modelStore.UpsertContainers); err != nil {
			return fmt.Errorf("persisting containers: %w", err)
		}
		if err := chunkUpsert(b.Equipment, modelStore.UpsertEquipment); err != nil {
			return fmt.Errorf("persisting equipment: %w", err)
		}
		if err := chunkUpsert(b.Nodes, modelStore.UpsertNodes); err != nil {
			return fmt.Errorf("persisting nodes: %w", err)
		}
		if err := chunkUpsert(b.Edges, modelStore.UpsertEdges); err != nil {
			return fmt.Errorf("persisting edges: %w", err)
		}
		owned := make(map[string]map[string]string, len(b.Groups))
		for owner, groups := range b.Groups {
			owned[owner] = groups
		}
		if err := modelStore.UpsertElectricalGroups(owned); err != nil {
			return fmt.Errorf("persisting electrical groups: %w", err)
		}
		containerCount += len(b.Containers)
		equipmentCount += len(b.Equipment)
		nodeCount += len(b.Nodes)
		edgeCount += len(b.Edges)
		for _, a := range b.Anomalies {
			fmt.Printf("  pass A anomaly: %s: %s\n", a.EquipmentID, a.Message)
		}
		for _, v := range b.Violations {
			fmt.Printf("  pass A violation [%s]: %s\n", v.Rule, v.Message)
		}
		return nil
	})
	if err != nil {
		return summary, fmt.Errorf("pass A: %w", err)
	}
	fmt.Printf("pass A: %d containers, %d equipment, %d nodes, %d edges (%s)\n", containerCount, equipmentCount, nodeCount, edgeCount, time.Since(passAStart))

	passBStart := time.Now()
	var aclineContainerCount, aclineEquipmentCount, aclineNodeCount, aclineEdgeCount int
	passB, err := common.RunPassB(store, result.Version, opts.ChunkSize, opts.PassBBatchSize, opts.PassBWorkers, sink, flags, func(b *common.PassBACLineBatchResult) error {
		if err := chunkUpsert(b.Containers, modelStore.UpsertContainers); err != nil {
			return fmt.Errorf("persisting pass B acline batch containers: %w", err)
		}
		if err := chunkUpsert(b.Equipment, modelStore.UpsertEquipment); err != nil {
			return fmt.Errorf("persisting pass B acline batch equipment: %w", err)
		}
		if err := chunkUpsert(b.Nodes, modelStore.UpsertNodes); err != nil {
			return fmt.Errorf("persisting pass B acline batch nodes: %w", err)
		}
		if err := chunkUpsert(b.Edges, modelStore.UpsertEdges); err != nil {
			return fmt.Errorf("persisting pass B acline batch edges: %w", err)
		}
		if err := chunkUpsert(b.Attributes, modelStore.UpsertAttributes); err != nil {
			return fmt.Errorf("persisting pass B acline batch attributes: %w", err)
		}
		if err := modelStore.UpsertElectricalGroups(map[string]map[string]string{b.OwnerID: b.Groups}); err != nil {
			return fmt.Errorf("persisting pass B acline batch electrical groups: %w", err)
		}
		aclineContainerCount += len(b.Containers)
		aclineEquipmentCount += len(b.Equipment)
		aclineNodeCount += len(b.Nodes)
		aclineEdgeCount += len(b.Edges)
		for _, a := range b.Anomalies {
			fmt.Printf("  pass B acline batch anomaly: %s: %s\n", a.EquipmentID, a.Message)
		}
		for _, v := range b.Violations {
			fmt.Printf("  pass B acline batch violation [%s]: %s\n", v.Rule, v.Message)
		}
		return nil
	})
	if err != nil {
		return summary, fmt.Errorf("pass B: %w", err)
	}
	if err := chunkUpsert(passB.Containers, modelStore.UpsertContainers); err != nil {
		return summary, fmt.Errorf("persisting pass B containers: %w", err)
	}
	if err := chunkUpsert(passB.Equipment, modelStore.UpsertEquipment); err != nil {
		return summary, fmt.Errorf("persisting pass B equipment: %w", err)
	}
	if err := chunkUpsert(passB.Nodes, modelStore.UpsertNodes); err != nil {
		return summary, fmt.Errorf("persisting pass B nodes: %w", err)
	}
	if err := chunkUpsert(passB.Edges, modelStore.UpsertEdges); err != nil {
		return summary, fmt.Errorf("persisting pass B edges: %w", err)
	}
	if err := chunkUpsert(passB.Attributes, modelStore.UpsertAttributes); err != nil {
		return summary, fmt.Errorf("persisting pass B attributes: %w", err)
	}
	if err := chunkUpsert(passB.LineRefs, modelStore.UpsertAttributes); err != nil {
		return summary, fmt.Errorf("persisting pass B line refs: %w", err)
	}
	passBOwned := make(map[string]map[string]string, len(passB.Groups))
	for owner, groups := range passB.Groups {
		passBOwned[owner] = groups
	}
	if err := modelStore.UpsertElectricalGroups(passBOwned); err != nil {
		return summary, fmt.Errorf("persisting pass B electrical groups: %w", err)
	}
	fmt.Printf("pass B: %d containers, %d equipment, %d nodes, %d edges (%s)\n",
		aclineContainerCount+len(passB.Containers), aclineEquipmentCount+len(passB.Equipment),
		aclineNodeCount+len(passB.Nodes), aclineEdgeCount+len(passB.Edges), time.Since(passBStart))
	for _, a := range passB.Anomalies {
		fmt.Printf("  pass B anomaly: %s: %s\n", a.EquipmentID, a.Message)
	}

	summary.Containers = containerCount + aclineContainerCount + len(passB.Containers)
	summary.Equipment = equipmentCount + aclineEquipmentCount + len(passB.Equipment)
	summary.Nodes = nodeCount + aclineNodeCount + len(passB.Nodes)
	summary.Edges = edgeCount + aclineEdgeCount + len(passB.Edges)
	summary.Attributes = sink.attrCount
	summary.Geometries = sink.geomCount
	summary.Elapsed = time.Since(overallStart)

	fmt.Printf("\nattributes: %d, geometries: %d\n", sink.attrCount, sink.geomCount)

	// Pass A/B completed without a fatal error: clean up this version's
	// ephemeral import-time bookkeeping (import_flag, and — unless
	// opts.KeepStaging — staging_records/staging_errors) the same way
	// cmd/phase2check does. See common.FinalizeImport's doc comment for
	// why this cleanup is opt-out rather than unconditional.
	if err := common.FinalizeImport(store, flags, result.Version, opts.KeepStaging, opts.SkipVacuum); err != nil {
		return summary, err
	}

	fmt.Printf("total: %s\n", summary.Elapsed)

	return summary, nil
}
