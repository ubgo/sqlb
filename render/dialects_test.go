package render_test

import (
	"database/sql"
	"strconv"
	"testing"

	"github.com/ubgo/sqlb/render"
)

// Three dialects this module does not ship, written the way a package in
// another repository would write them. Each is its whole implementation.
//
// They exist to pin the claim that adding an engine is three methods and needs
// no change to anything. The claim is easy to make and easy to have quietly
// stop being true: one branch on a dialect name inside the renderer, one
// assumption that a placeholder starts with "$", and adding a fourth engine
// becomes a patch to the core rather than a package somebody writes.

type mysqlDialect struct{}

func (mysqlDialect) Placeholder(int) string     { return "?" }
func (mysqlDialect) QuoteIdent(s string) string { return render.Quote(s, '`') }
func (mysqlDialect) Name() string               { return "mysql" }

type sqlServerDialect struct{}

func (sqlServerDialect) Placeholder(n int) string { return "@p" + strconv.Itoa(n) }
func (sqlServerDialect) QuoteIdent(s string) string {
	// Bracket quoting, which no dialect here uses, so it cannot be passing by
	// accidentally matching one that does.
	return "[" + s + "]"
}
func (sqlServerDialect) Name() string { return "sqlserver" }

type oracleDialect struct{}

func (oracleDialect) Placeholder(n int) string   { return ":" + strconv.Itoa(n) }
func (oracleDialect) QuoteIdent(s string) string { return render.Quote(s, '"') }
func (oracleDialect) Name() string               { return "oracle" }

// TestOutsideDialects_DriveTheRenderLayer pins that the three placeholder
// styles in common use all work: positional markers with no number, numbered
// markers with a prefix, and numbered markers with a different prefix.
func TestOutsideDialects_DriveTheRenderLayer(t *testing.T) {
	for _, c := range []struct {
		d      render.Dialect
		name   string
		quoted string
		first  string
		second string
	}{
		{mysqlDialect{}, "mysql", "`id`", "?", "?"},
		{sqlServerDialect{}, "sqlserver", "[id]", "@p1", "@p2"},
		{oracleDialect{}, "oracle", `"id"`, ":1", ":2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := render.DialectName(c.d); got != c.name {
				t.Errorf("DialectName = %q, want %q", got, c.name)
			}
			if got := c.d.QuoteIdent("id"); got != c.quoted {
				t.Errorf("QuoteIdent = %q, want %q", got, c.quoted)
			}
			a := render.NewArgs(c.d)
			if got := a.Next(1); got != c.first {
				t.Errorf("first placeholder = %q, want %q", got, c.first)
			}
			if got := a.Next(2); got != c.second {
				t.Errorf("second placeholder = %q, want %q", got, c.second)
			}
			if len(a.Values()) != 2 {
				t.Errorf("values = %v, want two", a.Values())
			}
		})
	}
}

// TestArgs_PassesNamedArgumentsThrough pins that a driver-specific argument
// type survives binding untouched.
//
// database/sql lets a caller pass sql.NamedArg for drivers that support named
// parameters, and a builder that inspected or converted its arguments would
// break that. Args appends whatever it is given and hands it back in order, so
// the named argument reaches the driver as the caller wrote it.
func TestArgs_PassesNamedArgumentsThrough(t *testing.T) {
	a := render.NewArgs(sqlServerDialect{})
	start := sql.Named("start", 100)

	if got := a.Next(start); got != "@p1" {
		t.Fatalf("placeholder = %q, want @p1", got)
	}

	vals := a.Values()
	if len(vals) != 1 {
		t.Fatalf("values = %v, want one", vals)
	}
	named, ok := vals[0].(sql.NamedArg)
	if !ok {
		t.Fatalf("the argument was converted to %T; it must reach the driver unchanged", vals[0])
	}
	if named.Name != "start" || named.Value != 100 {
		t.Fatalf("named argument was altered: %+v", named)
	}
}
