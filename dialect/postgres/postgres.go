// Package postgres is the Postgres dialect: how Postgres writes a bound
// parameter and quotes an identifier, and which optional capabilities it has.
//
// It is a package of its own so that the dialects this module ships are built
// exactly the way a dialect from another repository is built. It imports the
// render layer and nothing else, declares no construct, and knows nothing about
// statements. If adding an engine here needed anything the render layer does
// not export, that would be a hole in the extension point rather than a detail
// of this package, which is the property the split exists to keep testable.
//
// The constructs that only Postgres has live in github.com/ubgo/sqlb/pg. This
// package is the smaller half of an engine, and a caller that only needs to say
// which engine it is building for imports this one alone.
package postgres

import (
	"strconv"

	"github.com/ubgo/sqlb/render"
)

// Name is the engine name this dialect reports, and what an unsupported
// construct error prints. Engine packages guard on it.
const Name = render.DialectPostgres

// Dialect renders $1, $2 placeholders and double-quoted identifiers.
type Dialect struct{}

// Placeholder renders $1, $2 and so on. Postgres numbers its placeholders,
// which is what lets one bound value be referenced twice.
func (Dialect) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

// QuoteIdent double-quotes an identifier, doubling any embedded quote.
func (Dialect) QuoteIdent(ident string) string { return render.Quote(ident, '"') }

// Name reports "postgres", which is what an engine package checks and what an
// unsupported-construct error names.
func (Dialect) Name() string { return Name }

// SupportsReturning reports true: Postgres has RETURNING on every write.
func (Dialect) SupportsReturning() bool { return true }

// SupportsInsertAlias reports true: INSERT INTO t AS alias is Postgres only,
// and the capability interface is how the builder finds that out without
// naming the engine.
func (Dialect) SupportsInsertAlias() bool { return true }

// SupportsLikeEscape reports true: Postgres accepts an ESCAPE clause on LIKE.
//
// Postgres treats a backslash as the default escape, so the clause is redundant there and costs nothing to write; naming it keeps one rendering for both engines.
func (Dialect) SupportsLikeEscape() bool { return true }

// SupportsILike reports true: Postgres has ILIKE, and it is the engine whose
// LIKE is case-sensitive, so expr.ContainsFold and its siblings write ILIKE
// here and lower() on both sides everywhere else.
func (Dialect) SupportsILike() bool { return true }

// SupportsCombinedAlter reports true: Postgres applies several ALTER TABLE
// actions in one statement, under one lock.
func (Dialect) SupportsCombinedAlter() bool { return true }

// TextParam casts a bound parameter to text, which Postgres needs when the
// parameter is an argument of a function taking any, such as concat().
func (Dialect) TextParam(placeholder string) string { return "CAST(" + placeholder + " AS text)" }

// SupportsUnrestrictedAddColumn reports true: Postgres's ADD COLUMN takes any
// column CREATE TABLE would, rewriting the table when it must.
func (Dialect) SupportsUnrestrictedAddColumn() bool { return true }
