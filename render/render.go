// Package render is the bottom of the builder: how a value becomes a bound
// parameter, how an identifier is escaped, and how the first error of a render
// is carried out.
//
// Nothing here knows what SQL is. There are no operators, no join types and no
// keywords, because those are vocabulary rather than rendering and they live in
// the layers above. That absence is the point: any target whose wire form is
// text can build on this package, and a target that renders something other
// than text needs its own bottom layer rather than a dialect here.
//
// It imports nothing outside the standard library and never will, because the
// next level down, an actual driver, is a third-party dependency by definition.
package render

import (
	"errors"
	"fmt"
	"strconv"
)

// Dialect is the two things SQL engines disagree on in standard SQL: how a
// bound parameter is written and how an identifier is quoted.
//
// It is deliberately two methods. A wide dialect interface is where query
// builders go to die. Anything else an engine has or lacks is expressed as an
// optional interface (see Named, Returning) so a dialect never has to lie
// about a capability it does not have.
type Dialect interface {
	Placeholder(n int) string
	QuoteIdent(ident string) string
}

// Named is satisfied by dialects that can name their engine. The name appears
// in ErrUnsupportedDialect messages and is what engine packages compare
// against. Dialects that do not implement it are reported as DialectUnknown.
type Named interface {
	Name() string
}

// Returning is satisfied by dialects that support INSERT ... RETURNING. A
// write builder type-asserts for it and uses the documented two-statement
// fallback when it is absent.
type Returning interface {
	SupportsReturning() bool
}

// LikeEscaping is satisfied by dialects that accept an ESCAPE clause on LIKE.
//
// It exists because escaping a LIKE pattern is not portable in the way it
// looks. Postgres and MySQL treat a backslash in a pattern as an escape
// character by default; SQLite has no default escape character at all, so a
// pattern escaped with backslashes matches nothing unless ESCAPE is written
// out. That difference is invisible: the statement runs and returns no rows.
//
// A predicate that must escape a user's text therefore asks the dialect first
// and records Args.Unsupported when the answer is no, rather than rendering
// something that silently matches the wrong rows. See expr.Contains.
type LikeEscaping interface {
	SupportsLikeEscape() bool
}

// CaseInsensitiveLike is satisfied by a dialect with an ILIKE operator, a
// LIKE that ignores case.
//
// Postgres is the one engine that has it, and it is the one engine that needs
// it: measured on Postgres 17, 'Ada' LIKE 'ada%' is false, while MySQL under
// its default collation and SQLite for ASCII both answer true. A predicate that
// must match regardless of case asks for this and writes ILIKE, which keeps the
// column bare; without it the predicate writes lower(column) LIKE lower(pattern),
// which every engine accepts. Both spellings answer the same question, so the
// capability chooses a spelling and never a meaning.
//
// It is a capability rather than a check of the engine's name so that a
// dialect from another repository, CockroachDB say, gets ILIKE by implementing
// one method, and one that declines gets the portable form rather than an
// error. See expr.ContainsFold.
type CaseInsensitiveLike interface {
	SupportsILike() bool
}

// LiteralQuoting is satisfied by a dialect that can write a value into the
// statement text as a literal the engine reads back as exactly that value.
//
// Binding is always the better answer and everything that can be bound is.
// This is for the positions that take no parameter: a jsonpath or a
// text-search configuration a function wants as a constant, and every value in
// DDL, which no engine will parameterise at all. A literal is the one place a
// value becomes statement text, so a wrong quoting rule here is an injection
// or, subtler and worse, a value the engine reads differently from what was
// written. Postgres reads a backslash in a plain string literal as an escape
// when standard_conforming_strings is off, so the same text stores different
// bytes depending on how the server is configured; a correct Postgres
// implementation writes such a string in the E'...' form, which reads the same
// either way.
//
// The argument is any for the reason Args.Next takes any: this is Next's
// inline counterpart, and a value arrives from a typed Expr[T] whose T is
// open. The set a dialect renders is closed. A value outside it, or one the
// engine could not store faithfully (a string holding NUL, a NaN), is
// ErrNotInlinable; it is never written by a best guess.
//
// A dialect declines the capability by not implementing the interface, and a
// literal requested of such a dialect fails with ErrNoLiteralQuoting.
type LiteralQuoting interface {
	QuoteLiteral(v any) (string, error)
}

// ErrNotInlinable reports a value a dialect will not write as a literal,
// because the dialect has no faithful spelling for its type or for this value.
var ErrNotInlinable = errors.New("sqlb: value cannot be written as a literal")

// ErrNoLiteralQuoting reports a literal requested of a dialect that does not
// implement LiteralQuoting.
var ErrNoLiteralQuoting = errors.New("sqlb: dialect cannot write a literal")

// CombinedAlter is satisfied by dialects that accept several actions in one
// ALTER TABLE, separated by commas. Postgres does, and applies them under one
// lock and at most one table rewrite; SQLite accepts exactly one action per
// statement. A DDL builder writes one statement per action for a dialect
// without it, so the author's code is the same either way.
type CombinedAlter interface {
	SupportsCombinedAlter() bool
}

// UnrestrictedAddColumn is satisfied by dialects whose ALTER TABLE ... ADD
// COLUMN accepts any column CREATE TABLE would. Postgres does. SQLite does not:
// it refuses a NOT NULL column without a default, a non-constant default, and
// a key constraint on the added column, each at run time with an error about
// syntax. A DDL builder refuses those at Build for a dialect without this, so
// the author learns the rule rather than the error.
type UnrestrictedAddColumn interface {
	SupportsUnrestrictedAddColumn() bool
}

// UntypedParameters is satisfied by a dialect that cannot infer a type for a
// bound parameter passed to a function taking any type, and says how to give
// it one. Postgres is the case: concat($1) fails with "could not determine
// data type of parameter", measured on Postgres 17 by the conformance suite,
// while MySQL and SQLite type the parameter themselves and so decline this.
// A construct that passes a bare parameter to such a function asks for it and
// writes TextParam(placeholder) instead of the placeholder alone.
type UntypedParameters interface {
	TextParam(placeholder string) string
}

// NumericParameters is satisfied by a dialect that widens an untyped integer
// parameter when it meets a DECIMAL, and says how to give it a type. MySQL is
// the case: sum() of an integer column is DECIMAL there, and COALESCE(sum(x), ?)
// or sum(x) + ? in a prepared statement comes back as DECIMAL(65,30), so the
// value 40 arrives as "40.000000000000000000000000000000" and cannot be scanned
// into the int64 an Expr[int64] promises. Measured on MySQL 8.4.11 on
// 2026-09-28 by sqlbtest's crud_transcript case; CASE and NULLIF with the same
// parameter do not widen, and neither does a float parameter.
//
// A construct that unifies a bare parameter with another operand's type,
// COALESCE and the arithmetic operators, asks for this and writes
// NumericParam(placeholder, value) instead of the placeholder alone. v is the
// value just bound, typed any for the reason Args.Next takes any. An
// implementation returns the placeholder unchanged for a value it has no
// reason to type. Postgres and SQLite decline it: neither widens.
type NumericParameters interface {
	NumericParam(placeholder string, v any) string
}

// InsertAliasing is satisfied by dialects that accept "INSERT INTO t AS
// alias", which Postgres does and MySQL does not. With it, RETURNING and
// ON CONFLICT ... DO UPDATE may use alias-qualified columns; without it the
// INSERT renders the bare table and RETURNING renders bare column names.
type InsertAliasing interface {
	SupportsInsertAlias() bool
}

// The engine names this module ships.
//
// Not a closed set. Nothing in the core branches on these, and DialectName
// asks the Named interface, so a dialect from another repository returning
// any string works exactly as these three do. They are constants because the
// engine packages here guard on them and an error message prints them, and a
// typo in either would be silent.
const (
	DialectPostgres   = "postgres"
	DialectSQLite     = "sqlite"
	DialectClickHouse = "clickhouse"
	DialectMySQL      = "mysql"
	DialectUnknown    = "unknown"
)

// DialectName reports the engine name of d, or DialectUnknown.
func DialectName(d Dialect) string {
	if n, ok := d.(Named); ok {
		return n.Name()
	}
	return DialectUnknown
}

// Quote wraps s in q and doubles any embedded q. Exported so an engine package
// can implement QuoteIdent with a different quote character.
func Quote(s string, q byte) string { return quote(s, q) }

func quote(s string, q byte) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, q)
	for i := 0; i < len(s); i++ {
		if s[i] == q {
			out = append(out, q)
		}
		out = append(out, s[i])
	}
	out = append(out, q)
	return string(out)
}

// ErrUnsupportedDialect is returned by Build when a construct was rendered
// through a dialect that does not support it. The construct and dialect names
// are joined to it. Failing at Build rather than at the database is the rule
// for every engine-specific construct.
var ErrUnsupportedDialect = errors.New("sqlb: construct is not supported by this dialect")

// Args collects bound values during one render and hands out the placeholder
// for each. It also carries the first render error, so render functions keep
// a single string return value and Build still fails closed.
type Args struct {
	dialect Dialect
	values  []any
	err     error
	// inline makes Next write each value as a literal instead of binding it.
	inline bool
	// bare makes a column render by its name alone, without its table.
	bare bool
	// byName is set while TableNamed runs; see ByTableName.
	byName bool
}

// NewArgs starts an empty argument list for d.
func NewArgs(d Dialect) *Args { return &Args{dialect: d} }

// NewInlineArgs returns Args whose Next writes every value as a literal
// instead of binding it, for a statement that takes no parameters at all.
//
// DDL is that statement. No engine accepts a placeholder in a DEFAULT, a CHECK,
// a partial index's WHERE or a view's body: PREPARE rejects every DDL
// statement at the Postgres server, and SQLite rejects ? in CREATE TABLE. Those
// positions render through the same expression constructs a query uses, so
// rather than a second renderer for each construct, the Args decides: bound
// here, written inline there. Values stays empty, which is what makes a DDL
// statement's Args a mistake to pass to the driver.
//
// A value the dialect cannot write faithfully fails Build with the dialect's
// error, and a dialect without LiteralQuoting fails with ErrNoLiteralQuoting;
// neither writes a best guess.
func NewInlineArgs(d Dialect) *Args { return &Args{dialect: d, inline: true} }

// Next appends v and returns the placeholder for its position.
func (a *Args) Next(v any) string {
	if a.inline {
		return a.Literal(v)
	}
	a.values = append(a.values, v)
	return a.dialect.Placeholder(len(a.values))
}

// Unqualified reports whether a column should render by its bare name.
//
// Inside a statement's own definition a column cannot be qualified: a CHECK in
// CREATE TABLE users writes "status", not "u"."status", because the alias does
// not exist there. A column construct asks this and drops its qualifier; every
// other construct ignores it.
func (a *Args) Unqualified() bool { return a.bare }

// Bare renders fn with every column written by its bare name, and restores the
// previous mode afterwards, so a CHECK inside CREATE TABLE and a view body in
// the same statement each get what they need. It is a separate switch from the
// inline mode for that reason: a view body needs literals and qualified names,
// a CHECK needs literals and bare ones.
func (a *Args) Bare(fn func() string) string {
	prev := a.bare
	a.bare = true
	defer func() { a.bare = prev }()
	return fn()
}

// ByTableName reports whether a column should be qualified by its table's
// name rather than its alias.
//
// A statement that takes no alias but exposes a second relation of the same
// columns needs it: MySQL's INSERT ... AS new ON DUPLICATE KEY UPDATE takes no
// table alias, so "i"."hits" is an unknown column, and exposes new.hits, so a
// bare "hits" is ambiguous. Only "c_items"."hits" names the existing row.
func (a *Args) ByTableName() bool { return a.byName }

// TableNamed renders fn with every column qualified by its table's name, and
// restores the previous mode afterwards, as Bare does.
func (a *Args) TableNamed(fn func() string) string {
	prev := a.byName
	a.byName = true
	defer func() { a.byName = prev }()
	return fn()
}

// Literal writes v as a literal through the dialect's LiteralQuoting, whatever
// mode the Args is in, and records a failure rather than writing a guess.
//
// It is for the positions that never take a parameter even in a query, a
// jsonpath or a text-search configuration, and it is the one place a literal
// is written: Next in inline mode calls it, and so does EngineQuoteLit.
func (a *Args) Literal(v any) string {
	q, ok := a.dialect.(LiteralQuoting)
	if !ok {
		a.Fail(fmt.Errorf("%w: %q", ErrNoLiteralQuoting, DialectName(a.dialect)))
		return ""
	}
	lit, err := q.QuoteLiteral(v)
	if err != nil {
		a.Fail(err)
		return ""
	}
	return lit
}

// Values returns the bound values in placeholder order.
func (a *Args) Values() []any { return a.values }

// Dialect returns the dialect this render is for.
func (a *Args) Dialect() Dialect { return a.dialect }

// Unsupported records that construct cannot render through this dialect. The
// first error wins; later ones are dropped because the statement is already
// unusable. Build returns the recorded error.
func (a *Args) Unsupported(construct string) {
	a.Fail(errors.Join(ErrUnsupportedDialect,
		errors.New(construct+" under dialect "+strconv.Quote(DialectName(a.dialect)))))
}

// Fail records the first render error. Later errors are dropped because the
// statement is already unusable. Build returns the recorded error.
func (a *Args) Fail(err error) {
	if a.err == nil {
		a.err = err
	}
}

// Err returns the first error recorded during the render, or nil.
func (a *Args) Err() error { return a.err }

// SQLer is anything that can render itself into SQL text using the statement's
// Args. It is the one interface every construct in every layer above satisfies,
// which is what lets a raw fragment and a typed expression occupy the same
// position.
type SQLer interface {
	SQL(a *Args) string
}

type sqlerFunc func(a *Args) string

func (f sqlerFunc) SQL(a *Args) string { return f(a) }

// Raw makes a SQLer from a render function. It is the way into the builder for
// a fragment this module has no type for, and the reason going raw costs type
// checking rather than safety: the function still receives Args, so values are
// still bound and placeholders still numbered by the dialect.
func Raw(render func(a *Args) string) SQLer { return sqlerFunc(render) }
