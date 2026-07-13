// Command catalogimport seeds a JAG SQLite database's ParameterCatalog
// table from the JSON files under catalog/ (see internal/impl/catalog).
// Intended to run whenever a new database is created, so the default
// catalog (cables, fuses, transformers, ...) is always present — see
// Konzept.md's Sachdaten/ParameterCatalog section.
package main

import (
	"flag"
	"fmt"
	"os"

	implcatalog "gitlab.com/openk-nsc/jag/internal/impl/catalog"
	"gitlab.com/openk-nsc/jag/internal/sqlite"
)

func main() {
	dbPath := flag.String("db", "jag.db", "path to the SQLite database to seed")
	catalogDir := flag.String("catalog", "catalog", "directory containing the catalog *.json seed files")
	flag.Parse()

	entries, err := implcatalog.LoadDir(*catalogDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading catalog files: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("loaded %d catalog entries from %s\n", len(entries), *catalogDir)

	store, err := sqlite.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening %s: %v\n", *dbPath, err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Catalog().Upsert(entries); err != nil {
		fmt.Fprintf(os.Stderr, "upserting catalog entries: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("seeded %d catalog entries into %s\n", len(entries), *dbPath)

	byKind := map[string]int{}
	for _, e := range entries {
		for _, a := range e.Attributes {
			if a.Key == "catalog_kind" {
				if kind, ok := a.Value.(string); ok {
					byKind[kind]++
				}
			}
		}
	}
	for kind, count := range byKind {
		fmt.Printf("  %-14s %d\n", kind, count)
	}
}
