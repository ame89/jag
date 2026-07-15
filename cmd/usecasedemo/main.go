// Command usecasedemo runs the full Phase 1-3 import pipeline against a
// CGMES/NSC example directory (default: examples/cgmes/Telemark_LV_Fuse,
// chosen as the best all-rounder for usecase coverage — see Impl.md: it
// has a real Fuse/Breaker/BusbarSection/Bay hierarchy, real SSH switch
// states, and real GL-profile WGS84 geometry), persists the result into a
// SQLite model_* database via ModelStore, and then demonstrates every
// usecase currently implemented in internal/impl/usecase against that
// persisted data: UC1 (station subgraph), UC2a (physical reachability),
// UC2b/UC4 (electrical connectivity), UC3 (region/bounding-box geometry),
// UC12 (container-type counts). See spec/Usecases.md for the full usecase
// catalogue and spec/Impl.md for the architecture this demo exercises.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/impl/usecase"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	dir := "examples/cgmes/Telemark_LV_Fuse"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	dbPath := "usecasedemo.db"
	os.Remove(dbPath)

	store, err := sqlite.Open(dbPath)
	if err != nil {
		fatalf("opening store: %v", err)
	}
	defer store.Close()
	defer os.Remove(dbPath)
	model := store.Model()

	// --- Phase 1: raw streaming import ---------------------------------
	xmlFiles, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		fatalf("globbing %s: %v", dir, err)
	}
	if len(xmlFiles) == 0 {
		fatalf("no .xml files found in %s", dir)
	}
	sort.Strings(xmlFiles)

	result, err := phase1.RunCGMESFiles(store, xmlFiles)
	if err != nil {
		fatalf("phase1: %v", err)
	}
	fmt.Printf("phase1: version=%d records=%d errors=%d\n", result.Version, result.RecordCount, len(result.Errors))

	// --- Phase 2: build the node-edge model + hierarchy -----------------
	// BuildContainers no longer depends on ResolveTerminals' output (see
	// its doc comment — top-down restructuring, 2026-07-16): container
	// membership comes directly from Equipment.EquipmentContainer, with
	// only a small targeted Terminal lookup for the few exceptions
	// (standalone Junction). Called first so a container-resolution
	// problem is reported before paying ResolveTerminals' full-model cost.
	containers, err := common.BuildContainers(store, result.Version, 1000)
	if err != nil {
		fatalf("building containers: %v", err)
	}
	resolved, nodeRoleIDs, _, err := common.ResolveTerminals(store, result.Version, 1000)
	if err != nil {
		fatalf("resolve terminals: %v", err)
	}
	if err := model.UpsertContainers(containers.Containers); err != nil {
		fatalf("persisting containers: %v", err)
	}
	equipmentRows := make([]coremodel.Equipment, 0, len(resolved))
	for eqID := range resolved {
		equipmentRows = append(equipmentRows, coremodel.Equipment{ID: eqID, ContainerID: containers.EquipmentToCont[eqID]})
	}
	if err := model.UpsertEquipment(equipmentRows); err != nil {
		fatalf("persisting equipment: %v", err)
	}

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
	nodeOnlyIDs := map[string]bool{}
	for eqID := range busbarSectionIDs {
		nodeOnlyIDs[eqID] = true
	}
	for eqID := range nodeRoleIDs {
		nodeOnlyIDs[eqID] = true
	}
	mergedResolved := common.MergeBusbarSectionNodes(junctionMerged, containers, nodeOnlyIDs)

	nodes, edges := common.BuildNodesAndEdges(mergedResolved, nodeOnlyIDs)
	if err := model.UpsertNodes(nodes); err != nil {
		fatalf("persisting nodes: %v", err)
	}
	if err := model.UpsertEdges(edges); err != nil {
		fatalf("persisting edges: %v", err)
	}
	fmt.Printf("built + persisted: %d containers, %d equipment, %d nodes, %d edges\n",
		len(containers.Containers), len(equipmentRows), len(nodes), len(edges))

	groups, _, err := common.BuildElectricalGroups(store, result.Version, nodes, edges, nil)
	if err != nil {
		fatalf("electrical topology: %v", err)
	}
	// This demo builds one whole-model grouping in a single pass (not
	// Pass A/B's per-station design), so a single fixed owner id is
	// enough here.
	if err := model.UpsertElectricalGroups(map[string]map[string]string{"usecasedemo": groups}); err != nil {
		fatalf("persisting electrical groups: %v", err)
	}

	equipmentIDs := map[string]bool{}
	for eqID := range resolved {
		equipmentIDs[eqID] = true
	}
	containerIDs := map[string]bool{}
	for _, c := range containers.Containers {
		containerIDs[c.ID] = true
	}
	geoSink := &modelSink{model: model}
	if err := common.BuildGeometry(store, result.Version, 1000, equipmentIDs, containerIDs, geoSink); err != nil {
		fatalf("building geometry: %v", err)
	}
	fmt.Printf("persisted %d electrical-group assignments, %d geometries\n", len(groups), geoSink.total)

	// --- Usecase demo -----------------------------------------------------
	svc := usecase.NewService(
		sqlite.ContainerAdapter{ModelStore: model},
		sqlite.EquipmentAdapter{ModelStore: model},
		sqlite.GeometryAdapter{ModelStore: model},
		model,
		sqlite.ElectricalAdapter{ModelStore: model},
	)

	fmt.Println("\n--- UC12: container counts by type ---")
	counts, err := svc.ContainerCounts()
	if err != nil {
		fatalf("ContainerCounts: %v", err)
	}
	for _, t := range []string{"substation", "bay", "busbar", "acline", "junction", "distribution-box"} {
		fmt.Printf("  %-18s %d\n", t, counts[t])
	}

	var stationID string
	for _, c := range containers.Containers {
		if c.Type == "substation" {
			stationID = c.ID
			break
		}
	}
	if stationID != "" {
		fmt.Printf("\n--- UC1: station subgraph for %s ---\n", stationID)
		sub, err := svc.StationSubgraph(stationID)
		if err != nil {
			fatalf("StationSubgraph: %v", err)
		}
		fmt.Printf("  containers=%d equipment=%d nodes=%d edges=%d\n", len(sub.Containers), len(sub.Equipment), len(sub.Nodes), len(sub.Edges))
	} else {
		fmt.Println("\n--- UC1: no substation container found, skipping ---")
	}

	if len(nodes) >= 2 {
		a, b := nodes[0].EquipmentID, nodes[1].EquipmentID
		fmt.Printf("\n--- UC2a: physical reachability from %s ---\n", a)
		reachable, err := svc.ReachablePhysical([]string{a})
		if err != nil {
			fatalf("ReachablePhysical: %v", err)
		}
		fmt.Printf("  %d nodes physically reachable\n", len(reachable))

		fmt.Printf("\n--- UC2b/UC4: electrical connectivity %s <-> %s ---\n", a, b)
		connected, err := svc.ElectricallyConnected(a, b)
		if err != nil {
			fatalf("ElectricallyConnected: %v", err)
		}
		fmt.Printf("  connected=%v\n", connected)
	}

	fmt.Println("\n--- UC3: geometry in a world-wide bounding box ---")
	geoms, err := svc.GeometryInRegion(-90, -180, 90, 180)
	if err != nil {
		fatalf("GeometryInRegion: %v", err)
	}
	fmt.Printf("  %d geometry entries found\n", len(geoms))
}

// modelSink adapts ModelStore.UpsertGeometry to common.Sink for this
// demo's plain (non-parallel) BuildGeometry call — WriteAttributes is
// never called on this path (BuildGeometry only emits Geometries), so it
// is intentionally left a no-op.
type modelSink struct {
	model *sqlite.ModelStore
	total int
}

func (s *modelSink) WriteAttributes(batch []coremodel.Attribute) error { return nil }

func (s *modelSink) WriteGeometries(batch []coremodel.Geometry) error {
	s.total += len(batch)
	return s.model.UpsertGeometry(batch)
}
