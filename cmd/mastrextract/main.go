// Command mastrextract builds a slimmed-down regional copy of an imported
// mastr.db (see cmd/mastrimport), keeping only units located in a given set
// of Landkreise/kreisfreie Städte (plus, for wind, offshore units in the
// North Sea) and every table row that transitively belongs to them
// (their Anlagen-Zusatzdaten, Lokation, Netzanschlusspunkt, Marktakteur).
//
// Default region ("nds-west"): the western part of Lower Saxony as
// requested — Emsland/Grafschaft Bentheim, the Osnabrück area, the whole
// coast (Ostfriesland, Wilhelmshaven, Cuxhaven) including North Sea
// offshore wind, the Oldenburger Münsterland, the Bremen area (Bremen
// itself is a separate Bundesland but explicitly requested), and the
// southward corridor down to the Hessian border (Diepholz, Nienburg,
// Schaumburg, Hameln-Pyrmont, Holzminden).
//
// Catalog/lookup tables (Katalogkategorien, Katalogwerte, Lokationstypen,
// Marktfunktionen, Marktrollen, Einheitentypen, Bilanzierungsgebiete,
// Netze) are copied in full — they're small and needed to resolve coded
// fields regardless of region.
//
// Deliberately NOT carried over into the regional copy: the audit/history
// tables (EinheitenAenderungNetzbetreiberzuordnungen,
// GeloeschteUndDeaktivierteEinheiten, GeloeschteUndDeaktivierteMarktakteure),
// EinheitenGenehmigung (permit bookkeeping), and Ertuechtigungen (small
// repowering-approval bookkeeping table). None of these carry information
// needed for a "current state of the regional grid" working copy; they can
// be added later if a concrete use case needs them.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"gitlab.com/openk-nsc/jag/internal/mastr"
)

// landkreise is the set of Landkreis/kreisfreie-Stadt names (exactly as
// they appear in MaStR's own "Landkreis" field) that define the "nds-west"
// region. Bremen/Bremerhaven are included even though they belong to the
// separate Bundesland Bremen, per explicit request ("bis zum Bremer
// Raum"). Cloppenburg and Vechta (Oldenburger Münsterland) are included
// too even though not literally named in the original request, since
// without them the region would have a hole between Emsland/Osnabrück and
// Oldenburg/Bremen.
var landkreise = []string{
	"Emsland", "Grafschaft Bentheim", "Osnabrück",
	"Aurich", "Wittmund", "Leer", "Emden",
	"Friesland", "Wilhelmshaven", "Ammerland",
	"Cloppenburg", "Vechta",
	"Oldenburg", "Oldenburg (Oldb)", "Wesermarsch", "Cuxhaven", "Osterholz",
	"Delmenhorst", "Verden", "Diepholz", "Nienburg (Weser)",
	"Schaumburg", "Hameln-Pyrmont", "Holzminden",
	"Bremen", "Bremerhaven",
}

// geoUnitTables are the Einheiten* tables that carry a "Landkreis" column
// and therefore can be filtered by region directly. windTable additionally
// gets an OR'd offshore clause (see offshoreClause).
var geoUnitTables = []string{
	"EinheitenWind",
	"EinheitenSolar",
	"EinheitenBiomasse",
	"EinheitenWasser",
	"EinheitenVerbrennung",
	"EinheitenStromSpeicher",
	"EinheitenStromVerbraucher",
	"EinheitenGasErzeuger",
	"EinheitenGasverbraucher",
	"EinheitenGasSpeicher",
	"EinheitenKernkraft",
	"EinheitenGeothermieGrubengasDruckentspannung",
}

const windTable = "EinheitenWind"

// catalogTables are copied in full regardless of region.
var catalogTables = []string{
	"Katalogkategorien", "Katalogwerte", "Lokationstypen",
	"Marktfunktionen", "Marktrollen", "Einheitentypen",
	"Bilanzierungsgebiete", "Netze",
}

func main() {
	srcPath := flag.String("src", ".data/mastr/mastr.db", "path to the source mastr.db (see cmd/mastrimport)")
	outPath := flag.String("out", ".data/mastr/mastr.nds-west.db", "path to the regional database to (re-)build")
	flag.Parse()

	if err := run(*srcPath, *outPath); err != nil {
		log.Fatal(err)
	}
}

func run(srcPath, outPath string) error {
	if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale %s: %w", outPath, err)
	}

	db, err := sql.Open("sqlite", outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`ATTACH DATABASE ? AS src`, srcPath); err != nil {
		return fmt.Errorf("attaching source %s: %w", srcPath, err)
	}

	lkList := quoteList(landkreise)

	// 1. Geo-filtered Einheiten* tables: region match on Landkreis, plus
	// (wind only) an OR'd North-Sea-offshore clause — offshore units have
	// no Landkreis at all, so they'd otherwise be dropped entirely.
	for _, t := range geoUnitTables {
		where := fmt.Sprintf(`Landkreis IN (%s)`, lkList)
		if t == windTable {
			where += ` OR (GebietNachDemFlaechenentwicklungsplanNordsee IS NOT NULL AND GebietNachDemFlaechenentwicklungsplanNordsee != '')`
		}
		if err := createFiltered(db, t, where); err != nil {
			return err
		}
	}

	// 2. AnlagenEeg* / AnlagenKwk / AnlagenGasSpeicher / AnlagenStromSpeicher:
	// each keeps only rows whose own MaStR-Nummer is referenced from an
	// already-copied (and thus already region-filtered) Einheiten* table.
	eegLinks := map[string][]string{ // Anlagen table -> Einheiten* tables referencing it via EegMaStRNummer
		"AnlagenEegWind":        {"EinheitenWind"},
		"AnlagenEegSolar":       {"EinheitenSolar"},
		"AnlagenEegBiomasse":    {"EinheitenBiomasse"},
		"AnlagenEegWasser":      {"EinheitenWasser"},
		"AnlagenEegGeothermieGrubengasDruckentspannung": {"EinheitenGeothermieGrubengasDruckentspannung"},
	}
	for anlage, units := range eegLinks {
		sub := unionSelect(units, "EegMaStRNummer")
		where := fmt.Sprintf(`EegMaStRNummer IN (%s)`, sub)
		if err := createFiltered(db, anlage, where); err != nil {
			return err
		}
	}

	kwkUnits := []string{"EinheitenBiomasse", "EinheitenVerbrennung", "EinheitenGeothermieGrubengasDruckentspannung"}
	if err := createFiltered(db, "AnlagenKwk",
		fmt.Sprintf(`KwkMastrNummer IN (%s)`, unionSelect(kwkUnits, "KwkMaStRNummer"))); err != nil {
		return err
	}

	gasSpeicherUnits := []string{"EinheitenGasSpeicher", "EinheitenGasErzeuger"}
	if err := createFiltered(db, "AnlagenGasSpeicher",
		fmt.Sprintf(`MaStRNummer IN (%s)`, unionSelect(gasSpeicherUnits, "SpeicherMaStRNummer"))); err != nil {
		return err
	}

	if err := createFiltered(db, "AnlagenStromSpeicher",
		fmt.Sprintf(`MaStRNummer IN (%s)`, unionSelect([]string{"EinheitenStromSpeicher"}, "SpeMastrNummer"))); err != nil {
		return err
	}

	// 3. Lokationen: referenced from any already-copied Einheiten* table
	// via LokationMaStRNummer.
	if err := createFiltered(db, "Lokationen",
		fmt.Sprintf(`MastrNummer IN (%s)`, unionSelect(geoUnitTables, "LokationMaStRNummer"))); err != nil {
		return err
	}

	// 4. Netzanschlusspunkte: referenced from the just-copied Lokationen.
	if err := createFiltered(db, "Netzanschlusspunkte",
		`LokationMaStRNummer IN (SELECT MastrNummer FROM Lokationen)`); err != nil {
		return err
	}

	// 5. Marktakteure: referenced as (Alt-)AnlagenbetreiberMastrNummer from
	// any already-copied Einheiten* table, or as the responsible
	// Netzbetreiber of an already-copied Netzanschlusspunkt (without this,
	// grid operators — the actors most likely to actually have a
	// MarktakteureUndRollen entry — would be dropped entirely, since
	// they're rarely also a plant's Anlagenbetreiber).
	betreiberUnion := unionSelect(geoUnitTables, "AnlagenbetreiberMastrNummer") +
		" UNION " + unionSelect(geoUnitTables, "AltAnlagenbetreiberMastrNummer") +
		" UNION " + unionSelect([]string{"Netzanschlusspunkte"}, "NetzbetreiberMaStRNummer")
	if err := createFiltered(db, "Marktakteure",
		fmt.Sprintf(`MastrNummer IN (%s)`, betreiberUnion)); err != nil {
		return err
	}

	// 6. MarktakteureUndRollen: referenced from the just-copied Marktakteure.
	if err := createFiltered(db, "MarktakteureUndRollen",
		`MarktakteurMastrNummer IN (SELECT MastrNummer FROM Marktakteure)`); err != nil {
		return err
	}

	// 7. Catalog/lookup tables: copied in full.
	for _, t := range catalogTables {
		if err := createFull(db, t); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`DETACH DATABASE src`); err != nil {
		return fmt.Errorf("detaching source: %w", err)
	}

	// Carry over the source's Meta (same export snapshot, just a regional
	// subset of it) rather than fabricating a new one.
	srcDB, err := sql.Open("sqlite", srcPath)
	if err != nil {
		return fmt.Errorf("reopening source for meta: %w", err)
	}
	defer srcDB.Close()
	var m mastr.Meta
	row := srcDB.QueryRow(`SELECT source_file, source_date, source_version, imported_at FROM meta WHERE id = 1`)
	var sourceDate, importedAt string
	if err := row.Scan(&m.SourceFile, &sourceDate, &m.SourceVersion, &importedAt); err != nil {
		return fmt.Errorf("reading source meta: %w", err)
	}
	if m.SourceDate, err = time.Parse(time.RFC3339, sourceDate); err != nil {
		return fmt.Errorf("parsing source_date: %w", err)
	}
	if m.ImportedAt, err = time.Parse(time.RFC3339, importedAt); err != nil {
		return fmt.Errorf("parsing imported_at: %w", err)
	}
	if err := mastr.WriteMeta(db, m); err != nil {
		return fmt.Errorf("writing meta: %w", err)
	}

	return printStats(db, outPath)
}

// createFiltered creates table (in the target/main schema) as the rows of
// src.table matching where.
func createFiltered(db *sql.DB, table, where string) error {
	q := fmt.Sprintf(`CREATE TABLE %q AS SELECT * FROM src.%q WHERE %s`, table, table, where)
	if _, err := db.Exec(q); err != nil {
		return fmt.Errorf("copying %s: %w", table, err)
	}
	return nil
}

// createFull copies a whole table over unfiltered (used for small catalog
// tables that are needed regardless of region).
func createFull(db *sql.DB, table string) error {
	q := fmt.Sprintf(`CREATE TABLE %q AS SELECT * FROM src.%q`, table, table)
	if _, err := db.Exec(q); err != nil {
		return fmt.Errorf("copying %s: %w", table, err)
	}
	return nil
}

// unionSelect builds "SELECT col FROM t1 WHERE col IS NOT NULL AND col!=''
// UNION SELECT col FROM t2 ..." across tables, for tables that may not all
// carry col with the exact same non-empty semantics — used to gather the
// set of referenced MaStR-Nummern from several already-copied unit tables.
// Tables that don't have the column are silently skipped by the caller
// having pre-filtered the list (all callers here only pass tables known to
// carry the column).
func unionSelect(tables []string, col string) string {
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT %q FROM %q WHERE %q IS NOT NULL AND %q != ''`, col, t, col, col)
	}
	return strings.Join(parts, " UNION ")
}

func quoteList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return strings.Join(quoted, ", ")
}

func printStats(db *sql.DB, outPath string) error {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name != 'meta' ORDER BY name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		tables = append(tables, n)
	}
	total := 0
	fmt.Printf("=== %s ===\n", outPath)
	for _, t := range tables {
		var n int
		if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %q`, t)).Scan(&n); err != nil {
			return err
		}
		fmt.Printf("  %-45s %10d\n", t, n)
		total += n
	}
	fmt.Printf("  %-45s %10d\n", "TOTAL", total)
	return nil
}
