# `contrib/debug` — read a statement with its values in it

```go
import "github.com/ubgo/sqlb/contrib/debug"
```

Renders a built statement with its arguments substituted, **for reading and never for running**.

## Contents

- [The problem](#the-problem)
- [Usage](#usage)
- [Why the result is not a string](#why-the-result-is-not-a-string)
- [What it handles](#what-it-handles)
- [What it is not](#what-it-is-not)

## The problem

`sqlb` never puts your values in the SQL. It emits markers and ships the values separately, which is why an injection cannot happen: a value containing `'; DROP TABLE users; --` is still a value, because it never travelled as SQL.

That is the right default, and it makes a log line hard to read:

```
SELECT "o"."id", "o"."total_cents" FROM "orders" AS "o" WHERE ("o"."status" = $1 AND "o"."region" IN ($2, $3, $4) AND "o"."created_at" >= $5 AND "o"."created_at" < $6 AND "o"."total_cents" > $7) ORDER BY "o"."total_cents" DESC LIMIT $8
[shipped eu-west eu-north us-east 2026-01-01 2026-02-01 5000 20]
```

Eight markers and eight values, and you have to count. Is `$5` the start of the range or the end? You also cannot paste that into `psql` to try a variation, which is usually the fastest way to find out why a report is wrong.

## Usage

```go
q, err := stmt.NewSelect(d).Exprs(id, name).From(users).
	Where(expr.Eq(name, "O'Brien"), expr.Greater(id, 10)).
	Limit(5).
	Build()

fmt.Println(debug.Interpolate(d, q))
```

```
SELECT "u"."id", "u"."name" FROM "users" AS "u" WHERE ("u"."name" = 'O''Brien' AND "u"."id" > 10) LIMIT 5
```

Paste-able. Note that `O'Brien` came out as `'O''Brien'`, with the quote doubled.

`Explain` adds what the bare statement loses, for a log line:

```go
debug.Explain(d, q)
// SELECT ...  -- 2 arg(s), postgres
```

The dialect is a parameter because placeholder syntax is the dialect's decision. Asking it for `Placeholder(1)` and `Placeholder(2)` is how this works out whether markers are positional, as `?` is, or numbered, as `$1` is, without knowing anything about the engine.

## Why the result is not a string

The output is a complete, runnable SQL statement with values already inside it. Execute that and every protection above is gone.

The usual defence is a doc comment saying "do not execute this". Comments do not stop anyone, and once a function returns a plain `string` the value flows anywhere a string flows, three functions away from the warning.

So `Interpolate` returns `Rendered`, a **named type**:

```go
type Rendered string
```

Its underlying type is `string`, but it is not `string`. It prints identically:

```go
fmt.Println(r)   // works
```

and it cannot reach a query slot:

```go
db.ExecContext(ctx, debug.Interpolate(d, q))
// cannot use ... (variable of string type Rendered) as string value in argument to ExecContext
```

That is a compile error, not a warning. The program does not build.

The escape hatch is deliberate and visible:

```go
execute(string(r))   // compiles
```

The aim is not to make the text unreachable; it is to make the mistake impossible **by accident**. `execute(r)` fails to build, and `execute(string(r))` is something somebody had to type and a reviewer can see in the diff. `TestRendered_CannotBeExecuted` pins that `Rendered` has not been quietly turned into an alias.

## What it handles

**Both placeholder families.** Numbered markers are replaced by their own number, highest first so `$1` does not match inside `$10`, which is pinned with eleven arguments. Positional `?` markers are replaced left to right, since order is the only thing binding a value to a position.

**Quoted runs are skipped.** A question mark inside a string literal is data, not a placeholder, and substituting it would corrupt the very thing you are trying to read:

```
SELECT 'is this a ? or not', "we?rd" FROM t WHERE a = ? AND b = ?
SELECT 'is this a ? or not', "we?rd" FROM t WHERE a = 1 AND b = 2
```

**Driver wrappers are unwrapped**: `sql.NamedArg` and anything implementing `driver.Valuer`, so `sql.NullString{String: "maybe", Valid: true}` reads as `'maybe'` rather than as a struct.

**Values are formatted for reading**: strings quoted with doubling, `nil` as `NULL`, booleans as `TRUE`/`FALSE`, byte slices as `'\xdead'`, times as RFC 3339, numbers bare.

## What it is not

The output is **for a human**. It is not guaranteed to be valid SQL for any particular engine, it does not attempt the escaping rules of every type a driver accepts, and it must never be sent to a database.

It lives in `contrib` rather than the core for the same reason: reading a query is a development concern, and a package whose job is building safe statements should not ship the tool for defeating them in the same import.
