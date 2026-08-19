// Command hjsonimport is the reference CLI for the current, authoritative
// HJSON Fachmodell dialect (see pkg/importer/hjson's doc comment):
// it parses a directory tree of *.hjson files produced by cmd/hjsonexport
// into the staging store, runs it through the existing Pass A/B Phase 2/3
// pipeline unchanged, and persists the result via ModelStore. The older,
// deprecated dialect lives on as cmd/hjsonimport-deprecated/
// cmd/hjsonexport-deprecated (pkg/importer/hjson-deprecated,
// internal/exporter/hjson-deprecated).
//
// The actual pipeline lives in pkg/impl/hjsonimport so it can be shared
// with cmd/hjsonwatch, which re-runs the same import whenever the *.hjson
// tree changes on disk.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ame89/jag/pkg/impl/hjsonimport"
)

func main() {
	root := "examples/hjson"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	dbPath := "hjsonimport.db"
	if v := os.Getenv("JAG_DB_PATH"); v != "" {
		dbPath = v
	}

	opts, err := optionsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if _, err := hjsonimport.Run(root, dbPath, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func intEnv(name string, dflt int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return dflt, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return n, nil
}

func optionsFromEnv() (hjsonimport.Options, error) {
	var opts hjsonimport.Options
	var err error
	if opts.ChunkSize, err = intEnv("JAG_CHUNK_SIZE", 2000); err != nil {
		return opts, err
	}
	if opts.BatchSize, err = intEnv("JAG_STATION_BATCH_SIZE", 0); err != nil {
		return opts, err
	}
	if opts.StationWorkers, err = intEnv("JAG_STATION_WORKERS", 0); err != nil {
		return opts, err
	}
	if opts.PassBWorkers, err = intEnv("JAG_PASS_B_WORKERS", 0); err != nil {
		return opts, err
	}
	if opts.PassBBatchSize, err = intEnv("JAG_PASS_B_BATCH_SIZE", 0); err != nil {
		return opts, err
	}
	opts.KeepStaging = os.Getenv("JAG_KEEP_STAGING") == "1"
	opts.SkipVacuum = os.Getenv("JAG_SKIP_VACUUM") == "1"
	return opts, nil
}
