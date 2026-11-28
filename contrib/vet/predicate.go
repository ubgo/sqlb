package vet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// The comparison of two partial-index predicates, the declared one and the one
// the database reports.
//
// The rule has two steps. First both texts are put in a normal form that only
// removes spelling the engines ignore: runs of white space become one space,
// everything outside a quoted literal or identifier is lower-cased, a quoted
// identifier that would read the same unquoted loses its quotes, no space is
// kept inside a parenthesis, and parentheses wrapping the whole predicate are
// dropped. Equal normal forms are the same predicate on every engine.
//
// When the normal forms differ the texts may still mean the same: Postgres
// re-prints a predicate, adding parentheses and casts (x = 'a' comes back as
// (x = 'a'::text)). So CheckTables asks the engine. On Postgres it creates a
// temporary view over the table for each predicate inside a transaction it
// rolls back, and compares the two definitions pg_get_viewdef prints, which is
// the engine's own canonical text for both; equal is the same predicate and
// different is reported as a difference. SQLite keeps the CREATE INDEX text as
// written and has no canonical form, so there unequal normal forms are a
// difference. Two spellings of one condition that the normal form does not
// join, a = 1 against 1 = a, are reported as a difference on both engines:
// they are different definitions, even if equivalent.
//
// A predicate the rule cannot compare, because the engine refused the view
// (no TEMP privilege, a read-only transaction, a predicate that does not
// parse) or because CompareKeys was called without a database, is reported as
// FindingIndexPredicateNotCompared, a warning naming both texts, and never as
// equal.

// predicateMatch is what comparing two predicates found.
type predicateMatch int

const (
	predicateNotCompared predicateMatch = iota
	predicateSame
	predicateDifferent
)

// predicateComparer compares a declared predicate with the database's for one
// table, saying why when it could not.
type predicateComparer func(t schema.Table, declared, live string) (predicateMatch, string)

// textOnly is the comparer CompareKeys uses: equal normal forms are the same,
// anything else is not compared, because without the engine a re-printed
// predicate cannot be told from a different one.
func textOnly(_ schema.Table, declared, live string) (predicateMatch, string) {
	if normalPredicate(declared) == normalPredicate(live) {
		return predicateSame, ""
	}
	return predicateNotCompared, "the texts differ and no database was asked"
}

// textStrict is SQLite's comparer: the database holds the text as written, so
// unequal normal forms are a different definition.
func textStrict(_ schema.Table, declared, live string) (predicateMatch, string) {
	if normalPredicate(declared) == normalPredicate(live) {
		return predicateSame, ""
	}
	return predicateDifferent, ""
}

// comparerFor is the comparer CheckTables uses on dialect.
func comparerFor(ctx context.Context, db *sql.DB, dialect string) predicateComparer {
	switch dialect {
	case render.DialectPostgres:
		return pgPredicates(ctx, db)
	case render.DialectSQLite:
		return textStrict
	}
	return textOnly
}

// pgPredicateView names the temporary views pgPredicates creates; they exist
// only inside a transaction that is always rolled back.
const pgPredicateView = "sqlb_vet_predicate_"

// pgPredicates asks Postgres for its canonical text of both predicates.
func pgPredicates(ctx context.Context, db *sql.DB) predicateComparer {
	return func(t schema.Table, declared, live string) (predicateMatch, string) {
		if normalPredicate(declared) == normalPredicate(live) {
			return predicateSame, ""
		}
		defs, err := pgCanonical(ctx, db, t, declared, live)
		if err != nil {
			return predicateNotCompared, err.Error()
		}
		if defs[0] == defs[1] {
			return predicateSame, ""
		}
		return predicateDifferent, ""
	}
}

// pgCanonical returns pg_get_viewdef of SELECT 1 FROM t WHERE p for each
// predicate. The predicate texts are the library's own description and the
// database's catalogue, the same trust as the DDL; the transaction is rolled
// back whatever happens, so nothing is left behind.
func pgCanonical(ctx context.Context, db *sql.DB, t schema.Table, preds ...string) (_ []string, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, ignoreDone(tx.Rollback())) }()
	from := pgQuote(t.Name)
	if t.Schema != "" {
		from = pgQuote(t.Schema) + "." + from
	}
	out := make([]string, len(preds))
	for i, p := range preds {
		view := fmt.Sprintf("%s%d", pgPredicateView, i)
		if _, err := tx.ExecContext(ctx, "CREATE TEMP VIEW "+view+" AS SELECT 1 AS one FROM "+from+" WHERE ("+p+")"); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT pg_get_viewdef('"+view+"'::regclass)").Scan(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ignoreDone drops the error a rollback returns for a transaction the driver
// already ended, which is not a failure of the comparison.
func ignoreDone(err error) error {
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

// pgQuote quotes a Postgres identifier.
func pgQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// normalPredicate is the normal form described above. It scans bytes: every
// delimiter it looks for is ASCII, so a multi-byte character is copied whole.
func normalPredicate(s string) string {
	var b strings.Builder
	space := false
	write := func(t string) {
		if space && b.Len() > 0 && b.String()[b.Len()-1] != '(' && t != ")" {
			b.WriteByte(' ')
		}
		space = false
		b.WriteString(t)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			space = true
		case c == '\'' || c == '"' || c == '`':
			j := closingQuote(s, i)
			lit := s[i : j+1]
			if c != '\'' {
				lit = unquotedIdent(lit)
			}
			write(lit)
			i = j
		default:
			write(strings.ToLower(string(c)))
		}
	}
	return stripOuterParens(b.String())
}

// closingQuote is the index of the quote closing the one at i, a doubled quote
// being an escaped one; the last index when it is never closed.
func closingQuote(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] != q {
			continue
		}
		if j+1 < len(s) && s[j+1] == q {
			j++
			continue
		}
		return j
	}
	return len(s) - 1
}

// unquotedIdent drops the quotes from an identifier that reads the same
// without them: lower-case letters, digits and underscores, not starting with
// a digit. Any other quoted identifier is kept as written.
func unquotedIdent(q string) string {
	if len(q) < 3 || q[len(q)-1] != q[0] {
		return q
	}
	in := q[1 : len(q)-1]
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c != '_' && (c < 'a' || c > 'z') && (i == 0 || c < '0' || c > '9') {
			return q
		}
	}
	return in
}

// stripOuterParens drops parentheses that wrap the whole text, repeatedly.
func stripOuterParens(s string) string {
	for len(s) >= 2 && s[0] == '(' && closingParen(s, 0) == len(s)-1 {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// closingParen is the index of the parenthesis closing the one at i, skipping
// quoted text, or -1.
func closingParen(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\'', '"', '`':
			j = closingQuote(s, j)
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// errNoPredicate is returned when a partial index's CREATE INDEX text has no
// WHERE after its key list where SQLite's grammar puts it.
var errNoPredicate = errors.New("sqlb vet: keys: cannot find the predicate of a partial index")

// sqliteIndexPredicate returns the predicate of a CREATE INDEX statement as
// SQLite stores it: the text after WHERE, which follows the key list, the
// first parenthesis outside a quoted name.
func sqliteIndexPredicate(create string) (string, error) {
	for i := 0; i < len(create); i++ {
		switch create[i] {
		case '\'', '"', '`':
			i = closingQuote(create, i)
		case '[':
			if j := strings.IndexByte(create[i:], ']'); j >= 0 {
				i += j
			}
		case '(':
			end := closingParen(create, i)
			if end < 0 {
				return "", errNoPredicate
			}
			rest := strings.TrimSpace(create[end+1:])
			const kw = "WHERE"
			if len(rest) > len(kw) && strings.EqualFold(rest[:len(kw)], kw) {
				if next := rest[len(kw)]; next == ' ' || next == '\t' || next == '\n' || next == '\r' || next == '(' {
					return strings.TrimSpace(rest[len(kw):]), nil
				}
			}
			return "", errNoPredicate
		}
	}
	return "", errNoPredicate
}
