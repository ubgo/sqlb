# driver-pgx

Opens Postgres for `github.com/ubgo/sqlb` through `github.com/jackc/pgx/v5`'s `database/sql` adapter, and is where the `pg` package is **executed** rather than asserted.

This module exists for the tests more than for the driver. `pg` holds five hundred exported constructs, and until this module every one of them was checked as rendered text: a comparison with another Go builder found a table declared without an alias rendering a quoted empty identifier, which SQLite accepts and Postgres rejects, so the entire suite stayed green while the SQL was invalid on the engine the package is named after. The conformance suite and the construct catalogue here run against a real server, which is the only thing that turns an asserted construct into a proven one.

## Contents

- [Setup](#setup)
- [Opening a database](#opening-a-database)
- [Running the proof](#running-the-proof)
- [What is proven](#what-is-proven)

## Setup

```go
import (
	driverpgx "github.com/ubgo/sqlb/contrib/driver-pgx"
)
```

## Opening a database

```go
db, err := driverpgx.Open("postgres://localhost:5432/app?sslmode=disable")
```

`Open` registers nothing you have to name — the adapter registers itself under `DriverName`, `"pgx"` — and pings, so a bad DSN fails here rather than at the first query. Two defaults are applied and each has an `Option`: `DefaultMaxOpenConns` (`WithMaxOpenConns`) caps the pool, because a Postgres connection is a server process and an uncapped pool exhausts `max_connections` before anything on the client; `DefaultConnMaxLifetime` (`WithConnMaxLifetime`) recycles connections so a restart or a pooler does not leave dead sockets in the pool. The `*sql.DB` satisfies `exec.DBTX` exactly as SQLite's does.

## Running the proof

The tests read the connection string from `EnvDSN`, which is `SQLB_PG_DSN`; `DSNFromEnv` is exported so a consumer's own integration tests can share the convention.

```
SQLB_PG_DSN="postgres://localhost:5432/sqlb_conformance?sslmode=disable" go test ./contrib/driver-pgx/ -v
```

Absence is a **loud skip**, never a silent pass: without the variable every test in this package skips with a message naming it, so a green gate with the proof skipped is visible in the skip count rather than mistaken for the proof having run. The tests create and drop their own tables and extensions and touch nothing else in the database they are given.

## What is proven

- The shared conformance suite in `sqlbtest`, all cases plus `RunReturning`, on a second engine.
- The `pg` construct catalogue, one file per area — `pg_constructs_test.go` for clauses and the empty-alias class, `pg_arrays_json_test.go`, `pg_scalar_test.go` for text, time, ranges, full-text search and math, `pg_statements_test.go` for aggregates, windows, writes, `LOCK TABLE`, `MERGE`, `TRUNCATE` and recursive options, and `pg_extensions_test.go` for the eight extension packages, which it creates in the database it is given. 408 of `pg`'s 411 exports and all 89 extension exports are executed, 555 cases; `Is` is a Go-side check, `TerminateBackend` would end the connection, and `ArrayReverse`, `ArraySort` and `ArraySortDesc` skip below Postgres 18 with a message that says so.
- What executing found, each with a passing rendering test: bare parameters that Postgres cannot type in `ARRAY[]`, `jsonb_build_object`, `JSON_OBJECT`, `generate_series`, `substring` and range `@>`; `->` with a text parameter taking the key form; `LOCK TABLE` and `TRUNCATE` rendered with the table's alias; and two functions documented as Postgres 17 that are 18. Every fix is a cast or a spelling in `pg`, and every one is pinned here.
- The empty-alias class specifically — tables declared without an alias, as a CTE reference is — because that is the mistake SQLite tolerates and Postgres does not.
