package sqlb

import (
	"github.com/ubgo/sqlb/dialect/clickhouse"
	"github.com/ubgo/sqlb/dialect/mysql"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/render"
)

// This file is the facade over the render layer, and is named for the layer
// rather than for what it used to hold. It was dialect.go until the dialects
// moved into dialect/, and a file named dialect.go sitting beside a directory
// named dialect/ with different contents is the kind of thing a reader is
// entitled to get wrong. Every name below is an alias
// or a forwarder, declared here so that splitting the bottom layer out into its
// own package did not break a single import.
//
// Why aliases rather than wrappers: a Go type alias is the same type, so a
// dialect written against render.Dialect satisfies sqlb.Dialect with no
// conversion, an *Args passes between the layers untouched, and the facade
// costs nothing at runtime. A wrapper type would have made the two spellings
// different types and forced every engine package to pick one.
//
// The rule for adding to this file: a name belongs here only if it is defined
// in render. Anything that names an operator, a join type, a sort direction or
// a keyword is SQL vocabulary and stays in the layers that own it.

// Dialect is the two things SQL engines disagree on: how a bound parameter is
// written and how an identifier is quoted. See render.Dialect.
type Dialect = render.Dialect

// Named is satisfied by dialects that can name their engine. See render.Named.
type Named = render.Named

// Returning is satisfied by dialects that support INSERT ... RETURNING.
type Returning = render.Returning

// InsertAliasing is satisfied by dialects that accept "INSERT INTO t AS alias".
type InsertAliasing = render.InsertAliasing

// NewInlineArgs returns Args whose Next writes every value as a literal, for a
// statement that takes no parameters. See render.NewInlineArgs.
func NewInlineArgs(d Dialect) *Args { return render.NewInlineArgs(d) }

// LiteralQuoting is satisfied by a dialect that can write a value as a literal
// the engine reads back exactly. See render.LiteralQuoting.
type LiteralQuoting = render.LiteralQuoting

// ErrNotInlinable reports a value a dialect will not write as a literal.
var ErrNotInlinable = render.ErrNotInlinable

// ErrNoLiteralQuoting reports a literal requested of a dialect without the
// capability.
var ErrNoLiteralQuoting = render.ErrNoLiteralQuoting

// CombinedAlter is satisfied by dialects that accept several actions in one
// ALTER TABLE. See render.CombinedAlter.
type CombinedAlter = render.CombinedAlter

// UnrestrictedAddColumn is satisfied by dialects whose ADD COLUMN takes any
// column. See render.UnrestrictedAddColumn.
type UnrestrictedAddColumn = render.UnrestrictedAddColumn

// UntypedParameters is satisfied by a dialect that must be told a bound
// parameter's type in a function taking any. See render.UntypedParameters.
type UntypedParameters = render.UntypedParameters

// LikeEscaping is satisfied by dialects that accept an ESCAPE clause on LIKE.
// See render.LikeEscaping for why escaping a pattern is not portable.
type LikeEscaping = render.LikeEscaping

// CaseInsensitiveLike is satisfied by a dialect with ILIKE. See
// render.CaseInsensitiveLike for why the fold predicates ask for it.
type CaseInsensitiveLike = render.CaseInsensitiveLike

// NumericParameters is satisfied by a dialect that widens an untyped integer
// parameter beside a DECIMAL. See render.NumericParameters for the MySQL
// measurement behind it.
type NumericParameters = render.NumericParameters

// Args collects bound values during one render and carries the first error.
type Args = render.Args

// SQLer is anything that can render itself into SQL text using the statement's
// Args.
type SQLer = render.SQLer

// PostgresDialect renders $1, $2 placeholders and double-quoted identifiers.
//
// The three dialects below are aliases because each one now lives in its own
// package under dialect/, built the way a dialect from another repository is
// built: importing the render layer and nothing else. Keeping the old names
// here means the split cost no caller an import, and it keeps a one-import
// entry point for code that just needs to say which engine it targets.
type PostgresDialect = postgres.Dialect

// SQLiteDialect renders ? placeholders and double-quoted identifiers.
type SQLiteDialect = sqlite.Dialect

// ClickHouseDialect renders ? placeholders and backquoted identifiers. It
// deliberately does not satisfy Returning; ClickHouse has no RETURNING.
type ClickHouseDialect = clickhouse.Dialect

// MySQLDialect renders ? placeholders and backquoted identifiers. It does not
// satisfy Returning; MySQL has no RETURNING.
type MySQLDialect = mysql.Dialect

// The engine names this module ships. Not a closed set; see render.
const (
	DialectPostgres   = render.DialectPostgres
	DialectSQLite     = render.DialectSQLite
	DialectClickHouse = render.DialectClickHouse
	DialectMySQL      = render.DialectMySQL
	DialectUnknown    = render.DialectUnknown
)

// ErrUnsupportedDialect is returned by Build when a construct was rendered
// through a dialect that does not support it.
var ErrUnsupportedDialect = render.ErrUnsupportedDialect

// DialectName reports the engine name of d, or DialectUnknown.
func DialectName(d Dialect) string { return render.DialectName(d) }

// Quote wraps s in q and doubles any embedded q.
func Quote(s string, q byte) string { return render.Quote(s, q) }

// NewArgs starts an empty argument list for d.
func NewArgs(d Dialect) *Args { return render.NewArgs(d) }

// Raw makes a SQLer from a render function, for a fragment in a position typed
// SQLer, such as a GROUP BY item or a raw WHERE condition.
func Raw(f func(a *Args) string) SQLer { return render.Raw(f) }
