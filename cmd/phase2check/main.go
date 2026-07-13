package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

func main() {
	dir := "examples/cgmes/ReliCapGrid_Espheim"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no .xml files found in %s (err: %v)\n", dir, err)
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
	result, err := phase1.RunCGMESFiles(store, files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "phase1: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("phase1: version=%d records=%d errors=%d (%s)\n", result.Version, result.RecordCount, len(result.Errors), time.Since(phase1Start))
	for _, e := range result.Errors {
		fmt.Printf("  parse error: %s line=%d offset=%d: %s\n", e.SourceFile, e.Line, e.ByteOffset, e.Message)
	}

	termStart := time.Now()
	resolved, anomalies, err := common.ResolveTerminals(store, result.Version, 1000)
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

	nodes, edges := common.BuildNodesAndEdges(resolved)
	fmt.Printf("\nbuilt %d Nodes, %d Edges\n", len(nodes), len(edges))
	gndEdges := 0
	for _, e := range edges {
		if e.Terminal2NodeID == common.GNDNodeID {
			gndEdges++
		}
	}
	fmt.Printf("edges pointing to GND: %d\n", gndEdges)

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
	attrs, err := common.BuildAttributes(store, result.Version, 1000, resolved)
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
				if c.ID == cid && c.Type == coremodel.ContainerTypeACLine {
					sizeHist[n]++
				}
			}
		}
	}
	fmt.Printf("acline chain size histogram (segments per acline container): %v\n", sizeHist)

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
	fmt.Printf("\ntotal wall-clock (open+phase1+phase2): %s\n", time.Since(overallStart))
}
