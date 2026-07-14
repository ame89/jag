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

// chunkUpsertMap splits a map (electrical groups: node_id -> group_id) into
// bounded-size sub-maps before handing each to upsert, for the same
// transaction-size reason as chunkUpsert above.
func chunkUpsertMap(items map[string]string, upsert func(map[string]string) error) error {
	chunk := make(map[string]string, persistChunkSize)
	for k, v := range items {
		chunk[k] = v
		if len(chunk) >= persistChunkSize {
			if err := upsert(chunk); err != nil {
				return err
			}
			chunk = make(map[string]string, persistChunkSize)
		}
	}
	if len(chunk) > 0 {
		return upsert(chunk)
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

	overallStart := time.Now()
	store, err := sqlite.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	fmt.Printf("using sqlite file: %s\n", dbPath)
	modelStore := store.Model()

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

	termStart := time.Now()
	resolved, nodeRoleIDs, anomalies, err := common.ResolveTerminals(store, result.Version, 1000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve terminals: %v\n", err)
		os.Exit(1)
	}

	oneTerm, twoTerm := 0, 0
	nodeSet := map[string]bool{}
	for _, et := range resolved {
		if et.Node2 == "" {
			oneTerm++
		} else {
			twoTerm++
		}
		if et.Node1 != "" {
			nodeSet[et.Node1] = true
		}
		if et.Node2 != "" {
			nodeSet[et.Node2] = true
		}
	}

	fmt.Printf("\nresolved equipment: %d (1-terminal=%d, 2-terminal=%d) (%s)\n", len(resolved), oneTerm, twoTerm, time.Since(termStart))
	fmt.Printf("distinct ConnectivityNode IDs referenced (-> Nodes): %d\n", len(nodeSet))
	fmt.Printf("anomalies: %d\n", len(anomalies))
	for i, a := range anomalies {
		if i >= 30 {
			fmt.Printf("  ... (%d more)\n", len(anomalies)-i)
			break
		}
		fmt.Printf("  %s: %s (%d raw terminals)\n", a.EquipmentID, a.Message, len(a.Terminals))
	}

	contStart := time.Now()
	containers, err := common.BuildContainers(store, result.Version, 1000, resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building containers: %v\n", err)
		os.Exit(1)
	}
	byType := map[string]int{}
	for _, c := range containers.Containers {
		byType[string(c.Type)]++
	}
	fmt.Printf("\ncontainers: %d total (%s)\n", len(containers.Containers), time.Since(contStart))
	for _, t := range []string{"substation", "bay", "busbar", "acline", "junction", "distribution-box"} {
		fmt.Printf("  %-18s %d\n", t, byType[t])
	}
	fmt.Printf("equipment assigned to a container: %d / %d resolved\n", len(containers.EquipmentToCont), len(resolved))
	fmt.Printf("container anomalies: %d\n", len(containers.Anomalies))
	for i, a := range containers.Anomalies {
		if i >= 15 {
			fmt.Printf("  ... and %d more\n", len(containers.Anomalies)-i)
			break
		}
		fmt.Printf("  %s: %s\n", a.ObjectID, a.Message)
	}
	fmt.Printf("cim:Line references kept as Sachdaten (untrusted): %d\n", len(containers.LineRefs))

	persistStart := time.Now()
	if err := chunkUpsert(containers.Containers, modelStore.UpsertContainers); err != nil {
		fmt.Fprintf(os.Stderr, "persisting containers: %v\n", err)
		os.Exit(1)
	}
	equipmentRows := make([]coremodel.Equipment, 0, len(resolved))
	for eqID := range resolved {
		equipmentRows = append(equipmentRows, coremodel.Equipment{ID: eqID, ContainerID: containers.EquipmentToCont[eqID]})
	}
	if err := chunkUpsert(equipmentRows, modelStore.UpsertEquipment); err != nil {
		fmt.Fprintf(os.Stderr, "persisting equipment: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("persisted %d containers, %d equipment rows (%s)\n", len(containers.Containers), len(equipmentRows), time.Since(persistStart))

	// acline chain size distribution — sanity check for the topology-based
	// grouping (see BuildContainers doc comment).
	chainSize := map[string]int{}
	for _, cid := range containers.EquipmentToCont {
		chainSize[cid]++
	}
	sizeHist := map[int]int{}
	for cid, n := range chainSize {
		if byType["acline"] > 0 {
			for _, c := range containers.Containers {
				if c.ID == cid && c.Type == common.ContainerTypeACLine {
					sizeHist[n]++
				}
			}
		}
	}
	fmt.Printf("acline chain size histogram (segments per acline container): %v\n", sizeHist)

	busbarContainerSet := map[string]bool{}
	for _, c := range containers.Containers {
		if c.Type == common.ContainerTypeBusbar {
			busbarContainerSet[c.ID] = true
		}
	}
	busbarSectionIDs := map[string]bool{}
	for eqID, contID := range containers.EquipmentToCont {
		if busbarContainerSet[contID] {
			busbarSectionIDs[eqID] = true
		}
	}

	junctionMerged := common.MergeJunctionNodes(resolved, nodeRoleIDs)
	junctionMerges := 0
	for eqID := range nodeRoleIDs {
		if junctionMerged[eqID].Node1 != resolved[eqID].Node1 {
			junctionMerges++
		}
	}
	fmt.Printf("\njunction nodes remapped (own multi-terminal splice unified): %d\n", junctionMerges)

	nodeOnlyIDs := map[string]bool{}
	for eqID := range busbarSectionIDs {
		nodeOnlyIDs[eqID] = true
	}
	for eqID := range nodeRoleIDs {
		nodeOnlyIDs[eqID] = true
	}

	mergedResolved := common.MergeBusbarSectionNodes(junctionMerged, containers, nodeOnlyIDs)
	merges := 0
	for eqID := range busbarSectionIDs {
		if mergedResolved[eqID].Node1 != resolved[eqID].Node1 {
			merges++
		}
	}
	fmt.Printf("busbar-section nodes remapped (previously disconnected, same busbar container): %d\n", merges)

	nodes, edges := common.BuildNodesAndEdges(mergedResolved, nodeOnlyIDs)
	fmt.Printf("built %d Nodes, %d Edges\n", len(nodes), len(edges))
	nodesEdgesPersistStart := time.Now()
	if err := chunkUpsert(nodes, modelStore.UpsertNodes); err != nil {
		fmt.Fprintf(os.Stderr, "persisting nodes: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(edges, modelStore.UpsertEdges); err != nil {
		fmt.Fprintf(os.Stderr, "persisting edges: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("persisted %d nodes, %d edges (%s)\n", len(nodes), len(edges), time.Since(nodesEdgesPersistStart))
	gndEdges := 0
	for _, e := range edges {
		if e.Terminal2NodeID == common.GNDNodeID {
			gndEdges++
		}
	}
	fmt.Printf("edges pointing to GND: %d\n", gndEdges)

	circStart := time.Now()
	circuits, _, _, err := common.BuildCircuits(store, result.Version, nodes, edges, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building circuits: %v\n", err)
		os.Exit(1)
	}
	circSizes := make([]int, 0, len(circuits))
	for _, c := range circuits {
		circSizes = append(circSizes, len(c.Nodes))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(circSizes)))
	fmt.Printf("circuits: %d (node-count sizes desc: %v) (%s)\n", len(circuits), circSizes, time.Since(circStart))

	// Cross-check: does every ConnectivityNode object in the source
	// actually appear among our built Nodes (Idee.md invariant: a
	// ConnectivityNode with reference count 0 is an error)?
	nodeIDSet := map[string]bool{}
	for _, n := range nodes {
		nodeIDSet[n.EquipmentID] = true
	}
	afterID := ""
	unreferenced := 0
	total := 0
	for {
		records, err := store.GetByClass(result.Version, "ConnectivityNode", afterID, 1000)
		if err != nil {
			fmt.Fprintf(os.Stderr, "checking ConnectivityNodes: %v\n", err)
			os.Exit(1)
		}
		if len(records) == 0 {
			break
		}
		seen := map[string]bool{}
		var ids []string
		for _, r := range records {
			if !seen[r.ID] {
				seen[r.ID] = true
				ids = append(ids, r.ID)
			}
		}
		for _, id := range ids {
			total++
			if !nodeIDSet[id] {
				unreferenced++
				fmt.Printf("  unreferenced ConnectivityNode: %s\n", id)
			}
		}
		afterID = ids[len(ids)-1]
		if len(ids) < 1000 {
			break
		}
	}
	fmt.Printf("ConnectivityNode objects in source: %d, unreferenced (ref-count 0): %d\n", total, unreferenced)

	// JAG_STATION_WORKERS controls the number of station-worker goroutines
	// for common.BuildSachdatenAndGeometryParallel ("step (b)" of the
	// parallel-import decision — see that function's doc comment); 0/unset
	// uses common.DefaultStationWorkers. Only used on the normal (no
	// JAG_SACHDATEN_SAMPLE) path below, since the sample diagnostic
	// restricts BuildAttributes' input without restricting BuildGeometry's
	// equipmentIDs/containerIDs the same way, which the combined parallel
	// function doesn't support.
	stationWorkers := 0
	if v := os.Getenv("JAG_STATION_WORKERS"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_STATION_WORKERS: %v\n", convErr)
			os.Exit(1)
		}
		stationWorkers = n
	}

	// JAG_DISABLE_ANHAENGSEL is a diagnostic-only switch (see
	// common.DisableSatelliteWalk's doc comment): when "1", the Sachdaten
	// phase never walks into any satellite/Anhängsel object at all, only
	// emitting each Equipment's own literal attributes. Used to measure the
	// Sachdaten phase's baseline duration/RAM without any many-to-one hub
	// risk, while hunting for hub classes to add to structuralClasses.
	if os.Getenv("JAG_DISABLE_ANHAENGSEL") == "1" {
		common.DisableSatelliteWalk = true
		fmt.Println("JAG_DISABLE_ANHAENGSEL=1: satellite/Anhängsel walk disabled, only literal Equipment attributes will be emitted")
	}

	attrsStart := time.Now()
	sachdatenInput := resolved
	if v := os.Getenv("JAG_SACHDATEN_SAMPLE"); v != "" {
		// Diagnostic-only knob: restrict BuildAttributes to the first N
		// equipment IDs (sorted) so a CPU profile of the Sachdaten/
		// Anhängsel walk can be captured in a reasonable time against a
		// large dataset, instead of waiting for the full run. Not used in
		// normal operation.
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_SACHDATEN_SAMPLE: %v\n", convErr)
			os.Exit(1)
		}
		var ids []string
		for id := range resolved {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if n < len(ids) {
			ids = ids[:n]
		}
		sample := make(map[string]common.EquipmentTerminals, len(ids))
		for _, id := range ids {
			sample[id] = resolved[id]
		}
		sachdatenInput = sample
		fmt.Printf("\n[diagnostic] JAG_SACHDATEN_SAMPLE=%d -> sampling %d/%d equipment for BuildAttributes\n", n, len(sample), len(resolved))
	}
	sampled := sachdatenInput != nil && len(sachdatenInput) != len(resolved)

	equipmentIDs := map[string]bool{}
	for eqID := range resolved {
		equipmentIDs[eqID] = true
	}
	containerIDs := map[string]bool{}
	for _, c := range containers.Containers {
		containerIDs[c.ID] = true
	}

	geoStart := attrsStart
	sink := &persistSink{reportSink: newReportSink(), model: modelStore}
	if sampled {
		// Sampling restricts BuildAttributes' input without restricting
		// BuildGeometry's equipmentIDs/containerIDs the same way — the
		// combined parallel path doesn't support that split, so fall back
		// to the plain sequential calls whenever JAG_SACHDATEN_SAMPLE is
		// in play (a diagnostic-only knob, not normal operation anyway).
		if err := common.BuildAttributes(store, result.Version, 1000, sachdatenInput, nil, sink); err != nil {
			fmt.Fprintf(os.Stderr, "building attributes: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nsachdaten: %d attribute rows (%s)\n", sink.totalAttrs, time.Since(attrsStart))

		geoStart = time.Now()
		if err := common.BuildGeometry(store, result.Version, 1000, equipmentIDs, containerIDs, sink); err != nil {
			fmt.Fprintf(os.Stderr, "building geometry: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Normal path: step (b) of the parallel-import decision — station
		// workers build Sachdaten+Geometry concurrently, one station (or
		// bundle of stations) per goroutine, plus one dedicated goroutine
		// for ACLine/unassigned equipment (see BuildSachdatenAndGeometryParallel's
		// doc comment). Each worker flushes through sink as it goes (see
		// reportSink/Sink's 2026-07-14 fix doc comments) instead of this
		// call returning the whole model's Attributes/Geometries at once.
		if err := common.BuildSachdatenAndGeometryParallel(store, result.Version, 1000, resolved, containers, stationWorkers, sink); err != nil {
			fmt.Fprintf(os.Stderr, "building sachdaten+geometry (parallel): %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nsachdaten+geometry (parallel, %d workers): %d attribute rows, %d geometries (%s)\n", stationWorkers, sink.totalAttrs, sink.totalGeoms, time.Since(attrsStart))
	}

	byOwner := sink.byOwner
	if len(byOwner) > 0 {
		fmt.Printf("sachdaten: %d attribute rows across %d equipments (avg %.1f/equipment)\n", sink.totalAttrs, len(byOwner), float64(sink.totalAttrs)/float64(len(byOwner)))
	}

	// Show a SynchronousMachine's attributes as a spot check (should include
	// its own RotatingMachine.* fields plus GeneratingUnit/FossilFuel/
	// ControlAreaGeneratingUnit satellite attributes) — sampleAttrs was
	// captured by reportSink on the fly (first owner seen with >15
	// attributes), instead of scanning the whole model's attrs afterward.
	if sink.sampleOwner != "" {
		fmt.Printf("\nsample equipment %s (%d attributes):\n", sink.sampleOwner, len(sink.sampleAttrs))
		for _, a := range sink.sampleAttrs {
			fmt.Printf("  %-45s = %v\n", a.Key, a.Value)
		}
	}

	fmt.Printf("\ngeometries resolved: %d (0 expected — Espheim ships no GL profile) (%s)\n", sink.totalGeoms, time.Since(geoStart))

	phase3Start := time.Now()
	phase3, err := common.CheckInvariants(store, result.Version, mergedResolved, containers, nodes, edges, isNSC)
	if err != nil {
		fmt.Fprintf(os.Stderr, "phase3: %v\n", err)
		os.Exit(1)
	}
	byRule := map[string]int{}
	for _, v := range phase3.Violations {
		byRule[v.Rule]++
	}
	fmt.Printf("\nphase3 invariant violations: %d (%s)\n", len(phase3.Violations), time.Since(phase3Start))
	for rule, n := range byRule {
		fmt.Printf("  %-20s %d\n", rule, n)
	}
	for i, v := range phase3.Violations {
		if i >= 30 {
			fmt.Printf("  ... and %d more\n", len(phase3.Violations)-i)
			break
		}
		fmt.Printf("  [%s] %s: %s\n", v.Rule, v.ObjectID, v.Message)
	}

	// PROTOTYPE: electrical topology (Zero-Ohm reduction), not yet wired
	// into CheckInvariants/Phase 3 — see internal/impl/common/electrical.go.
	elecStart := time.Now()
	groups, switches, err := common.BuildElectricalGroups(store, result.Version, nodes, edges, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "electrical topology: %v\n", err)
		os.Exit(1)
	}
	closed, open := 0, 0
	byClass := map[string]int{}
	for _, s := range switches {
		byClass[s.Class]++
		if s.Open {
			open++
		} else {
			closed++
		}
	}
	distinctGroups := map[string]bool{}
	for _, g := range groups {
		distinctGroups[g] = true
	}
	fmt.Printf("\nelectrical topology (prototype): %d switch-like equipment (closed=%d, open=%d), classes=%v\n", len(switches), closed, open, byClass)
	fmt.Printf("  %d physical Nodes reduced to %d electrical groups (%s)\n", len(nodes), len(distinctGroups), time.Since(elecStart))

	groupsPersistStart := time.Now()
	if err := chunkUpsertMap(groups, modelStore.UpsertElectricalGroups); err != nil {
		fmt.Fprintf(os.Stderr, "persisting electrical groups: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  persisted %d electrical group assignments (%s)\n", len(groups), time.Since(groupsPersistStart))

	// Re-derive the "circuits: N (sizes desc)" report from the persisted
	// DB state itself (model_electrical_group), not from the in-memory
	// `groups` map above — this is the same report as during import, but
	// now sourced from the final DB model to confirm the DB round-trip is
	// faithful (GROUP BY-computed, no full node-ID scan into Go memory).
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
	fmt.Printf("  circuits from DB model_electrical_group: %d (node-count sizes desc: %v)\n", len(dbGroupSizes), dbCircSizes)

	// Dynamic switch-map demo: JAG itself doesn't track live switching
	// state (see Konzept.md), but BuildElectricalGroups/BuildCircuits
	// already accept a SwitchStateOverrides map for a caller that does
	// (e.g. SCADA-driven). Flip the first closed switch found to "open"
	// as a smoke test, recompute in-memory only (not persisted — the
	// model_electrical_group table above stays the static import-default
	// grouping), and show how the circuit count/sizes react.
	var demoSwitchID string
	for _, sw := range switches {
		if !sw.Open {
			demoSwitchID = sw.EquipmentID
			break
		}
	}
	if demoSwitchID != "" {
		overrides := common.SwitchStateOverrides{demoSwitchID: true} // force open
		dynGroups, _, err := common.BuildElectricalGroups(store, result.Version, nodes, edges, overrides)
		if err != nil {
			fmt.Fprintf(os.Stderr, "electrical topology (dynamic override): %v\n", err)
			os.Exit(1)
		}
		dynDistinct := map[string]bool{}
		for _, g := range dynGroups {
			dynDistinct[g] = true
		}
		fmt.Printf("  dynamic switch-map demo: forcing %s open -> %d electrical groups (was %d)\n",
			demoSwitchID, len(dynDistinct), len(distinctGroups))
	} else {
		fmt.Printf("  dynamic switch-map demo: no closed switch found, skipped\n")
	}

	mismatchStart := time.Now()
	mismatches, err := common.CheckElectricalTopologyAgainstCGMES(store, result.Version, groups)
	if err != nil {
		fmt.Fprintf(os.Stderr, "electrical topology cross-check: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  cross-check vs. CGMES TopologicalNode: %d mismatches (%s)\n", len(mismatches), time.Since(mismatchStart))
	for i, m := range mismatches {
		if i >= 15 {
			fmt.Printf("    ... and %d more\n", len(mismatches)-i)
			break
		}
		fmt.Printf("    [%s] %s: %s\n", m.Rule, m.ObjectID, m.Message)
	}

	fmt.Printf("\ntotal wall-clock (open+phase1+phase2+phase3): %s\n", time.Since(overallStart))
}
