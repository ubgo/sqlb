# `scan` — results as values

```go
import "github.com/ubgo/sqlb/scan"
```

The other result strategy. A `Reader` captures no caller memory: it allocates fresh destinations for every row and hands back a value.

That single difference is why **one `Reader` is safe to share across any number of concurrent executions**, where a bound query is not. Sixty-four goroutines share one `Reader` against a real database under `-race` in the test suite, and every one gets its own correct rows.

## Contents

- [Setup](#setup)
- [A Reader for one column](#a-reader-for-one-column)
- [Composing with Map2](#composing-with-map2)
- [Running one](#running-one)
- [Why only Map2](#why-only-map2)
- [Building a Reader from outside](#building-a-reader-from-outside)
- [Folding a join into a tree](#folding-a-join-into-a-tree)
- [Aggregating in the database instead](#aggregating-in-the-database-instead)
- [When the columns are only known at runtime](#when-the-columns-are-only-known-at-runtime)
- [Choosing between bind and scan](#choosing-between-bind-and-scan)

## A Reader for one column

```go
r := scan.Field(UserEmail)   // scan.Reader[string]
```

The destination is a fresh `T` per row, which is what lets the value be returned rather than aliased. `T` comes from the expression, so scanning a text column into an `int` is a compile error here exactly as it is with `Bind`.

## Setup

Every snippet on this page uses these declarations.

```go
import (
	"context"
	"database/sql"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
)

// A library declares its table and its typed columns, and exports them.
var (
	Users     = expr.Table{Name: "app_user", Alias: "u"}
	UserID    = expr.Col[string](Users, "id")
	UserEmail = expr.Col[string](Users, "email")
	UserAge   = expr.Col[int](Users, "age")
)

// Supplied by the caller in the snippets below.
var (
	ctx = context.Background()
	db  *sql.DB              // any *sql.DB, *sql.Tx, or other exec.DBTX
	d   = postgres.Dialect{} // the engine being rendered for
)

// The struct the rows are read into.
type person struct {
	Email string
	Age   int
}
```

## Composing with Map2

```go
type person struct {
	Email string
	Age   int
}

var personReader = scan.Map2(
	scan.Field(UserEmail),
	scan.Field(UserAge),
	func(e string, a int) person { return person{Email: e, Age: a} },
)
```

Three columns is `Map2` applied twice:

```go
r := scan.Map2(personReader, scan.Field(UserID),
	func(p person, id string) wide { return wide{p, id} })
```

```
SELECT "u"."email", "u"."age", "u"."id" FROM "app_user" AS "u"
```

Columns come out in composition order, and the scan destinations are allocated in the same order, so a `Reader` built from six `Map2` calls still lines its values up with its columns.

`Map` is the one-column case, for wrapping a scanned value in a domain type:

```go
scan.Map(scan.Field(UserID), func(s string) UserID { return UserID(s) })
```

## Running one

`All` and `One` finish the statement with the reader's columns and execute it:

```go
people, err := scan.All(ctx, db, stmt.NewSelect(d).From(Users), personReader)
p, err := scan.One(ctx, db, sel, personReader)
```

There is no per-row callback and no warning about copying, because each row is already its own value. Appending the result of one iteration cannot alias the next.

`One` returns `ErrNoRows` when nothing matched. It does **not** reject a second row: the binding API's `ErrMultipleRows` exists because its caller is resolving an identity, whereas a `Reader` is normally applied to a query that carries its own `LIMIT`. Use `All` and check the length when uniqueness is the thing being asserted.

A `Reader` that selects nothing is rejected with `ErrNoColumns` before a statement is built, because an empty select list is always a mistake and catching it here names the reader rather than letting the database report a syntax error.

The statement is **cloned** before its select list is finished, so `All` and `One` do not write to the value you passed. That matters more than it sounds: every builder method mutates its receiver, so without the clone a second call on the same statement selected every column twice — legal SQL, and therefore a failure that arrived somewhere else — and two goroutines calling `All` on one statement raced on the same slice. A `Reader` is safe to share; the statement beside it now is too, because a caller cannot be asked to remember which half of the pair is the mutable one.

## Why only Map2

Go generics cannot express a variadic type list. The alternatives are a generated family of `Map3` through `Map12`, or pairwise composition that reaches any width with one function.

Pairwise wins. This is the standard answer to that limitation, not a workaround for it.

## Building a Reader from outside

`Field`, `Map`, `Map2` and `JSON` cover the cases this package knows about. `New` is for the ones it does not:

```go
func New[T any](cols []render.SQLer, alloc func() (dests []any, value func() T)) Reader[T]
```

It exists because a `Reader` could not otherwise be made outside this package, and that asymmetry had a cost. `stmt.Binding` is an interface precisely so a package outside this module can implement one, which is what let [`contrib/structbind`](../../contrib/structbind) exist; the result layer's other half had no such door, so a reflection-driven mapping could produce bindings and could not produce a `Reader`. Reading a query into a slice of structs — the single most common thing a caller wants — was therefore a hand-written loop with a copy per row.

`Mapping.Reader` is the first user, and shows the shape:

```go
rows, err := scan.All(ctx, db, sel, accountMap.Reader())  // []account
```

**The contract is the whole safety argument.** `alloc` is called once per row and must return destinations it has *just allocated*, together with a closure reading those same destinations. It must never hand back a pointer into memory the caller keeps. Nothing in this package takes the query guard, because a `Reader` is supposed to hold no caller memory; a `Reader` that broke that rule would interleave rows across goroutines with no error and no guard to catch it.

## Folding a join into a tree

A join returns a **rectangle**, and the shape you want is a **tree**. Those are not the same thing, and something has to convert between them.

One query for actors and their films returns this:

```
actor_id | actor_name | film_title
---------+------------+-----------
1        | Ada        | Alpha
1        | Ada        | Beta        <- Ada repeated, once per film
2        | Bob        | NULL        <- no films, still a row
3        | Cyd        | Gamma
```

Four rows. Three actors. What you want in Go is `Ada → [Alpha, Beta]`, `Bob → []`, `Cyd → [Gamma]`.

The alternative to folding is a query per parent, which is one query for the list and then one more for each row it returned. That is the N+1 problem, and for a thousand parents it is a thousand and one round trips. The join is one.

### Without GroupBy

You can write the fold yourself, and there is nothing wrong with doing so:

```go
rows, err := scan.All(ctx, db, sel, reader)

byID := map[int64]*actor{}
var order []int64
for _, r := range rows {
	a, seen := byID[r.ActorID]
	if !seen {
		a = &actor{Name: r.ActorName}
		byID[r.ActorID] = a
		order = append(order, r.ActorID)   // (1)
	}
	if r.FilmTitle != nil {                // (2)
		a.Films = append(a.Films, *r.FilmTitle)
	}
}
out := make([]actor, 0, len(order))
for _, id := range order {
	out = append(out, *byID[id])
}
```

The two marked lines are the whole reason the helper exists, and **neither failure is loud**.

Drop **(1)** and iterate the map instead, and your `ORDER BY` is gone: Go randomises map iteration deliberately, so the result is in a different order on every run and the bug surfaces as an intermittent failure in somebody else's test.

Drop **(2)** and `Bob` comes back with one film whose title is the empty string, rather than no films. No panic, no error, just a wrong answer. Dereference without the guard instead and it is a nil-pointer panic on the first childless parent, which at least is loud.

### With GroupBy

```go
rows, err := scan.All(ctx, db, sel, reader)

out := scan.GroupBy(rows,
	func(r row) int64 { return r.ActorID },
	func(first row, g []row) actor {
		return actor{first.ActorName, scan.CollectPtr(g, func(r row) *string { return r.FilmTitle })}
	})
```

```
[{Ada [Alpha Beta]} {Bob []} {Cyd [Gamma]}]
```

Both produce the same result. The query is identical; only what happens after the rows arrive differs. The helper is that fold written once with both traps handled, rather than at every call site where somebody might miss one.

`combine` receives the first row of the group and every row in it. Taking both rather than only the slice keeps the parent's own columns available without indexing into `group[0]` and having to wonder whether it can be empty; it cannot.

The result is in **first-appearance order**, so a query with an `ORDER BY` keeps it.

### LEFT JOIN: use GroupByPtr for the child key

`GroupBy` is for a key that cannot be NULL. When the join is a `LEFT JOIN`, the child key is nullable, and `GroupByPtr` drops the rows where it is nil:

```go
films := scan.GroupByPtr(rowsForThisActor,
	func(r row) *int64 { return r.FilmID },
	func(f row, g []row) filmWithGenres { ... })
```

Without that, a parent with no children is grouped under a zero key and comes back holding one zero-valued child. That is the difference between "Bob has no films" and "Bob has one film with no name".

The type system tells you which to use: a column is `Expr[*int64]` only when it is nullable, so the pointer in the signature is the hint.

### Nesting deeper

Two levels is `GroupBy` applied again inside `combine`, and there is no limit:

```go
actors := scan.GroupBy(rows, byActorID, func(first row, byActor []row) actorOut {
	films := scan.GroupByPtr(byActor, byFilmID, func(f row, byFilm []row) filmOut {
		return filmOut{*f.FilmTitle, scan.CollectPtr(byFilm, genreOf)}
	})
	return actorOut{first.ActorName, films}
})
```

`Collect` and `CollectPtr` exist so a `combine` function is one line rather than a loop:

```go
// Inner join: every child is present, so Collect takes them all.
scan.Collect(group, func(r row) film { return r.Film })

// Left join: CollectPtr drops the nils and dereferences the rest.
scan.CollectPtr(group, func(r row) *string { return r.FilmTitle })
```

Which one you need follows from the join, and the column's Go type says which join you have.

## Aggregating in the database instead

The other way to get one row per parent is to build the array in the database, which `scan.JSON` reads back:

```go
reader := scan.Map2(
	scan.Field(authorName),
	scan.JSON[[]string](pg.JSONAgg(bookTitle)),
	func(name string, titles []string) author { return author{name, titles} },
)
```

The column is taken as a `SQLer` rather than a typed expression because JSON arrives as a different Go type per engine: Postgres hands back `json.RawMessage` for `jsonb`, SQLite hands back a string because `json_group_array` returns `TEXT`. The type parameter that matters is `T`, the shape you get back, and that is still checked.

Which of the two to use is a real decision:

| | `GroupBy` | database aggregate + `scan.JSON` |
|---|---|---|
| Rows on the wire | one per child, parent repeated | one per parent |
| Wide parent | mostly duplicated bytes | sent once |
| Value types | SQL-typed | JSON-typed: a timestamp is a string, a large integer can lose precision |
| Engines | every one | engine-specific aggregate |
| `LIMIT` on parents | **wrong**: a join's `LIMIT` counts rows, so it truncates a parent's children | correct |

That `LIMIT` row is the one that decides it in practice. Paginating parents over a join does not work, because the database has no idea your rows come in groups.

**The trap to know about.** An aggregate over a `LEFT JOIN` with no match does not produce an empty array. It produces an array holding one NULL, because the join supplied one all-NULL row and the aggregate faithfully collected it:

```
names=[Ada Bob Cyd] lengths=[2 1 1]
```

Bob has no films and his array has length one. Filter the nulls out in the aggregate, with `FILTER (WHERE f.id IS NOT NULL)` in Postgres, or read the element type as a pointer and drop the nils in Go. This is pinned by a test rather than left as a warning, because it is silent and it looks right.

An SQL `NULL` in the column itself is the zero `T` rather than an error, since a `LEFT JOIN` with no match means "no children" and failing there would make the common case the error case.

## When the columns are only known at runtime

Everything above needs the columns at compile time. `Field` takes a typed expression, and `bind.Bind` takes a pointer to a variable that has to exist — both are the right trade for a query somebody wrote in Go, and both are no help at all when the shape comes from outside.

That case is real: an export screen with column checkboxes, an admin table, a SQL console. The column list arrives with the request, so no set of variables declared in advance can match it.

The builder already handled it, because a select list is assembled from a slice and a slice can be any length. Only reading was missing, which made this an asymmetry rather than a decision — the library could build a query it could not read back.

```go
// Whatever the user ticked. Only known now.
picked := []string{"email", "age"}

sel := stmt.NewSelect(d).From(Users)
for _, c := range picked {
	sel = sel.Exprs(expr.Col[any](Users, c))
}

q, err := sel.Build()
if err != nil {
	return err
}

rows, err := scan.Maps(ctx, db, q) // []scan.Row, keyed by column name
if err != nil {
	return err
}
for _, r := range rows {
	fmt.Println(r["email"], r["age"])
}
```

`Maps` takes a built `Query` rather than a `Select`, because a caller reaching for this has assembled the select list themselves and there is no `Reader` to add to it.

### What lands in the map

The values are whatever the driver produced, not converted:

| Column | Value in the map |
|---|---|
| integer | `int64` |
| text | `string` |
| real | `float64` |
| blob | `[]byte`, copied |
| `NULL` | `nil` |

Not converting is deliberate. Normalising would have to guess — is this `int64` an ID or a count, is this string a date — and a wrong guess here is invisible, so the driver's own answer is what you get. `NULL` in particular stays `nil` rather than becoming a zero value, because afterwards you could not tell the two apart.

The blob copy is not cosmetic. A driver may hand back a slice into its own read buffer and overwrite it on the next row, so a reader that kept the slice would corrupt every row but the last, and only for blob columns.

### Two columns with the same name

`SELECT a.id, b.id` is an ordinary query, and a map can hold one of the two. Keeping one and dropping the other silently is exactly the kind of wrong answer this library refuses, so `Maps` fails with `ErrDuplicateColumn` naming the column, and `Slices` is the form that loses nothing:

```go
names, vals, err := scan.Slices(ctx, db, q)
if err != nil {
	return err
}
fmt.Println(names)   // [id id] — both kept, positionally
for _, row := range vals {
	fmt.Println(row) // []any, same order as names
}
```

`Slices` is also the right form for anything writing rows out in order, such as a CSV export, where the names are a header written once rather than a key per cell.

### Streaming

`EachMap` calls a function per row instead of building a slice. Unlike the binding API's `Each`, the map handed to the callback is fresh every row and is never reused, so a callback may keep it:

```go
err := scan.EachMap(ctx, db, q, func(r scan.Row) error {
	return w.Write(r) // keeping r is safe; nothing is reused
})
```

Returning an error stops iteration and returns that error.

### What this costs

This is the escape hatch for the result layer, in the same sense that [`render.Raw`](../render/README.md) is the escape hatch for rendering, and it costs the same thing. A value is an `any`, so a wrong assumption about a type is a runtime panic rather than a compile error, and a misspelled column name is a missing key rather than a build failure. Use it where the shape is genuinely dynamic and nowhere else.

One thing to get right at the call site: a column name cannot be a bound argument, because no engine accepts a placeholder where an identifier goes. The name is rendered into the SQL text. The dialect doubles an embedded quote, so an attacker-controlled string stays one identifier and the query fails with "no such column" rather than running a second statement — `TestCol_NameFromOutsideStaysOneIdentifier` pins that. It is still worth checking the name against a list your code owns, so a user gets a clear error instead of a database one, and so nobody can read a column you did not mean to expose.

## Choosing between bind and scan

| | [`bind`](../bind/README.md) | `scan` |
|---|---|---|
| Call site | shorter: `Bind(col, &dst)` | composed: `Map2(...)` |
| Holds caller memory | yes, a pointer per column | no |
| Safe to share between goroutines | **no** — data race | yes |
| Safe to cache or hold in a struct field | no | yes |
| Per-row callback must copy | yes | no — `EachMap` allocates per row |
| Columns known at compile time | required | required, except `Maps`/`Slices` |

Use `bind` for a query built where its destinations live, which is most queries. Use `scan` for a query shared between goroutines, cached, or held in a struct field.

Neither is deprecated, and [`stmt`](../stmt/README.md) holds no opinion about either, so a third strategy is somebody else's package rather than a fork.
