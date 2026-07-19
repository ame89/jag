package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"sync"
	"time"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/postgres"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// modelWriter is the subset of *sqlite.ModelStore's/*postgres.ModelStore's
// method set actually used by this driver. Both backends' ModelStore types
// implement every one of these methods with identical signatures (all
// parameters/return values are coremodel/plain Go types, never a
// backend-specific concrete type), so either one satisfies this interface
// without an adapter — this is what lets main() below pick the backend at
// runtime while the rest of the file stays backend-agnostic.
type modelWriter interface {
	UpsertContainers(containers []coremodel.Container) error
	UpsertEquipment(equipment []coremodel.Equipment) error
	UpsertNodes(nodes []coremodel.Node) error
	UpsertEdges(edges []coremodel.Edge) error
	UpsertElectricalGroups(owned map[string]map[string]string) error
	UpsertAttributes(attributes []coremodel.Attribute) error
	UpsertGeometry(geometries []coremodel.Geometry) error
	// PersistBatch writes Containers/Equipment/Nodes/Edges/Attributes/
	// Geometries/Groups for ONE Pass A/B batch inside a single
	// transaction, instead of one transaction per entity type. See
	// internal/postgres/model.go's PersistBatch doc comment for the
	// PostgreSQL-specific commit/fsync-count rationale that made this
	// necessary (internal/sqlite's implementation is a thin sequential
	// wrapper around its existing per-entity Upsert* methods, since
	// SQLite's in-process commits don't pay that cost).
	PersistBatch(
		containers []coremodel.Container,
		equipment []coremodel.Equipment,
		nodes []coremodel.Node,
		edges []coremodel.Edge,
		attributes []coremodel.Attribute,
		geometries []coremodel.Geometry,
		groups map[string]map[string]string,
	) error
	GroupSizes() (map[string]int, error)
}

// storeCloser is the small extra bit staging.Store itself doesn't require
// (see internal/core/staging/store.go's doc comment: Close is a resource
// lifecycle concern, not a staging-data operation) but both
// *sqlite.StagingStore and *postgres.StagingStore provide.
type storeCloser interface {
	staging.Store
	Close() error
}

// reportSink is phase2check's common.Sink implementation: it only needs
// counts/a small sample for reporting, so (unlike the old code, which held
// every Attribute/Geometry of the whole model in RAM) it never
// accumulates more than one candidate "sample equipment"'s attributes —
// see BuildSachdatenAndGeometryParallel's 2026-07-14 fix doc comment. Safe
// for concurrent use: every station worker calls WriteAttributes/
// WriteGeometries concurrently.
type reportSink struct {
	mu          sync.Mutex
	totalAttrs  int
	totalGeoms  int
	byOwner     map[string]int
	sampleOwner string
	sampleAttrs []coremodel.Attribute
}

func newReportSink() *reportSink {
	return &reportSink{byOwner: map[string]int{}}
}

func (s *reportSink) WriteAttributes(batch []coremodel.Attribute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalAttrs += len(batch)
	// Each batch contains complete per-owner attribute sets (BuildAttributes
	// never splits one owner's attributes across two batches), so it's safe
	// to decide "is this a good sample owner" using only this batch.
	perOwnerThisBatch := map[string][]coremodel.Attribute{}
	for _, a := range batch {
		s.byOwner[a.OwnerID]++
		if s.sampleOwner == "" {
			perOwnerThisBatch[a.OwnerID] = append(perOwnerThisBatch[a.OwnerID], a)
		}
	}
	if s.sampleOwner == "" {
		for owner, as := range perOwnerThisBatch {
			if s.byOwner[owner] > 15 { // machines with many attached satellites stand out
				s.sampleOwner = owner
				s.sampleAttrs = as
				break
			}
		}
	}
	return nil
}

func (s *reportSink) WriteGeometries(batch []coremodel.Geometry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalGeoms += len(batch)
	return nil
}

// persistSink wraps a *reportSink (for the existing counters/sample used
// in the report below) and additionally persists every batch through
// ModelStore into the model_* schema (internal/sqlite/model.go) — this is
// the "wiring in" of the previously schema-only target model: Attributes
// and Geometries built by BuildAttributes/BuildGeometry (and the parallel
// station-worker variant) now actually land in SQLite, batch by batch,
// instead of only ever being counted. Each ModelStore.UpsertX call runs in
// its own transaction (see model.go), so this keeps the same bounded-RAM
// streaming property the plain reportSink already had — no batch is held
// onto beyond this one call. Safe for concurrent use for the same reason
// reportSink is: every station worker calls these methods concurrently,
// and each call's transaction is independent.
type persistSink struct {
	*reportSink
	model modelWriter
}

func (s *persistSink) WriteAttributes(batch []coremodel.Attribute) error {
	if err := s.model.UpsertAttributes(batch); err != nil {
		return fmt.Errorf("persisting attributes: %w", err)
	}
	return s.reportSink.WriteAttributes(batch)
}

func (s *persistSink) WriteGeometries(batch []coremodel.Geometry) error {
	if err := s.model.UpsertGeometry(batch); err != nil {
		return fmt.Errorf("persisting geometries: %w", err)
	}
	return s.reportSink.WriteGeometries(batch)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	phase1.SetLogger(logger)
	common.SetLogger(logger)

	dir := "examples/cgmes/ReliCapGrid_Espheim"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	if cpuProfilePath := os.Getenv("JAG_CPU_PROFILE"); cpuProfilePath != "" {
		f, err := os.Create(cpuProfilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "creating cpu profile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "starting cpu profile: %v\n", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}

	// The argument may name either a directory (globbed for *.xml/*.rdf,
	// the original behavior) or a single file directly — the latter is
	// what lets a caller point straight at e.g.
	// examples/lasttest/lasttest-200-10-10_10s.xml without first having to
	// copy/hardlink it into an isolated directory just so this glob-based
	// directory scan sees exactly one file (see .github/copilot-
	// instructions.md's binding "always use examples/lasttest/ directly,
	// never re-create copies under .data/" rule — examples/lasttest/
	// deliberately holds two independent, same-named-object-space fixtures
	// side by side, so a directory-only argument could never isolate one
	// from the other without a copy).
	var xmlFiles, rdfFiles []string
	var err error
	if info, statErr := os.Stat(dir); statErr == nil && !info.IsDir() {
		switch filepath.Ext(dir) {
		case ".xml":
			xmlFiles = []string{dir}
		case ".rdf":
			rdfFiles = []string{dir}
		default:
			fmt.Fprintf(os.Stderr, "unsupported file extension for %s (expected .xml or .rdf)\n", dir)
			os.Exit(1)
		}
	} else {
		// NSC dialect files use a .rdf extension instead of CGMES's .xml — the
		// underlying parser is dialect-neutral RDF/XML either way (see
		// internal/importer/cgmes/parser.go). Which Phase 1 entry point to use
		// is decided per directory (not per file): if any .rdf files are
		// present, the whole directory is treated as an NSC dataset and run
		// through phase1.RunNSCFiles (which also normalizes NSC's dialect
		// quirks — see internal/importer/nsc's doc comment); a pure .xml
		// directory keeps using phase1.RunCGMESFiles. Mixing both dialects in
		// one directory isn't a real scenario in the example data and isn't
		// supported here.
		var err error
		xmlFiles, err = filepath.Glob(filepath.Join(dir, "*.xml"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "globbing %s: %v\n", dir, err)
			os.Exit(1)
		}
		rdfFiles, err = filepath.Glob(filepath.Join(dir, "*.rdf"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "globbing %s: %v\n", dir, err)
			os.Exit(1)
		}
	}
	// The 20 NSC ".rdf" scenario files under examples/nsc turned out to be
	// non-canonical, independent variant fragments that share IDs with
	// each other and with example_as_cim.xml (see phase1.RunNSCFiles'
	// duplicate-ID guard) — the user decided to ignore them rather than
	// fix that up further. That leaves no ".rdf" file to trigger the NSC
	// dialect heuristic below for a directory containing only
	// example_as_cim.xml, so JAG_FORCE_NSC=1 lets a caller force the NSC
	// Phase 1 path (RunNSCFiles, with its 0-based sequenceNumber /
	// multi-Terminal BusbarSection normalization) even for a pure ".xml"
	// directory.
	isNSC := len(rdfFiles) > 0 || os.Getenv("JAG_FORCE_NSC") == "1"
	files := xmlFiles
	if isNSC {
		files = append(append([]string{}, xmlFiles...), rdfFiles...)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no .xml/.rdf files found in %s\n", dir)
		os.Exit(1)
	}
	sort.Strings(files)

	// Real SQLite file (not :memory:) so timings reflect actual disk I/O,
	// not an in-process B-tree kept entirely in RAM.
	dbPath := "phase2check.db"
	if v := os.Getenv("JAG_DB_PATH"); v != "" {
		dbPath = v
	}
	os.Remove(dbPath) // fresh run each time, avoid stale data from a previous invocation

	// JAG_CHUNK_SIZE controls the cursor-based batch size (staging.Store.
	// GetByClass "limit" argument) used by every per-class scan below
	// (BuildContainers, ResolveTerminals, the ConnectivityNode
	// unreferenced-check loop): how many distinct IDs are fetched from the
	// staging store per DB roundtrip. Larger values mean fewer roundtrips
	// but a bigger transient batch in RAM (each fetched record + its
	// resolved references live in memory at once); smaller values mean
	// more roundtrips but a lower RAM high-water-mark.
	//
	// UPDATE 2026-07-15: the 2026-07-14 sweep that originally picked 1000
	// here (chunk=1000/2000/3000/5000 x workers=2/4/8/16 on lt200/lt500,
	// see Konzept.md's "Offene Punkte") turned out to be measuring noise —
	// it varied this value while the REAL RAM driver (whole-model
	// structures held by BuildContainers/ResolveTerminals/CheckInvariants/
	// BuildElectricalGroups, now replaced by Pass A/B's per-batch design,
	// see pass_a_pipeline.go/pass_b.go) dominated regardless, so no value
	// in that sweep was meaningfully better than another. Now that Pass
	// A/B bounds RAM by batch size (not this chunk size), a larger default
	// only helps DB roundtrip efficiency (fewer roundtrips, consistent
	// with Idee.md's bulk-operations mandate) without the earlier RAM
	// risk — raised to 2000. Should be re-validated against a real
	// lt200/lt500 rerun once cmd/phase2check is fully wired to Pass A/B
	// (see wire-and-validate/ram-scaling-global-model todos); not yet
	// re-measured, so treat this as a reasoned starting default, not a
	// finally confirmed one.
	chunkSize := 2000
	if v := os.Getenv("JAG_CHUNK_SIZE"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_CHUNK_SIZE: %v\n", convErr)
			os.Exit(1)
		}
		chunkSize = n
	}

	// JAG_STATION_BATCH_SIZE controls Pass A's per-batch Substation/Building root
	// count (common.DefaultStationBatchSize=1000 if unset/0) — see
	// pass_a_pipeline.go's doc comment: this, not chunkSize, is now the
	// real RAM-bounding knob (a batch's own Node/Edge/Attribute/Geometry
	// footprint scales with batchSize, not with total model size).
	batchSize := 0
	if v := os.Getenv("JAG_STATION_BATCH_SIZE"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_STATION_BATCH_SIZE: %v\n", convErr)
			os.Exit(1)
		}
		batchSize = n
	}

	// JAG_STATION_WORKERS controls the number of Pass A pull-pool worker
	// goroutines (common.DefaultPassAWorkers=4 if unset/0) — each worker
	// pulls one batch of Substation/Building roots at a time and runs the
	// full per-station Phase 2/3 pipeline on it (ProcessStationBatch).
	stationWorkers := 0
	if v := os.Getenv("JAG_STATION_WORKERS"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_STATION_WORKERS: %v\n", convErr)
			os.Exit(1)
		}
		stationWorkers = n
	}

	// JAG_PASS_B_WORKERS controls the number of Pass B pull-pool worker
	// goroutines (common.DefaultPassBWorkers=4 if unset/0) — each worker
	// discovers and processes one independent ACLineSegment chain
	// (cable route) at a time, mirroring stationWorkers' shape but keyed
	// by cable route instead of station (see discoverACLineChainsStreaming).
	passBWorkers := 0
	if v := os.Getenv("JAG_PASS_B_WORKERS"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_PASS_B_WORKERS: %v\n", convErr)
			os.Exit(1)
		}
		passBWorkers = n
	}

	// JAG_PASS_B_BATCH_SIZE controls Pass B's per-batch ACLineSegment-chain
	// count (common.DefaultPassBBatchSize=1000 if unset/0) — see
	// pass_b.go's doc comment: analogous to JAG_STATION_BATCH_SIZE, but for
	// Pass B's own ACLineSegment build+write step, which previously wasn't
	// batched at all (a real load-test finding, see README.md's table).
	passBBatchSize := 0
	if v := os.Getenv("JAG_PASS_B_BATCH_SIZE"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_PASS_B_BATCH_SIZE: %v\n", convErr)
			os.Exit(1)
		}
		passBBatchSize = n
	}

	overallStart := time.Now()
	// Backend selection: JAG_BACKEND=postgres (see
	// internal/postgres/dsn.go's DSNFromEnv doc comment for the full
	// JAG_POSTGRES_* variable set) switches to a PostgreSQL-backed store;
	// anything else (including unset, the default) keeps the original
	// SQLite-file behavior unchanged.
	var store storeCloser
	var modelStore modelWriter
	var flags common.FlagStore
	if dsn, usePostgres := postgres.DSNFromEnv(); usePostgres {
		pg, err := postgres.Open(dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "opening postgres store: %v\n", err)
			os.Exit(1)
		}
		store = pg
		modelStore = pg.Model()
		flags = pg.Flags()
		fmt.Println("using postgres backend")
	} else {
		sq, err := sqlite.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "opening sqlite store: %v\n", err)
			os.Exit(1)
		}
		store = sq
		modelStore = sq.Model()
		flags = sq.Flags()
		fmt.Printf("using sqlite file: %s\n", dbPath)
	}
	defer store.Close()

	phase1Start := time.Now()
	var result phase1.Result
	if isNSC {
		result, err = phase1.RunNSCFiles(store, files)
	} else {
		result, err = phase1.RunCGMESFiles(store, files)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "phase1: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("phase1: version=%d records=%d errors=%d (%s)\n", result.Version, result.RecordCount, len(result.Errors), time.Since(phase1Start))
	for _, e := range result.Errors {
		fmt.Printf("  parse error: %s line=%d offset=%d: %s\n", e.SourceFile, e.Line, e.ByteOffset, e.Message)
	}

	// report accumulates only small, issue-count-scaled state across Pass
	// A's per-batch results (never a whole-model Node/Edge/Attribute
	// slice) — this is the RAM-bounded replacement for the old pipeline's
	// single big in-memory containers/resolved/nodes/edges variables.
	report := newPassReport()

	sink := &persistSink{reportSink: newReportSink(), model: modelStore}

	passAStart := time.Now()
	err = common.RunPassA(store, result.Version, chunkSize, batchSize, stationWorkers, sink, flags, isNSC, func(b *common.BatchResult) error {
		// Pass A's own groups: one independently-computed
		// map[node_id]group_id per owner (station root ID) — persisted
		// via UpsertElectricalGroups, which replaces only each owner's own
		// rows (see that method's doc comment), so this never clobbers any
		// other owner's (including Pass B's) rows for a shared boundary
		// node.
		owned := make(map[string]map[string]string, len(b.Groups))
		for owner, groups := range b.Groups {
			owned[owner] = groups
		}
		// Containers/Equipment/Nodes/Edges/Groups for this whole batch are
		// written in exactly ONE transaction via PersistBatch (see its doc
		// comment) — NOT one transaction per entity type. Attributes/
		// Geometries for this batch flow through sink (WriteAttributes/
		// WriteGeometries), streamed per-station as they're computed
		// rather than accumulated for the whole batch, so they aren't
		// included here (kept as their own already-batched, already
		// single-transaction-per-chunk writes for RAM-boundedness reasons
		// — see persistSink's doc comment).
		if err := modelStore.PersistBatch(b.Containers, b.Equipment, b.Nodes, b.Edges, nil, nil, owned); err != nil {
			return fmt.Errorf("persisting batch: %w", err)
		}
		report.addBatch(b)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pass A: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\npass A (batchSize=%d, workers=%d): %d containers, %d equipment, %d nodes, %d edges, %d groups (%s)\n",
		batchSizeOrDefault(batchSize), stationWorkers, report.containers, report.equipment, report.nodes, report.edges, len(report.distinctGroups), time.Since(passAStart))
	fmt.Printf("pass A anomalies: %d\n", len(report.anomalies))
	for i, a := range report.anomalies {
		if i >= 30 {
			fmt.Printf("  ... (%d more)\n", len(report.anomalies)-i)
			break
		}
		fmt.Printf("  %s: %s\n", a.EquipmentID, a.Message)
	}

	passBStart := time.Now()
	var aclineContainers, aclineEquipment, aclineNodes, aclineEdges, aclineGroupNodes, aclineBatches int
	// onACLineBatch persists-and-discards each Pass B ACLineSegment batch
	// immediately (batchSize=passBBatchSize, see RunPassB's doc comment) —
	// the low-RAM counterpart to Pass A's onBatchResult above, added to fix
	// the 2026-07-18/19 load-test finding that Pass B's peak RAM scaled
	// with its total group/container count regardless of
	// JAG_STATION_BATCH_SIZE (Pass B never read that variable at all).
	passB, err := common.RunPassB(store, result.Version, chunkSize, passBBatchSize, passBWorkers, sink, flags, func(b *common.PassBACLineBatchResult) error {
		// This batch's own groups persist under their own batch-distinct
		// owner ID (b.OwnerID, see PassBACLineBatchResult's doc comment)
		// — coexists independently alongside every other batch's/Pass A
		// station's rows for the same Node ID, since UpsertElectricalGroups
		// only ever replaces the given owner's own rows and query-time
		// code unions across ALL owners with no owner-id filtering.
		groups := map[string]map[string]string{b.OwnerID: b.Groups}
		if err := modelStore.PersistBatch(b.Containers, b.Equipment, b.Nodes, b.Edges, b.Attributes, nil, groups); err != nil {
			return fmt.Errorf("persisting pass B acline batch: %w", err)
		}
		report.addACLineBatch(b)
		aclineBatches++
		aclineContainers += len(b.Containers)
		aclineEquipment += len(b.Equipment)
		aclineNodes += len(b.Nodes)
		aclineEdges += len(b.Edges)
		aclineGroupNodes += len(b.Groups)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pass B: %v\n", err)
		os.Exit(1)
	}
	// The remaining passB (Junction/boundary EquivalentInjection data,
	// still small/class-size-bounded, not batched — see RunPassB's doc
	// comment) is persisted once, in a single transaction via
	// PersistBatch (passB.Attributes and passB.LineRefs are both
	// Attribute rows, concatenated so both go through the same call/
	// transaction instead of two separate UpsertAttributes transactions).
	// Pass B's own (Junction/boundary) groups persist under its fixed
	// sentinel owner ID (common.PassBOwnerID, see RunPassB's doc comment)
	// — this coexists independently alongside any Pass A station's rows
	// and every ACLine batch's own owner rows for the same Node ID, with
	// no run-order requirement and no special "if absent" logic needed
	// (UpsertElectricalGroups always replaces only the given owner's own
	// rows).
	passBOwned := make(map[string]map[string]string, len(passB.Groups))
	for owner, groups := range passB.Groups {
		passBOwned[owner] = groups
	}
	passBAttributes := make([]coremodel.Attribute, 0, len(passB.Attributes)+len(passB.LineRefs))
	passBAttributes = append(passBAttributes, passB.Attributes...)
	passBAttributes = append(passBAttributes, passB.LineRefs...)
	if err := modelStore.PersistBatch(passB.Containers, passB.Equipment, passB.Nodes, passB.Edges, passBAttributes, nil, passBOwned); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B remainder: %v\n", err)
		os.Exit(1)
	}
	report.addPassB(passB)
	passBGroupNodeCount := aclineGroupNodes
	for _, groups := range passB.Groups {
		passBGroupNodeCount += len(groups)
	}
	fmt.Printf("\npass B (aclineBatchSize=%d, aclineBatches=%d, workers=%d): %d containers, %d equipment, %d nodes, %d edges, %d groups (%s)\n",
		passBBatchSizeOrDefault(passBBatchSize), aclineBatches, passBWorkers,
		aclineContainers+len(passB.Containers), aclineEquipment+len(passB.Equipment), aclineNodes+len(passB.Nodes), aclineEdges+len(passB.Edges),
		passBGroupNodeCount, time.Since(passBStart))
	fmt.Printf("pass B anomalies: %d\n", len(passB.Anomalies))
	for i, a := range passB.Anomalies {
		if i >= 30 {
			fmt.Printf("  ... (%d more)\n", len(passB.Anomalies)-i)
			break
		}
		fmt.Printf("  %s: %s\n", a.EquipmentID, a.Message)
	}

	fmt.Printf("\ncontainers by type:\n")
	for _, t := range []string{"substation", "bay", "busbar", "acline", "distribution-box", "house"} {
		fmt.Printf("  %-18s %d\n", t, report.byType[t])
	}

	fmt.Printf("\nphase3 invariant violations (batch-scoped, from pass A+B): %d\n", len(report.violations))
	byRule := map[string]int{}
	for _, v := range report.violations {
		byRule[v.Rule]++
	}
	for rule, n := range byRule {
		fmt.Printf("  %-20s %d\n", rule, n)
	}
	for i, v := range report.violations {
		if i >= 30 {
			fmt.Printf("  ... and %d more\n", len(report.violations)-i)
			break
		}
		fmt.Printf("  [%s] %s: %s\n", v.Rule, v.ObjectID, v.Message)
	}

	// Final, genuinely paged whole-model completeness scans
	// ("unreferenced-node", "equipment-without-container") — see flags.go/
	// CheckInvariantsFlagged's doc comments. RAM stays bounded by
	// chunkSize regardless of model size; an empty result means no
	// anomaly.
	flaggedStart := time.Now()
	flaggedViolations, err := common.CheckInvariantsFlagged(store, flags, result.Version, chunkSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "phase3 (flagged completeness checks): %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nphase3 flagged completeness violations (unreferenced-node / equipment-without-container): %d (%s)\n", len(flaggedViolations), time.Since(flaggedStart))
	for i, v := range flaggedViolations {
		if i >= 30 {
			fmt.Printf("  ... and %d more\n", len(flaggedViolations)-i)
			break
		}
		fmt.Printf("  [%s] %s: %s\n", v.Rule, v.ObjectID, v.Message)
	}

	// Flags are purely ephemeral import-time bookkeeping (see flags.go) —
	// clear them now that the final completeness scans have run.
	if err := flags.ClearFlags(result.Version); err != nil {
		fmt.Fprintf(os.Stderr, "clearing import flags: %v\n", err)
		os.Exit(1)
	}

	byOwner := sink.byOwner
	if len(byOwner) > 0 {
		fmt.Printf("\nsachdaten: %d attribute rows across %d equipments (avg %.1f/equipment)\n", sink.totalAttrs, len(byOwner), float64(sink.totalAttrs)/float64(len(byOwner)))
	}
	if sink.sampleOwner != "" {
		fmt.Printf("\nsample equipment %s (%d attributes):\n", sink.sampleOwner, len(sink.sampleAttrs))
		for _, a := range sink.sampleAttrs {
			fmt.Printf("  %-45s = %v\n", a.Key, a.Value)
		}
	}
	fmt.Printf("geometries resolved: %d\n", sink.totalGeoms)

	// Electrical groups (switch-state merge, see BuildElectricalGroups) are
	// re-derived from the persisted DB state itself
	// (model_electrical_group), not from in-memory groups — confirms the
	// DB round-trip (Pass A upsert + Pass B upsert-if-absent) is faithful,
	// GROUP BY-computed, no full node-ID scan into Go memory. This is the
	// only globally-correct grouping number Pass A/B produces at import
	// time: Circuit ("Schaltkreis", full physical reachability across ALL
	// edges, not just switches) is deliberately NOT computed here — a
	// per-batch/per-station BuildCircuits call cannot see the ACLineSegment
	// chains (Pass B) connecting one station to the next, so it would only
	// ever report batch-local, misleadingly small circuit sizes. Circuits
	// are a query-time construct per the confirmed design (assembled from
	// these persisted ElectricalGroups snippets plus a dynamic switch-map,
	// see Usecases.md UC2b/UC4/UC7), not an import-time report metric.
	dbGroupSizes, err := modelStore.GroupSizes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading electrical group sizes from DB: %v\n", err)
		os.Exit(1)
	}
	dbCircSizes := make([]int, 0, len(dbGroupSizes))
	for _, n := range dbGroupSizes {
		dbCircSizes = append(dbCircSizes, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(dbCircSizes)))
	fmt.Printf("electrical groups from DB model_electrical_group: %d (node-count sizes desc: %v)\n", len(dbGroupSizes), dbCircSizes)

	fmt.Printf("\ntotal wall-clock (open+phase1+passA+passB+phase3): %s\n", time.Since(overallStart))
}

// batchSizeOrDefault mirrors common.RunPassA's own "0 -> DefaultStationBatchSize"
// substitution, purely for the report line above (RunPassA itself already
// applies the same default internally).
func batchSizeOrDefault(batchSize int) int {
	if batchSize <= 0 {
		return common.DefaultStationBatchSize
	}
	return batchSize
}

// passBBatchSizeOrDefault mirrors batchSizeOrDefault, but for Pass B's own
// separate ACLineSegment-chain batch-size knob (see RunPassB's doc
// comment) — purely for the report line above.
func passBBatchSizeOrDefault(batchSize int) int {
	if batchSize <= 0 {
		return common.DefaultPassBBatchSize
	}
	return batchSize
}

// passReport accumulates only small, issue-count-scaled state across every
// Pass A/B result — never a whole-model Node/Edge/Attribute slice (that
// would reintroduce exactly the RAM-scaling bug this rewiring fixes).
// Per-container-type counts and a small capped violation/anomaly sample
// are all safe to keep in memory: in a healthy model they stay at or near
// zero regardless of how many Substations/ACLineSegments were processed.
//
// addBatch is invoked from RunPassA's onBatchResult callback, which
// RunPassA's doc comment explicitly requires to be safe for concurrent
// calls from multiple worker goroutines (workers > 1) — mu guards exactly
// that. Found the hard way (2026-07-15 lasttest-500 run): with
// stationWorkers=4 and enough batches to make a same-millisecond overlap
// likely, the previously unguarded map writes below crashed with "fatal
// error: concurrent map writes" — lasttest-200 (fewer, and thus less
// likely to race, batches) had not exposed it. addPassB is only ever
// called once, single-threaded, after RunPassA's own wg.Wait() returns,
// but takes the same lock for consistency/defensiveness at negligible
// cost.
type passReport struct {
	mu                                   sync.Mutex
	containers, equipment, nodes, edges  int
	byType                               map[string]int
	distinctGroups                       map[string]bool
	violations                           []common.InvariantViolation
	anomalies                            []common.Anomaly
}

func newPassReport() *passReport {
	return &passReport{byType: map[string]int{}, distinctGroups: map[string]bool{}}
}

func (r *passReport) addBatch(b *common.BatchResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.containers += len(b.Containers)
	r.equipment += len(b.Equipment)
	r.nodes += len(b.Nodes)
	r.edges += len(b.Edges)
	for _, c := range b.Containers {
		r.byType[string(c.Type)]++
	}
	for _, groups := range b.Groups {
		for _, g := range groups {
			r.distinctGroups[g] = true
		}
	}
	r.violations = append(r.violations, b.Violations...)
	r.anomalies = append(r.anomalies, b.Anomalies...)
}

func (r *passReport) addPassB(b *common.PassBResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.containers += len(b.Containers)
	r.equipment += len(b.Equipment)
	r.nodes += len(b.Nodes)
	r.edges += len(b.Edges)
	for _, c := range b.Containers {
		r.byType[string(c.Type)]++
	}
	for _, groups := range b.Groups {
		for _, g := range groups {
			r.distinctGroups[g] = true
		}
	}
	r.violations = append(r.violations, b.Violations...)
	r.anomalies = append(r.anomalies, b.Anomalies...)
}

// addACLineBatch is invoked from RunPassB's onACLineBatch callback (one
// per ACLineSegment batch, see PassBACLineBatchResult) — mirrors addBatch
// exactly, since a Pass B ACLine batch is structurally the same "small,
// transient, persist-then-discard" shape as a Pass A station batch.
func (r *passReport) addACLineBatch(b *common.PassBACLineBatchResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.containers += len(b.Containers)
	r.equipment += len(b.Equipment)
	r.nodes += len(b.Nodes)
	r.edges += len(b.Edges)
	for _, c := range b.Containers {
		r.byType[string(c.Type)]++
	}
	for _, g := range b.Groups {
		r.distinctGroups[g] = true
	}
	r.violations = append(r.violations, b.Violations...)
	r.anomalies = append(r.anomalies, b.Anomalies...)
}

