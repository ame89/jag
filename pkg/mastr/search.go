package mastr

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Row is one generic query result row: column name -> value. All MaStR
// data columns are stored as TEXT (see import.go), so values are always
// strings; a nil map value would only occur for a NULL/absent field, which
// Row represents by simply omitting the key (see scanRows).
type Row map[string]string

// Tables returns the names of every imported MaStR dataset table (i.e.
// every table except the internal "meta" bookkeeping table), sorted
// alphabetically. Useful for building a generic "search across everything"
// UI/CLI without hardcoding dataset names.
func (s *Store) Tables() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT name FROM sqlite_master
		 WHERE type = 'table' AND name != 'meta' AND name NOT LIKE 'sqlite_%'
		 ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("mastr: listing tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("mastr: scanning table name: %w", err)
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// Columns returns the column names of table, in schema order, including
// the internal "_mastr_row_id" primary key column. Returns an error if
// table doesn't exist (or isn't a valid identifier).
func (s *Store) Columns(table string) ([]string, error) {
	if err := validateIdent(table); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdent(table)))
	if err != nil {
		return nil, fmt.Errorf("mastr: reading schema of %s: %w", table, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("mastr: scanning schema of %s: %w", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("mastr: table %q does not exist", table)
	}
	return columns, nil
}

// Filter is one exact-match condition for Query: column must equal Value.
type Filter struct {
	Column string
	Value  string
}

// Query returns rows from table matching all of filters (AND-combined,
// exact match), ordered by "_mastr_row_id", up to limit rows starting at
// offset. Pass no filters to page through the whole table. limit <= 0
// means "no limit".
func (s *Store) Query(table string, filters []Filter, limit, offset int) ([]Row, error) {
	if err := validateIdent(table); err != nil {
		return nil, err
	}
	var where []string
	var args []any
	for _, f := range filters {
		if err := validateIdent(f.Column); err != nil {
			return nil, err
		}
		where = append(where, fmt.Sprintf("%s = ?", quoteIdent(f.Column)))
		args = append(args, f.Value)
	}

	query := fmt.Sprintf(`SELECT * FROM %s`, quoteIdent(table))
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY "_mastr_row_id"`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("mastr: querying %s: %w", table, err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// Search performs a substring search (case-insensitive, SQLite LIKE) for
// term across every column of table, OR-combined: a row matches if any
// column contains term. Intended for ad-hoc/UI lookups (e.g. "find
// anything mentioning this street or Marktakteur name"), not as a
// performance-critical path — it's a full table scan with no index
// support, since every dataset's columns are discovered dynamically at
// import time and this never has out-of-the-box indexes to lean on.
func (s *Store) Search(table string, term string, limit int) ([]Row, error) {
	if err := validateIdent(table); err != nil {
		return nil, err
	}
	columns, err := s.Columns(table)
	if err != nil {
		return nil, err
	}

	var like []string
	var args []any
	needle := "%" + escapeLike(term) + "%"
	for _, c := range columns {
		if c == "_mastr_row_id" {
			continue
		}
		like = append(like, fmt.Sprintf("%s LIKE ? ESCAPE '\\'", quoteIdent(c)))
		args = append(args, needle)
	}
	if len(like) == 0 {
		return nil, nil
	}

	query := fmt.Sprintf(`SELECT * FROM %s WHERE %s ORDER BY "_mastr_row_id"`,
		quoteIdent(table), strings.Join(like, " OR "))
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("mastr: searching %s: %w", table, err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// SearchAll runs Search(term) across every table returned by Tables(),
// capping each table's result at limitPerTable rows. Returns only tables
// that had at least one match. This is the "search everything" entry
// point for callers that don't know (or don't want to hardcode) which
// dataset a term might appear in.
func (s *Store) SearchAll(term string, limitPerTable int) (map[string][]Row, error) {
	tables, err := s.Tables()
	if err != nil {
		return nil, err
	}
	results := make(map[string][]Row)
	for _, table := range tables {
		rows, err := s.Search(table, term, limitPerTable)
		if err != nil {
			return nil, fmt.Errorf("mastr: searching table %s: %w", table, err)
		}
		if len(rows) > 0 {
			results[table] = rows
		}
	}
	return results, nil
}

// scanRows converts the current result set into []Row, using each row's
// own column list (from rows.Columns()) rather than a fixed struct, since
// every MaStR table has a different, dynamically-discovered schema. NULL
// values are omitted from the resulting Row rather than represented as an
// empty string, so callers can distinguish "field absent" from "field
// present but empty".
func scanRows(rows *sql.Rows) ([]Row, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("mastr: reading result columns: %w", err)
	}

	var result []Row
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("mastr: scanning row: %w", err)
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			if vals[i].Valid {
				row[c] = vals[i].String
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// escapeLike escapes SQLite LIKE metacharacters (% and _) in a
// user-supplied search term so Search/SearchAll treat it as a literal
// substring rather than a pattern.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// validateIdent rejects identifiers that aren't plain SQLite identifiers
// (letters, digits, underscore, starting with a letter/underscore) before
// they're interpolated into generated SQL. All real MaStR table/column
// names satisfy this; this guards against building queries from arbitrary
// caller-supplied strings (e.g. a table name coming from an HTTP request).
func validateIdent(s string) error {
	if s == "" {
		return fmt.Errorf("mastr: empty identifier")
	}
	for i, r := range s {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("mastr: invalid identifier %q", s)
		}
	}
	return nil
}

// SortedTables is a convenience for callers that already have a []string
// of table names (e.g. from a config file) and want them in the same
// canonical order Tables() returns.
func SortedTables(tables []string) []string {
	out := append([]string(nil), tables...)
	sort.Strings(out)
	return out
}
