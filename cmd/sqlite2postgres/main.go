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
// PostgreSQL connection is configured via the same JAG_POSTGRES_* /
// JAG_POSTGRES_DSN environment variables cmd/phase2check uses (see
// pkg/postgres/dsn.go's DSNFromEnv doc comment) — JAG_BACKEND does not
// need to be set to "postgres" for this tool specifically, since its
// whole purpose is talking to PostgreSQL; JAG_POSTGRES_DSN (or the
// individual JAG_POSTGRES_HOST/PORT/USER/PASSWORD/DB/SSLMODE variables)
// are read directly.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ame89/jag/internal/dbmigrate"
	"github.com/ame89/jag/pkg/postgres"
	"github.com/ame89/jag/pkg/sqlite"
)

func main() {
	sqlitePath := flag.String("sqlite", "", "path to the source SQLite database file (required)")
	batchSize := flag.Int("batch-size", dbmigrate.BatchSize, "rows/owners copied per page")
	flag.Parse()

	if *sqlitePath == "" {
		fmt.Fprintln(os.Stderr, "usage: sqlite2postgres -sqlite path/to/model.db")
		fmt.Fprintln(os.Stderr, "PostgreSQL target is configured via JAG_POSTGRES_DSN or JAG_POSTGRES_HOST/PORT/USER/PASSWORD/DB/SSLMODE")
		os.Exit(2)
	}

	dsn := postgresDSN()

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

// postgresDSN builds the PostgreSQL DSN the same way cmd/phase2check does,
// but without requiring JAG_BACKEND=postgres (this tool's whole purpose is
// talking to PostgreSQL, so that extra opt-in switch would just be
// friction here).
func postgresDSN() string {
	if v := os.Getenv("JAG_POSTGRES_DSN"); v != "" {
		return v
	}
	// Temporarily set JAG_BACKEND so postgres.DSNFromEnv's env-variable
	// combination logic (host/port/user/password/db/sslmode with their
	// documented defaults) can be reused verbatim instead of duplicated
	// here.
	os.Setenv("JAG_BACKEND", "postgres")
	dsn, _ := postgres.DSNFromEnv()
	return dsn
}
