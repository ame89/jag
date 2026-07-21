// Command jag2nsc-apply applies the jag2nsc read-only VIEW layer (and, optionally, the
// locked-down jag2nsc_reader role, and/or the NSC_SUPPORT chain-collapsed topology feature)
// to a Postgres database that already contains an imported JAG "model_*"/"staging_*" schema.
//
// Usage:
//
//	jag2nsc-apply -dsn "postgres://postgres@localhost:55432/jag?sslmode=disable"
//	jag2nsc-apply -dsn "..." -with-reader-role -reader-password "s3cr3t"
//	jag2nsc-apply -dsn "..." -skip-views -with-reader-role -reader-password "s3cr3t"
//	jag2nsc-apply -dsn "..." -nsc-support   # also computes/applies the topology tables+views
//
// The DSN can also be supplied via the JAG2NSC_DSN environment variable, and the reader
// password via JAG2NSC_READER_PASSWORD (preferred over the -reader-password flag in
// automated/CI use, since flags are visible in process listings). The NSC_SUPPORT feature
// can also be toggled via the NSC_SUPPORT=1 environment variable instead of -nsc-support -
// this is the only env-var-controlled on/off switch for the whole feature (topology_tables.
// sql / BuildTopology / views_topology.sql): when neither is set, jag2nsc behaves exactly as
// before this feature existed - no new tables, no changed views. This feature only makes
// sense against a Postgres backend (it reads/writes Postgres-only jag2nsc_* tables); it is
// never applied to a SQLite-backed JAG database.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"gitlab.com/openk-nsc/jag/internal/jag2nsc"
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

func main() {
	jag2nsc.SetLogger(logger)
	if err := run(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		dsn            = flag.String("dsn", os.Getenv("JAG2NSC_DSN"), "Postgres connection string of the JAG database (env JAG2NSC_DSN)")
		skipViews      = flag.Bool("skip-views", false, "do not (re-)apply views.sql")
		withReaderRole = flag.Bool("with-reader-role", false, "also (re-)apply reader_role.sql, creating/granting the jag2nsc_reader role")
		readerPassword = flag.String("reader-password", os.Getenv("JAG2NSC_READER_PASSWORD"), "if set (with -with-reader-role), sets/rotates jag2nsc_reader's login password (env JAG2NSC_READER_PASSWORD preferred)")
		nscSupport     = flag.Bool("nsc-support", os.Getenv("NSC_SUPPORT") == "1", "also compute and apply the chain-collapsed topology feature (jag2nsc_terminal/connection/connection_terminal_map/line_segment tables, overriding the simpler default terminal/connection/connection_terminal_map/line_segment views); Postgres-only. Env NSC_SUPPORT=1 has the same effect")
		timeout        = flag.Duration("timeout", 5*time.Minute, "overall timeout for applying the SQL")
	)
	flag.Parse()

	if *dsn == "" {
		return fmt.Errorf("no DSN given: pass -dsn or set JAG2NSC_DSN")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	if !*skipViews {
		logger.Info("applying views.sql ...")
		if err := jag2nsc.ApplyViews(ctx, db); err != nil {
			return err
		}
		logger.Info("views.sql applied")

		logger.Info("applying views_placeholders.sql ...")
		if err := jag2nsc.ApplyPlaceholderViews(ctx, db); err != nil {
			return err
		}
		logger.Info("views_placeholders.sql applied")
	}

	if *withReaderRole {
		logger.Info("applying reader_role.sql ...")
		if err := jag2nsc.ApplyReaderRole(ctx, db, *readerPassword); err != nil {
			return err
		}
		logger.Info("reader_role.sql applied")
	}

	if *nscSupport {
		logger.Info("NSC_SUPPORT enabled: applying topology_tables.sql ...")
		if err := jag2nsc.ApplyTopologyTables(ctx, db); err != nil {
			return err
		}
		logger.Info("computing chain-collapsed topology ...")
		if err := jag2nsc.BuildTopology(ctx, db); err != nil {
			return err
		}
		logger.Info("applying views_topology.sql ...")
		if err := jag2nsc.ApplyTopologyViews(ctx, db); err != nil {
			return err
		}
		logger.Info("NSC_SUPPORT topology applied")

		logger.Info("applying circuit_tables.sql / network_group_tables.sql ...")
		if err := jag2nsc.ApplyCircuitTables(ctx, db); err != nil {
			return err
		}
		if err := jag2nsc.ApplyNetworkGroupTables(ctx, db); err != nil {
			return err
		}
		logger.Info("computing circuits ...")
		if err := jag2nsc.BuildCircuits(ctx, db, *dsn); err != nil {
			return err
		}
		logger.Info("computing network_group ...")
		if err := jag2nsc.BuildNetworkGroup(ctx, db); err != nil {
			return err
		}
		logger.Info("applying views_circuit.sql ...")
		if err := jag2nsc.ApplyCircuitViews(ctx, db); err != nil {
			return err
		}
		logger.Info("NSC_SUPPORT circuit/network_group applied")
	}

	return nil
}
