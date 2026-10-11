// Package sqlite is the SQLite dialect: positional placeholders and
// double-quoted identifiers.
//
// It imports the render layer and nothing else. See the postgres package in
// this directory for why each dialect is its own package.
//
// The constructs that only SQLite has live in github.com/ubgo/sqlb/sqlite.
package sqlite

import "github.com/ubgo/sqlb/render"

// Name is the engine name this dialect reports. Engine packages guard on it.
const Name = render.DialectSQLite

// Dialect renders ? placeholders and double-quoted identifiers.
type Dialect struct{}

// Placeholder renders ?. SQLite placeholders are positional, so the argument
// order is the only thing that binds a value to a position.
func (Dialect) Placeholder(int) string { return "?" }

// QuoteIdent double-quotes an identifier, doubling any embedded quote.
func (Dialect) QuoteIdent(ident string) string { return render.Quote(ident, '"') }

// Name reports "sqlite".
func (Dialect) Name() string { return Name }

// SupportsReturning reports true. SQLite has had RETURNING since 3.35, and the
// driver in contrib/driver-sqlite is well past that.
func (Dialect) SupportsReturning() bool { return true }

// SupportsLikeEscape reports true: SQLite accepts an ESCAPE clause on LIKE.
//
// SQLite has no default escape character, so the clause is not optional: a pattern escaped with backslashes matches nothing without it.
func (Dialect) SupportsLikeEscape() bool { return true }
