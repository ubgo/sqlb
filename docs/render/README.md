# `render` — text, placeholders and bound values

```go
import "github.com/ubgo/sqlb/render"
```

The bottom of the builder. It answers one question — how does a value become a bound parameter and an identifier become text for this engine — and refuses to know what the piece was or why.

**Nothing here knows what SQL is.** There are no operators, no join types and no keywords, because those are vocabulary rather than rendering and they live in the layers above. That absence is the point: any target whose wire form is text can build on this package. A Gremlin traversal has no `JOIN` and no `<>`, and `TestRender_ServesANonSQLTarget` drives the layer with a non-SQL dialect to keep the claim honest.

It imports nothing outside the standard library and never will, because the next level down is a driver, which is a third-party dependency by definition. `task zerodep` is that boundary expressed as a gate.

## Contents

- [What it gives you](#what-it-gives-you)
- [Args: binding values](#args-binding-values)
- [Raw: the way in for an untyped fragment](#raw-the-way-in-for-an-untyped-fragment)
- [Failing closed](#failing-closed)
- [Writing a literal](#writing-a-literal)
- [Args for DDL: inline and bare](#args-for-ddl-inline-and-bare)
- [Writing a dialect](#writing-a-dialect)
- [What lives above](#what-lives-above)

## What it gives you

| Name | What it is |
|---|---|
| `Dialect` | Two methods: `Placeholder(n int) string` and `QuoteIdent(string) string`. Everything else an engine has or lacks is an optional interface. |
| `Named`, `Returning`, `InsertAliasing` | Optional capability interfaces. A dialect declines one by not implementing it. |
| `Args` | Collects bound values during one render, hands out placeholders, and carries the first error. |
| `SQLer` | Anything that renders itself with the statement's `Args`. Every construct in every layer above satisfies it. |
| `Raw` | Makes a `SQLer` from a render function. |
| `Quote` | Wraps a string in a quote character, doubling any embedded one. |
| `DialectName` | The engine name, or `DialectUnknown`. |
| `ErrUnsupportedDialect` | What `Build` returns when a construct met the wrong engine. |

`DialectName` asks the optional `Named` interface and answers `DialectUnknown` when a dialect declines it. The engines this module ships have their names as constants — `DialectPostgres`, `DialectSQLite`, `DialectMySQL` and `DialectClickHouse` — but the set is deliberately **not closed**: nothing in this layer branches on them, so a dialect from another repository returning any string works exactly as these four do. They are constants because the engine packages guard on them and an error message prints them, and a typo in either would be silent rather than loud.

## Args: binding values

`Args` is the reason a raw fragment is still safe. Values go in, placeholders come out, and the caller never concatenates a value into SQL.

```go
a := render.NewArgs(postgres.Dialect{})

a.Dialect().QuoteIdent("email")   // "email"
a.Next("ada@example.com")         // $1
a.Next(36)                        // $2
a.Values()                        // [ada@example.com 36]
```

The placeholder style comes from the dialect, and that is most of what a dialect is for:

```go
for _, d := range []render.Dialect{postgres.Dialect{}, sqlite.Dialect{}} {
	a := render.NewArgs(d)
	fmt.Printf("%-10s %s %s\n", render.DialectName(d), d.QuoteIdent("user"), a.Next("x"))
}
```

```
postgres   "user" $1
sqlite     "user" ?
```

## Raw: the way in for an untyped fragment

`Raw` is how a fragment this module has no type for enters the builder. Going raw costs type checking, not safety: the function still receives `Args`, so the value is still bound and the placeholder still numbered by the dialect.

```go
frag := render.Raw(func(a *render.Args) string {
	return "lower(" + a.Dialect().QuoteIdent("email") + ") = " + a.Next("ADA@EXAMPLE.COM")
})
```

```
lower("email") = $1
[ADA@EXAMPLE.COM]
```

## Failing closed

Every engine-specific construct records its own name when it meets the wrong dialect, so the failure happens in your process at `Build` rather than at the database.

```go
a := render.NewArgs(sqlite.Dialect{})
a.Unsupported("pg.Merge")
fmt.Println(a.Err())
```

```
sqlb: construct is not supported by this dialect
pg.Merge under dialect "sqlite"
```

The first error wins. Later ones are dropped because the statement is already unusable, and that rule is what lets every render function keep a single `string` return value while `Build` still fails closed.

```go
a.Unsupported("first.Construct")
a.Unsupported("second.Construct")
// a.Err() names first.Construct
```

## Writing a literal

Binding is always the better answer, and everything that can be bound is. A few positions take no parameter at all: a jsonpath or a text-search configuration a function wants as a constant, and every value in DDL, which no engine will parameterise. There a value has to become statement text, and the dialect writes it through one capability:

```go
type LiteralQuoting interface {
	QuoteLiteral(v any) (string, error)
}
```

It is a capability rather than part of `Dialect` for the reason every capability is: a dialect that cannot do it faithfully declines by not implementing it, and a literal requested of it fails at `Build` with `ErrNoLiteralQuoting` naming the dialect. ClickHouse declines today, because its string literals take backslash escapes and nothing here has ever run against a ClickHouse server.

The value set is closed. A value the dialect has no faithful spelling for, a string holding NUL, a NaN, or a type outside the set, is `ErrNotInlinable`, never a best guess:

```go
lit, err := postgres.Dialect{}.QuoteLiteral("it's")     // 'it''s'
lit, err = postgres.Dialect{}.QuoteLiteral(`a\b`)        // E'a\\b'
lit, err = postgres.Dialect{}.QuoteLiteral("a\x00b")     // ErrNotInlinable
```

The Postgres rule is the one worth knowing. With `standard_conforming_strings` off, Postgres reads a backslash in a plain `'...'` literal as an escape, so the same text stores different bytes on a differently configured server, with only a WARNING. A string containing a backslash is therefore written in the `E'...'` form with the backslash doubled, which reads the same under both settings, and a string without one is written plainly, which also does. Both are executed under both settings against a real Postgres in `contrib/driver-pgx`.

## Args for DDL: inline and bare

A statement with no placeholder anywhere, which every DDL statement is, renders through `NewInlineArgs(d)` instead of `NewArgs(d)`. Its `Next` writes the value with the dialect's `LiteralQuoting` rather than recording an argument, so the same expression code renders either way, and a value that cannot be written fails the build exactly as `Literal` does. `a.Literal(v)` is that one step on its own, for a construct that always wants a literal whichever `Args` it is given.

`a.Bare(fn)` renders `fn` with every column written by its bare name, which a table's own `CHECK` or `DEFAULT` needs because it cannot name a column through an alias, and restores the previous mode afterwards. A column asks `a.Unqualified()` to know which mode it is in.

`CombinedAlter` is the capability a dialect implements when it accepts several actions in one `ALTER TABLE`. Postgres does; SQLite does not, and declines by leaving the interface out, so the `ddl` package writes one statement per action there. All three are used by [`ddl`](../ddl/README.md) and by nothing in the query layers.

## Writing a dialect

Three methods, and your package imports `render` and nothing else. The dialects this module ships are written exactly this way, which is the point: adding Postgres and adding an engine nobody has written yet are the same act.

```go
type MyDialect struct{}

func (MyDialect) Placeholder(n int) string       { return "?" }
func (MyDialect) QuoteIdent(ident string) string { return render.Quote(ident, '`') }
func (MyDialect) Name() string                   { return "mine" }
```

That is enough to drive the whole statement builder. If your engine's constructs write a literal, as a `read_parquet` taking a path does, implement `LiteralQuoting` as well: without it `EngineQuoteLit` fails at `Build` with `ErrNoLiteralQuoting` rather than guessing a quoting rule, because the rule a guess would pick, doubling the quote, is an injection on any engine that treats a backslash as an escape. Add a capability by implementing its interface, and decline one by leaving it out — never by implementing it and returning false, because a type assertion is how the builder asks.

See [`dialect`](../dialect/README.md) for the three that ship.

## What lives above

[`expr`](../expr/README.md) builds typed expressions on this layer. Everything else is above that.
