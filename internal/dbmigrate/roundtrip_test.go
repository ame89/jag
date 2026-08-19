package dbmigrate_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver, used only to truncate tables before the test

	"github.com/ame89/jag/internal/dbmigrate"
	coremodel "github.com/ame89/jag/pkg/core/model"
	"github.com/ame89/jag/pkg/impl/common"
	"github.com/ame89/jag/pkg/importer/phase1"
	"github.com/ame89/jag/pkg/postgres"
	"github.com/ame89/jag/pkg/sqlite"
)

// testDSN returns the PostgreSQL DSN to run this roundtrip test against,
// read from JAG_TEST_POSTGRES_DSN — same env var and skip-cleanly-if-unset
// behavior as pkg/postgres's own integration tests (see
// pkg/postgres/model_test.go's testDSN), so `go test ./...` stays green
// without Docker/Postgres reachable.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("JAG_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("JAG_TEST_POSTGRES_DSN not set, skipping SQLite<->PostgreSQL roundtrip test")
	}
	return dsn
}

// truncatePostgres wipes every model_* table before the test, so leftover
// data from a prior run against the same live database can't corrupt this
// test's exact-equality comparison. Uses its own throwaway *sql.DB
// connection (pgx driver) rather than reaching into *postgres.ModelStore's
// unexported db field.
func truncatePostgres(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening raw postgres connection for truncation: %v", err)
	}
	defer db.Close()
	for _, table := range []string{
		"model_equipment", "model_node", "model_edge", "model_edge_endpoint",
		"model_container", "model_geometry",
		// model_attribute_value and attribute_key must be truncated together
		// (a single TRUNCATE statement) since attribute_key.id is referenced
		// by model_attribute_value.key_id via a foreign key.
		"model_attribute_value, attribute_key",
		"model_electrical_group",
	} {
		if _, err := db.Exec("TRUNCATE TABLE " + table); err != nil {
			t.Fatalf("truncating %s: %v", table, err)
		}
	}
}

// modelSink writes every Attribute/Geometry batch straight into a
// ModelStore's model_attribute/model_geometry tables — the same wiring
// cmd/phase2check's persistSink does, minus the reporting/counting layer
// this test doesn't need.
type modelSink struct{ model *sqlite.ModelStore }

func (s *modelSink) WriteAttributes(batch []coremodel.Attribute) error {
	return s.model.UpsertAttributes(batch)
}

func (s *modelSink) WriteGeometries(batch []coremodel.Geometry) error {
	return s.model.UpsertGeometry(batch)
}

// importCGMESIntoSQLite runs the full Phase 1 + Pass A/B pipeline (the
// same one cmd/phase2check drives) against a real CGMES example dataset,
// persisting the resulting Container/Equipment/Node/Edge/Attribute/
// Geometry/ElectricalGroup rows into a fresh in-memory SQLite ModelStore.
func importCGMESIntoSQLite(t *testing.T, dir string) *sqlite.StagingStore {
	t.Helper()
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

	result, err := phase1.RunCGMESFiles(store, files)
	if err != nil {
		t.Fatalf("RunCGMESFiles: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("RunCGMESFiles reported %d collected errors: %+v", len(result.Errors), result.Errors)
	}

	model := store.Model()
	sink := &modelSink{model: model}

	err = common.RunPassA(store, result.Version, 1000, common.DefaultStationBatchSize, 4, sink, nil, false, func(b *common.BatchResult) error {
		owned := make(map[string]map[string]string, len(b.Groups))
		for owner, groups := range b.Groups {
			owned[owner] = groups
		}
		return model.PersistBatch(b.Containers, b.Equipment, b.Nodes, b.Edges, nil, nil, owned)
	})
	if err != nil {
		t.Fatalf("RunPassA: %v", err)
	}

	passB, err := common.RunPassB(store, result.Version, 1000, 0, 0, sink, nil, func(b *common.PassBACLineBatchResult) error {
		groups := map[string]map[string]string{b.OwnerID: b.Groups}
		return model.PersistBatch(b.Containers, b.Equipment, b.Nodes, b.Edges, b.Attributes, nil, groups)
	})
	if err != nil {
		t.Fatalf("RunPassB: %v", err)
	}
	passBOwned := make(map[string]map[string]string, len(passB.Groups))
	for owner, groups := range passB.Groups {
		passBOwned[owner] = groups
	}
	passBAttributes := make([]coremodel.Attribute, 0, len(passB.Attributes)+len(passB.LineRefs))
	passBAttributes = append(passBAttributes, passB.Attributes...)
	passBAttributes = append(passBAttributes, passB.LineRefs...)
	if err := model.PersistBatch(passB.Containers, passB.Equipment, passB.Nodes, passB.Edges, passBAttributes, nil, passBOwned); err != nil {
		t.Fatalf("persisting pass B remainder: %v", err)
	}

	return store
}

// --- generic page-and-sort readers, used only to compare two ModelStores' full contents ---

func allContainers(t *testing.T, m dbmigrate.ModelStore) []coremodel.Container {
	t.Helper()
	var all []coremodel.Container
	after := ""
	for {
		page, err := m.AllContainers(after, 500)
		if err != nil {
			t.Fatalf("AllContainers: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].ID
		if len(page) < 500 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

func allEquipment(t *testing.T, m dbmigrate.ModelStore) []coremodel.Equipment {
	t.Helper()
	var all []coremodel.Equipment
	after := ""
	for {
		page, err := m.AllEquipment(after, 500)
		if err != nil {
			t.Fatalf("AllEquipment: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].ID
		if len(page) < 500 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

func allNodes(t *testing.T, m dbmigrate.ModelStore) []coremodel.Node {
	t.Helper()
	var all []coremodel.Node
	after := ""
	for {
		page, err := m.AllNodes(after, 500)
		if err != nil {
			t.Fatalf("AllNodes: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].EquipmentID
		if len(page) < 500 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].EquipmentID < all[j].EquipmentID })
	return all
}

func allEdges(t *testing.T, m dbmigrate.ModelStore) []coremodel.Edge {
	t.Helper()
	var all []coremodel.Edge
	after := ""
	for {
		page, err := m.AllEdges(after, 500)
		if err != nil {
			t.Fatalf("AllEdges: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].EquipmentID
		if len(page) < 500 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].EquipmentID < all[j].EquipmentID })
	return all
}

func allGeometry(t *testing.T, m dbmigrate.ModelStore) []coremodel.Geometry {
	t.Helper()
	var all []coremodel.Geometry
	after := ""
	for {
		page, err := m.AllGeometry(after, 500)
		if err != nil {
			t.Fatalf("AllGeometry: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].OwnerID
		if len(page) < 500 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].OwnerID < all[j].OwnerID })
	return all
}

func allAttributes(t *testing.T, m dbmigrate.ModelStore) []coremodel.Attribute {
	t.Helper()
	var all []coremodel.Attribute
	after := ""
	for {
		page, err := m.AllAttributes(after, 500)
		if err != nil {
			t.Fatalf("AllAttributes: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].OwnerID
		if len(page) < 500 {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].OwnerID != all[j].OwnerID {
			return all[i].OwnerID < all[j].OwnerID
		}
		return all[i].Key < all[j].Key
	})
	return all
}

func allElectricalGroups(t *testing.T, m dbmigrate.ModelStore) []coremodel.ElectricalGroupRow {
	t.Helper()
	var all []coremodel.ElectricalGroupRow
	after := ""
	for {
		page, err := m.AllElectricalGroups(after, 500)
		if err != nil {
			t.Fatalf("AllElectricalGroups: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].OwnerID
		if len(page) < 500 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].OwnerID != all[j].OwnerID {
			return all[i].OwnerID < all[j].OwnerID
		}
		return all[i].NodeID < all[j].NodeID
	})
	return all
}

// TestRoundTripCGMESSQLitePostgresSQLite imports a real CGMES example
// dataset into SQLite via the full Phase 1 + Pass A/B pipeline, copies the
// resulting model SQLite -> PostgreSQL -> SQLite via
// cmd/sqlite2postgres's and cmd/postgres2sqlite's shared dbmigrate.CopyModel
// routine, and asserts the final SQLite database's full model content
// (Containers, Equipment, Nodes, Edges, Geometry, Attributes,
// ElectricalGroups) is byte-for-byte identical to the original — i.e. the
// round trip is lossless in both directions.
func TestRoundTripCGMESSQLitePostgresSQLite(t *testing.T) {
	dsn := testDSN(t)

	// postgres.Open creates the model_* schema if missing (CREATE TABLE IF
	// NOT EXISTS) but never truncates existing tables — on a genuinely
	// fresh database (e.g. a brand-new Docker container) the tables don't
	// exist yet at all, so Open must run once before truncatePostgres can
	// TRUNCATE them.
	pg, err := postgres.Open(dsn)
	if err != nil {
		t.Fatalf("postgres.Open: %v", err)
	}
	defer pg.Close()
	truncatePostgres(t, dsn)

	dir := filepath.Join("..", "..", "examples", "cgmes", "Telemark_LV_Fuse")
	original := importCGMESIntoSQLite(t, dir)
	defer original.Close()

	if len(allEquipment(t, original.Model())) == 0 {
		t.Fatalf("import produced no equipment at all — pipeline didn't run?")
	}

	var logged []string
	report := func(format string, args ...any) {
		logged = append(logged, format)
	}

	if err := dbmigrate.CopyModel(original.Model(), pg.Model(), 500, report); err != nil {
		t.Fatalf("CopyModel sqlite->postgres: %v", err)
	}
	if len(logged) == 0 {
		t.Fatalf("CopyModel sqlite->postgres reported no progress at all")
	}

	roundTripped, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open (roundtrip target): %v", err)
	}
	defer roundTripped.Close()

	if err := dbmigrate.CopyModel(pg.Model(), roundTripped.Model(), 500, report); err != nil {
		t.Fatalf("CopyModel postgres->sqlite: %v", err)
	}

	if got, want := allContainers(t, roundTripped.Model()), allContainers(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("containers differ after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
	if got, want := allEquipment(t, roundTripped.Model()), allEquipment(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("equipment differ after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
	if got, want := allNodes(t, roundTripped.Model()), allNodes(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("nodes differ after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
	if got, want := allEdges(t, roundTripped.Model()), allEdges(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("edges differ after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
	if got, want := allGeometry(t, roundTripped.Model()), allGeometry(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("geometry differs after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
	if got, want := allAttributes(t, roundTripped.Model()), allAttributes(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("attributes differ after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
	if got, want := allElectricalGroups(t, roundTripped.Model()), allElectricalGroups(t, original.Model()); !reflect.DeepEqual(got, want) {
		t.Errorf("electrical groups differ after roundtrip:\n got=%+v\nwant=%+v", got, want)
	}
}
