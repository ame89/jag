// Package jagdb resolves the single JAG_DATABASE environment variable
// into a backend selection (SQLite or PostgreSQL) plus the connection
// string/path each respective backend's Open function expects.
//
// JAG_DATABASE replaces the previous JAG_BACKEND/JAG_DB_PATH/
// JAG_POSTGRES_DSN/JAG_POSTGRES_HOST/JAG_POSTGRES_PORT/JAG_POSTGRES_USER/
// JAG_POSTGRES_PASSWORD/JAG_POSTGRES_DB/JAG_POSTGRES_SSLMODE variable set
// entirely — there is no separate backend-selector variable and no
// "build a DSN from parts" fallback anymore; a complete, ready-to-use
// value is always required.
//
// Recognized forms:
//
//   - "sqlite://<path>" — SQLite backend, using <path> unchanged as the
//     file path passed to sqlite.Open (e.g. "sqlite://foo.db").
//   - "sqlite://:memory" — SQLite backend, special-cased to SQLite's own
//     in-memory marker ":memory:" (note: no trailing colon in the
//     JAG_DATABASE value itself, one is added back internally).
//   - "postgres://..." or "postgresql://..." — PostgreSQL backend, using
//     the full value unchanged as the DSN passed to postgres.Open (e.g.
//     "postgres://jag:jag@localhost:5432/jag?sslmode=disable").
//
// Any other value (missing/unrecognized prefix), or JAG_DATABASE being
// unset entirely, is a hard error — there is deliberately no default
// backend/path anymore, so a misconfiguration fails loudly instead of
// silently falling back to some default file.
package jagdb

import (
	"fmt"
	"os"
	"strings"
)

// Backend identifies which storage backend a JAG_DATABASE value
// addresses.
type Backend int

const (
	// Unknown is the zero value, returned alongside an error whenever
	// parsing fails.
	Unknown Backend = iota
	SQLite
	Postgres
)

// String returns a short, human-readable backend name, e.g. for log/error
// messages.
func (b Backend) String() string {
	switch b {
	case SQLite:
		return "sqlite"
	case Postgres:
		return "postgres"
	default:
		return "unknown"
	}
}

const (
	sqlitePrefix       = "sqlite://"
	postgresPrefix     = "postgres://"
	postgresAltPrefix  = "postgresql://"
	sqliteMemorySuffix = ":memory"
)

// Parse splits a JAG_DATABASE value into a Backend and the connection
// string/path the matching backend's Open function expects. See the
// package doc comment for the recognized forms.
func Parse(databaseURL string) (Backend, string, error) {
	switch {
	case strings.HasPrefix(databaseURL, sqlitePrefix):
		path := strings.TrimPrefix(databaseURL, sqlitePrefix)
		if path == sqliteMemorySuffix {
			path = ":memory:"
		}
		return SQLite, path, nil
	case strings.HasPrefix(databaseURL, postgresPrefix), strings.HasPrefix(databaseURL, postgresAltPrefix):
		return Postgres, databaseURL, nil
	default:
		return Unknown, "", fmt.Errorf(
			"jagdb: unrecognized JAG_DATABASE value %q (must start with %q or %q/%q)",
			databaseURL, sqlitePrefix, postgresPrefix, postgresAltPrefix,
		)
	}
}

// FromEnv reads JAG_DATABASE and parses it via Parse. It returns an error
// if the variable is unset or empty — there is no default backend/path.
func FromEnv() (Backend, string, error) {
	v := os.Getenv("JAG_DATABASE")
	if v == "" {
		return Unknown, "", fmt.Errorf("jagdb: JAG_DATABASE is not set (expected e.g. %q or %q)", "sqlite://foo.db", "postgres://user:pass@host:5432/db?sslmode=disable")
	}
	return Parse(v)
}
