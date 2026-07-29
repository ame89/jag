package postgres

import "strings"

// rebind rewrites a SQLite-style query written with "?" positional
// placeholders (the style every query in this codebase — and its sibling
// package internal/sqlite — is written in) into PostgreSQL's native
// "$1, $2, ..." positional placeholder syntax.
//
// This lets every query/DDL string in this package be written and read
// exactly like internal/sqlite's originals (including the shared
// placeholders/idArgs/unionAllSelects helpers, which still emit "?"), with
// only this one translation step standing between them and a real
// database/sql call — rather than hand-converting every "?" occurrence to
// its correct "$N" number (error-prone, especially for the dynamically
// sized "IN (%s)" lists built via placeholders(n)). None of the queries in
// this package contain a literal '?' character inside a string constant
// (CIM/JAG IDs and Sachdaten values are bound as parameters, never spliced
// into the SQL text itself), so a naive "replace every '?' encountered, in
// order, with the next $N" pass is safe.
func rebind(query string) string {
	if !strings.ContainsRune(query, '?') {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// itoa is a tiny, allocation-light int->string helper (avoids pulling in
// strconv just for this one call site's use — kept private/minimal since
// rebind is the only caller and n is always small, positive, and bounded
// by a single query's placeholder count).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
