package postgres

import (
	"database/sql"
	"fmt"
)

// managedObject is one table/view Reset knows how to drop — kind is
// either "TABLE" or "VIEW".
type managedObject struct {
	kind string
	name string
}

// managedObjects lists every table/view this package creates (see
// staging.go's stagingSchema, catalog.go's catalogSchema, model.go's
// modelSchema, flags.go's import_flag) — in an order that is safe to
// drop top-to-bottom given the FOREIGN KEY references between them
// (model_edge_endpoint/model_attribute_value/model_geometry_value ->
// entity_id, model_attribute_value -> attribute_key). Kept as an
// explicit list rather than a catalog query so Reset only ever touches
// tables JAG itself owns, never anything else that might live in the
// same database/schema.
var managedObjects = []managedObject{
	{"VIEW", "model_attribute"},
	{"VIEW", "model_geometry"},
	{"TABLE", "model_attribute_value"},
	{"TABLE", "model_geometry_value"},
	{"TABLE", "model_edge_endpoint"},
	{"TABLE", "model_electrical_group"},
	{"TABLE", "model_edge"},
	{"TABLE", "model_equipment"},
	{"TABLE", "model_node"},
	{"TABLE", "model_container"},
	{"TABLE", "model_metadata"},
	{"TABLE", "attribute_key"},
	{"TABLE", "entity_id"},
	{"TABLE", "import_flag"},
	{"TABLE", "catalog_attributes"},
	{"TABLE", "staging_errors"},
	{"TABLE", "staging_records"},
	{"TABLE", "staging_version_counter"},
}

// Reset drops every table/view this package manages from the PostgreSQL
// database addressed by dsn, so a subsequent Open starts from a
// completely empty schema — the PostgreSQL equivalent of jaggit's/
// hjsonimport's SQLite "-clear" behavior (os.Remove the file before
// reopening), since there is no single file to remove for a PostgreSQL
// database. Only ever touches the objects in managedObjects (see its doc
// comment) — never DROPs or otherwise affects the database itself, its
// schema, or any unrelated table that might live alongside JAG's.
// Idempotent: dropping an already-absent object is a no-op (IF EXISTS).
func Reset(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("postgres: opening %s for reset: %w", dsn, err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("postgres: connecting to %s for reset: %w", dsn, err)
	}

	for _, obj := range managedObjects {
		if _, err := db.Exec(fmt.Sprintf(`DROP %s IF EXISTS %s CASCADE`, obj.kind, obj.name)); err != nil {
			return fmt.Errorf("postgres: dropping %s %s: %w", obj.kind, obj.name, err)
		}
	}
	return nil
}
