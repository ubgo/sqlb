# Changelog

All notable changes to `github.com/ubgo/sqlb` are documented here. The format follows Keep a Changelog, and versioning is SemVer with the pre-GA convention that `v0.x` may break.

## [Unreleased]

### Changed

- **SQLite spells the portable timestamp and date types `TIMESTAMP` and `DATE`, not `TEXT`.** The storage is text either way, but the modernc SQLite driver decodes a column declared `TIMESTAMP` or `DATE` into a `time.Time` and returns a `TEXT` one as a string, so a `Col[time.Time]` created by `ddl.Column` (or by `contrib/migrate`'s `CreateTable` from a `schema.TypeTimestamp` column) could be written and never scanned back. `schema.EngineType("sqlite", …)`, the `ddl` builders and `contrib/migrate` all read the one table. Upgrading: a SQLite database created by an earlier release's DDL still has `TEXT` columns, and `sqlb vet`'s `CompareTables`/`CheckTables` now report each as a type difference; declare such a column with `ddl.RawType[string]("TEXT")` and read it as a string, or move it with a table rebuild. A `STRICT` table accepts neither new spelling and needs `RawType("TEXT")` too.

### Added

- **`contrib/migrate`: a library with a migration history of its own can move onto the ledger, and can write the ledger's statements into an offline script.** A library that kept its own ledger table could not take its users' databases over, because the only way in was `Apply`, which re-ran migrations that had already run (an `ADD COLUMN` fails the second time), and it could not offer an offline script because the ledger's own DDL and row were unexported. `Ledger.Adopt` and `AdoptDB` record migrations as applied without running them, with the checksums `Apply` computes, in one transaction under the ledger's lock, idempotently, refusing an edited migration with `ErrChecksumMismatch` and honouring `RefuseUnknown`; a `migrate.Ran` callback says which of the known migrations ran and when, so the usual flow is to read the old ledger, adopt what it lists, then `Apply` the rest. `Ledger.Script`, `TableScript` and `RecordStatement` return the ledger's `CREATE TABLE`, each migration's statements and the `INSERT` that records it, built by the code `Apply` runs, so a script run by hand leaves a database on which `Apply` applies nothing. `Ledger.WithClock` injects the time recorded in `applied_at`, for tests and reproducible scripts; the default stays `time.Now`. `Apply`'s behaviour and every existing name are unchanged. `contrib/migrate` now requires the root module at `v0.1.3`, the first tagged root its own tests pass against (they expect SQLite's `TIMESTAMP` spelling). The guide's new section is [Moving an existing migration history onto the ledger](docs/declare-once.md#moving-an-existing-migration-history-onto-the-ledger).

- **A table's description can say how an index sorts and whether it is partial, and `sqlb vet` checks both.** A library that created `CREATE INDEX … WHERE record_path IS NULL` or `(account_id, started_at DESC)` with the typed builder could not list that index in its description: the live check reported a listed partial index as missing and compared a descending index equal to an ascending one. `schema.Index` gains `Orders` (a `schema.KeyOrder` per key: `Direction` `Ascending`/`Descending`, `NullsOrder` `NullsFirst`/`NullsLast`) and `Where`; `Describe()` on `pg.CreateIndex` and `sqlite.CreateIndex` returns the index's `schema.Index` with both filled from what the builder was given (an expression key is `ddl.ErrNotDescribable`), and `sqlite.UniqueOn` with a direction describes it. `contrib/vet`'s readers now report every key's direction (Postgres with NULLS placement, SQLite, MySQL) and Postgres and SQLite partial indexes with their predicate, and `CompareKeys`/`CheckTables` report a difference as a typed finding, `Finding.Kind` `vet.FindingIndexOrder` or `vet.FindingIndexPredicate`. A predicate is compared by a normal form and, on Postgres, by the engine's own canonical text of both sides (a temporary view in a rolled-back transaction); a pair that cannot be compared is the warning `vet.FindingIndexPredicateNotCompared`, never a pass. The rule and its limits are in the `contrib/vet` README. The widgets example gains a partial owner index in migration 0003, described by its own `CREATE INDEX`.

  Compatibility: an index described without `Orders` is not judged on order and one without `Where` is judged as before. Two behaviours change: a partial index in the database is no longer skipped, so a declared full index whose only database match over the same columns is partial is now reported as "partial in the database" instead of "not in the database", and an undeclared partial unique index is now the same warning as any undeclared unique index. `Finding` and `schema.Index` gain fields, so an unkeyed struct literal of either no longer compiles. Code that hands its own `IndexElement` to `pg.CreateIndex` or `sqlite.CreateIndex` builds as before, and its `Describe` is `ddl.ErrNotDescribable`.

- `examples/prefixed/widgets` and [`docs/declare-once.md`](docs/declare-once.md): a library whose tables are written once under a run-time prefix, with its declaration, hand-written numbered migrations on the `contrib/migrate` ledger, `vet.CheckTables` against the declaration, and `ON CONFLICT`, `RETURNING` and `FOR UPDATE` queries, executed on SQLite and Postgres 17 in `contrib/vet/enginetest`, including adopting a database an earlier release created with `CREATE TABLE IF NOT EXISTS` strings. The page ends with the recipe for converting such a module.

## [contrib modules, 2026-11-10]

`contrib/migrate/v0.1.1`, `contrib/structbind/v0.1.1`, `contrib/driver-sqlite/v0.1.1`, `contrib/driver-pgx/v0.1.1`, `contrib/driver-mysql/v0.1.1`, `contrib/vet/v0.1.2` and the new `contrib/vet/cmd/sqlb` module. No change to any library's Go code ([#4](https://github.com/ubgo/sqlb/issues/4)).

### Changed

- A released module now requires only what its library code imports. The tests that open a database moved to a never-released `enginetest` module beside each module (`internal: true` in its `.volt.yml`):
  - `contrib/migrate`: its tests imported the pgx, MySQL and SQLite drivers while its `go.mod` listed none of them, so `go mod tidy` in any module importing migrate added all three drivers to that module's `go.mod`. migrate now requires only the core.
  - `contrib/structbind` no longer requires `modernc.org/sqlite`; it requires only the core.
  - `contrib/driver-sqlite`, `contrib/driver-pgx` and `contrib/driver-mysql` no longer require the core, which only their conformance tests used; each requires only its database driver.
  - `contrib/vet` no longer requires any database driver. The `sqlb` command, which opens a database for `sqlb vet --dsn`, is its own module, `github.com/ubgo/sqlb/contrib/vet/cmd/sqlb`, and carries the three drivers; `go install github.com/ubgo/sqlb/contrib/vet/cmd/sqlb@latest` installs it as before.
- `scripts/covmerge.py` run inside an `enginetest` module measures the module it sits in, and the CI coverage floor for `contrib/driver-sqlite` runs in `contrib/driver-sqlite/enginetest`.

## [0.1.1] - 2026-10-11

### Added

- `install.sh` and `install.ps1` at the repository root, generated by volt, install the prebuilt `sqlb` and `sqlbvet` binaries for macOS, Linux and Windows: `curl -fsSL https://raw.githubusercontent.com/ubgo/sqlb/main/install.sh | sh`. The `sqlb` CLI's release archives carry `sqlbvet` beside it from `sqlb/v0.1.1`.

### Changed

- Releases are cut with volt: one tag stream per module and CLI, a manual release workflow that republishes an existing tag with `volt release --from-tag`, and a `ci` workflow running `volt ci`. The hand-tagging `task release:*` tasks are gone. The core and `contrib/vet` are released as `v0.1.1` and `contrib/vet/v0.1.1` with no change to their Go code; the `sqlb` CLI as `sqlb/v0.1.1`.

## [0.1.0] - 2026-10-11

### Documentation

- `docs/architecture.md`: the layer plan. One module per layer, the rule that every layer compiles without the ones above it, why one module per file is impossible given the current import cycles, the committed workspace, the zero-cost facade through generic type aliases, and the order the work has to happen in. Nothing in it is built; it is the target shape.

### Added

- The core builder: tables with typed columns, `Expr[T]`, `Param`, `Literal`, `Nullable` and `AssertNotNull`, the predicate set, `Bind` and `Bindings`.
- `Select` with joins, CTEs including recursive ones, set operations, window functions with frames, aggregates, subqueries, `GROUP BY` and `HAVING`, ordering, `LIMIT` and `OFFSET`.
- `Insert`, `Update` and `Delete`, with `RETURNING` where the dialect has it.
- Dialects for Postgres, SQLite and ClickHouse, all three in the core beside each other, capability interfaces for `RETURNING`, insert aliasing and named parameters, and `ErrUnsupportedDialect` so an engine construct fails at `Build`.
- `Clause` slots, `Option` functions and the typed door, the four mechanisms a library uses to let its callers extend a query it owns. `SlotAfterInsert` sits between the INSERT keyword and INTO, which is where SQLite spells `OR REPLACE` and no other engine spells anything.
- `pg`: JSONB and SQL/JSON, arrays, ranges and multiranges, text and full-text search, `DISTINCT ON`, locking clauses, `ON CONFLICT`, `MERGE`, `LATERAL`, recursive search and cycle clauses, session and catalogue functions.
- `pg` extension sub-packages, one per `CREATE EXTENSION`: `vector`, `trgm`, `fuzzy`, `ltree`, `crypto`, `earth`, `unaccent`, and `postgis` as the template. Each has a README and a guard test, and each is built from the guarded constructors `pg` exports, so adding an extension never edits `pg`.
- `pg/vector`: the full pgvector surface, four vector types, six distance operators and their function forms, the utility functions, element-wise aggregates and arithmetic, casts, typed parameters and the query-time settings, with the pgvector README covered query by query.
- `sqlite`: what only SQLite has. `INSERT OR REPLACE` and its four siblings, the `ON CONFLICT` upsert with `Excluded`, the JSON1 extension including `json_each` and `json_tree` as sources, FTS5 search with `rank`, `bm25`, `highlight` and `snippet`, the time-modifier family, `rowid` and the session counters, and the `INDEXED BY` planner hints. Every one of its 70 helpers is executed against a real database by `contrib/driver-sqlite`, which makes it the only engine package here whose SQL is proven rather than asserted.
- `ch`: `PREWHERE`, `FINAL`, `SAMPLE`, `ARRAY JOIN`, `LIMIT BY`, statement settings, and ClickHouse aggregate and time functions.
- Engine-package building blocks in the core: `EngineGuard`, `EngineExpr`, `EnginePred`, `EngineBinOp`, `EngineBinPred`, `EngineFn`, `EngineLit`, `EngineRaw`, `EngineClause`, `EngineSource`, `EngineList` and `EngineQuoteLit`. The guard rule that makes an engine construct fail closed under another dialect now lives in one place instead of being copied into `pg`, `sqlite` and `ch`, and a community package in another repository builds a dialect and its constructs with the same guarantees.
- `ErrUnknownSlot`. A clause aimed at a slot a statement does not render is an error naming the slot, where it used to be dropped silently. `Slot.String` names every slot, and a test walks `SlotValues` asserting each is either rendered by some statement or rejected.
- `Join.Clauses`, for a per-table hint such as SQLite's `INDEXED BY`, MySQL's `USE INDEX` or T-SQL's `WITH (NOLOCK)`. It is the one position a slot cannot name, because there is one per join rather than one per statement; putting such a hint in a statement slot bound it to the driving table instead.
- The slot set is complete rather than minimal: a slot at every boundary the renderer emits, so every position in a statement is reachable from a package in another repository. `SlotAfterColumns`, `SlotAfterWhere`, `SlotAfterHaving` and `SlotAfterVerb` join the set; `SlotAfterTarget` now covers INSERT and UPDATE as well as DELETE; and UPDATE and DELETE render `SlotTail`, which they previously did not. `SlotAfterGroupBy` is MySQL's `WITH ROLLUP` and `SlotBeforeOrder` is `QUALIFY`, which DuckDB, Snowflake and BigQuery use to filter on a window result.
- `ErrQueryInUse` and `NewQuery`. A Query holds pointers into the caller's variables, so sharing one across goroutines was a data race that interleaved rows silently; `One` and `Each` now refuse an overlapping execution instead. The guard belongs to the builder, so hoisting a builder is caught as well as hoisting a query. `NewQuery` lets an engine package rendering its own statement kind, such as pg's MERGE, get the same check instead of assembling a bare struct literal.
- `sqlbtest`: the shared conformance suite every driver integration runs against a real database.
- `contrib/migrate`: `Install` and `Plan` order several packages' tables by foreign key, `Execute` runs a `ddl.Script` honouring its flags, and an opt-in `Ledger` applies checksummed migrations once each under an `EngineLock`. `Ledger.Applied` and `AppliedDB` read the ledger back oldest first, an empty list when its table is missing; `RefuseUnknown` makes `Apply` fail with `ErrUnknownMigration` on a recorded migration it was not given; and on SQLite the ledger begins its transactions `BEGIN IMMEDIATE` and retries the connection open, `BEGIN` and `COMMIT` on busy, so two deploys on one file wait for each other instead of failing with `SQLITE_BUSY`, ending in `ErrLocked` at the context's deadline or `DefaultLockWait`.
- `contrib/driver-sqlite`: the first contrib module, opening SQLite with the pragmas a library-owned schema needs, and running the conformance suite.
- `examples/`: the compile-checked catalogues, and the negative build that fails if deliberately wrong code ever compiles.

[0.1.1]: https://github.com/ubgo/sqlb/releases/tag/v0.1.1

[0.1.0]: https://github.com/ubgo/sqlb/releases/tag/v0.1.0
