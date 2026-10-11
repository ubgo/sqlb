# sqlb — typed SQL for Go

**A typed SQL query builder for Go libraries that own tables and cannot carry an ORM. Zero dependencies.**

[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&logoColor=white)](./go.mod)
[![Dependencies](https://img.shields.io/badge/core%20dependencies-zero-brightgreen)](./go.mod)
[![Coverage](https://img.shields.io/badge/coverage-88.4%25%20core%20%7C%2097.7%25%20driver-brightgreen)](./COVERAGE.md)
[![Conformance](https://img.shields.io/badge/conformance-21%20cases%20on%20SQLite%20%2B%20Postgres%20%2B%20MySQL-brightgreen)](./docs/conformance.md)

`sqlb` makes a database column a typed Go value. A library exports its columns; an application composes them into queries, and the compiler checks every column, comparison, join condition and scan target. It speaks `database/sql` directly — pgx, modernc SQLite, clickhouse-go, anything with a driver — and ships engine packages for Postgres, SQLite, MySQL and ClickHouse, plus extension sub-packages for pgvector, pg_trgm, ltree, pgcrypto, earthdistance, unaccent and PostGIS.

```sh
go get github.com/ubgo/sqlb                        # the builder, sqlbtest, the engine packages — zero dependencies
go get github.com/ubgo/sqlb/contrib/driver-sqlite  # SQLite, with the pragmas a library-owned schema needs
go get github.com/ubgo/sqlb/contrib/driver-pgx     # Postgres, through pgx's database/sql adapter
go get github.com/ubgo/sqlb/contrib/driver-mysql   # MySQL 8, through go-sql-driver/mysql
```

Every module is tagged together: the core as `v0.1.0`, each contrib module as `contrib/<name>/v0.1.0`. Inside the repository a committed `go.work` resolves the modules against each other.

## Why it exists

A library that ships its own tables has a problem no ORM solves for it. It must let the application join across the boundary into tables it has never heard of, and it must do so without pulling a code generator or a second ORM into an application that may already have one. Passing SQL strings across that boundary gives the application no type safety and the library no control. `sqlb` is the smallest thing that gives both: the library exports typed column values, the application composes them, and the compiler checks the join. [`docs/cross-package.md`](./docs/cross-package.md) is that story end to end — four packages that do not import each other, consolidated into one statement.

- **A column is an `Expr[T]`.** Comparing it with the wrong type, joining it to a mismatched column, or binding it to the wrong pointer is a compile error, not a runtime surprise.
- **A query carries its own scan targets.** The column list and the destinations come from one slice, so inserting a column in the middle cannot silently shift a scan.
- **Zero third-party dependencies in the core**, enforced by a gate rather than by good intentions. Embedding `sqlb` adds no driver, no cgo and no version conflict.
- **Engine packages carry what one engine spells differently.** JSONB, arrays, ranges, full-text search, `DISTINCT ON`, `ON CONFLICT`, `MERGE` and `RETURNING` in `pg`; JSON1, FTS5 and both upsert forms in `sqlite`; `PREWHERE`, `FINAL` and `LIMIT BY` in `ch`. The `pg` surface is the large one — about five hundred constructs to the core's two hundred and sixty, which is what the universality rule produces: almost nothing is spelled identically by four engines.
- **Extensions are sub-packages over exported guarded constructors**, so adding pgvector support, or the next extension nobody has written yet, never edits the engine package.

## Checked three times, not claimed once

"Type-safe" means different things in different places, so here is exactly what is checked, where, and by what.

**1. The compiler checks the typed core.** A string column bound to an `*int`, a time column compared with a string, a vector distance over a text column, a sum over a non-numeric column — none of these compile. That is not asserted in prose: `examples/typecheck` holds deliberately wrong statements behind a build tag, and the gate compiles it and fails if it ever succeeds. An engine **clause** is also typed to its dialect, so `pg.ForUpdate()` on a SQLite statement does not compile either.

**2. `Build` checks engine expressions.** A `pg`, `sqlite` or `ch` expression records its own name while rendering, and under another dialect `Build` returns `ErrUnsupportedDialect` naming the construct — in your process, before anything reaches a database:

```go
dist := vector.L2(embedding, vector.ParamVector(query))

_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Columns(sqlb.Bind(dist, &d)).From(docs).Build()
// err: sqlb: construct is not supported by this dialect
//      vector.L2 under dialect "sqlite"
```

This is the honest limit of the type system: engine expressions are checked when the statement is built, not when the program is compiled. The failure is still in your process and still names the construct — it is never a syntax error from the database at three in the morning.

**3. A real database checks execution.** Rendering SQL and running it are different claims, and only execution proves the second. `sqlbtest` is a shared conformance suite every driver runs; `contrib/driver-sqlite` executes every `sqlite` construct against a real database, and `contrib/driver-pgx` executes every construct in `pg` and its eight extension packages — 593 cases — against Postgres 17, and the same suite again against Postgres 18 for the constructs 17 refuses. Those runs found eleven constructs whose rendering tests passed and whose SQL Postgres rejects, which is why execution, not rendering, is the bar for calling a construct proven. `ch` is the proof that the extension points carry a second engine; it has not been executed against a server.

## How it compares

Columns are categories, not products, and every cell is a claim about this repository or about what a category does by definition.

| | **sqlb** | ORM or code generator | String-building query builder | Hand-written SQL |
|---|---|---|---|---|
| Columns, joins and scan targets checked by the compiler | ✅ | ✅ | ❌ | ❌ |
| Works with no code-generation step | ✅ | ❌ | ✅ | ✅ |
| Third-party dependencies to adopt it | none | ⚠️ varies | ⚠️ varies | none |
| A library can export typed columns for code that has never seen its schema | ✅ | ⚠️ only if both sides adopt the same tool | ❌ | ❌ |
| An engine-only construct fails before reaching the database | ✅ | ⚠️ varies | ❌ | ❌ |
| Driver proven by a shared conformance suite | ✅ | ⚠️ varies | ❌ | ❌ |
| Migrations, relation graph, generated models | ❌ | ✅ | ❌ | ❌ |

If you want migrations, a relation graph and generated models, an ORM is the right tool. If you want a library's tables to be joinable by code that has never seen its schema, without either side adopting the other's ORM, that is `sqlb`.

## What it is not

It is not an ORM. There is no relation graph, no code generation, no derived DDL and no lazy loading. A project that wants those should use an ORM; this exists for libraries that cannot.

## Documentation

Every command this repository uses, from the gate to running the proof on all three engines, is in [COMMANDS.md](./COMMANDS.md).

Start at [`docs/README.md`](./docs/README.md). It indexes one document per topic — the core builder, dialects, the engine packages, extensions, execution, the conformance suite — and one page per package with runnable snippets, every one of them copied from a test whose expected output `go test` checks. [`docs/examples.md`](./docs/examples.md) indexes the compile-checked catalogues.

## Get started

Ten minutes end to end: declare a table, open a database, read a row, join across a package boundary, write, and reach for the one construct only your engine has.

### 1. Declare your tables

A library declares its tables once, as package-level values. Everything downstream — the library's own queries, the application's queries, the compiler's checking — works from these. There is no generator and nothing to re-run when a column is added.

```go
package users

import (
	"time"

	"github.com/ubgo/sqlb"
)

var Table = sqlb.Table{Name: "app_user", Alias: "u"}

var (
	ID        = sqlb.Col[string](Table, "id")
	Email     = sqlb.Col[string](Table, "email")
	CreatedAt = sqlb.Col[time.Time](Table, "created_at")
)
```

`Col[T]` is where the type comes from. `Email` is an `sqlb.Expr[string]`, so comparing it to an `int`, binding it to an `*int` or joining it to a `time.Time` column is a compile error.

### 2. Open a database

`sqlb` never opens a connection for you — it takes any `database/sql` handle, so `*sql.DB` and `*sql.Tx` both work and your existing pool is the pool. The two `contrib` modules are a convenience that applies the settings each engine wants, nothing more.

**Postgres**, through pgx's `database/sql` adapter:

```go
import driverpgx "github.com/ubgo/sqlb/contrib/driver-pgx"

db, err := driverpgx.Open("postgres://user:pass@localhost:5432/app?sslmode=disable")
if err != nil {
	return err
}
defer db.Close()
```

`Open` sets a pool size and a connection lifetime and pings, so a bad DSN fails there rather than at your first query. Using pgx directly — `sql.Open("pgx", dsn)` — works identically; the module exists so those defaults are not retyped.

**SQLite**, through the pure-Go modernc driver:

```go
import driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"

db, err := driversqlite.Open("app.db")   // or driversqlite.Memory() in tests
if err != nil {
	return err
}
defer db.Close()
```

`Open` applies the pragmas a library-owned schema needs — WAL, foreign keys, a busy timeout — to every connection in the pool, which is the part that is easy to get wrong once and then debug for a week. `Memory()` pins the pool to a single connection, because an in-memory SQLite database belongs to the connection that created it and a pool of two serves the second query from a second, empty database.

**MySQL 8**, through go-sql-driver/mysql:

```go
import drivermysql "github.com/ubgo/sqlb/contrib/driver-mysql"

db, err := drivermysql.Open("user:pass@tcp(127.0.0.1:3306)/app")
if err != nil {
	return err
}
defer db.Close()
```

`Open` forces `parseTime` and UTC whatever the DSN says, because the MySQL dialect writes times in UTC and a `DATETIME` read back in another zone shifts silently.

ClickHouse and anything else work the same way: bring your own driver and pass the `*sql.DB`.

### 3. Fix the dialect once

Every statement carries its dialect in its type. Write it out —

```go
q, err := sqlb.NewSelect(sqlb.PostgresDialect{}). /* … */ Build()
```

— or start it from your engine's package and stop repeating it. `pg.Select` is Postgres, `sqlite.Select` is SQLite and `mysql.Select` is MySQL, and the same package holds every construct you put inside the statement:

```go
import "github.com/ubgo/sqlb/pg"

q, err := pg.Select().Columns(sqlb.Bind(users.Email, &email)).
	From(users.Table).
	Where(sqlb.Eq(users.ID, "usr_1")).
	Build()
```

`Select()` with no arguments is the long form exactly, for a statement whose select items are bindings added with `Columns` — which is what a query you intend to scan wants. Passing items to `Select` directly adds them without scan targets, for a subquery or a CTE body.

The dialect is not decoration. `pg.ForUpdate()` on a SQLite statement does not compile, and a `pg` expression under the SQLite dialect fails at `Build` naming the construct.

### 4. Read a row, then many

Bind each column to a destination. `One` scans through the bindings, so nothing is passed for the destination at the call site — the column list and the scan targets come from one slice and cannot drift apart when someone inserts a column in the middle.

```go
var id, email string

q, err := pg.Select().Columns(sqlb.Bind(users.ID, &id), sqlb.Bind(users.Email, &email)).
	From(users.Table).
	Where(sqlb.Eq(users.Email, "a@example.com")).
	Build()
if err != nil {
	return err
}

if err := sqlb.One(ctx, db, q); err != nil {
	return err   // sql.ErrNoRows when there is no such user
}
```

`Each` refills the same destinations per row and calls your function:

```go
err = sqlb.Each(ctx, db, q, func() error {
	fmt.Println(id, email)   // the current row
	return nil
})
```

A `Query` holds pointers into `id` and `email`, so it is **not safe to share between goroutines** — build it where its destinations live. `One` and `Each` refuse an overlapping execution with `ErrQueryInUse` rather than returning interleaved rows. When you want a query you can share, the `scan` package's readers allocate per row instead of writing through your pointers.

### 5. Join across a package boundary

This is the boundary the library exists for. Another package declares `invoices` exactly as above, and a third package joins the two without either side having seen the other's schema:

```go
var email string
var total int64

q, err := pg.Select().Columns(sqlb.Bind(users.Email, &email), sqlb.Bind(invoices.Total, &total)).
	From(users.Table).
	Join(sqlb.Join{
		Type:  sqlb.JoinInner,
		Table: invoices.Table,
		On:    []sqlb.Predicate{sqlb.EqExpr(users.ID, invoices.UserID)},
	}).
	Order(sqlb.OrderBy(invoices.Total, sqlb.SortDesc)).
	Limit(10).
	Build()

// SELECT "u"."email", "i"."total" FROM "app_user" AS "u"
//   INNER JOIN "invoice" AS "i" ON ("u"."id" = "i"."user_id")
//   ORDER BY "i"."total" DESC LIMIT $1
```

`EqExpr` compares two columns, and it compares them at their Go types: joining `users.ID` to a `time.Time` column does not compile.

### 6. Write

Inserts, updates and deletes end in `Build()` the same way. An UPDATE or DELETE without a WHERE **does not compile** — the unfiltered statement has no `Build` method at all, so say `All()` when you mean every row. `RETURNING` is a function rather than a method because it is conditional on the dialect having it; on ClickHouse the line does not compile.

```go
var newID string

ins, err := pg.InsertReturning(
	pg.Insert(users.Table).
		Row(sqlb.Set(users.ID, "usr_1"), sqlb.Set(users.Email, "a@example.com")),
	sqlb.Bind(users.ID, &newID),
).Build()

upd, err := pg.Update(users.Table).
	Set(sqlb.Set(users.Email, "b@example.com")).
	Where(sqlb.Eq(users.ID, "usr_1")).
	Build()

del, err := pg.Delete(users.Table).
	Where(sqlb.Eq(users.ID, "usr_1")).
	Build()
```

### 7. Use what only your engine has

The core carries only what Postgres, MySQL, SQLite and ClickHouse all spell identically. Everything else lives in an engine package — `pg`, `sqlite`, `mysql`, `ch` — and composes with the core like any other expression or clause.

**Postgres**: an upsert, and a full-text search over user input.

```go
import "github.com/ubgo/sqlb/pg"

up, err := pg.Insert(docs.Table).
	Row(sqlb.Set(docs.ID, "d1"), sqlb.Set(docs.Title, "Ada")).
	Clause(pg.OnConflict(docs.ID).
		DoUpdate(sqlb.SetExpr(docs.Title, pg.Excluded(docs.Title))).
		Clause()).
	Build()

hits, err := pg.Select().Columns(sqlb.Bind(docs.ID, &id)).
	From(docs.Table).
	Where(pg.Matches(
		pg.ToTSVector("english", docs.Body),
		pg.WebsearchToTSQuery("english", userQuery),
	)).
	Build()
```

**SQLite**: the same upsert in SQLite's spelling, and FTS5.

```go
import "github.com/ubgo/sqlb/sqlite"

up, err := sqlite.Insert(docs.Table).
	Row(sqlb.Set(docs.ID, "d1"), sqlb.Set(docs.Title, "Ada")).
	Clause(sqlite.OnConflict(docs.ID).
		DoUpdate(sqlb.SetExpr(docs.Title, sqlite.Excluded(docs.Title))).
		Clause()).
	Build()

hits, err := sqlite.Select().Columns(sqlb.Bind(docs.ID, &id)).
	From(docs.Table).
	Where(sqlite.Match(docs.Body, userQuery)).
	Build()
```

**MySQL**: the upsert in MySQL's spelling, which fires on any unique key, and a full-text match.

```go
import "github.com/ubgo/sqlb/mysql"

up, err := mysql.Insert(docs.Table).
	Row(sqlb.Set(docs.ID, "d1"), sqlb.Set(docs.Title, "Ada")).
	Clause(mysql.OnDuplicateKeyUpdate(sqlb.SetExpr(docs.Title, mysql.New(docs.Title)))).
	Build()

hits, err := mysql.Select().Columns(sqlb.Bind(docs.ID, &id)).
	From(docs.Table).
	Where(sqlb.Greater(mysql.Match(userQuery, mysql.NaturalLanguage, docs.Body), 0.0)).
	Build()
```

**A Postgres extension** is a sub-package built on the guarded constructors `pg` exports, so adding one never edits `pg`. Here a pgvector distance, ordered like any other expression:

```go
import "github.com/ubgo/sqlb/pg/vector"

dist := vector.L2(docs.Embedding, vector.ParamVector(query))

near, err := pg.Select().Columns(sqlb.Bind(docs.ID, &id)).
	From(docs.Table).
	Order(sqlb.OrderBy(dist, sqlb.SortAsc)).
	Limit(10).
	Build()
```

### Where to go next

Every snippet above is the shape a test in this repository builds and checks. The deeper versions — predicates, aggregates, windows, CTEs, subqueries, set operations, upserts, JSON, arrays, ranges, full-text search and the eight extension packages — live one page per package under [`docs/`](./docs/README.md), with runnable snippets throughout. [`docs/cross-package.md`](./docs/cross-package.md) takes the join above to its full size — an auth library, a plugin that borrows one of its tables, a second library that imports nothing, and the application that consolidates all three. [`docs/switching-databases.md`](./docs/switching-databases.md) shows one codebase moving between Postgres, MySQL and SQLite by changing a dialect value, and every engine difference that had to be handled to make the answers identical. [`docs/engines.md`](./docs/engines.md) is the map of which construct lives where, and [`docs/execution.md`](./docs/execution.md) covers transactions, the `scan` readers and concurrency.

## FAQ

**Is sqlb an ORM?**

No. There is no relation graph, no code generation, no derived DDL, no lazy loading and no migrations. It builds SQL text and bound arguments, and executes them through `database/sql`. If you want an ORM, use one.

**Does it generate code?**

No. There is no build step and nothing to regenerate when a table changes. You declare columns as package-level values, and the compiler does the checking a generator would otherwise do.

**Which drivers does it work with?**

Any `database/sql` driver. The core takes a `DBTX` interface that both `*sql.DB` and `*sql.Tx` satisfy, so pgx through its `stdlib` wrapper, modernc SQLite, mattn SQLite and clickhouse-go all work. The core has no driver and no opinion about one; `contrib/driver-sqlite` is a convenience, not a requirement.

**What happens when sqlb has no helper for the SQL I need?**

You write it yourself and stay inside the type system. `sqlb.Fn[R]` is the typed door for any function call, and each engine package exports guarded constructors so an application can add a missing helper without waiting for a release. A missing helper is never a reason to go back to strings.

**How does it stop me using a Postgres feature on SQLite?**

Two ways, honestly distinguished. An engine **clause** is typed to its dialect and statement kind, so `pg.ForUpdate()` on a SQLite statement does not compile. An engine **expression** records its own name while rendering; under another dialect, `Build` returns `ErrUnsupportedDialect` naming the construct, for example `vector.L2 under dialect "sqlite"`. Both failures happen in your process, before anything reaches a database.

**Can I build a query once and reuse it across requests? Is a Query thread-safe?**

Sequentially, yes. Concurrently, no: a `Query` is **not safe for concurrent use**, and running one from two goroutines is a **data race**. It holds pointers into the variables you bound, so both write to the same memory and interleave each other's rows. `go test -race` reports it against those variables. Build it where its destination lives, per call; building is just assembling a string. `One` and `Each` refuse an overlapping execution with `ErrQueryInUse` rather than returning corrupted results. The `scan` package's readers, which allocate per row instead of writing through your pointers, are safe to share.

**Does importing sqlb pull anything into my dependency tree?**

Nothing. The core imports only the Go standard library, and a gate fails the build if a third-party requirement ever appears in its `go.mod`. Anything that needs a dependency is a separate module under `contrib/`.

**Is it production ready?**

It is pre-GA and versioned `v0.x`, so the API may still break. The behaviour is heavily pinned: several hundred statements have their exact SQL checked by tests, deliberately wrong code is compiled by a gate that fails if it succeeds, and execution is proven against real databases on both SQLite and Postgres — not just rendered. Read [COVERAGE.md](./COVERAGE.md) for the measured numbers and, more usefully, for what coverage does not measure.

## License

[Apache License 2.0](./LICENSE). Free for any use, including commercial, with a patent grant and the attribution the license requires.

<sub>Go SQL query builder · typed SQL for Go · generics · database/sql · zero dependencies · ORM alternative · PostgreSQL · SQLite · ClickHouse · pgvector · pg_trgm · ltree · pgcrypto · PostGIS · query builder for library authors · compile-checked joins and scan targets · open source, Apache-2.0</sub>
