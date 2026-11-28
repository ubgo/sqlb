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

It does **not diff against a live database**; `contrib/vet`'s `CompareTables` does that, and reports rather than repairs. Its **ledger is opt-in and records, nothing more**: `Ledger.Apply` runs the migrations it is handed, once each, under a lock, `Ledger.Adopt` records ones that already ran without running them (`Ledger.AdoptPrepared` when deciding which ran means reading the database), `Ledger.Script` writes what `Apply` would run as statements, and `Ledger.Applied` reads back what it recorded. Which migrations exist, what they are called, what order they come in and when a deploy runs them stay the application's decision, per ADR-0003: there is no migration directory, no file loader, no command line and no down migration.

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

This is the converged form: the table is declared once, as DDL, and the description that orders it is derived from that DDL rather than written beside it. `Plan`, below, is the older form that starts from a hand-written `schema.Set`. [Declare a table once](../../docs/declare-once.md) walks a whole library through it, with a run-time table prefix, numbered migrations on the ledger and adopting databases that hand-written `CREATE TABLE IF NOT EXISTS` strings created.

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

### Adopting a history that already ran

A library that kept its own ledger table, or a database a person migrated by hand, already holds the schema the first migrations create. `Apply` would run them again, and an `ALTER TABLE ... ADD COLUMN` cannot be written to repeat safely on SQLite or MySQL, so it fails. `Adopt(ctx, conn, ran, migrations...)` records migrations as applied without running them, and `AdoptDB` pins a connection from a pool. The flow is: read the old ledger, adopt what it lists, then `Apply` the rest.

```go
// old maps owner/name to when the old ledger says the migration ran.
ran := func(m migrate.Migration) (time.Time, bool) {
	at, ok := old[m.Owner+"/"+m.Name]
	return at, ok
}
adopted, err := l.AdoptDB(ctx, db, ran, all...) // records what ran, runs nothing
applied, err := l.ApplyDB(ctx, db, all...)      // runs only what is new
```

- **Pass every migration the code knows,** not only the ones that ran. `ran` is a `migrate.Ran`: it says which of them ran and when. A zero time records the migration at the ledger's clock, and a nil `ran` adopts them all. With the times kept, `Applied` lists the adopted history in its original order.
- **Same rows as `Apply`:** each row holds `Checksum(m.Script)`, so a later `Apply` recognises the migration and skips it. If the old ledger had its own checksums, check them against the old scripts before adopting; `Adopt` records the checksum of the script it is given.
- **Refuses what `Apply` refuses, before writing anything:** a bad migration is `ErrBadMigration`; a migration already recorded with another checksum is `ErrChecksumMismatch`, whether `ran` selects it or not; from a `RefuseUnknown` ledger, a recorded migration under a given owner that the list lacks is `ErrUnknownMigration`. A migration already recorded with the same checksum is left alone, so adopting twice records nothing the second time.
- **One transaction, under the ledger's lock:** the rows are plain `INSERT`s, so they commit together or not at all on every engine, MySQL included. `Adopt` takes the same lock as `Apply`, so two processes adopting at once, or one adopting while another applies, serialize, and the second records or runs only what is still missing.

#### Reading the old ledger under the lock

`ran` takes no connection and returns no error, so a caller that must read its old ledger table to answer it would read before `Adopt`, on a connection of its own, outside the lock. On SQLite that read is not safe at start-up: when two processes open a fresh file with `journal_mode(WAL)` in the DSN, the one converting the file to WAL makes the other's first statement fail with `database is locked`, whatever the busy timeout says. The ledger waits that out for the connection it pins; a read on any other connection gets no such wait (measured: 44 of 200 rounds with two processes failed). `AdoptPrepared` and `AdoptPreparedDB` take a `migrate.PrepareRan` instead, `func(ctx, conn) (migrate.Ran, error)`, which is called once with the ledger's own connection, after the lock is taken and the ledger is read, and before any migration is judged:

```go
prepare := func(ctx context.Context, conn *sql.Conn) (migrate.Ran, error) {
	old, err := readOldLedger(ctx, conn) // the library's own read, on conn
	if err != nil {
		return nil, err // nothing is recorded; the caller gets this error back, wrapped
	}
	return func(m migrate.Migration) (time.Time, bool) {
		at, ok := old[m.Owner+"/"+m.Name]
		return at, ok
	}, nil
}
adopted, err := l.AdoptPreparedDB(ctx, db, prepare, all...)
```

- **Read through `conn`, not the pool.** On SQLite it is the connection whose open the ledger waited out, holding the lock row; on Postgres and MySQL the lock is a session lock on it. `prepare` runs outside any transaction and should only read.
- **An error stops the adoption** before anything is recorded, the lock is released, and the error comes back wrapped, so `errors.Is` and `errors.As` find the caller's own error: a checksum mismatch the caller found in its old ledger, say.
- **It runs on every call,** even when everything is already recorded, so the caller's own checks of its old ledger run on every start. A nil `prepare`, or a nil `Ran` from it, adopts every migration, as a nil `ran` does. Everything else is `Adopt`'s behaviour; `Adopt(ctx, conn, ran, ...)` is `AdoptPrepared` with a `prepare` that returns `ran`.

### An offline script

Some teams apply schema changes by hand or with their own tooling. `l.Script(migrations...)` returns what `Apply` would run on a database with an empty ledger, as a `ddl.Script` and not run: `l.TableScript()`, the ledger's `CREATE TABLE IF NOT EXISTS`, then each migration's statements followed by `l.RecordStatement(m)`, the `INSERT` of its ledger row with every value a literal. They are built by the same code `Apply` uses, so after the script has run, `Apply` with the same migrations applies nothing and reports no mismatch. `TableScript` and `RecordStatement` are there for a caller that assembles its own script, a record-only script for adoption say.

```go
clocked := l.WithClock(func() time.Time { return releaseTime })
script, err := clocked.Script(pending...)
for _, st := range script {
	fmt.Fprintln(w, st.SQL+";")
}
```

`Script` reads no database: pass only the migrations the target lacks. It takes no lock and `RefuseUnknown` does not apply to it, so whoever runs the script keeps it from running beside a deploy. Each statement keeps its flags. As plain text they are lost: a statement that must run outside a transaction or is a check needs a runner that honours it, as `Execute` does, and a MySQL `CREATE INDEX` guarded by its `SkipIf` query runs unguarded.

### The clock

`l.WithClock(now)` returns a copy of the ledger that reads `applied_at` from `now` instead of `time.Now`, for tests that check the recorded time and for a script whose text must be the same on every run. `WithClock(nil)` restores `time.Now`. A `Ledger` can still be compared with `==`.

### Tests

On all three engines the tests apply a history, apply it again to no effect, refuse an edited migration, and hold the lock on one connection while a second deploy starts: it waits, and once released finds the migration applied by the holder and applies nothing. They also show that on SQLite a failed migration leaves no ledger row, read `Applied` back before and after with every field, and that `RefuseUnknown` refuses the one unknown row by name, ignores an owner it was not given, and leaves plain `Apply` as it was. The SQLite concurrency tests hold SQLite's write lock on one connection while `ApplyDB` starts on another handle, with and without a busy timeout and on a file whose first open must convert it to WAL, and require it to wait and then succeed; a third runs two handles on ten fresh files at once and requires every migration recorded exactly once. Before the fix each of the deterministic ones failed with `database is locked`, and the fresh-file run failed about one round in five. The lock test is deterministic because a test where two deploys race only fails when their timing lines up, and the first version of this one passed with the lock removed.

The adoption suite, written for all three engines, starts from a database an earlier ledger migrated (a table and two `ADD COLUMN`s, one from a second owner), shows `Apply` alone failing, then adopts what the old ledger lists with its times, applies only the new migration, adopts twice to no effect, and refuses an edited migration and an unknown row with nothing recorded. It holds the lock on one connection while a second process adopts, and again while a second process applies, so each race is deterministic; it also runs two adopters at once on one database, and two processes on ten fresh SQLite files, and requires every row recorded exactly once. The prepared-adoption suite, on all three engines, reads the old ledger through the ledger's connection, returns a caller's error and a refused read with nothing recorded and the caller's error findable with `errors.Is`, then adopts with the old times and applies only the new migration. On SQLite, two processes start together on 200 fresh files opened with `journal_mode(WAL)` in the DSN, each reading its old ledger inside `AdoptPreparedDB` and then applying, and every call must succeed; the same read on the caller's own handle before `Adopt` failed in 44 of 200 rounds. The script suite runs `Script`'s statements one `Exec` at a time, as a person would, and requires `Apply` afterwards to apply nothing and to read back the injected time.

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
