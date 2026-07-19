package postgres

import (
	"os"
	"testing"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// testDSN returns the PostgreSQL DSN to run these tests against, read from
// JAG_TEST_POSTGRES_DSN. Unlike internal/sqlite's tests (which can always
// use Open(":memory:")), this package requires a live PostgreSQL server —
// not available in every environment/sandbox — so every test here skips
// cleanly (not fails) when the env var isn't set, keeping `go test ./...`
// green without Docker/Postgres reachable.
//
// Example for local testing against the jag-pg-test Docker container used
// during development:
//
//	$env:JAG_TEST_POSTGRES_DSN = "postgres://postgres:jag@localhost:55432/jag?sslmode=disable"
//	go test ./internal/postgres/...
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("JAG_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("JAG_TEST_POSTGRES_DSN not set, skipping PostgreSQL integration test")
	}
	return dsn
}

// openTestStore opens a fresh StagingStore against the test DSN and wipes
// every table this package owns first, so tests don't interfere with each
// other or with leftover data from a prior run against the same live
// database (Open itself only creates schema if missing, it never truncates
// existing tables).
func openTestStore(t *testing.T) *StagingStore {
	t.Helper()
	dsn := testDSN(t)
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	for _, table := range []string{
		"staging_records", "staging_errors", "staging_version_counter",
		"catalog_attributes",
		"model_equipment", "model_node", "model_edge", "model_edge_endpoint",
		"model_container", "model_geometry", "model_attribute",
		"model_electrical_group", "import_flag",
	} {
		if _, err := s.db.Exec("TRUNCATE TABLE " + table); err != nil {
			t.Fatalf("truncating %s: %v", table, err)
		}
	}
	// staging_version_counter is reseeded the same way Open does.
	if _, err := s.db.Exec(rebind(`INSERT INTO staging_version_counter (id, last_version) VALUES (1, 0) ON CONFLICT (id) DO NOTHING`)); err != nil {
		t.Fatalf("reseeding staging_version_counter: %v", err)
	}
	return s
}

func TestNextVersionIncrementsAndNeverReused(t *testing.T) {
	s := openTestStore(t)

	v1, err := s.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	v2, err := s.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if v2 != v1+1 {
		t.Fatalf("expected v2 = v1+1, got v1=%d v2=%d", v1, v2)
	}

	if err := s.InsertBatch([]model.StagingRecord{{Version: v1, ID: "obj1", Class: "Foo", Attribute: "bar", Value: "1", Seq: 0}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := s.DeleteVersion(v1); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}
	count, err := s.CountByVersion(v1)
	if err != nil {
		t.Fatalf("CountByVersion: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records after DeleteVersion, got %d", count)
	}

	v3, err := s.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if v3 != v2+1 {
		t.Fatalf("expected v3 = v2+1 (no reuse of deleted v1), got v2=%d v3=%d", v2, v3)
	}
}

func TestInsertBatchChunksAcrossBoundary(t *testing.T) {
	s := openTestStore(t)

	version, err := s.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}

	// insertChunkSize is 200 — use a count that straddles two chunk
	// boundaries (200 + 200 + partial) to exercise the chunking loop.
	const total = 450
	records := make([]model.StagingRecord, total)
	for i := range records {
		records[i] = model.StagingRecord{
			Version:   version,
			ID:        "obj",
			Class:     "Foo",
			Attribute: "attr",
			Value:     "v",
			Seq:       i,
		}
	}

	if err := s.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	count, err := s.CountByVersion(version)
	if err != nil {
		t.Fatalf("CountByVersion: %v", err)
	}
	if count != total {
		t.Fatalf("expected %d records, got %d", total, count)
	}
}

func TestInsertErrorsAndDeleteVersion(t *testing.T) {
	s := openTestStore(t)

	version, err := s.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}

	errs := []model.StagingError{
		{Version: version, SourceFile: "bad.xml", Line: 42, ByteOffset: 123, Message: "malformed XML"},
	}
	if err := s.InsertErrors(errs); err != nil {
		t.Fatalf("InsertErrors: %v", err)
	}

	if err := s.InsertBatch([]model.StagingRecord{{Version: version, ID: "obj1", Class: "Foo", Attribute: "bar", Value: "1"}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	if err := s.DeleteVersion(version); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	var errCount int
	if err := s.db.QueryRow(rebind(`SELECT COUNT(*) FROM staging_errors WHERE version = ?`), version).Scan(&errCount); err != nil {
		t.Fatalf("counting staging_errors: %v", err)
	}
	if errCount != 0 {
		t.Fatalf("expected staging_errors cleared by DeleteVersion, got %d", errCount)
	}
}

func TestInsertBatchEmptyIsNoop(t *testing.T) {
	s := openTestStore(t)

	if err := s.InsertBatch(nil); err != nil {
		t.Fatalf("InsertBatch(nil): %v", err)
	}
	if err := s.InsertErrors(nil); err != nil {
		t.Fatalf("InsertErrors(nil): %v", err)
	}
}

func TestStagingStore_InsertAndGetByID(t *testing.T) {
	s := openTestStore(t)
	version, err := s.NextVersion()
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}

	records := []model.StagingRecord{
		{Version: version, ID: "obj1", Class: "Foo", Attribute: "name", Value: "hello", Seq: 0},
		{Version: version, ID: "obj1", Class: "Foo", Attribute: "ref", Value: "obj2", Seq: 0, IsReference: true},
		{Version: version, ID: "obj2", Class: "Bar", Attribute: "name", Value: "world", Seq: 0},
	}
	if err := s.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	got, err := s.GetByID(version, "obj1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records for obj1, got %d: %+v", len(got), got)
	}

	refs, err := s.GetReferencesTo(version, "obj2")
	if err != nil {
		t.Fatalf("GetReferencesTo: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "obj1" {
		t.Fatalf("expected exactly one reference from obj1 to obj2, got %+v", refs)
	}

	classes, err := s.ListClasses(version)
	if err != nil {
		t.Fatalf("ListClasses: %v", err)
	}
	if len(classes) != 2 {
		t.Fatalf("expected 2 distinct classes, got %+v", classes)
	}
}

func TestModelStore_ContainerHierarchyRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	containers := []coremodel.Container{
		{ID: "station1", Type: "substation", ParentID: ""},
		{ID: "bay1", Type: "bay", ParentID: "station1"},
		{ID: "bay2", Type: "bay", ParentID: "station1"},
		{ID: "eqcont1", Type: "bay", ParentID: "bay1"},
	}
	if err := m.UpsertContainers(containers); err != nil {
		t.Fatalf("UpsertContainers: %v", err)
	}

	got, err := m.ContainerGetByIDs([]string{"station1", "bay1"})
	if err != nil {
		t.Fatalf("ContainerGetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 containers, got %d: %+v", len(got), got)
	}

	children, err := m.GetChildren([]string{"station1"})
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 direct children of station1, got %d: %+v", len(children), children)
	}

	descendants, err := m.GetDescendants([]string{"station1"})
	if err != nil {
		t.Fatalf("GetDescendants: %v", err)
	}
	if len(descendants) != 3 {
		t.Fatalf("expected 3 descendants, got %d: %+v", len(descendants), descendants)
	}

	if err := m.UpsertContainers([]coremodel.Container{{ID: "bay1", Type: "busbar", ParentID: "station1"}}); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	got, err = m.ContainerGetByIDs([]string{"bay1"})
	if err != nil {
		t.Fatalf("ContainerGetByIDs after re-upsert: %v", err)
	}
	if len(got) != 1 || got[0].Type != "busbar" {
		t.Fatalf("expected bay1 overwritten to type=busbar, got %+v", got)
	}
}

func TestModelStore_PhysicalTopologyRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	nodes := []coremodel.Node{
		{EquipmentID: "A", Kind: coremodel.NodeKindReal},
		{EquipmentID: "B", Kind: coremodel.NodeKindReal},
		{EquipmentID: "C", Kind: coremodel.NodeKindReal},
		{EquipmentID: "D", Kind: coremodel.NodeKindReal},
	}
	if err := m.UpsertNodes(nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	edges := []coremodel.Edge{
		{EquipmentID: "AB", Terminal1NodeID: "A", Terminal2NodeID: "B"},
		{EquipmentID: "BC", Terminal1NodeID: "B", Terminal2NodeID: "C"},
	}
	if err := m.UpsertEdges(edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	gotNodes, err := m.GetNodesByIDs([]string{"A", "D"})
	if err != nil {
		t.Fatalf("GetNodesByIDs: %v", err)
	}
	if len(gotNodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %+v", len(gotNodes), gotNodes)
	}

	edgesAtB, err := m.GetEdgesByNodeIDs([]string{"B"})
	if err != nil {
		t.Fatalf("GetEdgesByNodeIDs: %v", err)
	}
	if len(edgesAtB) != 2 {
		t.Fatalf("expected 2 edges touching B, got %d: %+v", len(edgesAtB), edgesAtB)
	}

	reachable, err := m.GetReachableNodes([]string{"A"})
	if err != nil {
		t.Fatalf("GetReachableNodes: %v", err)
	}
	reachSet := map[string]bool{}
	for _, id := range reachable {
		reachSet[id] = true
	}
	if !reachSet["A"] || !reachSet["B"] || !reachSet["C"] {
		t.Fatalf("expected A, B, C reachable from A, got %v", reachable)
	}
	if reachSet["D"] {
		t.Fatalf("D must not be reachable from A (no edge), got %v", reachable)
	}
}

func TestModelStore_GeometryRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	geoms := []coremodel.Geometry{
		{OwnerID: "station1", OwnerKind: coremodel.GeometryOwnerContainer, Lat: 52.5, Lon: 13.4},
	}
	if err := m.UpsertGeometry(geoms); err != nil {
		t.Fatalf("UpsertGeometry: %v", err)
	}

	got, err := m.GetByIDsGeometry([]string{"station1"})
	if err != nil {
		t.Fatalf("GetByIDsGeometry: %v", err)
	}
	if len(got) != 1 || got[0].Lat != 52.5 || got[0].Lon != 13.4 {
		t.Fatalf("expected geometry round-trip to preserve exact float64 lat/lon, got %+v", got)
	}

	inBox, err := m.InBoundingBox(52.0, 13.0, 53.0, 14.0)
	if err != nil {
		t.Fatalf("InBoundingBox: %v", err)
	}
	if len(inBox) != 1 {
		t.Fatalf("expected station1 inside bounding box, got %+v", inBox)
	}
}

func TestModelStore_AttributeRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	attrs := []coremodel.Attribute{
		{OwnerID: "eq1", Key: "voltage", Value: "400"},
		{OwnerID: "eq1", Key: "phase_count", Value: float64(3)},
	}
	if err := m.UpsertAttributes(attrs); err != nil {
		t.Fatalf("UpsertAttributes: %v", err)
	}

	got, err := m.GetByOwnerIDs([]string{"eq1"})
	if err != nil {
		t.Fatalf("GetByOwnerIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 attributes, got %d: %+v", len(got), got)
	}
}

func TestModelStore_ElectricalGroupRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	owned := map[string]map[string]string{
		"station1": {"A": "grp1", "B": "grp1"},
	}
	if err := m.UpsertElectricalGroups(owned); err != nil {
		t.Fatalf("UpsertElectricalGroups: %v", err)
	}

	groups, err := m.GetElectricalGroup([]string{"A", "B"})
	if err != nil {
		t.Fatalf("GetElectricalGroup: %v", err)
	}
	if len(groups["A"]) != 1 || groups["A"][0] != "grp1" {
		t.Fatalf("expected A in grp1, got %+v", groups)
	}

	members, err := m.GetGroupMembers([]string{"grp1"})
	if err != nil {
		t.Fatalf("GetGroupMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members of grp1, got %+v", members)
	}

	sizes, err := m.GroupSizes()
	if err != nil {
		t.Fatalf("GroupSizes: %v", err)
	}
	if sizes["grp1"] != 2 {
		t.Fatalf("expected grp1 size 2, got %+v", sizes)
	}
}

func TestModelStore_EquipmentRoundTrip(t *testing.T) {
	s := openTestStore(t)
	m := s.Model()

	equipment := []coremodel.Equipment{
		{ID: "eq1", ContainerID: "station1"},
		{ID: "eq2", ContainerID: "station1"},
	}
	if err := m.UpsertEquipment(equipment); err != nil {
		t.Fatalf("UpsertEquipment: %v", err)
	}

	got, err := m.GetByIDs([]string{"eq1", "missing"})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 1 || got[0].ContainerID != "station1" {
		t.Fatalf("unexpected equipment result: %+v", got)
	}
}

func TestCatalogStore_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	c := s.Catalog()

	entries := []coremodel.CatalogEntry{
		{ID: "cat1", Attributes: []coremodel.Attribute{{OwnerID: "cat1", Key: "kind", Value: "cable"}}},
	}
	if err := c.Upsert(entries); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := c.GetByIDs([]string{"cat1"})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 1 || len(got[0].Attributes) != 1 {
		t.Fatalf("expected 1 catalog entry with 1 attribute, got %+v", got)
	}
}

func TestFlagStore_MarkAndClear(t *testing.T) {
	s := openTestStore(t)
	f := s.Flags()

	version := uint64(1)
	if err := f.MarkFlags(version, "installed", []string{"a", "b"}); err != nil {
		t.Fatalf("MarkFlags: %v", err)
	}
	// Marking the same id again must not error (ON CONFLICT DO NOTHING).
	if err := f.MarkFlags(version, "installed", []string{"a"}); err != nil {
		t.Fatalf("MarkFlags (repeat): %v", err)
	}

	unmarked, err := f.UnmarkedIDs(version, "installed", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("UnmarkedIDs: %v", err)
	}
	if len(unmarked) != 1 || unmarked[0] != "c" {
		t.Fatalf("expected only 'c' unmarked, got %+v", unmarked)
	}

	if err := f.ClearFlags(version); err != nil {
		t.Fatalf("ClearFlags: %v", err)
	}
	unmarked, err = f.UnmarkedIDs(version, "installed", []string{"a"})
	if err != nil {
		t.Fatalf("UnmarkedIDs after clear: %v", err)
	}
	if len(unmarked) != 1 {
		t.Fatalf("expected 'a' unmarked after ClearFlags, got %+v", unmarked)
	}
}
