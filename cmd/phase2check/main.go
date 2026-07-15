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
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// persistChunkSize bounds how many rows go into a single ModelStore
// Upsert* transaction, mirroring the 1000-row chunking BuildAttributes/
// BuildGeometry already use via Sink. Without this, persisting a whole
// model's Containers/Equipment/Nodes/Edges/electrical groups in one
// UpsertX call would open one single transaction spanning the entire
// model — correct, but an unnecessarily large, long-lived transaction
// (lock held the whole time, no incremental progress/durability) as
// dataset size grows. Chunking keeps each transaction's size and duration
// bounded regardless of model size, consistent with this project's
// bulk-not-unbounded persistence stance (see Idee.md).
const persistChunkSize = 1000

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
	model *sqlite.ModelStore
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
	xmlFiles, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "globbing %s: %v\n", dir, err)
		os.Exit(1)
	}
	rdfFiles, err := filepath.Glob(filepath.Join(dir, "*.rdf"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "globbing %s: %v\n", dir, err)
		os.Exit(1)
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

	// JAG_BATCH_SIZE controls Pass A's per-batch Substation/Building root
	// count (common.DefaultBatchSize=50 if unset/0) — see
	// pass_a_pipeline.go's doc comment: this, not chunkSize, is now the
	// real RAM-bounding knob (a batch's own Node/Edge/Attribute/Geometry
	// footprint scales with batchSize, not with total model size).
	batchSize := 0
	if v := os.Getenv("JAG_BATCH_SIZE"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_BATCH_SIZE: %v\n", convErr)
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

	overallStart := time.Now()
	store, err := sqlite.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	fmt.Printf("using sqlite file: %s\n", dbPath)
	modelStore := store.Model()
	flags := store.Flags()

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
		if err := modelStore.UpsertElectricalGroups(owned); err != nil {
			return fmt.Errorf("persisting electrical groups: %w", err)
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
	passB, err := common.RunPassB(store, result.Version, chunkSize, sink, flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pass B: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.Containers, modelStore.UpsertContainers); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B containers: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.Equipment, modelStore.UpsertEquipment); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B equipment: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.Nodes, modelStore.UpsertNodes); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B nodes: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.Edges, modelStore.UpsertEdges); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B edges: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.Attributes, modelStore.UpsertAttributes); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B acline-name attributes: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.LineRefs, modelStore.UpsertAttributes); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B cim:Line references: %v\n", err)
		os.Exit(1)
	}
	// Pass B's groups persist under their own fixed sentinel owner ID
	// (common.PassBOwnerID, see RunPassB's doc comment) — this coexists
	// independently alongside any Pass A station's rows for the same Node
	// ID, with no run-order requirement and no special "if absent" logic
	// needed (UpsertElectricalGroups always replaces only the given
	// owner's own rows).
	passBOwned := make(map[string]map[string]string, len(passB.Groups))
	for owner, groups := range passB.Groups {
		passBOwned[owner] = groups
	}
	if err := modelStore.UpsertElectricalGroups(passBOwned); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B electrical groups: %v\n", err)
		os.Exit(1)
	}
	report.addPassB(passB)
	passBGroupNodeCount := 0
	for _, groups := range passB.Groups {
		passBGroupNodeCount += len(groups)
	}
	fmt.Printf("\npass B: %d containers, %d equipment, %d nodes, %d edges, %d groups (%s)\n",
		len(passB.Containers), len(passB.Equipment), len(passB.Nodes), len(passB.Edges), passBGroupNodeCount, time.Since(passBStart))
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

// batchSizeOrDefault mirrors common.RunPassA's own "0 -> DefaultBatchSize"
// substitution, purely for the report line above (RunPassA itself already
// applies the same default internally).
func batchSizeOrDefault(batchSize int) int {
	if batchSize <= 0 {
		return common.DefaultBatchSize
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

