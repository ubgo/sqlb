# driver-sqlite — SQLite for sqlb

`driver-sqlite` opens SQLite databases for [`github.com/ubgo/sqlb`](https://github.com/ubgo/sqlb), with the pragmas a library-owned schema needs already applied.

It is a **separate Go module** at `contrib/driver-sqlite` inside the `ubgo/sqlb` repository. The core stays dependency-free; `modernc.org/sqlite` is pulled in only when you import this. That driver is pure Go, so this module needs no cgo and cross-compiles.

## Why it exists

Left at its defaults, SQLite does three things that are wrong for a process whose libraries own tables, and all three are invisible until production.

- **Foreign keys are not enforced.** SQLite parses the constraints your schema declares and then ignores them. A delete that should fail instead orphans rows, silently.
- **A contended write fails immediately.** The second writer gets `SQLITE_BUSY` rather than waiting, which is the error everyone meets first and understands last.
- **Readers queue behind the writer.** Without WAL, one write blocks every read in the process.

`Open` turns all three around, in one place, and says why in the source. Everything else is `database/sql`.

## Install

```sh
go get github.com/ubgo/sqlb/contrib/driver-sqlite@latest
```

The module is tagged `contrib/driver-sqlite/v0.1.0` and requires the core at `v0.1.0`. Inside the repository the committed `go.work` resolves the core against the checkout and needs nothing.

## Use

```go
db, err := driversqlite.Open("app.db")
if err != nil {
	return err
}
defer db.Close()

var id, email string
q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
	Columns(sqlb.Bind(UserID, &id), sqlb.Bind(UserEmail, &email)).
	From(Users).
	Where(sqlb.Eq(UserEmail, "a@example.com")).
	Build()
if err != nil {
	return err
}
err = sqlb.One(ctx, db, q)
```

The dialect is `sqlb.SQLiteDialect{}` from the core. This module adds no dialect of its own; there would be nothing in it.

In tests:

```go
db, err := driversqlite.Memory()
```

## The defaults

| Pragma | Value | What it fixes |
|---|---|---|
| `journal_mode` | `WAL` | readers run while a writer commits |
| `busy_timeout` | `5000` | a contended write waits up to five seconds instead of failing at once |
| `foreign_keys` | `ON` | the constraints the schema declares are enforced |
| `synchronous` | `NORMAL` | the documented companion of WAL: durable against a process crash |

They are applied through the DSN, not through an `Exec`. SQLite scopes most pragmas to a connection, so setting one with a bare `Exec` configures whichever pooled connection happened to serve it and leaves the others alone. In the DSN they hold for every connection the pool opens, including the ones it opens later. `TestPragmas_AppliedToEveryConnection` is what proves it.

`busy_timeout` does not cover every contended write, and two refusals escape it at once, both measured with two handles on one file. A deferred transaction whose first statement reads holds a read lock, and SQLite refuses its upgrade to a write while another connection writes, without consulting the timeout; begin a transaction that will write with `BEGIN IMMEDIATE` on a pinned `*sql.Conn` instead. And on a file not yet in WAL mode, the `journal_mode=WAL` this module applies to each new connection fails as busy while another connection is in the file, so opening a connection can fail; a file converted once stays in WAL and stops hitting it. `contrib/migrate`'s ledger handles both itself, which is why two deploys on one file wait for each other there.

Override one, keep the rest:

```go
db, err := driversqlite.Open("app.db", driversqlite.WithPragma("journal_mode", "DELETE"))
```

Drop them all and start from SQLite's own defaults:

```go
db, err := driversqlite.Open("app.db", driversqlite.WithoutDefaultPragmas())
```

The set above is `driversqlite.DefaultPragmas`, a slice of `Pragma` values, so a caller can read what is about to be applied rather than take this table's word for it:

```go
for _, p := range driversqlite.DefaultPragmas {
	fmt.Println(p.Name, p.Value)
}
```

### Pool size

```go
db, err := driversqlite.Open("app.db", driversqlite.WithMaxOpenConns(1))
```

Worth knowing rather than worth ignoring. SQLite allows one writer at a time, so under write contention a larger pool converts what would be a queue into `SQLITE_BUSY` retries against `busy_timeout`. One connection serialises writes in Go instead, which is the right answer for a write-heavy embedded database and the wrong one for a read-heavy one.

### The driver name

`driversqlite.DriverName` is the string this module registers with `database/sql`, exported so a caller can open a second handle themselves — for a tool, a migration runner, or a test — without hardcoding a name that would then be wrong if it changed:

```go
db, err := sql.Open(driversqlite.DriverName, dsn)
```

Doing that gives up the pragmas, which is exactly why `Open` exists.

## Documentation

A per-symbol cookbook is in [`docs/README.md`](./docs/README.md) and [`docs/features.md`](./docs/features.md).

## What this module really is for

Rendering SQL and executing it are different claims. The builder's own tests prove the text; only a database proves that the placeholders, the argument order and the scan targets line up. This module runs the shared conformance suite from [`github.com/ubgo/sqlb/sqlbtest`](../../sqlbtest) against a real database, twice: in memory and file-backed.

```sh
task conformance
```

It also executes every construct in [`github.com/ubgo/sqlb/sqlite`](../../sqlite) against that database: the JSON1 functions, FTS5 search, the time modifiers, the planner hints, and the difference between `INSERT OR REPLACE` and `ON CONFLICT DO UPDATE`, which is that the first changes the rowid and the second does not. That makes `sqlite` the only engine package in this repository whose SQL is proven rather than asserted.

Real output, both databases, every case:

```
--- PASS: TestConformance (0.00s)
    --- PASS: TestConformance/insert_select_one (0.00s)
    --- PASS: TestConformance/param_order (0.00s)
    --- PASS: TestConformance/null_scan (0.00s)
    --- PASS: TestConformance/no_rows (0.00s)
    --- PASS: TestConformance/multiple_rows (0.00s)
    --- PASS: TestConformance/each_order (0.00s)
    --- PASS: TestConformance/empty_in (0.00s)
    --- PASS: TestConformance/update (0.00s)
    --- PASS: TestConformance/delete (0.00s)
    --- PASS: TestConformance/join_group (0.00s)
    --- PASS: TestConformance/limit_offset (0.00s)
    --- PASS: TestConformance/returning (0.00s)
    --- PASS: TestConformance_FileBacked/insert_select_one (0.00s)
    --- PASS: TestConformance_FileBacked/param_order (0.00s)
    --- PASS: TestConformance_FileBacked/null_scan (0.00s)
    --- PASS: TestConformance_FileBacked/no_rows (0.00s)
    --- PASS: TestConformance_FileBacked/multiple_rows (0.00s)
    --- PASS: TestConformance_FileBacked/each_order (0.00s)
    --- PASS: TestConformance_FileBacked/empty_in (0.00s)
    --- PASS: TestConformance_FileBacked/update (0.00s)
    --- PASS: TestConformance_FileBacked/delete (0.00s)
    --- PASS: TestConformance_FileBacked/join_group (0.00s)
    --- PASS: TestConformance_FileBacked/limit_offset (0.00s)
    --- PASS: TestConformance_FileBacked/returning (0.00s)
ok  	github.com/ubgo/sqlb/contrib/driver-sqlite	0.379s
```

## License

[Apache License 2.0](./LICENSE), the same as the parent module. The driver it depends on, `modernc.org/sqlite`, carries its own license.
