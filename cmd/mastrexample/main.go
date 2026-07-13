// Command mastrexample demonstrates the internal/mastr generic search API
// (Store.Tables, Store.Columns, Store.Query, Store.Search, Store.SearchAll)
// against an already-imported mastr.db (see cmd/mastrimport). It's meant to
// be read as documentation-by-example, not as a real CLI tool: run it, look
// at both the code and the output side by side.
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"gitlab.com/openk-nsc/jag/internal/mastr"
)

func main() {
	dbPath := flag.String("db", ".data/mastr/mastr.nds-west.db", "path to the imported mastr database")
	flag.Parse()

	store, err := mastr.Open(mastr.Config{File: *dbPath})
	if err != nil {
		log.Fatalf("opening %s: %v", *dbPath, err)
	}
	defer store.Close()

	// 0. Which export is this database built from?
	if m, err := store.Meta(); err != nil {
		log.Printf("meta: %v (older/partial database?)", err)
	} else {
		fmt.Printf("=== Meta ===\nsource: %s (Stichtag %s, Version %s), imported %s\n\n",
			m.SourceFile, m.SourceDate.Format("2006-01-02"), m.SourceVersion, m.ImportedAt.Format("2006-01-02 15:04:05"))
	}

	// 1. Which tables exist at all? Useful when you don't know (or don't
	// want to hardcode) the dataset name up front.
	tables, err := store.Tables()
	if err != nil {
		log.Fatalf("listing tables: %v", err)
	}
	fmt.Printf("=== Tables (%d) ===\n%s\n\n", len(tables), strings.Join(tables, ", "))

	// 2. What columns does one specific table have? Column sets are
	// discovered dynamically at import time (see internal/mastr/import.go),
	// so this is the authoritative way to find out what's queryable rather
	// than guessing from MaStR's documentation.
	if cols, err := store.Columns("EinheitenWind"); err != nil {
		log.Printf("columns of EinheitenWind: %v", err)
	} else {
		fmt.Printf("=== Columns of EinheitenWind ===\n%s\n\n", strings.Join(cols, ", "))
	}

	// 3. Exact-match query: find wind turbine units in a given postal code.
	// Filter is AND-combined and exact-match; multiple Filters narrow
	// further (e.g. add {Column: "Gemeinde", Value: "Musterstadt"}).
	rows, err := store.Query("EinheitenWind", []mastr.Filter{
		{Column: "Postleitzahl", Value: "34298"},
	}, 5, 0)
	if err != nil {
		log.Printf("querying EinheitenWind by Postleitzahl: %v", err)
	} else {
		fmt.Printf("=== EinheitenWind where Postleitzahl = 34298 (first %d) ===\n", len(rows))
		for _, r := range rows {
			fmt.Printf("  %s: %s, %s %s (Breitengrad=%s, Laengengrad=%s)\n",
				r["EinheitMastrNummer"], r["Strasse"], r["Postleitzahl"], r["Ort"], r["Breitengrad"], r["Laengengrad"])
		}
		fmt.Println()
	}

	// 4. Paging through a table: Query with no filters + limit/offset.
	page2, err := store.Query("Katalogwerte", nil, 5, 5)
	if err != nil {
		log.Printf("paging Katalogwerte: %v", err)
	} else {
		fmt.Printf("=== Katalogwerte, rows 6-10 ===\n")
		for _, r := range page2 {
			fmt.Printf("  %v\n", r)
		}
		fmt.Println()
	}

	// 5. Substring search within one table (e.g. find a Marktakteur, a
	// grid operator, by (partial) company name). Case-insensitive, matches
	// anywhere in any column of that table.
	actors, err := store.Search("Marktakteure", "Stadtwerke Musterstadt", 5)
	if err != nil {
		log.Printf("searching Marktakteure: %v", err)
	} else {
		fmt.Printf("=== Marktakteure matching \"Stadtwerke Musterstadt\" (%d) ===\n", len(actors))
		for _, r := range actors {
			fmt.Printf("  %s: %s\n", r["MastrNummer"], r["Name"])
		}
		fmt.Println()
	}

	// 6. "I don't know which table it's in" search: SearchAll scans every
	// table (capped per table) and returns only tables with a hit. Handy
	// for interactive lookups (e.g. searching an address fragment or a
	// company name that could show up in Marktakteure, Lokationen, or an
	// Einheiten* table).
	all, err := store.SearchAll("Werkstraße 7", 3)
	if err != nil {
		log.Printf("searching all tables: %v", err)
	} else {
		fmt.Printf("=== SearchAll(\"Werkstraße 7\") — tables with matches: %d ===\n", len(all))
		for table, rows := range all {
			fmt.Printf("  %s: %d match(es)\n", table, len(rows))
		}
	}
}
