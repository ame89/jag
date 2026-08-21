// Command sqlite2postgres copies every model_* table (Container, Equipment,
// Node, Edge, Geometry, Attribute, ElectricalGroup) from a SQLite-backed
// JAG database into a PostgreSQL-backed one, using internal/dbmigrate's
// generic CopyModel routine. staging_* (Phase 1 scratch) and import_flag
// (ephemeral, import-scoped) are deliberately not copied — see
// dbmigrate's package doc comment.
//
// Since every write is an Upsert (INSERT ... ON CONFLICT DO UPDATE), the
// destination PostgreSQL database does NOT need to be empty beforehand.
//
// Usage:
//
//	sqlite2postgres -sqlite path/to/model.db
//
// PostgreSQL connection is configured via the JAG_DATABASE environment
// variable (see pkg/jagdb's doc comment) — it must be set to a
// postgres://... value; sqlite:// is rejected here since the SQLite side
// is already given via the -sqlite flag.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ame89/jag/internal/dbmigrate"
	"github.com/ame89/jag/pkg/jagdb"
	"github.com/ame89/jag/pkg/postgres"
	"github.com/ame89/jag/pkg/sqlite"
)

func main() {
	sqlitePath := flag.String("sqlite", "", "path to the source SQLite database file (required)")
	batchSize := flag.Int("batch-size", dbmigrate.BatchSize, "rows/owners copied per page")
	flag.Parse()

	if *sqlitePath == "" {
		fmt.Fprintln(os.Stderr, "usage: sqlite2postgres -sqlite path/to/model.db")
		fmt.Fprintln(os.Stderr, "PostgreSQL target is configured via JAG_DATABASE (postgres://...)")
		os.Exit(2)
	}

	dsn, err := postgresDSN()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	src, err := sqlite.Open(*sqlitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening sqlite database %s: %v\n", *sqlitePath, err)
		os.Exit(1)
	}
	defer src.Close()

	dst, err := postgres.Open(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening postgres database: %v\n", err)
		os.Exit(1)
	}
	defer dst.Close()

	fmt.Printf("copying model from sqlite %s to postgres...\n", *sqlitePath)
	start := time.Now()
	err = dbmigrate.CopyModel(src.Model(), dst.Model(), *batchSize, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("done in %s\n", time.Since(start).Round(time.Millisecond))
}

// postgresDSN reads JAG_DATABASE and requires it to resolve to the
// Postgres backend (the SQLite side of this migration is already given
// via the -sqlite flag, so JAG_DATABASE here can only mean the
// PostgreSQL target).
func postgresDSN() (string, error) {
	backend, conn, err := jagdb.FromEnv()
	if err != nil {
		return "", err
	}
	if backend != jagdb.Postgres {
		return "", fmt.Errorf("sqlite2postgres: JAG_DATABASE must be a postgres:// value, got backend %q", backend)
	}
	return conn, nil
}
