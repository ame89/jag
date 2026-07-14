package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"time"

	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

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
	attrs, err := common.BuildAttributes(store, result.Version, 1000, sachdatenInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building attributes: %v\n", err)
		os.Exit(1)
	}
	byOwner := map[string]int{}
	for _, a := range attrs {
		byOwner[a.OwnerID]++
	}
	fmt.Printf("\nsachdaten: %d attribute rows across %d equipments (avg %.1f/equipment) (%s)\n", len(attrs), len(byOwner), float64(len(attrs))/float64(len(byOwner)), time.Since(attrsStart))

	// Show a SynchronousMachine's attributes as a spot check (should include
	// its own RotatingMachine.* fields plus GeneratingUnit/FossilFuel/
	// ControlAreaGeneratingUnit satellite attributes).
	for eqID, count := range byOwner {
		if count > 15 { // machines with many attached satellites stand out
			fmt.Printf("\nsample equipment %s (%d attributes):\n", eqID, count)
			for _, a := range attrs {
				if a.OwnerID == eqID {
					fmt.Printf("  %-45s = %v\n", a.Key, a.Value)
				}
			}
			break
		}
	}

	equipmentIDs := map[string]bool{}
	for eqID := range resolved {
		equipmentIDs[eqID] = true
	}
	containerIDs := map[string]bool{}
	for _, c := range containers.Containers {
		containerIDs[c.ID] = true
	}
	geoStart := time.Now()
	geometries, err := common.BuildGeometry(store, result.Version, 1000, equipmentIDs, containerIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building geometry: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\ngeometries resolved: %d (0 expected — Espheim ships no GL profile) (%s)\n", len(geometries), time.Since(geoStart))

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
