package mastr

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite"
)

// Store wraps a database connection to the imported MaStR data and offers
// convenience search methods on top of it (see search.go).
type Store struct {
	db *sql.DB
}

// Open opens the MaStR database according to cfg. Only cfg.Driver ==
// "sqlite" (or empty, defaulting to sqlite) is currently supported.
func Open(cfg Config) (*Store, error) {
	switch cfg.Driver {
	case "", "sqlite":
		if cfg.File == "" {
			return nil, fmt.Errorf("mastr: Config.File must be set for the sqlite driver")
		}
		db, err := sql.Open("sqlite", cfg.File)
		if err != nil {
			return nil, fmt.Errorf("mastr: opening sqlite database %s: %w", cfg.File, err)
		}
		// The imported database is read-mostly (rebuilt wholesale on each
		// re-import, see cmd/mastrimport) — a single connection avoids
		// SQLite's concurrent-writer locking entirely and is plenty for
		// read-oriented query workloads.
		db.SetMaxOpenConns(1)
		return &Store{db: db}, nil
	default:
		return nil, fmt.Errorf("mastr: unsupported driver %q (only \"sqlite\" is implemented)", cfg.Driver)
	}
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB handle. Intended for the importer
// (which creates/fills per-dataset tables directly) and for WriteMeta/Meta;
// regular search-function callers should prefer the Store's typed methods
// instead of querying this directly where possible.
func (s *Store) DB() *sql.DB {
	return s.db
}
