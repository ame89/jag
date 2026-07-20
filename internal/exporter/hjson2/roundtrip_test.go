package hjson2_test

// End-to-end regression tests for the HJSON Fachmodell exporter/importer
// pair (internal/exporter/hjson + internal/importer/hjson), added
// 2026-07-19 after a series of real bugs found while exporting the NSC
// example dataset: (1) a PowerElectronicsUnit satellite (Wallbox) folded
// into its owning PowerElectronicsConnection's Sachdaten was excluded by
// the satellite walk's own-Equipment check, (2) multi-value Sachdaten keys
// (multiple Attribute rows sharing one OwnerID+Key) were silently
// collapsed to a single last-write-wins scalar by the exporter, (3) the
// HJSON writer had no array-rendering support at all, and (4) a
// Substation/House's own container-level Sachdaten (its "name") was
// computed by ResolveBatchContainers but never flushed to the sink.
// internal/impl/common/sachdaten_test.go already covers (1)/(4) at the
// Sachdaten-pipeline level; this file instead drives the actual
// ModelStore -> Load -> Build -> Write -> (re-)Emit round trip so the
// HJSON-specific layers (build.go/write.go/resolve.go) that were the
// direct site of bugs (2) and (3) get their own dedicated test coverage,
// which they previously had none of (only manual verification against the
// real NSC dataset).

import (
	"os"
	"path/filepath"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	exporthjson "gitlab.com/openk-nsc/jag/internal/exporter/hjson2"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	importhjson "gitlab.com/openk-nsc/jag/internal/importer/hjson2"
	importmodel "gitlab.com/openk-nsc/jag/internal/importer/model"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// byOwnerKey groups a flat []model.StagingRecord's literal attribute rows
// by (ID, Attribute) -> ordered values, mirroring
// internal/impl/common/sachdaten_test.go's own helper of the same name but
// operating on the importer's raw StagingRecord shape (this package can't
// import that internal test helper directly, and duplicating a five-line
// grouping helper is simpler than exporting it).
func byOwnerKey(recs []importmodel.StagingRecord) map[string]map[string][]string {
	out := map[string]map[string][]string{}
	for _, r := range recs {
		if r.IsReference {
			continue
		}
		if out[r.ID] == nil {
			out[r.ID] = map[string][]string{}
		}
		out[r.ID][r.Attribute] = append(out[r.ID][r.Attribute], r.Value)
	}
	return out
}

// TestExportImportRoundTrip builds a small but realistic model directly in
// a ModelStore (Substation with a Bay/Fuse, a House with a
// PowerElectronicsConnection carrying a Wallbox-satellite-style
// multi-value attribute, plus container-level name Sachdaten for both
// roots), exports it to a temp directory via Build+Write, then re-imports
// the written .hjson files via importhjson.Emit and verifies every
// bug-prone detail survives: multi-value array attributes, the
// PowerElectronicsUnit-style single-value attribute, and container-level
// name Sachdaten.
func TestExportImportRoundTrip(t *testing.T) {
	staging, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer staging.Close()
	ms := staging.Model()

	if err := ms.UpsertContainers([]coremodel.Container{
		{ID: "S1", Type: common.ContainerTypeSubstation},
		{ID: "FEED-1", Type: common.ContainerTypeBay, ParentID: "S1"},
		{ID: "H1", Type: common.ContainerTypeHouse},
	}); err != nil {
		t.Fatalf("UpsertContainers: %v", err)
	}
	if err := ms.UpsertEquipment([]coremodel.Equipment{
		{ID: "FU1", ContainerID: "FEED-1"},
		{ID: "PEC1", ContainerID: "H1"},
	}); err != nil {
		t.Fatalf("UpsertEquipment: %v", err)
	}
	if err := ms.UpsertEdges([]coremodel.Edge{
		{EquipmentID: "FU1", Terminal1NodeID: "CN1", Terminal2NodeID: "CN2"},
		{EquipmentID: "PEC1", Terminal1NodeID: "CN-PEC1", Terminal2NodeID: "GND"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}
	if err := ms.UpsertGeometry([]coremodel.Geometry{
		{OwnerID: "S1", OwnerKind: coremodel.GeometryOwnerContainer, Lat: 52.5, Lon: 13.4},
	}); err != nil {
		t.Fatalf("UpsertGeometry: %v", err)
	}
	if err := ms.UpsertAttributes([]coremodel.Attribute{
		{OwnerID: "S1", Key: common.AttributeKeyName, Value: "Substation Nord"},
		{OwnerID: "H1", Key: common.AttributeKeyName, Value: "Haus Nord 1"},

		{OwnerID: "FU1", Key: common.AttributeKeyClass, Value: "Fuse"},
		{OwnerID: "FU1", Key: "IdentifiedObject.name", Value: "FU1"},

		{OwnerID: "PEC1", Key: common.AttributeKeyClass, Value: "PowerElectronicsConnection"},
		{OwnerID: "PEC1", Key: "IdentifiedObject.name", Value: "PEC1"},
		// A folded Wallbox satellite: its own class + own attributes
		// bundled into one self-contained object value (see
		// AttributeKeySatellite's doc comment) — the exact shape of the
		// 2026-07-19 fix replacing the earlier ad-hoc
		// satellite_cim_class/parallel-array mechanism (which was the
		// exact shape of the 2026-07-18 bug found exporting the real NSC
		// dataset, see build.go's buildAttributes doc comment).
		{OwnerID: "PEC1", Key: common.AttributeKeySatellite, Value: map[string]interface{}{
			"class": "Wallbox",
			"attributes": map[string]interface{}{
				"IdentifiedObject.name":     "STEU-24",
				"PowerElectronicsUnit.maxP": "8000",
			},
		}},
	}); err != nil {
		t.Fatalf("UpsertAttributes: %v", err)
	}

	snap, err := exporthjson.Load(ms)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	outputs, err := exporthjson.Build(snap, "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(outputs) != 2 {
		t.Fatalf("Build produced %d FileOutputs, want 2 (S1 + H1)", len(outputs))
	}

	dir := t.TempDir()
	if err := exporthjson.Write(dir, outputs); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The written H-1 file must contain a proper HJSON array for the
	// multi-value IdentifiedObject.name key, not a malformed
	// %v-stringified blob (the exact write.go quoteValue bug found
	// 2026-07-18).
	houseFile := filepath.Join(dir, "default", "Haushalte", "H1.hjson")
	raw, err := os.ReadFile(houseFile)
	if err != nil {
		t.Fatalf("reading exported %s: %v", houseFile, err)
	}
	content := string(raw)
	if !contains(content, `satellites: [`) || !contains(content, `class: "Wallbox"`) ||
		!contains(content, `name: "STEU-24"`) || !contains(content, `maxP: "8000"`) {
		t.Errorf("exported %s does not contain the expected satellite object; got:\n%s", houseFile, content)
	}

	// The written S1 file must contain the exported geometry block (added
	// 2026-07-19 — a container's own coordinate previously had no HJSON
	// representation at all and was silently dropped on export).
	stationFile := filepath.Join(dir, "default", "ONS", "S1.hjson")
	stationRaw, err := os.ReadFile(stationFile)
	if err != nil {
		t.Fatalf("reading exported %s: %v", stationFile, err)
	}
	stationContent := string(stationRaw)
	if !contains(stationContent, "geometry:") || !contains(stationContent, "52.5") || !contains(stationContent, "13.4") {
		t.Errorf("exported %s does not contain the expected geometry block; got:\n%s", stationFile, stationContent)
	}

	// Re-import the exported directory and verify every bug-prone detail
	// round-tripped correctly.
	recs, errs, err := importhjson.Emit(1, dir)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("Emit reported errors: %+v", errs)
	}

	grouped := byOwnerKey(recs)

	// Container-level name Sachdaten (bug 4).
	if got := grouped["S1"][string(common.AttributeKeyName)]; len(got) != 1 || got[0] != "Substation Nord" {
		t.Errorf("S1 %s = %#v, want [\"Substation Nord\"]", common.AttributeKeyName, got)
	}
	if got := grouped["H1"][string(common.AttributeKeyName)]; len(got) != 1 || got[0] != "Haus Nord 1" {
		t.Errorf("H1 %s = %#v, want [\"Haus Nord 1\"]", common.AttributeKeyName, got)
	}

	// Equipment-local IDs ("PEC1"/"FU1", as originally stored in
	// ModelStore) are NOT already prefixed with their file's own
	// container ID, so resolveID's ID-prefixing rule (see resolve.go's
	// doc comment: "a name already starting with... another known
	// container's ID is used verbatim; anything else... gets the file's
	// own container ID prepended") re-prefixes them on re-import —
	// "PEC1" round-trips as "H1-PEC1", "FU1" as "S1-FU1". This is the
	// same, already-documented behavior real CIM mRIDs get too (verified
	// manually earlier this session against the real NSC dataset, e.g.
	// "PEC-24" -> "H-20-PEC-24"), not something introduced by this test.
	pec1ID, fu1ID := "H1-PEC1", "S1-FU1"

	// PEC1's own name is a plain single value (no more cross-satellite
	// merging, bugs 2/3's root cause).
	if got := grouped[pec1ID]["IdentifiedObject.name"]; len(got) != 1 || got[0] != "PEC1" {
		t.Errorf("%s IdentifiedObject.name = %#v, want [\"PEC1\"]", pec1ID, got)
	}
	if got := grouped[pec1ID]["PowerElectronicsUnit.maxP"]; len(got) != 0 {
		t.Errorf("%s PowerElectronicsUnit.maxP unexpectedly present on the owner itself: %#v (should only live on the synthesized satellite record)", pec1ID, got)
	}

	// The folded Wallbox satellite round-trips as its own synthesized
	// StagingRecord (satID = "<ownerID>-SAT<i>", see resolve.go's
	// addSatellites), carrying its own class and own attributes
	// untouched — this is what the real satellite walk
	// (internal/impl/common/sachdaten.go) will rediscover and re-fold on
	// Phase 2, achieving full export/import symmetry with no
	// special-casing (bug 1 and its 2026-07-19 redesign).
	satID := pec1ID + "-SAT0"
	var satClass string
	for _, rec := range recs {
		if rec.ID == satID {
			satClass = rec.Class
			break
		}
	}
	if satClass != "Wallbox" {
		t.Errorf("%s class = %q, want \"Wallbox\"", satID, satClass)
	}
	if got := grouped[satID]["IdentifiedObject.name"]; len(got) != 1 || got[0] != "STEU-24" {
		t.Errorf("%s IdentifiedObject.name = %#v, want [\"STEU-24\"]", satID, got)
	}
	if got := grouped[satID]["PowerElectronicsUnit.maxP"]; len(got) != 1 || got[0] != "8000" {
		t.Errorf("%s PowerElectronicsUnit.maxP = %#v, want [\"8000\"]", satID, got)
	}

	// The Fuse's own class and connects (Edge) round-trip too, as a basic
	// sanity check that the ordinary (non-bug-related) path still works.
	if got := grouped[fu1ID][string(common.AttributeKeyClass)]; len(got) != 0 {
		t.Errorf("%s %s unexpectedly present in re-imported Sachdaten: %#v (should only be surfaced via Equipment.Class on export, not re-emitted as a plain attribute)", fu1ID, common.AttributeKeyClass, got)
	}
	var fuTerminals int
	for _, r := range recs {
		if r.Class == "Terminal" && r.Attribute == "Terminal.ConductingEquipment" && r.Value == fu1ID {
			fuTerminals++
		}
	}
	if fuTerminals != 2 {
		t.Errorf("%s has %d Terminal.ConductingEquipment records after round-trip, want 2 (Zweipol)", fu1ID, fuTerminals)
	}

	// The container-level geometry round-trips as a synthesized
	// PowerSystemResource.Location -> Location -> PositionPoint chain
	// (see resolve.go's addGeometry), the exact CIM GL-profile shape
	// BuildGeometry (internal/impl/common/geometry.go) already knows how
	// to resolve on any subsequent Phase 2 run.
	var sawLocRef, sawXPos, sawYPos bool
	for _, rec := range recs {
		switch {
		case rec.ID == "S1" && rec.Attribute == "PowerSystemResource.Location" && rec.Value == "S1-LOC":
			sawLocRef = true
		case rec.ID == "S1-LOC-PP1" && rec.Attribute == "PositionPoint.xPosition" && rec.Value == "13.4":
			sawXPos = true
		case rec.ID == "S1-LOC-PP1" && rec.Attribute == "PositionPoint.yPosition" && rec.Value == "52.5":
			sawYPos = true
		}
	}
	if !sawLocRef || !sawXPos || !sawYPos {
		t.Errorf("S1 geometry did not round-trip as expected: locRef=%v xPos=%v yPos=%v", sawLocRef, sawXPos, sawYPos)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
