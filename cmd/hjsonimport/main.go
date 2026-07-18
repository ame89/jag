// Command hjsonimport is the reference CLI for the Fachmodell HJSON Phase 1
// dialect (see internal/importer/hjson's doc comment and Konzept.md's
// "HJSON Fachmodell" section): it parses a directory tree of *.hjson files
// (<root>/<Netzregion>/<ONS|KVS|Kabel|Haushalte>/<id>.hjson) into the
// staging store, runs it through the existing Pass A/B Phase 2/3 pipeline
// unchanged, and persists the result via ModelStore — mirroring
// cmd/phase2check's structure for the CGMES/NSC dialects.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

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

// countingSink is common.Sink's minimal implementation for this CLI: it
// persists straight into ModelStore and only keeps small running counts
// for the final summary, never a whole-model slice.
type countingSink struct {
	model      *sqlite.ModelStore
	attrCount  int
	geomCount  int
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

func main() {
	root := "examples/hjson"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	dbPath := "hjsonimport.db"
	if v := os.Getenv("JAG_DB_PATH"); v != "" {
		dbPath = v
	}
	os.Remove(dbPath)

	chunkSize := 2000
	if v := os.Getenv("JAG_CHUNK_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_CHUNK_SIZE: %v\n", err)
			os.Exit(1)
		}
		chunkSize = n
	}
	batchSize := 0
	if v := os.Getenv("JAG_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_BATCH_SIZE: %v\n", err)
			os.Exit(1)
		}
		batchSize = n
	}
	stationWorkers := 0
	if v := os.Getenv("JAG_STATION_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_STATION_WORKERS: %v\n", err)
			os.Exit(1)
		}
		stationWorkers = n
	}
	passBWorkers := 0
	if v := os.Getenv("JAG_PASS_B_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid JAG_PASS_B_WORKERS: %v\n", err)
			os.Exit(1)
		}
		passBWorkers = n
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
	result, err := phase1.RunHJSONFiles(store, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "phase1: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("phase1: version=%d records=%d errors=%d (%s)\n", result.Version, result.RecordCount, len(result.Errors), time.Since(phase1Start))
	for _, e := range result.Errors {
		fmt.Printf("  parse error: %s: %s\n", e.SourceFile, e.Message)
	}
	if len(result.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "phase1 reported errors, aborting before phase 2")
		os.Exit(1)
	}

	sink := &countingSink{model: modelStore}
	var containerCount, equipmentCount, nodeCount, edgeCount int

	passAStart := time.Now()
	err = common.RunPassA(store, result.Version, chunkSize, batchSize, stationWorkers, sink, flags, false, func(b *common.BatchResult) error {
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
		fmt.Fprintf(os.Stderr, "pass A: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pass A: %d containers, %d equipment, %d nodes, %d edges (%s)\n", containerCount, equipmentCount, nodeCount, edgeCount, time.Since(passAStart))

	passBStart := time.Now()
	passB, err := common.RunPassB(store, result.Version, chunkSize, passBWorkers, sink, flags)
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
		fmt.Fprintf(os.Stderr, "persisting pass B attributes: %v\n", err)
		os.Exit(1)
	}
	if err := chunkUpsert(passB.LineRefs, modelStore.UpsertAttributes); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B line refs: %v\n", err)
		os.Exit(1)
	}
	passBOwned := make(map[string]map[string]string, len(passB.Groups))
	for owner, groups := range passB.Groups {
		passBOwned[owner] = groups
	}
	if err := modelStore.UpsertElectricalGroups(passBOwned); err != nil {
		fmt.Fprintf(os.Stderr, "persisting pass B electrical groups: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pass B: %d containers, %d equipment, %d nodes, %d edges (%s)\n", len(passB.Containers), len(passB.Equipment), len(passB.Nodes), len(passB.Edges), time.Since(passBStart))
	for _, a := range passB.Anomalies {
		fmt.Printf("  pass B anomaly: %s: %s\n", a.EquipmentID, a.Message)
	}

	fmt.Printf("\nattributes: %d, geometries: %d\n", sink.attrCount, sink.geomCount)
	fmt.Printf("total: %s\n", time.Since(overallStart))
}
