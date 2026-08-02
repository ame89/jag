// Package mastr provides a Go service for accessing a local mirror of the
// Marktstammdatenregister (MaStR) "Gesamtdatenexport" — the German Federal
// Network Agency's (Bundesnetzagentur) public register of all electricity/
// gas market units, actors, locations and grid connection points.
//
// The MaStR bulk export is a large ZIP of XML files (one dataset per
// XML-file-prefix, e.g. "EinheitenSolar", "Marktakteure", "Lokationen",
// "Katalogwerte", each possibly split into several numbered parts). This
// package imports that export into a local SQLite database (see
// cmd/mastrimport) and offers a small Store with convenience search
// methods on top of it (see store.go).
//
// This is a standalone mirror for querying MaStR data, unrelated to JAG's
// own CIM/CGMES node-edge model — it does not feed into
// /internal/core/model.
package mastr

// Config describes how to reach the MaStR database. Fields beyond
// Driver/File are currently unused by the only implemented backend
// (SQLite, a single local file needs no user/password/host/port), but are
// kept here so a future non-SQLite backend (e.g. Postgres, for a
// multi-user/shared deployment) can reuse this same Config without
// breaking callers.
type Config struct {
	// Driver selects the backend. Only "sqlite" (the default, used when
	// empty) is currently implemented.
	Driver string

	// File is the path to the SQLite database file (e.g.
	// "./data/mastr/mastr.db"). Only used when Driver is "sqlite".
	File string

	// Host, Port, User, Password, Database are reserved for a future
	// network-based backend (e.g. Postgres) and are ignored by the
	// SQLite backend.
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// DefaultConfig returns the Config used when the caller doesn't need to
// override anything: SQLite, database file at "data/mastr/mastr.db"
// (relative to the process's working directory).
func DefaultConfig() Config {
	return Config{
		Driver: "sqlite",
		File:   "data/mastr/mastr.db",
	}
}
