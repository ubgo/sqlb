# `contrib/migrate` — ordering and running DDL

```go
import "github.com/ubgo/sqlb/contrib/migrate"
```

Orders several packages' tables so foreign keys resolve, from either the [`ddl`](../../docs/ddl/README.md) statements a library already writes or a [`schema`](../../docs/schema/README.md) declaration, and runs a `ddl.Script` the way its flags say.

It exists as much to prove that `schema` carries its weight as to be used. A schema is only worth describing if something can act on the description without knowing who wrote it, and this package acts on a `Set` assembled from packages that never imported each other.

## Contents

- [Deliberately narrow](#deliberately-narrow)
- [Installing from DDL](#installing-from-ddl)
- [Running a script](#running-a-script)
- [A ledger and a lock](#a-ledger-and-a-lock)
- [Use](#use)
- [Types](#types)
- [Indexes](#indexes)
- [Engines](#engines)
- [Why this is a separate module](#why-this-is-a-separate-module)

## Deliberately narrow

It orders the statements to create a database that does not yet exist, and it runs a script correctly, and it stops there.

It does **not diff against a live database**; `contrib/vet`'s `CompareTables` does that, and reports rather than repairs. Its **ledger is opt-in and records, nothing more**: `Ledger.Apply` runs the migrations it is handed, once each, under a lock, and `Ledger.Applied` reads back what it recorded. Which migrations exist, what they are called, what order they come in and when a deploy runs them stay the application's decision, per ADR-0003: there is no migration directory, no file loader, no command line and no down migration.

So this is not a migration tool in the golang-migrate or Atlas sense, and it does not want to become one.

## Installing from DDL

A library already writes its `CREATE TABLE` with the `ddl` builders, over the same `Col[T]` values its queries use. `Install` takes those statements from every package, orders them by the foreign keys each one's own `Describe` reports, and returns one `ddl.Script`:

```go
script, err := migrate.Install(
	migrate.Contribution{Owner: "ubgo/auth", Tables: []migrate.Table{authUsersTable}},
	migrate.Contribution{Owner: "the application", Tables: []migrate.Table{ordersTable}},
)
```

A `migrate.Table` is anything with `Build` and `Describe`, which `pg.CreateTable(...)`, `sqlite.CreateTable(...)` and `mysql.CreateTable(...)` all are once they have a column. Each `Contribution` names its owner. The order is `schema.Set.Sorted` over the descriptions, so it is deterministic and cycle-checked. A table two contributors both create is refused with `schema.ErrDuplicateTable`, naming both owners. A foreign key to a table no contribution creates is refused with `schema.ErrMissingTable`, because `Install` is for a whole database installed together. A nil entry in `Tables` is `migrate.ErrNilTable` naming its owner, not a panic, and a statement whose `Build` fails cannot be described, so it fails `Install` with `Build`'s error. A foreign key to a table in a schema, `auth.users`, resolves to exactly that table. The same contributions in any order give the same script.

This is the converged form: the table is declared once, as DDL, and the description that orders it is derived from that DDL rather than written beside it. `Plan`, below, is the older form that starts from a hand-written `schema.Set`.

## Running a script

`Execute` runs a `ddl.Script` on one `*sql.Conn`, honouring the three flags a `ddl.Statement` carries:

- an `OutsideTransaction` statement runs before any transaction opens, because SQLite's `PRAGMA foreign_keys` is a silent no-op inside one;
- a statement that is not `Transactional`, such as Postgres's `CREATE INDEX CONCURRENTLY` or SQLite's `VACUUM`, runs alone;
- a statement with a `SkipIf` query is skipped when that query returns a row, which is how MySQL writes `CREATE INDEX IF NOT EXISTS`;
- consecutive transactional statements share one transaction, which rolls back on any failure. That undoes the whole transaction on Postgres and SQLite, but not on MySQL: MySQL commits every DDL statement implicitly, so a failure there leaves the statements before it applied. Recover by running the script again, which works only when each statement can run twice, as a `CREATE TABLE` with `IfNotExists` can and a MySQL `CREATE INDEX` cannot;
- a `MustReturnNoRows` statement is queried, and a row fails the run with `ErrCheckFailed`. SQLite's `PRAGMA foreign_key_check` reports a broken reference by returning one.

It takes a connection rather than a pool by design. A setting like `PRAGMA foreign_keys` applies to one connection, and a pool may run the next statement on another. `ExecuteDB` pins a connection from a `*sql.DB` for the whole script. The tests run a SQLite table rebuild through `ExecuteDB` on a pool of four and show it succeeds, and that a rebuild leaving a dangling reference fails with `ErrCheckFailed`. `TestExecute_PinningIsWhatMakesAConnectionSettingHold` reproduces the failure an unpinned runner hits, deterministically: with one of two connections held busy, as a pool under load has, `PRAGMA foreign_keys = OFF` runs on one connection and the next insert on the other, which still enforces the key and refuses the row. The same script through `ExecuteDB` writes it.

## A ledger and a lock

A `Migration` is an `Owner`, a `Name` and a `ddl.Script`. `NewLedger(dialect, table, lock)` keeps a record of each applied migration in a table of its own, `DefaultLedgerTable` unless told another, and `Apply(ctx, conn, migrations...)` runs every one the ledger has not recorded, in the order given, and returns the names it applied. `ApplyDB` pins a connection from a pool.

```go
l, err := migrate.NewLedger(sqlb.PostgresDialect{}, "", migrate.EngineLock(sqlb.PostgresDialect{}, "auth_migrations"))
applied, err := l.ApplyDB(ctx, db,
	migrate.Migration{Owner: "ubgo/auth", Name: "001_users", Script: usersScript},
	migrate.Migration{Owner: "ubgo/auth", Name: "002_sessions", Script: sessionsScript},
)
```

- **Once each:** a migration the ledger has is skipped. Its identity is `Owner` and `Name`, so renaming one makes it new.
- **Edited after it ran:** each record holds `Checksum(script)`, SHA-256 over every statement and its flags, and a migration whose script no longer matches fails with `ErrChecksumMismatch`, and nothing after it runs. The database holds what the old script did, and pretending otherwise is how schemas drift.
- **Atomic where the engine allows:** the ledger row is the last statement of the migration's own script, so on Postgres and SQLite it commits in the same transaction as the change, and a failure leaves neither. MySQL commits DDL implicitly, so a failure there can leave the change without its row, and the next `Apply` runs it again: a MySQL migration should be safe to repeat.
- **One deploy at a time:** `Apply` takes its `Locker` first and holds it throughout, so a second process waits and then finds the work done. `EngineLock(dialect, key)` is `PostgresAdvisoryLock` (`pg_advisory_lock`, released by the server if the connection drops), `MySQLNamedLock` (`GET_LOCK`, waiting until the context's deadline or `DefaultLockWait`), or on SQLite, which has neither, `TableLock`: a row in a lock table, taken over after `StaleAfter` (`DefaultStaleAfter`) from a holder that crashed, and naming its process in `Holder`. While another connection holds SQLite's write lock the row's insert is refused as busy, and that is treated as the lock being held. A lock not got in time is `ErrLocked`.
- **Two deploys on one SQLite file:** they wait for each other rather than one failing with `SQLITE_BUSY`, so a caller needs no retry loop. Two refusals used to escape, both measured with two handles on one file under WAL and a busy timeout. A deferred transaction whose first statement reads, a `SkipIf` guard say, holds a read lock, and SQLite refuses its upgrade to the write lock at once, without consulting `busy_timeout`, while another connection writes; so the ledger begins its SQLite transactions `BEGIN IMMEDIATE`, taking the write lock up front where the busy timeout applies. And on a file not yet in WAL mode the connection `ApplyDB` opens runs `PRAGMA journal_mode=WAL`, which fails as busy whatever the timeout while another connection is in the file; `ApplyDB` and `AppliedDB` retry that open. `BEGIN` and `COMMIT` are retried on busy too, so a handle with no busy timeout still waits; all three retries end at the context's deadline, or `DefaultLockWait` without one, with `ErrLocked`. Busy is recognised without importing a driver: a `Code() int` method on the error, as modernc.org/sqlite has, or SQLite's own message, "database is locked".
- **Refusals:** a migration with no owner, name or script, or a name used twice, is `ErrBadMigration`; a dialect other than the three is `ErrNoLedgerDialect`.

### Reading the ledger

`Applied(ctx, conn)` returns every recorded migration as an `AppliedMigration`, its `Owner`, `Name`, `Checksum` and `AppliedAt` in UTC, oldest first, with rows recorded at the same instant in owner then name order. `AppliedDB` pins a connection from a pool. A database with no ledger table yet returns an empty list, not an error: the table's existence is read from the engine's catalogue (`to_regclass` on Postgres, `information_schema.tables` in the current database on MySQL, `sqlite_master` on SQLite), so any other failure is still returned rather than read as "nothing applied". It takes no lock, since a ledger row commits with its migration. A caller uses it to report a schema's version, or to refuse a database newer than its code without reading the ledger table's columns itself.

```go
rows, err := l.AppliedDB(ctx, db)
for _, r := range rows {
	fmt.Println(r.Owner, r.Name, r.AppliedAt)
}
```

`l.RefuseUnknown()` returns a copy of the ledger whose `Apply` does that refusal itself: when the ledger records a migration it was not given, under an owner it was given, `Apply` fails with `ErrUnknownMigration`, naming each such migration as `owner/name`, before running anything. After a rollback the ledger may hold `003_x` while the code knows only `001` and `002`; plain `Apply` skips what it recognises and says nothing, and the old code starts against a schema it was never written for. Owners not given are not judged, because one ledger table can serve several packages that each know only their own migrations. The default is unchanged: a ledger from `NewLedger` ignores rows it was not given.

```go
strict := l.RefuseUnknown()
_, err := strict.ApplyDB(ctx, db, myMigrations...)
if errors.Is(err, migrate.ErrUnknownMigration) {
	// the database was migrated by a newer release of this package
}
```

On all three engines the tests apply a history, apply it again to no effect, refuse an edited migration, and hold the lock on one connection while a second deploy starts: it waits, and once released finds the migration applied by the holder and applies nothing. They also show that on SQLite a failed migration leaves no ledger row, read `Applied` back before and after with every field, and that `RefuseUnknown` refuses the one unknown row by name, ignores an owner it was not given, and leaves plain `Apply` as it was. The SQLite concurrency tests hold SQLite's write lock on one connection while `ApplyDB` starts on another handle, with and without a busy timeout and on a file whose first open must convert it to WAL, and require it to wait and then succeed; a third runs two handles on ten fresh files at once and requires every migration recorded exactly once. Before the fix each of the deterministic ones failed with `database is locked`, and the fresh-file run failed about one round in five. The lock test is deterministic because a test where two deploys race only fails when their timing lines up, and the first version of this one passed with the lock removed.

## Use

`Plan` is the whole API for ordinary use: a `Set` in, ordered statements out.

```go
// One package's tables, under the name it is known by.
set, err := schema.New("ubgo/auth", auth.Tables()...)
if err != nil {
	return err
}

// Another's, which reference them. The two never imported each other.
if err := set.Add("the application", billing.Tables()...); err != nil {
	return err
}

stmts, err := migrate.Plan(postgres.Dialect{}, *set)
if err != nil {
	return err
}
for _, s := range stmts {
	if _, err := db.ExecContext(ctx, s); err != nil {
		return err
	}
}
```

Both `New` and `Add` take an owner name, and it is used only in errors. That is worth a parameter: when two modules both declare `users`, the question is always which two, and a `Set` that did not record it could only report the name. `Plan` takes the `Set` by value, so a plan cannot be changed by a later `Add`.

The order is topological over foreign keys and **deterministic**: two runs over the same `Set` produce the same slice, so the output can be committed to a repository and diffed. A cycle is an error naming the tables in it; a self-reference is not a cycle, which is the case that catches a naive implementation.

`CreateTable` renders one table if you want to assemble the plan yourself.

## Types

`SQLType` maps a `schema.Type` to one engine's spelling:

```go
migrate.SQLType(postgres.Dialect{}, col)  // "TEXT", "BIGINT", "TIMESTAMPTZ", ...
```

An engine that has no spelling for a type returns `ErrNoTypeMapping` rather than guessing:

```go
if _, err := migrate.SQLType(d, col); errors.Is(err, migrate.ErrNoTypeMapping) {
	// this engine cannot store that type; say so rather than emitting something plausible
}
```

That is the same fail-closed rule the engine packages follow. A DDL generator that silently substitutes a nearby type produces a table that accepts your writes and loses precision, which is the failure you find in production rather than at the gate.

## Indexes

`CreateIndexes` returns the `CREATE INDEX` statements for one table, separately from its `CREATE TABLE`:

```go
stmts := migrate.CreateIndexes(postgres.Dialect{}, t)
```

They are separate because they run separately in practice — an index on a large table is often built concurrently and long after the table exists — and because `Plan` needs every table to exist before any index that might reference one. It returns no error: an index declaration that cannot be rendered is a schema problem caught when the table is.

## Engines

```go
migrate.Engines()  // the engine names this package can render DDL for
```

Worth calling rather than assuming. This package renders DDL for fewer engines than `sqlb` renders queries for, and the list is the honest answer to which.

The table behind both `SQLType` and `Engines` is `schema`'s — `schema.EngineType` and `schema.EngineNames` — not this package's. It moved there so that `contrib/vet`, which checks a `Col[T]` declaration against a live catalogue, reads the same spelling this renderer writes; two copies of what a `bigint` is would have drifted.

## Why this is a separate module

`contrib` is for what does not belong in the core, and a third-party dependency is only the most common reason. This package has none: it is here because it targets specific engines and emits DDL, and the core stays free of both — free of DDL for the same reason it stays free of drivers.

`schema` describes and never does. This is the "does", and keeping it outside is what makes that claim checkable rather than a matter of taste.
