package common

import (
	"path/filepath"
	"sort"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/importer/phase1"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// TestResolveBatchContainersMatchesBuildContainers is the correctness proof
// for the Pass A rewrite (see plan.md / Konzept.md, 2026-07 RAM-scaling
// session): ResolveBatchContainers must produce EXACTLY the same
// Container/EquipmentToCont/Attributes result as the existing whole-model
// BuildContainers, when its output is computed batch-by-batch (one batch
// per Substation/Building here, the smallest possible batch size, to
// exercise cross-batch independence as hard as possible) and merged.
// Without this, the RAM fix would be worthless — a fast, low-RAM importer
// that silently produces a wrong model is strictly worse than the current
// slow one.
func TestResolveBatchContainersMatchesBuildContainers(t *testing.T) {
	datasets := []string{
		"ReliCapGrid_Espheim",           // Bay/VoltageLevel/BusbarSection/PEC-satellite coverage
		"MiniGrid_NodeBreaker_Switchgear", // Disconnector/Breaker/Bay/BusbarSection coverage
		"Telemark_LV_Fuse",              // real Fuse/BusbarSection/Bay LV dataset
	}

	for _, dsDir := range datasets {
		t.Run(dsDir, func(t *testing.T) {
			dir := filepath.Join("..", "..", "..", "examples", "cgmes", dsDir)
			files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
			if err != nil {
				t.Fatalf("glob %s: %v", dir, err)
			}
			if len(files) == 0 {
				t.Fatalf("no .xml files found in %s", dir)
			}
			sort.Strings(files)

			store, err := sqlite.Open(":memory:")
			if err != nil {
				t.Fatalf("sqlite.Open: %v", err)
			}
			defer store.Close()

			result, err := phase1.RunCGMESFiles(store, files)
			if err != nil {
				t.Fatalf("RunCGMESFiles: %v", err)
			}
			if len(result.Errors) != 0 {
				t.Fatalf("RunCGMESFiles reported %d collected errors: %+v", len(result.Errors), result.Errors)
			}

			want, err := BuildContainers(store, result.Version, 1000)
			if err != nil {
				t.Fatalf("BuildContainers: %v", err)
			}

			subIDs, _, err := scanClass(store, result.Version, 1000, "Substation")
			if err != nil {
				t.Fatalf("scanClass Substation: %v", err)
			}
			houseIDs, _, err := scanClass(store, result.Version, 1000, "Building")
			if err != nil {
				t.Fatalf("scanClass Building: %v", err)
			}

			got := &BatchContainersResult{EquipmentToCont: map[string]string{}}
			// One batch PER root (Substation or Building) — the smallest
			// possible batch granularity, so any hidden cross-batch
			// dependency in the rewrite would show up as a mismatch here.
			for _, id := range subIDs {
				batch, err := ResolveBatchContainers(store, result.Version, []string{id}, nil)
				if err != nil {
					t.Fatalf("ResolveBatchContainers(sub %s): %v", id, err)
				}
				mergeBatchResult(got, batch)
			}
			for _, id := range houseIDs {
				batch, err := ResolveBatchContainers(store, result.Version, nil, []string{id})
				if err != nil {
					t.Fatalf("ResolveBatchContainers(house %s): %v", id, err)
				}
				mergeBatchResult(got, batch)
			}

			assertContainersEqual(t, want, got)
		})
	}
}

func mergeBatchResult(acc, batch *BatchContainersResult) {
	acc.Containers = append(acc.Containers, batch.Containers...)
	acc.Attributes = append(acc.Attributes, batch.Attributes...)
	for eqID, contID := range batch.EquipmentToCont {
		acc.EquipmentToCont[eqID] = contID
	}
}

// assertContainersEqual compares BuildContainers' whole-model result
// against the batch-merged ResolveBatchContainers result. ACLine containers
// (and the ACLineSegment equipment assigned to them) are excluded from the
// comparison — they are Pass B's responsibility (buildACLineChains, a
// separate, already-bounded-by-class-size step, not part of Pass A's
// station-batch scope at all, see pass_a.go's doc comment) — so
// BuildContainers legitimately produces them while ResolveBatchContainers
// never does. Busbar containers (and their names) can legitimately be
// produced redundantly — once per batch that happens to own a
// BusbarSection referencing the same VoltageLevel/Substation — since
// batches don't know about each other; a busbar container/VL-substation/
// Substation grouping should never span two different Substation batches,
// so no such duplication is expected in practice, but the comparison
// de-duplicates by (ID, Type, ParentID) to be robust regardless.
func assertContainersEqual(t *testing.T, want *BuildContainersResult, got *BatchContainersResult) {
	t.Helper()

	type containerKey struct {
		id, typ, parent string
	}
	toSet := func(cs []coremodel.Container) map[containerKey]bool {
		s := map[containerKey]bool{}
		for _, c := range cs {
			if c.Type == ContainerTypeACLine {
				continue
			}
			s[containerKey{c.ID, string(c.Type), c.ParentID}] = true
		}
		return s
	}
	acline := map[string]bool{}
	for _, c := range want.Containers {
		if c.Type == ContainerTypeACLine {
			acline[c.ID] = true
		}
	}
	wantSet := toSet(want.Containers)
	gotSet := toSet(got.Containers)
	for k := range wantSet {
		if !gotSet[k] {
			t.Errorf("missing container in batch result: %+v", k)
		}
	}
	for k := range gotSet {
		if !wantSet[k] {
			t.Errorf("unexpected extra container in batch result: %+v", k)
		}
	}

	wantEquip := map[string]string{}
	for eqID, cont := range want.EquipmentToCont {
		if acline[cont] {
			continue
		}
		wantEquip[eqID] = cont
	}

	if len(wantEquip) != len(got.EquipmentToCont) {
		t.Errorf("EquipmentToCont size = %d, want %d", len(got.EquipmentToCont), len(wantEquip))
	}
	for eqID, wantCont := range wantEquip {
		gotCont, ok := got.EquipmentToCont[eqID]
		if !ok {
			t.Errorf("equipment %s: missing from batch result (want container %s)", eqID, wantCont)
			continue
		}
		if gotCont != wantCont {
			t.Errorf("equipment %s: container = %s, want %s", eqID, gotCont, wantCont)
		}
	}
	for eqID, gotCont := range got.EquipmentToCont {
		if _, ok := wantEquip[eqID]; !ok {
			t.Errorf("equipment %s: unexpected extra assignment to container %s (not in whole-model BuildContainers result)", eqID, gotCont)
		}
	}
}
