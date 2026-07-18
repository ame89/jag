package common

import (
	"sort"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

// capturingSink collects every Attribute batch handed to it, for test
// assertions — unlike pass_a_pipeline_test.go's nopSink (which discards
// everything, since that test only checks Node/Edge/Circuit results).
type capturingSink struct {
	attrs []coremodel.Attribute
}

func (s *capturingSink) WriteAttributes(batch []coremodel.Attribute) error {
	s.attrs = append(s.attrs, batch...)
	return nil
}
func (s *capturingSink) WriteGeometries(_ []coremodel.Geometry) error { return nil }

// byOwnerKey groups a flat Attribute slice into owner -> key -> ordered
// values, mirroring how internal/exporter/hjson's buildAttributes groups
// them for rendering (see that package's doc comment on multi-value keys).
func byOwnerKey(attrs []coremodel.Attribute) map[string]map[string][]interface{} {
	out := map[string]map[string][]interface{}{}
	for _, a := range attrs {
		if out[a.OwnerID] == nil {
			out[a.OwnerID] = map[string][]interface{}{}
		}
		out[a.OwnerID][string(a.Key)] = append(out[a.OwnerID][string(a.Key)], a.Value)
	}
	return out
}

// satellitesOf extracts ownerID's AttributeKeySatellite entries from a flat
// Attribute slice, decoded into (class, attributes) pairs for test
// assertions — mirrors internal/exporter/hjson's buildSatellites.
func satellitesOf(attrs []coremodel.Attribute, ownerID string) []struct {
	Class      string
	Attributes map[string]interface{}
} {
	var out []struct {
		Class      string
		Attributes map[string]interface{}
	}
	for _, a := range attrs {
		if a.OwnerID != ownerID || a.Key != AttributeKeySatellite {
			continue
		}
		obj, ok := a.Value.(map[string]interface{})
		if !ok {
			continue
		}
		class, _ := obj["class"].(string)
		sattrs, _ := obj["attributes"].(map[string]interface{})
		out = append(out, struct {
			Class      string
			Attributes map[string]interface{}
		}{Class: class, Attributes: sattrs})
	}
	return out
}

// litRecord builds a non-reference (literal) StagingRecord.
func litRecord(id, class, attr, value string) model.StagingRecord {
	return model.StagingRecord{ID: id, Class: class, Attribute: attr, Value: value, IsReference: false}
}

// refRecord builds a reference StagingRecord (id.attr -> value).
func refRecord(id, class, attr, value string) model.StagingRecord {
	return model.StagingRecord{ID: id, Class: class, Attribute: attr, Value: value, IsReference: true}
}

// TestBuildAttributesFoldsPowerElectronicsUnitSatellite is the regression
// test for the real bug found 2026-07-18 (see sachdaten.go's
// isPowerElectronicsUnitSatellite doc comment): a PowerElectronicsUnit
// satellite (e.g. a Wallbox) carries its own Equipment.EquipmentContainer
// (like any ordinary Equipment) but container.go deliberately never gives
// it its own root/Equipment identity — its data must still be folded into
// the owning PowerElectronicsConnection's Sachdaten via the satellite
// walk, not dropped because of the (otherwise correct) "has its own
// EquipmentContainer -> belongs to someone else" heuristic.
func TestBuildAttributesFoldsPowerElectronicsUnitSatellite(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	records := []model.StagingRecord{
		// PEC1: the root Equipment.
		litRecord("PEC1", "PowerElectronicsConnection", "IdentifiedObject.name", "PEC1"),
		litRecord("PEC1", "PowerElectronicsConnection", "PowerElectronicsConnection.controllableResourceIdentifier", "CR1"),

		// WB1: a Wallbox satellite of PEC1 — has its own
		// Equipment.EquipmentContainer (pointing at house H1, never
		// itself resolved as a container in this test) AND the
		// PowerElectronicsUnit.PowerElectronicsConnection back-reference
		// that container.go/sachdaten.go recognize as "this is a PEC
		// satellite, not independent Equipment".
		refRecord("WB1", "Wallbox", "PowerElectronicsUnit.PowerElectronicsConnection", "PEC1"),
		refRecord("WB1", "Wallbox", "Equipment.EquipmentContainer", "H1"),
		litRecord("WB1", "Wallbox", "IdentifiedObject.name", "WB1"),
		litRecord("WB1", "Wallbox", "PowerElectronicsUnit.maxP", "8000"),

		// OTHER1: an ordinary, genuinely separate piece of Equipment that
		// PEC1 happens to reference via some non-topology attribute, but
		// which is NOT a PowerElectronicsUnit satellite. It must stay
		// excluded (regression check for the general
		// hasEquipmentContainerAttr protection, which this test's fix
		// must not weaken).
		refRecord("PEC1", "PowerElectronicsConnection", "PowerElectronicsConnection.SomeOtherRef", "OTHER1"),
		refRecord("OTHER1", "SomeOtherEquipment", "Equipment.EquipmentContainer", "H1"),
		litRecord("OTHER1", "SomeOtherEquipment", "IdentifiedObject.name", "OTHER1"),
	}
	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.EnsureIndexes(); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	sink := &capturingSink{}
	if err := BuildAttributes(store, 0, 1000, nil, []string{"PEC1"}, sink); err != nil {
		t.Fatalf("BuildAttributes: %v", err)
	}

	grouped := byOwnerKey(sink.attrs)
	pec1 := grouped["PEC1"]
	if pec1 == nil {
		t.Fatalf("no attributes collected for PEC1; got %#v", sink.attrs)
	}

	// PEC1's own IdentifiedObject.name stays a single, root-only value now
	// — WB1's own name/attributes must be bundled separately under
	// AttributeKeySatellite, never merged into PEC1's flat attributes.
	if names := pec1["IdentifiedObject.name"]; len(names) != 1 || names[0] != "PEC1" {
		t.Errorf("PEC1 IdentifiedObject.name = %#v, want [\"PEC1\"] (Wallbox's own name must not merge into the root's flat attributes)", names)
	}
	if got := pec1["PowerElectronicsUnit.maxP"]; got != nil {
		t.Errorf("PEC1 PowerElectronicsUnit.maxP = %#v, want absent (Wallbox's own value belongs under the satellite object, not PEC1's flat attributes)", got)
	}

	satellites := satellitesOf(sink.attrs, "PEC1")
	if len(satellites) != 1 {
		t.Fatalf("PEC1 satellites = %#v, want exactly 1 (the Wallbox)", satellites)
	}
	wb := satellites[0]
	if wb.Class != "Wallbox" {
		t.Errorf("satellite class = %q, want \"Wallbox\"", wb.Class)
	}
	if got := wb.Attributes["IdentifiedObject.name"]; got != "WB1" {
		t.Errorf("Wallbox satellite IdentifiedObject.name = %#v, want \"WB1\"", got)
	}
	if got := wb.Attributes["PowerElectronicsUnit.maxP"]; got != "8000" {
		t.Errorf("Wallbox satellite PowerElectronicsUnit.maxP = %#v, want \"8000\"", got)
	}

	if _, ok := pec1["SomeOtherRef.marker"]; ok {
		t.Errorf("unexpected SomeOtherRef.marker attribute present")
	}
	for _, sat := range satellites {
		if sat.Attributes["IdentifiedObject.name"] == "OTHER1" {
			t.Errorf("OTHER1's own IdentifiedObject.name leaked into PEC1's Sachdaten — hasEquipmentContainerAttr exclusion regressed for non-PowerElectronicsUnit satellites")
		}
	}
}

// TestBuildAttributesMultiValueKey verifies that several distinct satellite
// objects contributing to the SAME Sachdaten key (e.g. several
// DiscreteControlLimit satellites, each with their own
// DiscreteControlLimit.value) each get their own, independent
// AttributeKeySatellite entry — one row per satellite object (see
// core/model.Attribute's doc comment, "Multi-value keys produce multiple
// Attribute rows sharing the same OwnerID+Key" — here the shared key is
// AttributeKeySatellite itself), never collapsed together or merged into
// a single flat DiscreteControlLimit.value array on the owner.
func TestBuildAttributesMultiValueKey(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	records := []model.StagingRecord{
		litRecord("PEC1", "PowerElectronicsConnection", "IdentifiedObject.name", "PEC1"),
		refRecord("DCL1", "DiscreteControlLimit", "DiscreteControlLimit.PowerElectronicsConnection", "PEC1"),
		litRecord("DCL1", "DiscreteControlLimit", "DiscreteControlLimit.value", "25"),
		refRecord("DCL2", "DiscreteControlLimit", "DiscreteControlLimit.PowerElectronicsConnection", "PEC1"),
		litRecord("DCL2", "DiscreteControlLimit", "DiscreteControlLimit.value", "75"),
	}
	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.EnsureIndexes(); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	sink := &capturingSink{}
	if err := BuildAttributes(store, 0, 1000, nil, []string{"PEC1"}, sink); err != nil {
		t.Fatalf("BuildAttributes: %v", err)
	}

	satellites := satellitesOf(sink.attrs, "PEC1")
	if len(satellites) != 2 {
		t.Fatalf("PEC1 satellites = %#v, want 2 (one per DiscreteControlLimit)", satellites)
	}
	var values []string
	for _, sat := range satellites {
		if sat.Class != "DiscreteControlLimit" {
			t.Errorf("satellite class = %q, want \"DiscreteControlLimit\"", sat.Class)
		}
		if v, ok := sat.Attributes["DiscreteControlLimit.value"].(string); ok {
			values = append(values, v)
		}
	}
	sort.Strings(values)
	if len(values) != 2 || values[0] != "25" || values[1] != "75" {
		t.Errorf("DiscreteControlLimit.value values = %#v, want [25 75]", values)
	}
}

// TestProcessStationBatchHouseWithWallboxAndPhotoVoltaic is a realistic,
// end-to-end regression test for the Wallbox bug (2026-07-18): a single
// House with TWO PowerElectronicsConnections — one with a Wallbox
// satellite, one with a PhotoVoltaicUnit satellite — run through the real
// production pipeline entry point (ProcessStationBatch, exactly what
// RunPassA calls per batch), not just the lower-level BuildAttributes used
// above. Confirms both satellites' data is folded into the correct owning
// PEC's Sachdaten, with no cross-contamination between the two PECs even
// though both satellites share the same House container (which is a
// structuralClasses entry and must never bridge them together).
func TestProcessStationBatchHouseWithWallboxAndPhotoVoltaic(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	records := []model.StagingRecord{
		// PEC-WB: PowerElectronicsConnection behind a Wallbox (consumer).
		refRecord("PEC-WB", "PowerElectronicsConnection", "Equipment.EquipmentContainer", "H1"),
		litRecord("PEC-WB", "PowerElectronicsConnection", "IdentifiedObject.name", "PEC-WB"),
		refRecord("PEC-WB-T1", "Terminal", "Terminal.ConductingEquipment", "PEC-WB"),
		refRecord("PEC-WB-T1", "Terminal", "Terminal.ConnectivityNode", "CN-WB"),
		litRecord("PEC-WB-T1", "Terminal", "ACDCTerminal.sequenceNumber", "1"),
		refRecord("WB1", "Wallbox", "PowerElectronicsUnit.PowerElectronicsConnection", "PEC-WB"),
		refRecord("WB1", "Wallbox", "Equipment.EquipmentContainer", "H1"),
		litRecord("WB1", "Wallbox", "IdentifiedObject.name", "WB1"),
		litRecord("WB1", "Wallbox", "PowerElectronicsUnit.maxP", "8000"),

		// PEC-PV: a SEPARATE PowerElectronicsConnection behind a
		// PhotoVoltaicUnit (producer), same House.
		refRecord("PEC-PV", "PowerElectronicsConnection", "Equipment.EquipmentContainer", "H1"),
		litRecord("PEC-PV", "PowerElectronicsConnection", "IdentifiedObject.name", "PEC-PV"),
		refRecord("PEC-PV-T1", "Terminal", "Terminal.ConductingEquipment", "PEC-PV"),
		refRecord("PEC-PV-T1", "Terminal", "Terminal.ConnectivityNode", "CN-PV"),
		litRecord("PEC-PV-T1", "Terminal", "ACDCTerminal.sequenceNumber", "1"),
		refRecord("PV1", "PhotoVoltaicUnit", "PowerElectronicsUnit.PowerElectronicsConnection", "PEC-PV"),
		refRecord("PV1", "PhotoVoltaicUnit", "Equipment.EquipmentContainer", "H1"),
		litRecord("PV1", "PhotoVoltaicUnit", "IdentifiedObject.name", "PV1"),
		litRecord("PV1", "PhotoVoltaicUnit", "PowerElectronicsUnit.maxP", "6000"),
	}
	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.EnsureIndexes(); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	sink := &capturingSink{}
	result, err := ProcessStationBatch(store, 0, nil, []string{"H1"}, 1000, sink, nil, true)
	if err != nil {
		t.Fatalf("ProcessStationBatch: %v", err)
	}

	// Both PECs must resolve as single-terminal Zweipol Edges (Terminal 2 =
	// GND implied, per the auto-GND-wiring decision).
	wantEdges := map[string]bool{"PEC-WB": false, "PEC-PV": false}
	for _, e := range result.Edges {
		if _, ok := wantEdges[e.EquipmentID]; ok {
			wantEdges[e.EquipmentID] = true
		}
	}
	for id, found := range wantEdges {
		if !found {
			t.Errorf("expected an Edge for %s, none found in %+v", id, result.Edges)
		}
	}

	grouped := byOwnerKey(sink.attrs)

	pecWB := grouped["PEC-WB"]
	if got := pecWB["IdentifiedObject.name"]; len(got) != 1 || got[0] != "PEC-WB" {
		t.Errorf("PEC-WB IdentifiedObject.name = %#v, want [\"PEC-WB\"] (Wallbox's own name must not merge into the root's flat attributes)", got)
	}
	wbSatellites := satellitesOf(sink.attrs, "PEC-WB")
	if len(wbSatellites) != 1 {
		t.Fatalf("PEC-WB satellites = %#v, want exactly 1 (the Wallbox)", wbSatellites)
	}
	if wbSatellites[0].Class != "Wallbox" {
		t.Errorf("PEC-WB satellite class = %q, want \"Wallbox\"", wbSatellites[0].Class)
	}
	if got := wbSatellites[0].Attributes["PowerElectronicsUnit.maxP"]; got != "8000" {
		t.Errorf("Wallbox satellite PowerElectronicsUnit.maxP = %#v, want \"8000\"", got)
	}
	if got := wbSatellites[0].Attributes["IdentifiedObject.name"]; got != "WB1" {
		t.Errorf("Wallbox satellite IdentifiedObject.name = %#v, want \"WB1\"", got)
	}

	pecPV := grouped["PEC-PV"]
	if got := pecPV["IdentifiedObject.name"]; len(got) != 1 || got[0] != "PEC-PV" {
		t.Errorf("PEC-PV IdentifiedObject.name = %#v, want [\"PEC-PV\"] (PhotoVoltaicUnit's own name must not merge into the root's flat attributes)", got)
	}
	pvSatellites := satellitesOf(sink.attrs, "PEC-PV")
	if len(pvSatellites) != 1 {
		t.Fatalf("PEC-PV satellites = %#v, want exactly 1 (the PhotoVoltaicUnit)", pvSatellites)
	}
	if pvSatellites[0].Class != "PhotoVoltaicUnit" {
		t.Errorf("PEC-PV satellite class = %q, want \"PhotoVoltaicUnit\"", pvSatellites[0].Class)
	}
	if got := pvSatellites[0].Attributes["PowerElectronicsUnit.maxP"]; got != "6000" {
		t.Errorf("PhotoVoltaicUnit satellite PowerElectronicsUnit.maxP = %#v, want \"6000\"", got)
	}
	if got := pvSatellites[0].Attributes["IdentifiedObject.name"]; got != "PV1" {
		t.Errorf("PhotoVoltaicUnit satellite IdentifiedObject.name = %#v, want \"PV1\"", got)
	}

	// Cross-contamination regression check: WB1's data must never appear
	// on PEC-PV, and PV1's data must never appear on PEC-WB, even though
	// both satellites share the same House container.
	for _, sat := range pvSatellites {
		if sat.Attributes["IdentifiedObject.name"] == "WB1" {
			t.Errorf("Wallbox's WB1 leaked into PEC-PV's Sachdaten (cross-contamination via shared House container)")
		}
	}
	for _, sat := range wbSatellites {
		if sat.Attributes["IdentifiedObject.name"] == "PV1" {
			t.Errorf("PhotoVoltaicUnit's PV1 leaked into PEC-WB's Sachdaten (cross-contamination via shared House container)")
		}
	}
}

// TestProcessStationBatchONSWithTransformerAndTwoFeeders is a realistic,
// end-to-end test for a Substation (ONS) containing a PowerTransformer
// (with two PowerTransformerEnds, one per winding side — see Konzept.md's
// Transformer decision: modeled as a single ordinary Zweipol Edge, NOT a
// four-terminal/Vierpol element, with side-specific attributes captured
// via the TransformerEnd satellite walk) plus two separate Feeders (NSC's
// Bay-equivalent container, see containertype.go), one on each side of the
// transformer. Verifies:
//   - both Feeders resolve as their own Bay containers under the
//     Substation (ResolveBatchContainers correctness for multiple bays);
//   - the PowerTransformer resolves as a single Zweipol Edge connecting the
//     OS-side node to the US-side node directly (no virtual star-point
//     node, per the decision);
//   - both PowerTransformerEnds' attributes (ratedU) fold into the
//     Transformer's own Sachdaten as a 2-value multi-value key (one value
//     per winding side) — the general satellite-walk mechanism already
//     handles this without any PowerElectronicsUnit-style special case,
//     since PowerTransformerEnd never carries its own
//     Equipment.EquipmentContainer.
func TestProcessStationBatchONSWithTransformerAndTwoFeeders(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	records := []model.StagingRecord{
		// Two Feeders (NSC's Bay-equivalent), both directly under the
		// Substation.
		refRecord("FEED-1", "Feeder", "Feeder.NormalEnergizingSubstation", "S1"),
		refRecord("FEED-2", "Feeder", "Feeder.NormalEnergizingSubstation", "S1"),

		// PowerTransformer TR1, placed in Feeder 1 (the incoming/OS-side
		// feeder) — matches real CGMES wiring: BOTH of TR1's own Terminals
		// reference TR1 directly (Terminal.ConductingEquipment), never a
		// PowerTransformerEnd (confirmed against
		// examples/cgmes/ReliCapGrid_Espheim's real EQ profile).
		refRecord("TR1", "PowerTransformer", "Equipment.EquipmentContainer", "FEED-1"),
		litRecord("TR1", "PowerTransformer", "IdentifiedObject.name", "TR1"),
		refRecord("TR1-T1", "Terminal", "Terminal.ConductingEquipment", "TR1"),
		refRecord("TR1-T1", "Terminal", "Terminal.ConnectivityNode", "CN-OS"),
		litRecord("TR1-T1", "Terminal", "ACDCTerminal.sequenceNumber", "1"),
		refRecord("TR1-T2", "Terminal", "Terminal.ConductingEquipment", "TR1"),
		refRecord("TR1-T2", "Terminal", "Terminal.ConnectivityNode", "CN-US"),
		litRecord("TR1-T2", "Terminal", "ACDCTerminal.sequenceNumber", "2"),

		// Two PowerTransformerEnds (one per winding side) — satellites of
		// TR1 via the ordinary (non-Equipment) reference attribute
		// PowerTransformerEnd.PowerTransformer, exactly like real CGMES.
		refRecord("PTE-OS", "PowerTransformerEnd", "PowerTransformerEnd.PowerTransformer", "TR1"),
		litRecord("PTE-OS", "PowerTransformerEnd", "IdentifiedObject.name", "TR1-OS"),
		litRecord("PTE-OS", "PowerTransformerEnd", "PowerTransformerEnd.ratedU", "20000"),
		refRecord("PTE-US", "PowerTransformerEnd", "PowerTransformerEnd.PowerTransformer", "TR1"),
		litRecord("PTE-US", "PowerTransformerEnd", "IdentifiedObject.name", "TR1-US"),
		litRecord("PTE-US", "PowerTransformerEnd", "PowerTransformerEnd.ratedU", "400"),

		// Fuse FU1 in Feeder 2 (the outgoing/US-side feeder), giving that
		// feeder some real Equipment and completing the electrical path
		// from TR1's US Terminal through to a second node.
		refRecord("FU1", "Fuse", "Equipment.EquipmentContainer", "FEED-2"),
		litRecord("FU1", "Fuse", "IdentifiedObject.name", "FU1"),
		refRecord("FU1-T1", "Terminal", "Terminal.ConductingEquipment", "FU1"),
		refRecord("FU1-T1", "Terminal", "Terminal.ConnectivityNode", "CN-US"),
		litRecord("FU1-T1", "Terminal", "ACDCTerminal.sequenceNumber", "1"),
		refRecord("FU1-T2", "Terminal", "Terminal.ConductingEquipment", "FU1"),
		refRecord("FU1-T2", "Terminal", "Terminal.ConnectivityNode", "CN-OUT"),
		litRecord("FU1-T2", "Terminal", "ACDCTerminal.sequenceNumber", "2"),
	}
	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.EnsureIndexes(); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	sink := &capturingSink{}
	result, err := ProcessStationBatch(store, 0, []string{"S1"}, nil, 1000, sink, nil, true)
	if err != nil {
		t.Fatalf("ProcessStationBatch: %v", err)
	}

	if len(result.Violations) != 0 {
		t.Errorf("unexpected Phase 3 violations: %+v", result.Violations)
	}

	// Both Feeders must resolve as their own Bay containers, both children
	// of the Substation.
	bayCount := 0
	for _, c := range result.Containers {
		if c.Type == ContainerTypeBay {
			bayCount++
			if c.ParentID != "S1" {
				t.Errorf("bay container %s has ParentID %q, want S1", c.ID, c.ParentID)
			}
		}
	}
	if bayCount != 2 {
		t.Errorf("expected 2 bay containers (FEED-1/FEED-2), got %d: %+v", bayCount, result.Containers)
	}

	// TR1 must resolve as a single Zweipol Edge OS -> US, not a Vierpol/
	// star-point construct.
	var trEdge *coremodel.Edge
	for i := range result.Edges {
		if result.Edges[i].EquipmentID == "TR1" {
			trEdge = &result.Edges[i]
		}
	}
	if trEdge == nil {
		t.Fatalf("no Edge found for TR1 in %+v", result.Edges)
	}
	gotNodes := map[string]bool{trEdge.Terminal1NodeID: true, trEdge.Terminal2NodeID: true}
	if !gotNodes["CN-OS"] || !gotNodes["CN-US"] {
		t.Errorf("TR1 edge = %+v, want it to connect CN-OS and CN-US directly", trEdge)
	}

	// Both PowerTransformerEnds must fold into TR1's own Sachdaten as
	// separate satellite objects — one per winding side, each carrying
	// its own ratedU.
	satellites := satellitesOf(sink.attrs, "TR1")
	if len(satellites) != 2 {
		t.Fatalf("TR1 satellites = %#v, want 2 (one per winding side)", satellites)
	}
	var ratedU []string
	for _, sat := range satellites {
		if sat.Class != "PowerTransformerEnd" {
			t.Errorf("TR1 satellite class = %q, want \"PowerTransformerEnd\"", sat.Class)
		}
		if v, ok := sat.Attributes["PowerTransformerEnd.ratedU"].(string); ok {
			ratedU = append(ratedU, v)
		}
	}
	sort.Strings(ratedU)
	if len(ratedU) != 2 || ratedU[0] != "20000" || ratedU[1] != "400" {
		t.Errorf("TR1 PowerTransformerEnd.ratedU values = %#v, want [20000 400] (order-independent, numeric sort not required)", ratedU)
	}
}

// TestProcessStationBatchFlushesContainerAttributes is a regression test
// for a real, pre-existing bug found 2026-07-19 while reviewing HJSON
// exporter round-trips: ResolveBatchContainers already computes
// res.Attributes (a Substation/Building root's own AttributeKeyName), but
// ProcessStationBatch never forwarded it to sink — the station/house's own
// name was silently dropped, even though the generic OwnerID-keyed
// Attribute channel and the HJSON exporter/importer both already fully
// support container-level Sachdaten. Verifies both a Substation root and a
// House root end up with their own "name" Sachdaten in the sink's output.
func TestProcessStationBatchFlushesContainerAttributes(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()

	records := []model.StagingRecord{
		litRecord("S1", "Substation", "IdentifiedObject.name", "Substation Nord"),
		refRecord("FEED-1", "Feeder", "Feeder.NormalEnergizingSubstation", "S1"),
		refRecord("FU1", "Fuse", "Equipment.EquipmentContainer", "FEED-1"),
		litRecord("FU1", "Fuse", "IdentifiedObject.name", "FU1"),
		refRecord("FU1-T1", "Terminal", "Terminal.ConductingEquipment", "FU1"),
		refRecord("FU1-T1", "Terminal", "Terminal.ConnectivityNode", "CN1"),
		litRecord("FU1-T1", "Terminal", "ACDCTerminal.sequenceNumber", "1"),
		refRecord("FU1-T2", "Terminal", "Terminal.ConductingEquipment", "FU1"),
		refRecord("FU1-T2", "Terminal", "Terminal.ConnectivityNode", "CN2"),
		litRecord("FU1-T2", "Terminal", "ACDCTerminal.sequenceNumber", "2"),

		litRecord("H1", "Building", "IdentifiedObject.name", "Haus Nord 1"),
		refRecord("PEC1", "PowerElectronicsConnection", "Equipment.EquipmentContainer", "H1"),
		litRecord("PEC1", "PowerElectronicsConnection", "IdentifiedObject.name", "PEC1"),
		refRecord("PEC1-T1", "Terminal", "Terminal.ConductingEquipment", "PEC1"),
		refRecord("PEC1-T1", "Terminal", "Terminal.ConnectivityNode", "CN-PEC1"),
		litRecord("PEC1-T1", "Terminal", "ACDCTerminal.sequenceNumber", "1"),
	}
	if err := store.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.EnsureIndexes(); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	sink := &capturingSink{}
	if _, err := ProcessStationBatch(store, 0, []string{"S1"}, []string{"H1"}, 1000, sink, nil, true); err != nil {
		t.Fatalf("ProcessStationBatch: %v", err)
	}

	grouped := byOwnerKey(sink.attrs)
	if got := grouped["S1"][string(AttributeKeyName)]; len(got) != 1 || got[0] != "Substation Nord" {
		t.Errorf("S1 %s = %#v, want [\"Substation Nord\"]", AttributeKeyName, got)
	}
	if got := grouped["H1"][string(AttributeKeyName)]; len(got) != 1 || got[0] != "Haus Nord 1" {
		t.Errorf("H1 %s = %#v, want [\"Haus Nord 1\"]", AttributeKeyName, got)
	}
}
