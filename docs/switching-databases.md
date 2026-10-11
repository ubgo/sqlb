# Switching databases

A library that owns tables, an auth system say, is embedded by applications that each chose their own database. One runs Postgres, one runs MySQL, one runs SQLite in development and something else in production. The library should not care, and moving an application from one to another should be a configuration change, not a port.

This page is that use case end to end: what switching usually costs, how the same thing is done with `sqlb`, where engines genuinely disagree, and how each disagreement is handled so the same program returns the same rows everywhere.

## Contents

- [What switching usually costs](#what-switching-usually-costs)
- [One declaration, a dialect chosen at run time](#one-declaration-a-dialect-chosen-at-run-time)
- [Creating the schema on the new engine](#creating-the-schema-on-the-new-engine)
- [Where engines disagree, and what sqlb does about it](#where-engines-disagree-and-what-sqlb-does-about-it)
- [Ignoring case the same way everywhere](#ignoring-case-the-same-way-everywhere)
- [What stays your decision](#what-stays-your-decision)
- [How "the same rows everywhere" is proven](#how-the-same-rows-everywhere-is-proven)

## What switching usually costs

The common shape in data-access tools is a table declaration per engine: one set of table objects for Postgres, another for MySQL, another for SQLite, because each engine's types and functions are modelled separately. Switching engines then means:

- declaring every table again in the new engine's vocabulary, or regenerating the declarations;
- starting a fresh migration history, since the old one is written for the old engine;
- revisiting every query that used an engine-specific construct;
- and, for a library that supports several engines, an adapter layer with `if engine == "postgres"` branches wherever the engines behave differently: case-insensitive matching, returning inserted rows, escaping search text.

Those branches are where the bugs live, because each branch is tested on the engine it was written for and nowhere else.

## One declaration, a dialect chosen at run time

In `sqlb` a table is declared once, as typed columns, and a statement is started with a dialect value:

```go
var (
	Users     = sqlb.Table{Name: "app_user", Alias: "u"}
	UserID    = sqlb.Col[int64](Users, "id")
	UserEmail = sqlb.Col[string](Users, "email")
)

func findByEmail(d sqlb.Dialect, email string, id *int64) (sqlb.Query, error) {
	return sqlb.NewSelect(d).
		Columns(sqlb.Bind(UserID, id)).
		From(Users).
		Where(sqlb.EqFold(UserEmail, email)).
		Build()
}
```

`d` is `sqlb.PostgresDialect{}`, `sqlb.MySQLDialect{}` or `sqlb.SQLiteDialect{}`, and it is the only thing that changes. Pass it into your library once, open the matching driver, and every statement renders for that engine: placeholders, identifier quoting, and every construct whose spelling differs.

A statement started this way may only use constructs every engine accepts. That is enforced rather than hoped for: an engine-only clause on a runtime-dialect statement is a compile error, and an engine-only expression fails at `Build` naming the construct and the dialect. So a library written against `d` cannot quietly depend on one engine. When you do want one engine's features, start from its package instead, `pg.Select`, `mysql.Select` or `sqlite.Select`, and the dialect is fixed in the type.

## Creating the schema on the new engine

The schema is described once too. A `schema.Table` is engine-neutral, and `migrate.Plan` renders the `CREATE TABLE` statements for whichever dialect it is handed, ordered so foreign keys resolve:

```go
users := schema.Table{
	Name:       "app_user",
	Columns:    []schema.Column{{Name: "id", Type: schema.TypeBigInt}, {Name: "email", Type: schema.TypeText}},
	PrimaryKey: []string{"id"},
}
set, err := schema.New("app", users)
if err != nil {
	return err
}
stmts, err := migrate.Plan(d, *set)
```

The typed query columns can be derived from the same table with `schema.MustCol[string](users, "email")`, so the declaration and the queries cannot drift. For DDL beyond what a portable description can say, [`ddl`](./ddl/README.md) builds `CREATE`, `ALTER` and `DROP TABLE` from the same `Col[T]` values, per engine. Switching engines is therefore: change `d`, open the other driver, run the plan. No second set of declarations and no rewritten queries.

## Where engines disagree, and what sqlb does about it

Rendering the right text is half of it. The other half is that engines give different answers to the same SQL, silently. These were each found by running one workload on all three engines and comparing the results line by line, and each is now handled:

| Difference | Postgres | SQLite | MySQL | How it is handled |
|---|---|---|---|---|
| Does `'Ada' LIKE 'ada%'` match? | no | yes | yes, under the default collation | `ContainsFold` and its siblings, below, give one answer |
| Does `'Ada' = 'ada'` match? | no | no | **yes**, under the default collation | declare MySQL text columns `utf8mb4_bin`, see below |
| Rows affected by an `UPDATE` that changes nothing | 1 | 1 | **0** by default | `contrib/driver-mysql` sets `clientFoundRows`, so all three report 1 |
| Type of `COALESCE(sum(x), ?)` | numeric | integer | **`DECIMAL(65,30)`** with a bound value | the MySQL dialect types the parameter, so it scans into `int64` |
| Escaping a typed `%` in a search | backslash by default | no default escape | backslash, unless `NO_BACKSLASH_ESCAPES` | every search predicate writes `ESCAPE '!'`, correct under every setting |
| `RETURNING` on a write | yes | yes | no | a type constraint: `InsertReturning` does not compile against MySQL |

The last row is deliberate. Emulating `RETURNING` on MySQL takes a second statement and a race window, and it only works for some tables. A library that must run on MySQL generates its identifiers in Go before the `INSERT`, which is correct on every engine and needs no emulation.

## Ignoring case the same way everywhere

Case is the difference most code meets first: an email typed as `Ada@Example.com` must find the row stored as `ada@example.com`. Six predicates ask for that by name, and the dialect chooses the spelling:

| Predicate | Postgres | Every other engine |
|---|---|---|
| `EqFold(e, v)` | `lower(e) = lower($1)` | `lower(e) = lower(?)` |
| `InFold(e, v...)`, `NotInFold` | `lower(e) IN (lower($1), ...)` | the same with `?` |
| `ContainsFold`, `StartsWithFold`, `EndsWithFold` | `e ILIKE $1 ESCAPE '!'` | `lower(e) LIKE lower(?) ESCAPE '!'` |

The choice of `ILIKE` comes from the dialect declaring the capability, not from a check of the engine's name, so a dialect written outside this module gets the right spelling by implementing one method. Both sides are folded by the engine's own `lower()`, so the value you pass and the stored column follow one rule.

Two limits belong to the database rather than to any library:

- **Beyond ASCII, the collation decides.** SQLite's `lower()` folds ASCII only, Postgres folds by the column's collation, and MySQL folds Unicode. ASCII answers are identical everywhere.
- **Accents follow the collation too.** Under MySQL's default collation `'café' = 'cafe'` is true; declare the column `utf8mb4_bin` if accents must count.

The full measurements are in [ignoring case, the same on every engine](./expr/README.md#ignoring-case-the-same-on-every-engine).

## What stays your decision

`sqlb` makes the same program produce the same statements and, where engines differ, the same answers. A few choices are still the application's, because they are properties of how a table is declared:

- **MySQL text collation.** For any column where exactness matters, identifiers, tokens and hashes, declare `utf8mb4_bin` so `=` compares bytes as it does on Postgres and SQLite.
- **Indexes for case-insensitive lookups.** `lower(e)` is not `e`, so declare the index on `lower(e)`; on Postgres, an `ILIKE` with a leading wildcard wants a trigram index.
- **Identifiers.** Generate them in Go if MySQL is a target, rather than reading them back.
- **The driver.** Open MySQL through `contrib/driver-mysql`, or set `clientFoundRows=true` in your DSN, before trusting rows-affected counts.

## How "the same rows everywhere" is proven

The shared [conformance suite](./conformance.md) runs on SQLite, Postgres 17 and 18, and MySQL 8.4 under two collations. Two of its cases carry this page's claim:

- **`crud_transcript`** runs one scripted workload of inserts, reads, filters, pages, aggregates, a join, updates and deletes, writes every result to a transcript, rows-affected counts included, and requires it to equal one fixed golden transcript line for line on every engine.
- **`case_fold`** holds the six fold predicates to one fixed list of expected rows on every engine, and Postgres runs it a second time with `ILIKE` switched off to prove both spellings agree.

Because every engine is compared with the same fixed expectations, an engine that passes returns exactly what every other passing engine returns. Run the suite against your own database with `sqlbtest.Run`.
