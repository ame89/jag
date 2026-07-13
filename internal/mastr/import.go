// Package mastr provides access to a local SQLite copy of the German
// Marktstammdatenregister (MaStR) bulk export ("Gesamtdatenexport"), plus
// the pipeline to (re-)build that copy from the official zip download.
//
// The export is a zip of ~400 UTF-16-encoded XML files, one per dataset
// (e.g. "EinheitenSolar", "Lokationen", "Netzanschlusspunkte", ...), split
// into numbered chunks for large datasets (e.g. "EinheitenSolar_12.xml").
// Every dataset observed so far is flat: <Dataset><Record><Field>value
// </Field>...</Record>...</Dataset> with no nesting inside a Record, and
// records within one dataset don't all carry the same set of fields
// (optional fields are simply omitted). ImportZip exploits this: it builds
// one SQLite table per dataset with columns discovered on the fly
// (ALTER TABLE ADD COLUMN as new fields are seen), rather than requiring a
// predefined schema per dataset.
package mastr

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gitlab.com/openk-nsc/jag/internal/xmlsax"
)

// datasetNamePattern strips an optional numeric "_<N>" chunk suffix before
// ".xml", e.g. "EinheitenSolar_12.xml" -> "EinheitenSolar",
// "Katalogwerte.xml" -> "Katalogwerte".
var datasetNamePattern = regexp.MustCompile(`^(.+?)(?:_(\d+))?\.xml$`)

// datasetGroup is one MaStR dataset, potentially split across several
// numbered zip entries that must all be imported into the same table.
type datasetGroup struct {
	table   string
	entries []*zip.File
}

// groupDatasetEntries groups a zip's file list by dataset name and orders
// each group's chunks numerically (Name_2.xml before Name_10.xml).
func groupDatasetEntries(files []*zip.File) []datasetGroup {
	byName := map[string][]*zip.File{}
	var order []string
	type chunkInfo struct {
		file *zip.File
		n    int
	}
	chunks := map[string][]chunkInfo{}

	for _, f := range files {
		m := datasetNamePattern.FindStringSubmatch(f.Name)
		if m == nil {
			continue // not a dataset XML file (shouldn't happen in a real export)
		}
		name := m[1]
		n := 0
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		}
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = append(byName[name], f)
		chunks[name] = append(chunks[name], chunkInfo{file: f, n: n})
	}

	sort.Strings(order)
	groups := make([]datasetGroup, 0, len(order))
	for _, name := range order {
		cs := chunks[name]
		sort.Slice(cs, func(i, j int) bool { return cs[i].n < cs[j].n })
		entries := make([]*zip.File, len(cs))
		for i, c := range cs {
			entries[i] = c.file
		}
		groups = append(groups, datasetGroup{table: name, entries: entries})
	}
	return groups
}

// quoteIdent quotes a SQLite identifier (table/column name) for safe use in
// generated SQL. MaStR field/dataset names are plain German words (letters,
// digits, underscore), but quoting defensively costs nothing.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// tableSchema tracks the currently known columns of one dataset's table
// (in the order they were discovered/added), so ImportZip can detect new
// fields and extend the table (and its prepared INSERT statement) on the
// fly.
type tableSchema struct {
	table   string
	columns []string
	colSet  map[string]bool
	insert  *sql.Stmt
}

func newTableSchema(db *sql.DB, table string) (*tableSchema, error) {
	if _, err := db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quoteIdent(table))); err != nil {
		return nil, fmt.Errorf("mastr: dropping existing table %s: %w", table, err)
	}
	// "_mastr_row_id" (rather than a plain "id") to avoid a collision with
	// a same-named MaStR data field: SQLite column names are
	// case-insensitive, and several datasets (e.g. Katalogkategorien,
	// Katalogwerte) have their own "Id" field.
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (_mastr_row_id INTEGER PRIMARY KEY AUTOINCREMENT)`, quoteIdent(table))); err != nil {
		return nil, fmt.Errorf("mastr: creating table %s: %w", table, err)
	}
	return &tableSchema{table: table, colSet: map[string]bool{}}, nil
}

// ensureColumns extends the table with any column in fields not already
// known, then (re)prepares the INSERT statement if the column set changed.
// Field names are deduplicated case-insensitively (colSet keys are
// lowercased) since SQLite column names are themselves case-insensitive —
// two differently-cased field names would otherwise collide on ALTER TABLE.
//
// tx is used for both ALTER TABLE and (re-)PREPARE: with the single
// connection the importer uses (db.SetMaxOpenConns(1)), issuing these
// against db directly while a transaction is in flight on that same sole
// connection would deadlock (db.Exec would wait forever for a free
// connection that tx is holding).
func (t *tableSchema) ensureColumns(tx *sql.Tx, fields map[string]string) error {
	changed := false
	for name := range fields {
		key := strings.ToLower(name)
		if t.colSet[key] {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s TEXT`, quoteIdent(t.table), quoteIdent(name))); err != nil {
			return fmt.Errorf("mastr: adding column %s to %s: %w", name, t.table, err)
		}
		t.colSet[key] = true
		t.columns = append(t.columns, name)
		changed = true
	}
	if changed || t.insert == nil {
		if err := t.rebindInsert(tx); err != nil {
			return err
		}
	}
	return nil
}

// rebindInsert (re-)prepares the INSERT statement for the current column
// set against tx. A prepared statement created via Tx.Prepare is only
// valid for that one transaction, so this must be called again against
// every new transaction the importer begins (see commit() in
// importDatasetGroup), not just when the column set changes.
func (t *tableSchema) rebindInsert(tx *sql.Tx) error {
	if len(t.columns) == 0 {
		return nil
	}
	placeholders := make([]string, len(t.columns))
	quotedCols := make([]string, len(t.columns))
	for i, c := range t.columns {
		placeholders[i] = "?"
		quotedCols[i] = quoteIdent(c)
	}
	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`,
		quoteIdent(t.table), strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
	stmt, err := tx.Prepare(query)
	if err != nil {
		return fmt.Errorf("mastr: preparing insert for %s: %w", t.table, err)
	}
	t.insert = stmt
	return nil
}

func (t *tableSchema) insertRecord(tx *sql.Tx, fields map[string]string) error {
	args := make([]any, len(t.columns))
	for i, c := range t.columns {
		if v, ok := fields[c]; ok {
			args[i] = v
		} else {
			args[i] = nil
		}
	}
	_, err := t.insert.Exec(args...)
	return err
}

// batchSize is how many records are committed per SQLite transaction
// during import. Large enough to amortize transaction overhead, small
// enough to bound WAL growth on multi-GB imports.
const batchSize = 5000

// ImportZip reads a MaStR Gesamtdatenexport zip and (re-)builds one table
// per dataset in db, replacing any existing tables of the same name. It
// does not write the "meta" table (see WriteMeta) or delete the zip
// afterwards — callers own that (see cmd/mastrimport).
//
// progress, if non-nil, is called after each dataset finishes with the
// dataset name and the number of records imported for it.
func ImportZip(zipPath string, db *sql.DB, progress func(table string, records int)) (map[string]int, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("mastr: opening zip %s: %w", zipPath, err)
	}
	defer zr.Close()

	groups := groupDatasetEntries(zr.File)
	stats := make(map[string]int, len(groups))

	for _, g := range groups {
		n, err := importDatasetGroup(db, g)
		if err != nil {
			return stats, fmt.Errorf("mastr: importing dataset %s: %w", g.table, err)
		}
		stats[g.table] = n
		if progress != nil {
			progress(g.table, n)
		}
	}
	return stats, nil
}

// importDatasetGroup imports every chunk of one dataset into its own table
// and returns the number of records imported.
func importDatasetGroup(db *sql.DB, g datasetGroup) (int, error) {
	schema, err := newTableSchema(db, g.table)
	if err != nil {
		return 0, err
	}
	defer func() {
		if schema.insert != nil {
			schema.insert.Close()
		}
	}()

	total := 0
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("mastr: beginning transaction: %w", err)
	}
	pending := 0

	commit := func() error {
		if pending == 0 {
			return nil
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("mastr: committing batch: %w", err)
		}
		tx, err = db.Begin()
		if err != nil {
			return fmt.Errorf("mastr: beginning transaction: %w", err)
		}
		if err := schema.rebindInsert(tx); err != nil {
			return err
		}
		pending = 0
		return nil
	}

	for _, entry := range g.entries {
		rc, err := entry.Open()
		if err != nil {
			return total, fmt.Errorf("mastr: opening %s: %w", entry.Name, err)
		}
		utf8r, err := newUTF8Reader(rc)
		if err != nil {
			rc.Close()
			return total, fmt.Errorf("mastr: decoding %s: %w", entry.Name, err)
		}
		dec := xmlsax.NewDecoder(utf8r)

		depth := 0
		var fields map[string]string
		var currentField string
		var fieldText strings.Builder

		for {
			tok, err := dec.Token()
			if err != nil {
				break // io.EOF or malformed tail; treated as end of this file
			}
			switch tok.Kind {
			case xmlsax.StartElement:
				depth++
				switch depth {
				case 2: // <Record> start
					fields = map[string]string{}
				case 3: // <Field> start
					currentField = tok.Name
					fieldText.Reset()
				}
			case xmlsax.CharData:
				if depth == 3 {
					fieldText.WriteString(tok.Text)
				}
			case xmlsax.EndElement:
				switch depth {
				case 3: // <Field> end
					fields[currentField] = fieldText.String()
					currentField = ""
				case 2: // <Record> end
					if err := schema.ensureColumns(tx, fields); err != nil {
						rc.Close()
						return total, err
					}
					if err := schema.insertRecord(tx, fields); err != nil {
						rc.Close()
						return total, fmt.Errorf("mastr: inserting into %s: %w", g.table, err)
					}
					total++
					pending++
					if pending >= batchSize {
						if err := commit(); err != nil {
							rc.Close()
							return total, err
						}
					}
					fields = nil
				}
				depth--
			}
		}
		rc.Close()
	}

	// Whatever's left in tx (whether it still holds pending rows, or is a
	// fresh empty transaction started by the last in-loop commit()) must be
	// closed explicitly.
	if err := tx.Commit(); err != nil {
		return total, fmt.Errorf("mastr: committing final transaction: %w", err)
	}
	return total, nil
}
