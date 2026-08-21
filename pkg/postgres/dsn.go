package postgres

// DefaultSchema is the PostgreSQL schema all of this package's tables are
// created in. JAG does not create or select a custom schema — every
// CREATE TABLE IF NOT EXISTS in staging.go/catalog.go/model.go is
// unqualified, so it resolves against the connecting role's default
// search_path, which is PostgreSQL's own built-in "public" schema unless
// the target database/role has been reconfigured otherwise. This is a
// plain PostgreSQL default, not a JAG design decision — if an operator
// wants JAG's tables isolated in a dedicated schema (e.g. to share one
// database across several unrelated applications), they must configure
// that on the server/role/search_path side, e.g. via the DSN's
// search_path query parameter (see pkg/jagdb for how JAG_DATABASE's
// postgres:// value is passed through to Open unchanged).
const DefaultSchema = "public"
