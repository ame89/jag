// Command catalogimport seeds a JAG SQLite database's ParameterCatalog
// table from JAG's bundled catalog (see pkg/impl/catalog, embedded via
// go:embed) or, if -catalog is given, from a caller-supplied directory of
// *.json seed files instead. Intended to run whenever a new database is
// created, so the default catalog (cables, fuses, transformers, ...) is
// always present — see Konzept.md's Sachdaten/ParameterCatalog section.
package main

import (
	"flag"
	"fmt"
	"os"

	coremodel "github.com/ame89/jag/pkg/core/model"
	implcatalog "github.com/ame89/jag/pkg/impl/catalog"
	"github.com/ame89/jag/pkg/sqlite"
)

func main() {
	dbPath := flag.String("db", "jag.db", "path to the SQLite database to seed")
	catalogDir := flag.String("catalog", "", "optional directory of *.json seed files to use instead of JAG's bundled catalog")
	flag.Parse()

	var (
		entries []coremodel.CatalogEntry
		err     error
		source  string
	)
	if *catalogDir == "" {
		entries, err = implcatalog.Default()
		source = "JAG's bundled catalog"
	} else {
		entries, err = implcatalog.LoadDir(*catalogDir)
		source = *catalogDir
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading catalog: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("loaded %d catalog entries from %s\n", len(entries), source)

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
