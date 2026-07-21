package hjson2_test

// TestBusbarRoundTripAgainstRealDatasets is a dedicated regression test
// for the hjson2 Busbar/BusbarSection redesign (see spec/Konzept.md's
// "hjson2-Busbar-Design, final (2026-07-21)" section): a Busbar Container
// is physically exactly one Node; hjson2 achieves import/export symmetry
// by reusing the real CIM ConnectivityNode ID (shortened) as the Busbar's
// own ID, instead of inventing a synthetic one that then relied on
// MergeBusbarSectionNodes to merge everything back together on reimport.
//
// Before that redesign, a busbar with more than one electrically
// convergent piece of equipment fragmented into multiple Nodes on hjson2
// reimport — e.g. ReliCapGrid_Espheim's Circuit count went from 48 to 52
// with one Circuit splitting 1 -> 4 because 4 Disconnectors converging on
// one busbar each got their own synthetic Section/Node instead of sharing
// one. This test drives the full real pipeline for several real datasets
// (CGMES, NSC, a pandapower-oriented CIM export) end to end: raw import
// (phase1) -> Pass A/B -> persist into a ModelStore -> compute Circuits ->
// export via hjson2 -> reimport the exported .hjson tree through the same
// Pass A/B pipeline into a second ModelStore -> compute Circuits again ->
// assert Node count, Edge count, Circuit count, and the Circuit size
// histogram all match exactly between the two runs.
import (
	"path/filepath"
	"sort"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	exporthjson "gitlab.com/openk-nsc/jag/internal/exporter/hjson2"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// modelSink persists Attribute/Geometry batches straight into a ModelStore
// — unlike internal/impl/common's own nopSink-based tests, this round
// trip actually needs Sachdaten (e.g. Equipment.Class, IdentifiedObject.name)
// and Geometry to survive, since hjson2's exporter reads them back out of
// the ModelStore to reconstruct the .hjson tree.
type modelSink struct {
	model *sqlite.ModelStore
}

func (s modelSink) WriteAttributes(batch []coremodel.Attribute) error {
	return s.model.UpsertAttributes(batch)
}

func (s modelSink) WriteGeometries(batch []coremodel.Geometry) error {
	return s.model.UpsertGeometry(batch)
}

// runPassAB imports files (CGMES or NSC dialect) via phase1, then runs the
// production Pass A/B pipeline (common.RunPassA/common.RunPassB),
// persisting every batch straight into a fresh in-memory ModelStore. The
// caller owns and must Close the returned *sqlite.StagingStore.
func runPassAB(t *testing.T, files []string, isNSC bool) *sqlite.StagingStore {
	t.Helper()

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	var result phase1.Result
	if isNSC {
		result, err = phase1.RunNSCFiles(store, files)
	} else {
		result, err = phase1.RunCGMESFiles(store, files)
	}
	if err != nil {
		store.Close()
		t.Fatalf("phase1 Run*Files: %v", err)
	}
	if len(result.Errors) != 0 {
		store.Close()
		t.Fatalf("phase1 reported %d errors: %+v", len(result.Errors), result.Errors)
	}

	runPassABOnStore(t, store, result.Version)
	return store
}

// runHJSON2ImportPassAB mirrors runPassAB but imports a previously
// hjson2-exported directory tree (via phase1.RunHJSON2Files) instead of
// raw CIM/CGMES/NSC files, exactly like cmd/hjsonimport2.
func runHJSON2ImportPassAB(t *testing.T, root string) *sqlite.StagingStore {
	t.Helper()

	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	result, err := phase1.RunHJSON2Files(store, root)
	if err != nil {
		store.Close()
		t.Fatalf("phase1.RunHJSON2Files: %v", err)
	}
	if len(result.Errors) != 0 {
		store.Close()
		t.Fatalf("phase1.RunHJSON2Files reported %d errors: %+v", len(result.Errors), result.Errors)
	}

	runPassABOnStore(t, store, result.Version)
	return store
}

// runPassABOnStore runs Pass A then Pass B against an already-phase1'd
// store/version, persisting every batch into the store's own ModelStore
// (mirroring cmd/phase2check/cmd/hjsonimport2's production wiring).
func runPassABOnStore(t *testing.T, store *sqlite.StagingStore, version uint64) {
	t.Helper()

	modelStore := store.Model()
	flags := store.Flags()
	sink := modelSink{model: modelStore}

	err := common.RunPassA(store, version, 1000, 0, 0, sink, flags, false, func(b *common.BatchResult) error {
		if err := modelStore.UpsertContainers(b.Containers); err != nil {
			return err
		}
		if err := modelStore.UpsertEquipment(b.Equipment); err != nil {
			return err
		}
		if err := modelStore.UpsertNodes(b.Nodes); err != nil {
			return err
		}
		if err := modelStore.UpsertEdges(b.Edges); err != nil {
			return err
		}
		owned := make(map[string]map[string]string, len(b.Groups))
		for owner, groups := range b.Groups {
			owned[owner] = groups
		}
		return modelStore.UpsertElectricalGroups(owned)
	})
	if err != nil {
		store.Close()
		t.Fatalf("RunPassA: %v", err)
	}

	passB, err := common.RunPassB(store, version, 1000, 0, 0, sink, flags, func(b *common.PassBACLineBatchResult) error {
		if err := modelStore.UpsertContainers(b.Containers); err != nil {
			return err
		}
		if err := modelStore.UpsertEquipment(b.Equipment); err != nil {
			return err
		}
		if err := modelStore.UpsertNodes(b.Nodes); err != nil {
			return err
		}
		if err := modelStore.UpsertEdges(b.Edges); err != nil {
			return err
		}
		if err := modelStore.UpsertAttributes(b.Attributes); err != nil {
			return err
		}
		return modelStore.UpsertElectricalGroups(map[string]map[string]string{b.OwnerID: b.Groups})
	})
	if err != nil {
		store.Close()
		t.Fatalf("RunPassB: %v", err)
	}
	if err := modelStore.UpsertContainers(passB.Containers); err != nil {
		store.Close()
		t.Fatalf("persisting pass B containers: %v", err)
	}
	if err := modelStore.UpsertEquipment(passB.Equipment); err != nil {
		store.Close()
		t.Fatalf("persisting pass B equipment: %v", err)
	}
	if err := modelStore.UpsertNodes(passB.Nodes); err != nil {
		store.Close()
		t.Fatalf("persisting pass B nodes: %v", err)
	}
	if err := modelStore.UpsertEdges(passB.Edges); err != nil {
		store.Close()
		t.Fatalf("persisting pass B edges: %v", err)
	}
	if err := modelStore.UpsertAttributes(passB.Attributes); err != nil {
		store.Close()
		t.Fatalf("persisting pass B attributes: %v", err)
	}
	if err := modelStore.UpsertAttributes(passB.LineRefs); err != nil {
		store.Close()
		t.Fatalf("persisting pass B line refs: %v", err)
	}
	passBOwned := make(map[string]map[string]string, len(passB.Groups))
	for owner, groups := range passB.Groups {
		passBOwned[owner] = groups
	}
	if err := modelStore.UpsertElectricalGroups(passBOwned); err != nil {
		store.Close()
		t.Fatalf("persisting pass B electrical groups: %v", err)
	}
}

// circuitsOf reads back every Edge from the given ModelStore (paged, like
// scratch/circuits' allEdges helper), derives the Node set from the Edge
// endpoints (excluding GND), and computes Circuits (common.BuildCircuits)
// against them — this is exactly the check used manually this session to
// verify the busbar fix, now pinned as an automated test.
func circuitsOf(t *testing.T, store *sqlite.StagingStore) (nodeCount, edgeCount, circuitCount int, sizesDesc []int) {
	t.Helper()

	model := store.Model()
	var edges []coremodel.Edge
	after := ""
	for {
		page, err := model.AllEdges(after, 5000)
		if err != nil {
			t.Fatalf("AllEdges: %v", err)
		}
		edges = append(edges, page...)
		if len(page) < 5000 {
			break
		}
		after = page[len(page)-1].EquipmentID
	}

	seen := map[string]bool{}
	var nodes []coremodel.Node
	for _, e := range edges {
		for _, n := range []string{e.Terminal1NodeID, e.Terminal2NodeID} {
			if n == "" || n == common.GNDNodeID || seen[n] {
				continue
			}
			seen[n] = true
			nodes = append(nodes, coremodel.Node{EquipmentID: n})
		}
	}

	circuits, _, _, err := common.BuildCircuits(store, 1, nodes, edges, nil)
	if err != nil {
		t.Fatalf("BuildCircuits: %v", err)
	}

	sizes := map[int]int{}
	for _, c := range circuits {
		sizes[len(c.Nodes)]++
	}
	var distinctSizes []int
	for sz := range sizes {
		distinctSizes = append(distinctSizes, sz)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(distinctSizes)))
	for _, sz := range distinctSizes {
		for i := 0; i < sizes[sz]; i++ {
			sizesDesc = append(sizesDesc, sz)
		}
	}

	return len(nodes), len(edges), len(circuits), sizesDesc
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func globXMLFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .xml files found in %s", dir)
	}
	sort.Strings(files)
	return files
}

func TestBusbarRoundTripAgainstRealDatasets(t *testing.T) {
	tests := []struct {
		name  string
		dir   string // directory to glob *.xml from; ignored if file is set
		file  string // single file, for NSC datasets that must be imported alone
		isNSC bool
	}{
		{name: "ReliCapGrid_Espheim", dir: filepath.Join("..", "..", "..", "examples", "cgmes", "ReliCapGrid_Espheim")},
		{name: "MicroGrid_NL_BusCoupler", dir: filepath.Join("..", "..", "..", "examples", "cgmes", "MicroGrid_NL_BusCoupler")},
		{name: "MiniGrid_NodeBreaker_Switchgear", dir: filepath.Join("..", "..", "..", "examples", "cgmes", "MiniGrid_NodeBreaker_Switchgear")},
		{name: "Telemark_LV_Fuse", dir: filepath.Join("..", "..", "..", "examples", "cgmes", "Telemark_LV_Fuse")},
		{name: "pandapower-cim", dir: filepath.Join("..", "..", "..", "examples", "pandapower-cim")},
		{name: "pf-cim-beispiel-ortsnetz", dir: filepath.Join("..", "..", "..", "examples", "pf-cim-beispiel-ortsnetz")},
		{
			name:  "NSC example_as_cim",
			file:  filepath.Join("..", "..", "..", "examples", "nsc", "example_as_cim.xml"),
			isNSC: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var files []string
			if tt.file != "" {
				files = []string{tt.file}
			} else {
				files = globXMLFiles(t, tt.dir)
			}

			origStore := runPassAB(t, files, tt.isNSC)
			defer origStore.Close()
			origNodes, origEdges, origCircuits, origSizes := circuitsOf(t, origStore)
			if origCircuits == 0 {
				t.Fatalf("dataset produced 0 circuits — test fixture likely broken")
			}

			snap, err := exporthjson.Load(origStore.Model())
			if err != nil {
				t.Fatalf("hjson2 Load: %v", err)
			}
			outputs, err := exporthjson.Build(snap, "default")
			if err != nil {
				t.Fatalf("hjson2 Build: %v", err)
			}
			dir := t.TempDir()
			if err := exporthjson.Write(dir, outputs); err != nil {
				t.Fatalf("hjson2 Write: %v", err)
			}

			reStore := runHJSON2ImportPassAB(t, dir)
			defer reStore.Close()
			reNodes, reEdges, reCircuits, reSizes := circuitsOf(t, reStore)

			if reNodes != origNodes {
				t.Errorf("Node count after hjson2 round trip = %d, want %d (unchanged)", reNodes, origNodes)
			}
			if reEdges != origEdges {
				t.Errorf("Edge count after hjson2 round trip = %d, want %d (unchanged)", reEdges, origEdges)
			}
			if reCircuits != origCircuits {
				t.Errorf("Circuit count after hjson2 round trip = %d, want %d (unchanged) — a busbar likely fragmented into multiple Nodes again", reCircuits, origCircuits)
			}
			if !equalIntSlices(reSizes, origSizes) {
				t.Errorf("Circuit size histogram after hjson2 round trip = %v, want %v (unchanged)", reSizes, origSizes)
			}
		})
	}
}
