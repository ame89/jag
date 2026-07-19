package postgres

import (
	"fmt"
	"net/url"
	"os"
)

// DefaultDatabase is the database name used when JAG_POSTGRES_DB is unset.
// "jag" is only a starting suggestion, not a fixed requirement — any
// caller/operator is free to point at a differently named database (e.g.
// a per-Netzregion database like "stromnord") by setting JAG_POSTGRES_DB
// explicitly. There is nothing JAG-specific baked into the schema name
// itself (see DefaultSchema below).
const DefaultDatabase = "jag"

// DefaultUser and DefaultPassword are the PostgreSQL role used when
// JAG_POSTGRES_USER/JAG_POSTGRES_PASSWORD are unset — a dedicated "jag"
// role/password rather than PostgreSQL's own "postgres" superuser, so a
// local-development server (see the jag-pg-test Docker container used to
// verify this package) doesn't need its superuser credentials handed to
// every JAG process by default. Both are only defaults, freely
// overridable per deployment.
const (
	DefaultUser     = "jag"
	DefaultPassword = "jag"
)

// DefaultSchema is the PostgreSQL schema all of this package's tables are
// created in. JAG does not create or select a custom schema — every
// CREATE TABLE IF NOT EXISTS in staging.go/catalog.go/model.go is
// unqualified, so it resolves against the connecting role's default
// search_path, which is PostgreSQL's own built-in "public" schema unless
// the target database/role has been reconfigured otherwise. This is a
// plain PostgreSQL default, not a JAG design decision — if an operator
// wants JAG's tables isolated in a dedicated schema (e.g. to share one
// database across several unrelated applications), they must configure
// that on the server/role/search_path side; JAG itself has no
// JAG_POSTGRES_SCHEMA knob (not requested, and DSN's search_path query
// parameter already covers this for pgx if ever needed).
const DefaultSchema = "public"

// DSNFromEnv builds a PostgreSQL connection string from environment
// variables, for use as cmd/phase2check's (and similar drivers')
// alternative to the SQLite JAG_DB_PATH backend. Returns ok=false if
// PostgreSQL wasn't selected at all (JAG_BACKEND is unset or not
// "postgres"), so callers can fall back to their existing SQLite default
// without any behavior change when these variables aren't used.
//
// Two ways to configure the connection:
//   - JAG_POSTGRES_DSN: a complete PostgreSQL connection string/URL (e.g.
//     "postgres://user:pass@host:5432/dbname?sslmode=disable"), used
//     verbatim if set — every other JAG_POSTGRES_* variable is ignored in
//     this case.
//   - Otherwise, the individual JAG_POSTGRES_HOST / JAG_POSTGRES_PORT /
//     JAG_POSTGRES_USER / JAG_POSTGRES_PASSWORD / JAG_POSTGRES_DB /
//     JAG_POSTGRES_SSLMODE variables are combined into a DSN, each
//     defaulting to a common local-development value if unset
//     (host=localhost, port=5432, user=DefaultUser="jag",
//     password=DefaultPassword="jag", db=DefaultDatabase="jag",
//     sslmode=disable). JAG_POSTGRES_DB (like JAG_POSTGRES_USER/
//     JAG_POSTGRES_PASSWORD) is deliberately just a default, not a fixed
//     name — set it to whatever database/credentials the deployment
//     actually uses (e.g. db=stromnord for a regionally named database).
func DSNFromEnv() (dsn string, ok bool) {
	if os.Getenv("JAG_BACKEND") != "postgres" {
		return "", false
	}
	if v := os.Getenv("JAG_POSTGRES_DSN"); v != "" {
		return v, true
	}

	host := envOrDefault("JAG_POSTGRES_HOST", "localhost")
	port := envOrDefault("JAG_POSTGRES_PORT", "5432")
	user := envOrDefault("JAG_POSTGRES_USER", DefaultUser)
	password := envOrDefault("JAG_POSTGRES_PASSWORD", DefaultPassword)
	database := envOrDefault("JAG_POSTGRES_DB", DefaultDatabase)
	sslmode := envOrDefault("JAG_POSTGRES_SSLMODE", "disable")

	u := url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%s", host, port),
		Path:   "/" + database,
	}
	if password != "" {
		u.User = url.UserPassword(user, password)
	} else {
		u.User = url.User(user)
	}
	q := u.Query()
	q.Set("sslmode", sslmode)
	u.RawQuery = q.Encode()

	return u.String(), true
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
