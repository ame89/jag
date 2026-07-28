package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	coremodel "github.com/ame89/jag/internal/core/model"
)

// TestGoldenSnapshotAgainstRealDatasets is a stronger companion to
// TestBuildCircuitsAgainstRealDatasets/*CGMES3/*CigreMV/*NSC/
// *PfCimBeispielOrtsnetz/*PandapowerCIM in circuits_dataset_test.go.
//
// Those tests only pin *aggregate* numbers (Equipment/Busbar/Circuit
// counts, size distributions) — a refactoring that silently reassigns
// which Node an Edge connects to, drops/renames a Container, or changes an
// Equipment's Node/Edge role could still pass every one of those
// assertions as long as the aggregate counts happen to come out the same.
//
// This test instead dumps a full, deterministic (sorted, plain-text)
// snapshot of every Node, Edge, and Container produced by the Pass A/B
// pipeline (BuildContainers -> ResolveTerminals -> MergeJunctionNodes ->
// MergeBusbarSectionNodes -> BuildNodesAndEdges, i.e. the same pipeline
// buildPipelineForFiles in circuits_dataset_test.go already runs) for every
// real example dataset, and byte-compares it against a checked-in golden
// file under testdata/golden/. ANY change to which Nodes/Edges/Containers
// exist, their IDs, their Kind, their Terminal wiring, or their Container
// Type/ParentID will show up as an exact diff here — this is the intended
// "examples stay stable under refactoring" safety net.
//
// Golden files are checked in under
// internal/impl/common/testdata/golden/<dataset>.golden. To (re)generate
// them after an intentional model-building change, run:
//
//	JAG_UPDATE_GOLDEN=1 go test ./internal/impl/common/... -run TestGoldenSnapshotAgainstRealDatasets -v
//
// and review the resulting diff in the golden files like any other code
// change before committing it — a passing regeneration does NOT by itself
// prove the change is correct, only that the snapshot now matches the new
// (reviewed) behavior.
func TestGoldenSnapshotAgainstRealDatasets(t *testing.T) {
	tests := []struct {
		name  string
		dir   string
		files []string // nil = glob *.xml under dir; otherwise these files (relative to dir) are imported one call per file (NSC)
		isNSC bool
	}{
		{name: "cgmes_BaseCase_Complete", dir: filepath.Join("cgmes", "BaseCase_Complete")},
		{name: "cgmes_MicroGrid_NL_BusCoupler", dir: filepath.Join("cgmes", "MicroGrid_NL_BusCoupler")},
		{name: "cgmes_MiniGrid_NodeBreaker_Switchgear", dir: filepath.Join("cgmes", "MiniGrid_NodeBreaker_Switchgear")},
		{name: "cgmes_ReliCapGrid_Espheim", dir: filepath.Join("cgmes", "ReliCapGrid_Espheim")},
		{name: "cgmes_Telemark_LV_Fuse", dir: filepath.Join("cgmes", "Telemark_LV_Fuse")},
		{name: "cgmes3_MicroGrid", dir: filepath.Join("cgmes3", "MicroGrid")},
		{name: "cgmes3_MiniGrid", dir: filepath.Join("cgmes3", "MiniGrid")},
		{name: "cgmes3_SmallGrid", dir: filepath.Join("cgmes3", "SmallGrid")},
		{name: "cgmes3_Svedala", dir: filepath.Join("cgmes3", "Svedala")},
		{name: "cigre_mv", dir: "cigre_mv"},
		{name: "pf_cim_beispiel_ortsnetz", dir: "pf-cim-beispiel-ortsnetz"},
		{name: "pandapower_cim", dir: "pandapower-cim"},
		{name: "nsc_example_as_cim", dir: "nsc", files: []string{"example_as_cim.xml"}, isNSC: true},
		{name: "nsc_ons_2kvs_3muffen_9haeuser", dir: "nsc", files: []string{"Eine_ONS_mit_2_KVS_3_Muffen_und_9_Häuser_ohne_Trafo_MD.xml"}, isNSC: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exampleDir := filepath.Join("..", "..", "..", "examples", tt.dir)

			var files []string
			if tt.files == nil {
				var err error
				files, err = filepath.Glob(filepath.Join(exampleDir, "*.xml"))
				if err != nil {
					t.Fatalf("glob %s: %v", exampleDir, err)
				}
				if len(files) == 0 {
					t.Fatalf("no .xml files found in %s", exampleDir)
				}
				sort.Strings(files)
			} else {
				for _, f := range tt.files {
					files = append(files, filepath.Join(exampleDir, f))
				}
			}

			store, _, nodes, edges, containers, _, _ := buildPipelineForFiles(t, files, tt.isNSC)
			defer store.Close()

			got := snapshotModel(nodes, edges, containers.Containers)
			compareGolden(t, tt.name, got)
		})
	}
}

// snapshotModel renders a deterministic, sorted plain-text dump of Nodes,
// Edges, and Containers. Sorting is by primary key (EquipmentID/ID) so the
// output does not depend on any incidental map/slice iteration order in
// the pipeline — only genuine content changes should ever change the
// output.
func snapshotModel(nodes []coremodel.Node, edges []coremodel.Edge, containers []coremodel.Container) string {
	var b strings.Builder

	nodeLines := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeLines = append(nodeLines, fmt.Sprintf("%s\t%s", n.EquipmentID, n.Kind))
	}
	sort.Strings(nodeLines)
	fmt.Fprintf(&b, "NODES (%d)\n", len(nodeLines))
	for _, l := range nodeLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}

	edgeLines := make([]string, 0, len(edges))
	for _, e := range edges {
		edgeLines = append(edgeLines, fmt.Sprintf("%s\t%s\t%s", e.EquipmentID, e.Terminal1NodeID, e.Terminal2NodeID))
	}
	sort.Strings(edgeLines)
	fmt.Fprintf(&b, "\nEDGES (%d)\n", len(edgeLines))
	for _, l := range edgeLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}

	contLines := make([]string, 0, len(containers))
	for _, c := range containers {
		contLines = append(contLines, fmt.Sprintf("%s\t%s\t%s", c.ID, c.Type, c.ParentID))
	}
	sort.Strings(contLines)
	fmt.Fprintf(&b, "\nCONTAINERS (%d)\n", len(contLines))
	for _, l := range contLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}

	return b.String()
}

// compareGolden compares got against the checked-in golden file for name
// (internal/impl/common/testdata/golden/<name>.golden). If the
// JAG_UPDATE_GOLDEN environment variable is set (to any non-empty value),
// the golden file is (over)written with got instead of compared — used to
// intentionally regenerate golden files after a reviewed model-building
// change, never as part of a normal test run.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name+".golden")

	if os.Getenv("JAG_UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
		t.Logf("wrote golden file %s (JAG_UPDATE_GOLDEN set)", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s not found (run with JAG_UPDATE_GOLDEN=1 to generate it, then review + commit it): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("snapshot for %s does not match golden file %s.\nThis means the Node/Edge/Container set produced by the pipeline changed.\nIf this is an intentional, reviewed change, regenerate with:\n  JAG_UPDATE_GOLDEN=1 go test ./internal/impl/common/... -run TestGoldenSnapshotAgainstRealDatasets/%s -v\nand review the resulting diff before committing.\n\n--- got ---\n%s", name, path, name, got)
	}
}
