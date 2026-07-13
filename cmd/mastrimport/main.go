// Command mastrimport (re-)builds the local MaStR SQLite database
// (default: data/mastr/mastr.db) from the official Marktstammdatenregister
// Gesamtdatenexport.
//
// By default it discovers and downloads the current export zip, imports it,
// records which export it came from (see mastr.WriteMeta), and then deletes
// the zip again — the zip is only a transient download artifact, not meant
// to be kept around after a successful import. Use -zip to import an
// already-downloaded zip instead of fetching a new one, and -keep-zip to
// keep it afterwards.
//
// To refresh the database with the latest export, simply run this command
// again (no flags needed): it re-discovers the current download link,
// downloads it, rebuilds the database from scratch into a temp file, and
// only replaces the previous database once the import succeeded.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"gitlab.com/openk-nsc/jag/internal/mastr"
)

func main() {
	var (
		zipPath   = flag.String("zip", "", "path to an already-downloaded Gesamtdatenexport zip (skips discovery/download if set)")
		dbPath    = flag.String("db", ".data/mastr/mastr.db", "path to the SQLite database to (re-)build")
		keepZip   = flag.Bool("keep-zip", false, "keep the zip file after a successful import (default: delete it)")
		userAgent = flag.String("user-agent", "jag-mastrimport/0.1", "User-Agent header sent for download/discovery HTTP requests")
	)
	flag.Parse()

	zp := *zipPath
	downloadedHere := false
	if zp == "" {
		url, err := mastr.DiscoverLatestDownloadURL(*userAgent)
		if err != nil {
			log.Fatalf("discovering download URL: %v", err)
		}
		zp = filepath.Join(filepath.Dir(*dbPath), filepath.Base(url))
		log.Printf("downloading %s -> %s", url, zp)
		start := time.Now()
		if err := mastr.DownloadZip(url, zp, *userAgent); err != nil {
			log.Fatalf("downloading zip: %v", err)
		}
		downloadedHere = true
		log.Printf("download finished in %s", time.Since(start).Round(time.Second))
	}

	sourceDate, sourceVersion, err := mastr.ParseSourceFileName(filepath.Base(zp))
	if err != nil {
		log.Fatalf("parsing source file name of %s: %v", zp, err)
	}

	tmpDBPath := *dbPath + ".importing"
	if err := os.Remove(tmpDBPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("removing stale temp database %s: %v", tmpDBPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(tmpDBPath), 0o755); err != nil {
		log.Fatalf("creating directory for %s: %v", tmpDBPath, err)
	}
	db, err := sql.Open("sqlite", tmpDBPath)
	if err != nil {
		log.Fatalf("opening temp database %s: %v", tmpDBPath, err)
	}
	// A single connection avoids SQLite's writer-locking entirely: with the
	// default database/sql pool, ALTER TABLE (schema change) and pending
	// INSERT transactions can land on different pooled connections and
	// collide with SQLITE_BUSY even though the whole import is logically
	// single-threaded.
	db.SetMaxOpenConns(1)

	log.Printf("importing %s -> %s", zp, tmpDBPath)
	start := time.Now()
	stats, err := mastr.ImportZip(zp, db, func(table string, records int) {
		log.Printf("  %-45s %8d records", table, records)
	})
	if err != nil {
		db.Close()
		log.Fatalf("import failed: %v", err)
	}

	if err := mastr.WriteMeta(db, mastr.Meta{
		SourceFile:    filepath.Base(zp),
		SourceDate:    sourceDate,
		SourceVersion: sourceVersion,
		ImportedAt:    time.Now(),
	}); err != nil {
		db.Close()
		log.Fatalf("writing meta: %v", err)
	}
	if err := db.Close(); err != nil {
		log.Fatalf("closing temp database: %v", err)
	}

	if err := os.Rename(tmpDBPath, *dbPath); err != nil {
		log.Fatalf("replacing %s with %s: %v", *dbPath, tmpDBPath, err)
	}

	total := 0
	for _, n := range stats {
		total += n
	}
	log.Printf("import complete in %s: %d datasets, %d records total -> %s",
		time.Since(start).Round(time.Second), len(stats), total, *dbPath)

	if !*keepZip {
		if err := os.Remove(zp); err != nil {
			log.Printf("warning: could not remove zip %s: %v", zp, err)
		} else {
			log.Printf("removed %s", zp)
		}
	} else if !downloadedHere {
		fmt.Fprintln(os.Stderr, "note: -zip was given an existing file and -keep-zip is set, leaving it in place")
	}
}
