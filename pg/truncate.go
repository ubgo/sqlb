package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructTruncate = "pg.Truncate"

	kwTruncate        = "TRUNCATE TABLE"
	kwRestartIdentity = "RESTART IDENTITY"
	kwContinueIdent   = "CONTINUE IDENTITY"
	kwCascade         = "CASCADE"
	kwRestrict        = "RESTRICT"
	kwOnly            = "ONLY"
)

// Truncate builds a TRUNCATE statement. Every method returns a derived
// statement and leaves its receiver alone, as the core's builders do, so
// deriving two statements from one base is safe.
//
// It is here rather than in the core because SQLite has no TRUNCATE at all and
// emulates it with an unfiltered DELETE, so the core's rule that a construct
// ships only when all four engines accept it with the same meaning excludes it.
//
// It is also a second worked example of a statement kind written outside the
// statement package, after pg.Merge: it satisfies sqlb.Statement, so it can be
// a CTE body, and it renders through the same guarded constructors every other
// construct here uses. Nothing in stmt changed to allow it.
//
// Worth stating because the API does not: TRUNCATE is not transactional on
// every engine and removes every row without firing per-row triggers. This
// builder will not stop you; Delete with an explicit All() is the checked
// alternative.
type Truncate struct {
	dialect  Dialect
	tables   []sqlb.Table
	only     bool
	identity string
	refs     string
}

// NewTruncate starts a TRUNCATE for d. The dialect is typed: a TRUNCATE is
// Postgres-shaped, so passing another engine's dialect does not compile.
func NewTruncate(d Dialect, tables ...sqlb.Table) *Truncate {
	return &Truncate{dialect: d, tables: slices.Clone(tables)}
}

// derive copies the statement so a method can change the copy. The table
// slice is cloned because appending to a shared backing array would write
// into the receiver.
func (t *Truncate) derive() *Truncate {
	c := *t
	c.tables = slices.Clone(t.tables)
	return &c
}

// Table adds a table. Truncating several together is the reason the statement
// exists rather than being a DELETE: one statement, one lock, and foreign keys
// between the named tables do not block each other.
func (t *Truncate) Table(tables ...sqlb.Table) *Truncate {
	c := t.derive()
	c.tables = append(c.tables, tables...)
	return c
}

// Only truncates the named tables without their inheritance children.
func (t *Truncate) Only() *Truncate { c := t.derive(); c.only = true; return c }

// RestartIdentity resets owned sequences, which is usually what somebody
// truncating a table in a test actually wants.
func (t *Truncate) RestartIdentity() *Truncate {
	c := t.derive()
	c.identity = kwRestartIdentity
	return c
}

// ContinueIdentity leaves sequences alone. It is the default, and naming it
// makes the choice visible in a statement where the other option is common.
func (t *Truncate) ContinueIdentity() *Truncate {
	c := t.derive()
	c.identity = kwContinueIdent
	return c
}

// Cascade also truncates tables with a foreign key to any named here.
//
// Destructive in a way the rest of this library is not: it reaches tables the
// caller did not name, including tables owned by another package. It is
// spelled out rather than being an option value so that it is visible at the
// call site and in a diff.
func (t *Truncate) Cascade() *Truncate { c := t.derive(); c.refs = kwCascade; return c }

// Restrict refuses when another table references one named here, which is the
// default.
func (t *Truncate) Restrict() *Truncate { c := t.derive(); c.refs = kwRestrict; return c }

// Build renders the statement, or returns the first error it found.
func (t *Truncate) Build() (sqlb.Query, error) {
	args := sqlb.NewArgs(t.dialect)
	var b strings.Builder
	t.WriteSQL(&b, args)
	if err := args.Err(); err != nil {
		return sqlb.Query{}, err
	}
	return sqlb.NewQuery(b.String(), args.Values(), nil), nil
}

// ErrNoTables is returned by Build when no table was named, which would
// otherwise render a statement the database rejects.
var ErrNoTables = errors.New("pg: TRUNCATE names no table")

// WriteSQL renders the statement into b, satisfying sqlb.Statement. A table
// is written by its qualified name alone: neither statement accepts an
// alias, which the rendering test asserted the wrong way until a real server
// rejected it.
func (t *Truncate) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if !guard(a, constructTruncate) {
		return
	}
	if len(t.tables) == 0 {
		a.Fail(ErrNoTables)
		return
	}
	d := a.Dialect()

	b.WriteString(kwTruncate)
	b.WriteString(" ")
	if t.only {
		b.WriteString(kwOnly)
		b.WriteString(" ")
	}
	for i, tb := range t.tables {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tb.Qualified(d))
	}
	if t.identity != "" {
		b.WriteString(" ")
		b.WriteString(t.identity)
	}
	if t.refs != "" {
		b.WriteString(" ")
		b.WriteString(t.refs)
	}
}
