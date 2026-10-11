// Package clickhouse is the ClickHouse dialect: positional placeholders and
// backtick-quoted identifiers.
//
// It imports the render layer and nothing else. See the postgres package in
// this directory for why each dialect is its own package.
//
// The constructs that only ClickHouse has live in github.com/ubgo/sqlb/ch.
package clickhouse

import "github.com/ubgo/sqlb/render"

// Name is the engine name this dialect reports. Engine packages guard on it.
const Name = render.DialectClickHouse

// Dialect renders ? placeholders and backtick-quoted identifiers.
//
// It deliberately does not satisfy render.Returning. ClickHouse has no
// RETURNING, so a write builder asked for one fails at Build instead of
// producing a statement the server rejects. Declining to implement an optional
// interface is how a dialect says "I cannot do this" without lying.
type Dialect struct{}

// Placeholder renders ?. ClickHouse placeholders are positional.
func (Dialect) Placeholder(int) string { return "?" }

// QuoteIdent backtick-quotes an identifier, doubling any embedded backtick.
// ClickHouse uses backticks where Postgres and SQLite use double quotes, which
// is most of what a dialect is for.
func (Dialect) QuoteIdent(ident string) string { return render.Quote(ident, '`') }

// Name reports "clickhouse".
func (Dialect) Name() string { return Name }
