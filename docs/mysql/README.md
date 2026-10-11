# `mysql` — MySQL statements and constructs

```go
import "github.com/ubgo/sqlb/mysql"
```

Everything MySQL in one import: the statements start here (`mysql.Select`, `mysql.Insert`, `mysql.Update`, `mysql.Delete`, and `mysql.CreateTable`, `AlterTable`, `DropTable`), and the constructs MySQL spells differently from the other engines are here too.

Every construct here is **executed**, not only rendered: `contrib/driver-mysql` runs each one against a real MySQL 8.4, gated on `SQLB_MYSQL_DSN`. Several were wrong until they ran, which is the reason for the rule.

## Contents

- [Setup](#setup)
- [Starting a statement](#starting-a-statement)
- [Upserts and INSERT IGNORE](#upserts-and-insert-ignore)
- [UPDATE and DELETE with LIMIT](#update-and-delete-with-limit)
- [Locking reads and WITH ROLLUP](#locking-reads-and-with-rollup)
- [Functions](#functions)
- [Tables](#tables)
- [Changing a table](#changing-a-table)
- [Repeatable indexes and multi-statement triggers](#repeatable-indexes-and-multi-statement-triggers)
- [Partitioning](#partitioning)
- [Indexes, views, triggers](#indexes-views-triggers)
- [Closed sets are checked, not assumed](#closed-sets-are-checked-not-assumed)
- [The other forms of CREATE TABLE](#the-other-forms-of-create-table)
- [Events and stored routines](#events-and-stored-routines)
- [What MySQL does not have](#what-mysql-does-not-have)
- [Reference](#reference)

## Setup

Every snippet on this page uses these declarations.

```go
import (
	"encoding/json"

	"github.com/ubgo/sqlb/expr"
)

var (
	Items     = expr.Table{Name: "items", Alias: "i"}
	ItemID    = expr.Col[int64](Items, "id")
	ItemSKU   = expr.Col[string](Items, "sku")
	ItemHits  = expr.Col[int64](Items, "hits")
	ItemDoc   = expr.Col[json.RawMessage](Items, "doc")
	ItemBody  = expr.Col[string](Items, "body")
)
```

## Starting a statement

Each constructor is exactly the long form with the dialect written out, pinned by `TestEntry_IsTheLongFormExactly`: `mysql.Select(items...)` is `sqlb.NewSelect(mysql.Dialect{}).Exprs(items...)`. The types are aliases, `SelectQuery`, `InsertQuery`, `InsertReady`, `UpdateQuery`, `FilteredUpdate`, `DeleteQuery`, `FilteredDelete`, and for DDL `CreateTableQuery`, `CreateTableReady`, `AlterTableQuery`, `AlterTableReady` and `DropTableQuery`. There are no `InsertReturning` and siblings, because MySQL has no `RETURNING`, so `sqlb.InsertReturning` on a MySQL statement does not compile.

The clause types are `SelectClause`, `InsertClause`, `UpdateClause` and `DeleteClause`, each an alias for `sqlb.Clause[mysql.Dialect, K]`. `Guard`, `Expr`, `Func` and `ClauseIn` are the guarded constructors a helper of your own is built on, and `Is` reports whether a dialect is MySQL's.

## Upserts and INSERT IGNORE

```go
mysql.Insert(Items).Row(sqlb.Set(ItemID, 1), sqlb.Set(ItemHits, 5)).
	Clause(mysql.OnDuplicateKeyUpdate(sqlb.SetExpr(ItemHits, sqlb.Add(ItemHits, mysql.New(ItemHits)))))
// INSERT INTO `items` (`id`, `hits`) VALUES (?, ?) AS `new` ON DUPLICATE KEY UPDATE `hits` = (`items`.`hits` + `new`.`hits`)
```

`OnDuplicateKeyUpdate` names the inserted row with MySQL 8's row alias, `AS new`, and `mysql.New(col)` refers to it; `VALUES(col)` is deprecated in that position. The existing row's columns are written qualified by the table's name, because the `INSERT` takes no alias, so an alias-qualified column is unknown, and the row alias exposes columns of the same names, so a bare one is ambiguous. Both were errors the first run found, and `Args.TableNamed` is the mechanism that fixed them. It fires on any unique key, where Postgres's `ON CONFLICT` targets one.

`mysql.InsertIgnore()` skips a row that would violate a unique key, and also downgrades other errors, a value too long for its column among them, to warnings, which is why the upsert is the better answer when a duplicate is the only expected conflict.

## UPDATE and DELETE with LIMIT

`mysql.UpdateLimit(n, order...)` and `mysql.DeleteLimit(n, order...)` cap a single-table write, in the order given, which is how a large backfill or purge runs in batches:

```go
mysql.Delete(Items).Where(sqlb.Less(ItemHits, 1)).Clause(mysql.DeleteLimit(1000, sqlb.OrderBy(ItemID, sqlb.SortAsc)))
```

## Locking reads and WITH ROLLUP

`mysql.ForUpdate(opts...)`, `ForUpdateOf(t, opts...)` and `ForShare(opts...)` take `LockOption`s, `NoWait` or `SkipLocked`; `SkipLocked` is how a job queue hands each worker a different row. `mysql.WithRollup()` adds the super-aggregate rows to a `GROUP BY`, as a suffix, where Postgres writes `ROLLUP(...)` inside it.

## Functions

`GroupConcat(e, sep)` joins a group's values; its separator is a literal, since MySQL takes no parameter there, and the result truncates silently at `group_concat_max_len`. `IfNull`, `CharLength` (characters; MySQL's `length()` counts bytes), `RegexpLike`, `UUID` and `UTCNow` (`utc_timestamp(6)`, for a `DATETIME(6)` kept in UTC) are the rest of the scalar set. JSON is `JSONExtract`, `JSONValue` (the `->>` operator, written as the functions so the path may be bound) and `JSONContains`. Full-text search is `Match(query, mode, cols...)` with a `MatchMode` of `NaturalLanguage` or `Boolean`, over columns a FULLTEXT index covers exactly.

## Tables

```go
mysql.CreateTable(Users).IfNotExists().
	Column(ddl.Column(UserID), ddl.Column(UserEmail)).
	Clause(mysql.AutoIncrement(UserID), mysql.PrimaryKey(UserID), mysql.ColumnCollate(UserEmail, "utf8mb4_bin"),
		mysql.Unique(mysql.TableConstraint{Table: Users, Name: "users_email"}, UserEmail), mysql.InnoDB(), mysql.DefaultCharset("utf8mb4"))
```

- **Keys:** `PrimaryKey`, `Unique(c, cols...)` with a declared `TableConstraint`, and the inline `TableIndex(ix, cols...)` and `TableFulltext(ix, cols...)` over an `Index` value, which MySQL allows inside `CREATE TABLE`. Each is a `KeyClause`. Text is `VARCHAR(255)` in the type table, because a `TEXT` column cannot be a key without a prefix length.
- **Foreign keys:** `References(local, ref)`, `ForeignKey(c, pairs...)` and `RefPair`, with `OnDelete` and `OnUpdate` taking `RefCascade`, `RefRestrict`, `RefSetNull` or `RefNoAction`. There is no `SET DEFAULT`, which InnoDB rejects, and no deferrable form, which MySQL lacks. `ForeignKeyClause` is the type.
- **Column clauses:** `AutoIncrement` on an integer column, `ColumnCollate(col, name)` (the default `utf8mb4_0900_ai_ci` is case-insensitive, so a UNIQUE on it refuses "Ada" beside "ada"), `ColumnComment`, `GeneratedVirtual` and `GeneratedStored`.
- **Checks:** `ddl.Check` and `ddl.OneOf` in `CREATE TABLE`, and `mysql.Check`, a `CheckClause`, for `ALTER TABLE ... ADD`; MySQL enforces them since 8.0.16.
- **Table options:** `InnoDB`, which is `TableEngine(EngineInnoDB)` spelled for the one engine a library's table wants, `TableEngine` for `EngineMemory` and `EngineMyISAM` besides, `RowFormat` (`RowFormatDynamic`, `RowFormatCompressed`, `RowFormatCompact`, `RowFormatRedundant`, `RowFormatDefault`), `DefaultCharset`, `TableCollate` and `TableComment`. Each has an `ALTER TABLE` form under [changing a table](#changing-a-table); a value outside the constants fails `Build` with `ErrUnknownKeyword`, because MySQL takes an `ENGINE` it does not know as a warning and the server default.

## Changing a table

`ModifyColumn(spec, with...)` redefines a column in place: MySQL has no `ALTER COLUMN ... TYPE`, so a type change restates the whole definition and a property left out is dropped. `SetDefault` and `DropDefault` change a default; `AddConstraint` takes a `mysql.Constraint` (a unique key, a foreign key or a check); `DropForeignKey`, `DropCheck`, `DropKey` and `RenameKey` take the constraint's declared `TableConstraint` and refuse one of another table, and `RenameIndex` renames a plain `Index`; `RenameTo` renames the table. The universal `ddl.AddColumn`, `DropColumn` and `RenameColumn` work too, and MySQL combines actions into one statement. One thing it refuses, measured: an `ALTER TABLE` cannot refer to a column the same statement adds, so adding a column and then setting its default is two statements.

Every table option has an `ALTER` form, which is what lets a table declared by this module be altered by it rather than by a string: `SetEngine`, `SetRowFormat`, `SetTableComment`, `SetAutoIncrement(n)`, `SetDefaultCharset`, `SetTableCollate` and `ConvertToCharset`. The last is the one that rewrites existing rows; `SetDefaultCharset` changes what new columns get and leaves the old ones where they were, which is the difference between fixing a table and appearing to. `SetAutoIncrement` reserves a range rather than rewinding one, since MySQL will not lower the counter below the current maximum key, and `SetAutoIncrement(0)` fails with `ErrAutoIncrementZero` because MySQL reads a zero as "reset to the default".

`Algorithm(AlgorithmInstant)` and `Lock(LockNone)` say how the change must be performed and fail when it cannot be, which is the point: without them MySQL silently falls back to copying the table and taking the lock. `AlgorithmInplace`, `AlgorithmCopy`, `AlgorithmDefault`, `LockShared`, `LockExclusive` and `LockDefault` are the rest; write them last among the actions, as MySQL's grammar does. `ForceRebuild()` rebuilds with no other change, `SetCheckEnforced` and `SetCheckNotEnforced` turn a declared `CHECK` off for a bulk load and back on (re-enforcing validates every existing row, so it fails while one violates it), `SetIndexInvisible` and `SetIndexVisible` hide an index from the optimizer while keeping it maintained, which is how dropping one is tested reversibly, `WithValidation` and `WithoutValidation` cover a change to a generated column an index covers, `EnableKeys` and `DisableKeys` are MyISAM's bulk-load pair, and `DiscardTablespace` and `ImportTablespace` move a table's data file between servers.

`AddColumn(spec, with...)`, `ChangeColumn(from, spec, with...)` and `ModifyColumnAt(spec, placement, with...)` are the column actions that can say where the column goes, with `First()` or `After(col)`; `ddl.AddColumn` is the same action where the position does not matter. `CHANGE COLUMN` is the only way MySQL renames and redefines at once. `SetColumnVisible` and `SetColumnInvisible` change a column's visibility without restating its definition, and `ColumnVisible`/`ColumnInvisible` are the `CREATE TABLE` form; an invisible column is absent from `SELECT *` and present when named, which is not a privilege. `DropPrimaryKey()` is its own action because MySQL names every primary key `PRIMARY` and so it never has a declared `TableConstraint`; `DropConstraint(c)` drops one whatever its kind. `Check(...).NotEnforced()` adds a `CHECK` turned off, which is how one is declared over a table whose rows do not satisfy it yet.

`RawAlter(sql)` is the one documented escape hatch, for MySQL's forty-odd storage-tuning table options — `KEY_BLOCK_SIZE`, `COMPRESSION`, `ENCRYPTION`, `STATS_PERSISTENT`, `DATA DIRECTORY` and the rest — which are an operator's tuning rather than a schema a library ships. Nothing validates the string and it names no column, so `ALTER TABLE` cannot check that it belongs to the table; a value from outside the program must never reach it.

## Repeatable indexes and multi-statement triggers

MySQL has no `IF NOT EXISTS` for `CREATE INDEX`, nor `IF EXISTS` for `DROP INDEX`. `CreateIndex(ix, t).On(keys...).IfNotExists()` and `DropIndex(ix, t).IfExists()` write the statement with a `SkipIf` guard, a query for the index in `information_schema.statistics` that the runner checks first: `contrib/migrate`'s `Execute` skips the statement when the guard finds a row, and a runner that does not know the field runs it and fails loudly on a repeat. `CreateTrigger(tr).After(event, t).Do(first, more...)` takes one or more typed steps; several are written `BEGIN s1; s2; END` and need no `DELIMITER`, which only the `mysql` client uses, because `database/sql` sends the statement whole.

## Partitioning

`PARTITION BY` is a `Partitioning` clause at the option position of `CreateTable`, and `Build` enforces the rules MySQL imposes on a partitioned table, each measured on 8.4 with the error the server raises for it: every primary and unique key must include every partition column (`ErrPartitionKeyNotInKey`, error 1503), the table can have no foreign key (`ErrPartitionForeignKey`, 1506), RANGE bounds must strictly increase with `MAXVALUE` only last (`ErrPartitionBounds`, 1493 and 1481), and partition names must be present and unique (`ErrPartitionDefinition`, 1517).

```go
orders := mysql.CreateTable(Orders).Column(ddl.Column(OrderID), ddl.Column(OrderYear)).
	Clause(mysql.PrimaryKey(OrderID, OrderYear),
		mysql.PartitionByRange(mysql.PartitionOn(OrderYear)).With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax")))
```

```sql
CREATE TABLE `orders` (... PRIMARY KEY (`id`, `year`)) PARTITION BY RANGE (`year`) (PARTITION `p2025` VALUES LESS THAN (2026), PARTITION `pmax` VALUES LESS THAN (MAXVALUE))
```

- **Position:** `PARTITION BY` is always written after the table's options, wherever you pass it. MySQL takes its table options in any order among themselves but partitioning only last — measured on 8.4, `PARTITION BY HASH(id) PARTITIONS 2 ENGINE=InnoDB` is `ER_PARSE_ERROR` — and since 2026-09-24 the statement moves it there. The table options stay in the order you gave them, since MySQL does not care, and so does a `RawTableOption`, which counts as a table option. `ExampleOptionRank_mysql` in `examples/packages` shows it, and the reasoning is in [Clause order is the engine's](../ddl/README.md#clause-order-is-the-engines-not-the-callers).

```go
mysql.CreateTable(Events).Column(ddl.Column(EventID)).Clause(
	mysql.PartitionByHash(mysql.PartitionOn(EventID), 4), // passed first
	mysql.TableComment("audit trail"),
	mysql.InnoDB(),
)
```

```sql
CREATE TABLE `event` (`id` BIGINT NOT NULL) COMMENT = 'audit trail' ENGINE = InnoDB PARTITION BY HASH (`id`) PARTITIONS 4
```

- **Forms:** `PartitionByRange(key)` and `PartitionByList(key)` over a `PartitionKey`; `PartitionByRangeColumns(cols...)` and `PartitionByListColumns(cols...)` over columns, which also take strings and dates; `PartitionByHash(key, n)`; and `PartitionByKey(n, cols...)`, MySQL's own hash, over any column type.
- **Keys:** `PartitionOn(col)` for an integer column, and the functions MySQL allows on a date: `PartitionYear`, `PartitionToDays` and `PartitionToSeconds`. `UNIX_TIMESTAMP` is not offered: MySQL refuses it on a `DATETIME` as timezone-dependent (error 1486), and `DATETIME(6)` is how this module writes a timestamp.
- **Partitions:** `LessThan(name, values...)` and `LessThanMax(name)` for RANGE, one value per partition column, and `In(name, values...)` for LIST, given with `With`. `MAXVALUE` is written in parentheses once per column, the one form both RANGE and RANGE COLUMNS accept, measured.
- **Maintenance:** `AddPartitions`, `AddHashPartitions(n)`, `DropPartitions`, `TruncatePartitions`, `ReorganizePartitions(from, defs...)`, the way to split the `MAXVALUE` partition when a new year arrives, `CoalescePartitions(n)` and `RemovePartitioning()`. Each is a statement of its own, as MySQL requires.
- **Upkeep:** `RebuildPartitions`, `AnalyzePartitions`, `CheckPartitions`, `OptimizePartitions` and `RepairPartitions`, each taking partition names or the constant `AllPartitions`, and `ExchangePartition(name, with)`, which swaps a partition's rows with an unpartitioned table's in constant time by exchanging their tablespaces — the statement behind archiving. Its `WithoutValidation()` skips the per-row check that every row belongs in the partition, and a row that does not is then stored where pruning will never find it again. The last four report what they found by **returning a result set** and do not fail, one row per partition with a `Msg_type` column, so a runner must query them; executing one reports success whatever it found. `contrib/driver-mysql`'s `TestDDL_MySQLPartitionMaintenance` is the worked example.

Every form, the row placement, and the maintenance sequence are executed on MySQL 8.4 by `TestPartitioning_ExecutesOnMySQL` in `contrib/driver-mysql`. A new RANGE partition added to an existing table must lie above the ones already there; the server checks that, because the existing bounds are not in the statement.

## Closed sets are checked, not assumed

Eight types here are named strings with a fixed list of constants: `IndexMethod`, `TriggerEvent`, `ViewAlgorithmKind`, `Engine`, `RowFormatKind`, `AlterAlgorithm`, `AlterLock` and `IntervalUnit`. Go lets a caller write `mysql.Engine("anything")`, so a type of that shape is only a closed set if something checks it — and where nothing does, the value goes into the statement text verbatim, which is an injection path rather than merely a way to reach a wrong keyword.

Every one of them is checked at every site that renders it, and a value outside the constants fails `Build` with `ErrUnknownKeyword`, or `ErrBadSchedule` for `IntervalUnit`. `IndexMethod` was the one that was not: it had two render sites, `CREATE INDEX` and an inline key in `CREATE TABLE`, and neither asked. Both now go through one renderer, which is the point — a fix applied to one of two sites leaves the other open, and the second site is the one nobody remembers. `TestClosedStringSets_RefuseAHostileValue` drives a statement-closing value at all eighteen render sites and builds every real constant of all eight types, because a check that refuses everything would pass the first half and break the package; it was verified by making each `valid()` return true and watching all eighteen leak.

Everything else a caller supplies as a string is a value rather than a keyword, and each is quoted by the dialect rather than checked: object, column, partition, charset, collation and parser names through `QuoteIdent`, and comments, a `SIGNAL`'s message and `GroupConcat`'s separator through `QuoteLiteral`. Patterns, JSON paths and a full-text query are bound as parameters. The four documented escape hatches — `RawAlter`, `RawTableOption`, `StepRaw` and a routine's `Body` — are the exception and say so at each definition: nothing validates them, and a value from outside the program must never reach one.

## The other forms of CREATE TABLE

`CREATE TABLE` has three shapes and the ordinary one is only the first. `Temporary()` is a clause, so a temporary table takes the same columns, keys and options as any other; `CreateTableLike(t, src)` copies a table's structure and no rows, and `CreateTableAs(t, query)` derives the columns from a query and fills the table in one statement, with `OnConflict(ConflictIgnore)` or `ConflictReplace` for a row the new table's own key refuses. Both take `IfNotExists` and `Temporary`. They are statements of their own rather than methods because `CreateTable`'s typestate requires a column before it will build and a table defined by `LIKE` or by a query has none — the same reason `pg.CreateTableLike` is its own statement. `DropTemporaryTable(t, more...)` is the drop that names the keyword, and the keyword is not decoration: a temporary table shadows a permanent one of the same name, so `DROP TABLE` there removes the temporary while looking like it removed the other.

Two behaviours are measured rather than assumed. `CreateTableAs(...).IfNotExists()` on a table that already exists skips the query too, so the rows are not doubled — older MySQL appended. And `CREATE TABLE ... LIKE` copies the keys but no foreign key and no table comment.

`RawTableOption(sql)` is the `CREATE TABLE` counterpart of `RawAlter`, for the same set of storage-tuning options; neither validates the string.

## Indexes, views, triggers

`CreateIndex(ix, t)` takes `Unique`, `Fulltext`, `Spatial` and `Using(BTree)` or `Hash` (an `IndexMethod`, validated: see below), then `On` key parts, each an `IndexElement`: `IndexKey(col)`, `IndexPrefix(col, n)` for a text column, and `IndexExpr(e)` for a functional key, each an `IndexKeyElem` with `Desc`. `DropIndex(ix, t)` drops one. Neither has `IF [NOT] EXISTS`, which MySQL lacks, so there is no such method. The stages are `CreateIndexQuery`, `CreateIndexReady` and `DropIndexStatement`.

`With(opts...)` adds index options, which an inline `PrimaryKey`, `Unique`, `TableIndex`, `TableFulltext` or `TableSpatial` takes too, along with `Using`: `IndexComment(text)`, `IndexKeyBlockSize(kb)`, `IndexWithParser(name)` for a FULLTEXT index needing its own word breaking, and `IndexVisible()` or `IndexInvisible()`, which creates the index hidden so it can be measured before any query depends on it. `Algorithm` and `Lock` say how the index must be built or dropped and fail when it cannot be done that way. They are separated from the options by a space and not by a comma, which is the thing no rendering test could have known: `ALTER TABLE` takes its algorithm and lock as comma-separated actions and `CREATE INDEX` does not, and MySQL answers the comma with `ER_PARSE_ERROR`.

`CreateView(v).OrReplace().As(body)` takes a `*mysql.SelectQuery`, with `WithCheckOption(mysql.Local)` or `Cascaded`; the stages are `CreateViewQuery` and `CreateViewStatement`, and `DropView(v).IfExists()` is a `DropStatement`. Before `As`, both `CreateView` and `AlterView` take `Algorithm(ViewMerge)`, `ViewTemptable` or `ViewUndefined`, `Security(ViewInvoker)` or `ViewDefiner`, and `Columns(names...)` to rename the query's columns. `ViewMerge` is what makes a view free — MySQL rewrites the query against the view into one against its tables, so the outer `WHERE` reaches the indexes — and it cannot be used for a view with an aggregate, `DISTINCT`, `LIMIT`, `UNION` or a window function. `information_schema.views` records the security type and not the algorithm, so the algorithm is read back with `SHOW CREATE VIEW`.

`AlterView(v).As(body)` redefines an existing view and is not the same statement as `CreateView(v).OrReplace()`: the replace form creates the view when it is absent, so a typo in the name makes a second view instead of failing, while `ALTER VIEW` fails with `ER_NO_SUCH_TABLE`. It takes the same `WithCheckOption`, and a check option the `ALTER` leaves out is one the view loses. The stages are `AlterViewQuery` and `AlterViewStatement`.

`CreateTrigger(tr).IfNotExists().After(mysql.OnUpdate, t).Do(step, more...)` takes one `TriggerEvent` and one or more `TriggerStep`s, with `OldRow(col)` and `NewRow(col)` for the row; MySQL has no `WHEN`, so a condition belongs in the body statement's `WHERE`. A step is `StepInsert`, `StepUpdate` or `StepDelete` over a complete MySQL statement; `StepSetNew(col, value)` writes a column of the row being inserted or updated, the one thing only a `BEFORE` trigger can do; and `StepSignal(UserSQLState, message)` rejects the row with an error, which is how a rule a `CHECK` cannot express is enforced. `StepRaw(sql)` is the escape hatch for MySQL's procedural control flow — `IF`, `WHILE`, `LOOP`, `REPEAT`, `CASE`, `DECLARE` and cursors — which is a second language with its own scoping and handlers, not SQL a dialect can check; nothing validates the string.

`StepBlock(opener, closer, inner...)` wraps raw procedural text around typed steps, so the statements inside a guard or a loop stay steps rather than becoming more string:

```go
mysql.CreateTrigger(mysql.Trigger{Name: "entries_bi"}).Before(mysql.OnInsert, Entries).
	Do(mysql.StepBlock("IF NOT (SELECT is_open FROM accounts WHERE id = NEW.account_id) THEN", "END IF",
		mysql.StepSignal(mysql.UserSQLState, "the account is closed")))
```

```sql
CREATE TRIGGER `entries_bi` BEFORE INSERT ON `entries` FOR EACH ROW IF NOT (SELECT is_open FROM accounts WHERE id = NEW.account_id) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'the account is closed'; END IF
```

Three `StepRaw` values cannot do this and the reason is the separator. A body of several steps renders `BEGIN s1; s2; END`, so every step is separated, and a raw step that *opens* a block is not a statement — written as three steps the guard above renders `IF ... THEN; SIGNAL ...; END IF`, which MySQL refuses. The rule `StepBlock` follows is measured and is not symmetric: no separator after the opener, and one after every inner statement **including the last**, because MySQL refuses `IF c THEN SET x = 1 END IF` and accepts `IF c THEN SET x = 1; END IF`. Both halves were verified by breaking them against a real 8.4 and watching each become `ER_PARSE_ERROR`. It models no `IF` and no `WHILE` — the procedural language stays the caller's, and the opener and closer are unvalidated like any raw text. An inner step may itself be a `StepBlock`, so blocks nest.

This construct exists because an end-to-end task needed it: `TestTask_RefuseAWriteNoCheckConstraintCanExpress` in `contrib/driver-mysql` is a rule about two tables, which a `CHECK` cannot express, and until 2026-09-24 the only way to write it was one hand-rolled string with the `SIGNAL` spelled out inside. `Follows(other)` and `Precedes(other)` place this trigger among the others of the same timing and event, which MySQL otherwise fires in the order somebody happened to install them in. The stages are `CreateTriggerQuery`, `TimedTriggerQuery` and `CreateTriggerStatement`. `DropTrigger(tr).IfExists()`, `Truncate(t)` (a `TruncateStatement`) and `RenameTable(from, to).And(from2, to2)` (a `RenameTableStatement`) complete the set — and `And` is the reason `RENAME TABLE` exists beside `ALTER TABLE ... RENAME TO`, since MySQL performs every rename in one statement atomically, so `RenameTable(live, old).And(next, live)` swaps two tables with no instant in which the live name is missing, and a missing name fails with `ErrEmptyName`, an unknown closed-set value with `ErrUnknownKeyword`, a foreign key over two tables with `ErrMixedReferences`, and a `SIGNAL` outside a five-character SQLSTATE with `ErrBadSQLState`.

## Events and stored routines

An `Event` is a statement the server runs on a schedule. `CreateEvent(e).IfNotExists().On(schedule).OnCompletion(Preserve).Status(EventDisable).Comment(text).Do(step, more...)` builds one, over the same body steps a trigger takes; the schedule is `EventAt(t)` for a single run, `EventAfter(n, UnitHour)` for one relative to the server's own clock, or `EventEvery(n, UnitDay)` with `Starts` and `Ends`, over `UnitSecond` through `UnitYear`, and a unit outside those or a count below one fails with `ErrBadSchedule`. `EventAfter` is the one that needs no time zone, since MySQL resolves `CURRENT_TIMESTAMP` itself. `Preserve` keeps an event that has no run left, which is what a migration that declares one wants, since a dropped event is one the next install creates again; `NotPreserve` is MySQL's default. `AlterEvent(e)` changes any part — `On`, `OnCompletion`, `Status`, `Comment`, `Rename` and `Do` — and has no `Build` until one of them is called, because MySQL refuses an `ALTER EVENT` naming nothing. `DropEvent(e).IfExists()` drops one. The stages are `CreateEventQuery`, `ScheduledEventQuery`, `CreateEventStatement`, `AlterEventQuery` and `AlterEventStatement`.

Two server-side conditions make a correctly built event do nothing visible, so both are worth knowing: the scheduler is off unless `SET GLOBAL event_scheduler = ON`, and a schedule's timestamp is read in the **session's** time zone while the dialect writes a `time.Time` as UTC text, so the connection that installs the event should be on `time_zone = '+00:00'`.

A stored `Function` returns a value and is called in an expression; a `Procedure` is invoked by `CALL`. `CreateFunction(f, returns)` takes the return type as a `ddl.SQLType[T]` — it is a function rather than a method because a method cannot introduce a type parameter — and `CreateProcedure(p)` takes none; both then take `IfNotExists`, `Param(RoutineParam(name, type).Mode(ModeInOut))`, `Characteristic` and `Comment(text)`, and `Body(sql)` completes the statement. A mode belongs to a procedure's parameter only: MySQL's grammar has none on a function's, so `CreateFunction` refuses one with `ErrFunctionParamMode` rather than rendering a parse error — a rendering test had asserted the `IN` form until the server was asked. `AlterFunction` and `AlterProcedure` change only what MySQL's `ALTER` accepts, through `Characteristic` and `Comment`: the comment, the language, the SQL-data clause and the security clause, so `Deterministic` there fails with `ErrCharacteristicNotAlterable` and the body is changed by dropping and recreating the routine. Two characteristics of one kind fail with `ErrConflictingCharacteristics`, because MySQL resolves that silently by keeping the last. `DropFunction(f)` and `DropProcedure(p)` take `IfExists`. The stages are `CreateRoutineQuery`, `CreateRoutineStatement`, `AlterRoutineQuery` and `AlterRoutineStatement`.

The body is a string the caller wrote, and that is deliberate. MySQL's stored-program language has its own declarations, handlers, cursors and control flow; a builder for it would be a compiler for a language this module does not speak, and it would look exactly like every other construct here while checking nothing. Nothing parses or validates it, so a value from outside the program must never reach it. A function must declare `Deterministic`, `NoSQL` or `ReadsSQLData` to be created at all while binary logging is on and `log_bin_trust_function_creators` is off, and one declared `Deterministic` that is not lets the optimizer cache a call's result, which is a wrong answer rather than a slow one.

```go
add := mysql.Function{Name: "add"}
mysql.CreateFunction(add, ddl.RawType[int64]("BIGINT")).
	Param(mysql.RoutineParam("a", ddl.RawType[int64]("BIGINT")), mysql.RoutineParam("b", ddl.RawType[int64]("BIGINT"))).
	Characteristic(mysql.Deterministic, mysql.NoSQL).
	Body("RETURN a + b")
```

```sql
CREATE FUNCTION `add` (`a` BIGINT, `b` BIGINT) RETURNS BIGINT DETERMINISTIC NO SQL RETURN a + b
```

## What MySQL does not have

`RETURNING`, `IF [NOT] EXISTS` on an index, a trigger's `WHEN`, deferrable constraints, `SET DEFAULT` as a referential action in InnoDB, `CREATE TYPE`, sequences and schemas as a library's objects: none has a constructor here, so each absence is a compile error. The detector in `mysql/ddl_coverage_test.go` classifies every statement the server's own `HELP 'Data Definition'` lists; since 2026-09-24 it has no `partial` and no `left-out` entry, and what it still does not build is the seventeen statements it marks `operator` — server, storage and account administration, which a library's schema does not own.

A `built` entry names a constructor and says nothing about the forms inside the statement, which is a real limit of a statement-level detector: `CREATE TABLE` was marked built while `TEMPORARY`, `LIKE` and `AS SELECT` were all absent. Those were closed on 2026-09-24 by reading MySQL 8.4's own `HELP` output for each built statement and comparing it against this package. What remains unbuilt inside a built statement is `DEFINER = user` on every object that takes it (a MySQL account is an operator's grant, and the security context a library can choose is `SQL SECURITY`, which is built), `ALTER TABLE ... ORDER BY` (MyISAM's physical row order, ignored by InnoDB), the storage-tuning table options (reachable through `RawTableOption` and `RawAlter`), `ENGINE_ATTRIBUTE`, `SECONDARY_ENGINE_ATTRIBUTE`, `COLUMN_FORMAT` and `STORAGE` (each belongs to a storage engine this module does not target), the inline column `UNIQUE KEY` and `PRIMARY KEY` (the table-level forms build the same constraint and can be named, which `DropKey` and `RenameKey` need), `MATCH FULL | PARTIAL | SIMPLE` on a foreign key (InnoDB parses and ignores it, so building it would imply a guarantee no engine here provides), `RESTRICT | CASCADE` on `DROP TABLE` and `DROP VIEW` (MySQL parses both and does nothing with either), and the loadable-function form of `CREATE FUNCTION`, which loads a shared library into the server.

## Reference

<!-- BEGIN GENERATED REFERENCE -->

Generated from the source by `task docs:reference`. Every exported name in `mysql` appears here.

### entry

| Name | What it is |
|---|---|
| `mysql.SelectQuery` | The statement constructors |
| `mysql.InsertQuery` | the statement Insert returns, with no Build until a row, a query or a body completes it |
| `mysql.InsertReady` | a complete INSERT |
| `mysql.UpdateQuery` | the statement Update returns, with no Build until Where or All says which rows |
| `mysql.FilteredUpdate` | an UPDATE that has said which rows |
| `mysql.DeleteQuery` | the statement Delete returns, with no Build until Where or All |
| `mysql.FilteredDelete` | a DELETE that has said which rows |
| `mysql.Select` | Select starts a SELECT for MySQL |
| `mysql.Insert` | Insert starts an INSERT into t for MySQL |
| `mysql.Update` | Update starts an UPDATE of t for MySQL |
| `mysql.Delete` | Delete starts a DELETE from t for MySQL |
| `mysql.CreateTableQuery` | a CREATE TABLE with no column yet, which has no Build |
| `mysql.CreateTableReady` | a CREATE TABLE with at least one column |
| `mysql.AlterTableQuery` | an ALTER TABLE with no action yet, which has no Build |
| `mysql.AlterTableReady` | an ALTER TABLE with at least one action |
| `mysql.DropTableQuery` | a DROP TABLE, complete from its constructor |
| `mysql.CreateTable` | CreateTable starts a CREATE TABLE of t for MySQL |
| `mysql.AlterTable` | AlterTable starts an ALTER TABLE of t for MySQL |
| `mysql.DropTable` | DropTable drops one or more tables for MySQL, one statement per table |

### forms

| Name | What it is |
|---|---|
| `mysql.Temporary` | Temporary makes the table a temporary one: visible to this connection only, dropped when it closes, and free to shadow a permanent table of the same name — which is the hazard as well as the point, since a query naming that table then reads the temporary one with nothing to say so |
| `mysql.RawTableOption` | a CREATE TABLE option written out, the counterpart of RawAlter and the same escape hatch for the same set: MySQL's storage-tuning table options, which are an operator's decision rather than a schema a library ships |
| `mysql.CreateTableLikeStatement` | CREATE TABLE .. |
| `mysql.CreateTableLike` | CreateTableLike creates t with the structure of src: every column, every index and the table's attributes, and no rows |
| `mysql.RowConflict` | what CREATE TABLE .. |
| `mysql.ConflictIgnore` | ConflictIgnore skips the duplicate row and continues |
| `mysql.ConflictReplace` | ConflictReplace deletes the row already there and inserts the new one |
| `mysql.CreateTableAsStatement` | CREATE TABLE .. |
| `mysql.CreateTableAs` | CreateTableAs creates t from the result of a query, deriving every column's name and type from the select list and filling the table in the same statement |
| `mysql.DropTemporaryTableStatement` | DROP TEMPORARY TABLE |
| `mysql.DropTemporaryTable` | DropTemporaryTable drops temporary tables, one statement each |

### mysql

| Name | What it is |
|---|---|
| `mysql.Dialect` | the MySQL dialect, re-exported so a caller importing this package for its constructs needs no second import to name the engine |
| `mysql.Is` | whether d is the MySQL dialect |
| `mysql.SelectClause` | a clause for a SELECT on MySQL |
| `mysql.InsertClause` | a clause for an INSERT on MySQL |
| `mysql.UpdateClause` | a clause for an UPDATE on MySQL |
| `mysql.DeleteClause` | a clause for a DELETE on MySQL |
| `mysql.Guard` | whether rendering may continue under the dialect in a, recording the construct as unsupported when it may not |
| `mysql.Expr` | Expr makes a guarded typed expression, for a helper this package lacks |
| `mysql.Func` | a guarded function call typed R |
| `mysql.ClauseIn` | ClauseIn makes a guarded clause for a statement slot, typed to MySQL and to the statement kind K |

### objects

| Name | What it is |
|---|---|
| `mysql.ErrEmptyName` | returned for an object value with no name |
| `mysql.Index` | a MySQL index, declared once |
| `mysql.IndexMethod` | BTREE or HASH; InnoDB builds BTREE whatever is asked |
| `mysql.BTree` | — |
| `mysql.Hash` | — |
| `mysql.IndexElement` | one key part of an index |
| `mysql.IndexKeyElem` | a column, prefix or expression key part |
| `mysql.IndexKey` | IndexKey indexes a column |
| `mysql.IndexPrefix` | IndexPrefix indexes the first n characters of a text column, which is the only way a TEXT column is indexed at all |
| `mysql.IndexExpr` | IndexExpr indexes an expression, a functional key part, which MySQL writes in a second pair of parentheses |
| `mysql.CreateIndexQuery` | CREATE INDEX with no key yet, which has no Build |
| `mysql.CreateIndexReady` | a CREATE INDEX with at least one key |
| `mysql.CreateIndex` | CreateIndex starts an index on t |
| `mysql.DropIndexStatement` | DROP INDEX .. |
| `mysql.DropIndex` | DropIndex drops an index from its table |
| `mysql.View` | a MySQL view |
| `mysql.CheckOption` | what WITH CHECK OPTION checks |
| `mysql.Local` | — |
| `mysql.Cascaded` | — |
| `mysql.CreateViewQuery` | CREATE VIEW with no body yet, which has no Build |
| `mysql.CreateView` | CreateView starts a view |
| `mysql.CreateViewStatement` | a complete CREATE VIEW |
| `mysql.DropStatement` | DROP VIEW or DROP TRIGGER, with IF EXISTS |
| `mysql.DropView` | DropView drops a view |
| `mysql.Trigger` | a MySQL trigger; its name is unique per schema |
| `mysql.TriggerEvent` | the one event that fires a trigger |
| `mysql.OnInsert` | — |
| `mysql.OnUpdate` | — |
| `mysql.OnDelete` | — |
| `mysql.OldRow` | a column's value before the change, in a trigger's body |
| `mysql.NewRow` | a column's value after the change, in a trigger's body |
| `mysql.TriggerStep` | a trigger's body statement, closed to the constructors below, each typed to a complete MySQL statement |
| `mysql.StepInsert` | an INSERT as a trigger's body |
| `mysql.StepUpdate` | an UPDATE as a trigger's body |
| `mysql.StepDelete` | a DELETE as a trigger's body |
| `mysql.CreateTriggerQuery` | CREATE TRIGGER with no timing yet |
| `mysql.CreateTrigger` | CreateTrigger starts a trigger |
| `mysql.TimedTriggerQuery` | CREATE TRIGGER with its timing and no body yet |
| `mysql.CreateTriggerStatement` | a complete CREATE TRIGGER |
| `mysql.DropTrigger` | DropTrigger drops a trigger |
| `mysql.TruncateStatement` | TRUNCATE TABLE |
| `mysql.Truncate` | Truncate empties a table by recreating it, resetting AUTO_INCREMENT |
| `mysql.RenameTableStatement` | RENAME TABLE |
| `mysql.RenameTable` | RenameTable renames a table, and can move it to another schema, which ALTER TABLE .. |
| `mysql.AlterViewQuery` | ALTER VIEW with no body yet, which has no Build: a view is altered by giving its new definition, so there is nothing to build until As has been called |
| `mysql.AlterView` | AlterView starts a redefinition of an existing view |
| `mysql.AlterViewStatement` | a complete ALTER VIEW |
| `mysql.StepSetNew` | StepSetNew assigns a value to a column of the row being written, the one thing only a BEFORE trigger can do: NEW.col is writable there and read-only in an AFTER trigger, where MySQL raises ER_TRIGGER_CANT_CHANGE_ROW rather than ignoring the assignment |
| `mysql.ErrBadSQLState` | returned for a SIGNAL whose SQLSTATE is not five characters, or is class 00, which MySQL reads as success and refuses to signal |
| `mysql.UserSQLState` | the SQLSTATE MySQL's own documentation uses for an application-raised error: class 45 is "unhandled user-defined exception" and is the one a trigger rejecting a row should raise, because every other class means something to the client driver |
| `mysql.StepSignal` | StepSignal rejects the row with an error, which is how a trigger enforces a rule a CHECK constraint cannot express — one that reads another table, say |
| `mysql.StepRaw` | a body statement written out, and it is the documented escape hatch for MySQL's procedural control flow: IF .. |
| `mysql.StepBlock` | raw procedural text wrapped around typed steps: the opener and the closer are the caller's words, and every statement between them is still a TriggerStep |
| `mysql.IndexOption` | one of MySQL's index options, which CREATE INDEX and an inline key both take |
| `mysql.IndexComment` | IndexComment attaches a comment to the index, which SHOW INDEX reports |
| `mysql.IndexKeyBlockSize` | IndexKeyBlockSize suggests the index's page size in kilobytes |
| `mysql.IndexWithParser` | IndexWithParser names the full-text parser plugin for a FULLTEXT index, the clause a language needing its own word breaking requires |
| `mysql.IndexInvisible` | IndexInvisible creates the index hidden from the optimizer, which is how an index is added and measured before any query is allowed to depend on it |
| `mysql.IndexVisible` | IndexVisible creates the index available to the optimizer, which is the default; it exists so the visibility can be written out rather than implied |
| `mysql.ViewAlgorithmKind` | how MySQL processes a view |
| `mysql.ViewMerge` | ViewMerge rewrites a query against the view into one against its tables, so the outer WHERE reaches the indexes |
| `mysql.ViewTemptable` | ViewTemptable materialises the view into a temporary table first, which has no index and is therefore the slow path — asking for it explicitly is how a view is kept from being merged into a query that would lock its tables for longer |
| `mysql.ViewUndefined` | ViewUndefined lets MySQL choose, which is the default |
| `mysql.ViewSecurity` | whose privileges MySQL checks when a view is read |
| `mysql.ViewDefiner` | ViewDefiner checks the view's definer, which is MySQL's default and is how a view grants narrowed access to a table the reader cannot select from directly |
| `mysql.ViewInvoker` | ViewInvoker checks the reader's own privileges, so the view grants nothing the reader did not already have |

### options

| Name | What it is |
|---|---|
| `mysql.Engine` | a MySQL storage engine |
| `mysql.EngineInnoDB` | the only one with transactions, foreign keys and row locks, which is what a library's table needs |
| `mysql.EngineMemory` | the table in RAM and loses it on restart: a scratch table, never one that owns data |
| `mysql.EngineMyISAM` | EngineMyISAM has no transactions and locks the whole table; it is here because a table inherited from an older schema may still be on it and SetEngine(EngineInnoDB) is how it is moved off |
| `mysql.RowFormatKind` | how InnoDB stores a row |
| `mysql.RowFormatDynamic` | RowFormatDynamic stores a long value entirely off-page, keeping a 20 byte pointer in the row |
| `mysql.RowFormatCompressed` | Dynamic with the pages compressed, trading CPU for disk |
| `mysql.RowFormatCompact` | RowFormatCompact stores the first 768 bytes of a long value in the row |
| `mysql.RowFormatRedundant` | the pre-5.0.3 format, kept for a table restored from an old dump |
| `mysql.RowFormatDefault` | RowFormatDefault defers to innodb_default_row_format, which is what a table written without the option already does; it exists so an ALTER can put a table back under the server's default |
| `mysql.TableEngine` | TableEngine sets the table's storage engine in CREATE TABLE |
| `mysql.RowFormat` | RowFormat sets how InnoDB stores a row of the table in CREATE TABLE |
| `mysql.SetEngine` | SetEngine changes the table's storage engine, and is how a table is moved onto InnoDB |
| `mysql.SetRowFormat` | SetRowFormat changes how InnoDB stores a row, which rebuilds the table |
| `mysql.SetTableComment` | SetTableComment replaces the table's comment |
| `mysql.ErrAutoIncrementZero` | returned for an AUTO_INCREMENT of 0: MySQL reads it as "reset to the default", which is the opposite of what a caller asking for a specific next value means |
| `mysql.SetAutoIncrement` | SetAutoIncrement sets the value the next inserted row gets |
| `mysql.SetDefaultCharset` | SetDefaultCharset changes the character set new columns of the table get |
| `mysql.SetTableCollate` | SetTableCollate changes the table's default collation, again for new columns only |
| `mysql.ConvertToCharset` | ConvertToCharset rewrites every text column of the table into charset, which is the one statement that fixes data already stored under another one |
| `mysql.AlterAlgorithm` | how MySQL performs an ALTER TABLE |
| `mysql.AlgorithmInstant` | AlgorithmInstant changes the table's metadata only |
| `mysql.AlgorithmInplace` | AlgorithmInplace rebuilds the table in place without copying it to a new file, and usually allows concurrent writes |
| `mysql.AlgorithmCopy` | AlgorithmCopy copies every row into a new table, which is the slow path and the one that blocks writes for the duration |
| `mysql.AlgorithmDefault` | AlgorithmDefault lets MySQL pick the cheapest it can, which is what an ALTER TABLE without the clause does |
| `mysql.AlterLock` | how much concurrent access the ALTER TABLE allows |
| `mysql.LockNone` | LockNone allows concurrent reads and writes |
| `mysql.LockShared` | LockShared allows reads and blocks writes |
| `mysql.LockExclusive` | LockExclusive blocks both |
| `mysql.LockDefault` | LockDefault allows as much concurrency as the change permits, which is what an ALTER TABLE without the clause does |
| `mysql.Algorithm` | Algorithm says how MySQL must perform the change, and fails the statement when it cannot be done that way |
| `mysql.Lock` | Lock says how much concurrent access the change must allow, and fails when it cannot allow that much |
| `mysql.SetCheckEnforced` | SetCheckEnforced turns a declared CHECK constraint back on, and MySQL validates every existing row as it does so, so the statement fails when one violates it |
| `mysql.SetCheckNotEnforced` | SetCheckNotEnforced turns a declared CHECK constraint off while keeping its definition, which is how a bulk load of rows that do not yet satisfy it is done without dropping and re-adding the constraint |
| `mysql.SetIndexInvisible` | SetIndexInvisible hides an index from the optimizer while keeping it maintained, which is how dropping one is tested: the queries plan as if it were gone, and SetIndexVisible undoes it instantly, where recreating a dropped index on a large table does not |
| `mysql.SetIndexVisible` | SetIndexVisible makes an index available to the optimizer again |
| `mysql.ForceRebuild` | ForceRebuild rebuilds the table with no other change, which is how InnoDB is made to rewrite it: with SetEngine it is the pair of statements that reclaim space after a large delete |
| `mysql.WithValidation` | WithValidation makes MySQL check every existing row against a change to a column an index covers, which is its default for a stored generated column |
| `mysql.WithoutValidation` | WithoutValidation skips that check, which MySQL allows only for a change to a VIRTUAL generated column: the index is rebuilt from the new expression without verifying the old rows, so a change that alters the expression's result leaves index entries that no longer match the data |
| `mysql.DisableKeys` | DisableKeys stops MyISAM updating its non-unique indexes, so a bulk load writes rows once and the indexes are built in one pass by EnableKeys |
| `mysql.EnableKeys` | EnableKeys rebuilds the indexes DisableKeys suspended |
| `mysql.DiscardTablespace` | DiscardTablespace detaches the table's data file, leaving the definition with no rows: the first half of moving a table between servers by copying its .ibd file |
| `mysql.ImportTablespace` | ImportTablespace attaches a data file copied from another server, the other half of the pair |
| `mysql.RawAlter` | an ALTER TABLE action written out, and it is this builder's one documented escape hatch |

### partition

| Name | What it is |
|---|---|
| `mysql.ErrPartitionKeyNotInKey` | a primary or unique key that does not include every partition column |
| `mysql.ErrPartitionForeignKey` | a foreign key on a partitioned table, which InnoDB does not support |
| `mysql.ErrPartitionBounds` | RANGE bounds that do not strictly increase, a MAXVALUE that is not last, or a bound with the wrong number of values |
| `mysql.ErrPartitionDefinition` | a partition with no name, a name used twice, a definition of the wrong kind for the partitioning, or none at all where RANGE and LIST need at least one |
| `mysql.PartitionKey` | what a RANGE, LIST or HASH partitioning is computed from: an integer column, or one of the functions MySQL allows over a date column |
| `mysql.PartitionOn` | PartitionOn partitions by an integer column's own value |
| `mysql.PartitionYear` | PartitionYear partitions by YEAR(col) |
| `mysql.PartitionToDays` | PartitionToDays partitions by TO_DAYS(col), the day number |
| `mysql.PartitionToSeconds` | PartitionToSeconds partitions by TO_SECONDS(col) |
| `mysql.PartitionDef` | one partition: a name and its bound |
| `mysql.LessThan` | a RANGE partition holding rows below values, one per partition column |
| `mysql.LessThanMax` | the last RANGE partition, holding every row the others do not |
| `mysql.In` | a LIST partition holding rows whose key is one of values |
| `mysql.Partitioning` | a PARTITION BY clause for CREATE TABLE |
| `mysql.PartitionByRange` | PartitionByRange partitions by ranges of a key |
| `mysql.PartitionByRangeColumns` | PartitionByRangeColumns partitions by ranges of one or more columns compared in order, which also takes dates and strings |
| `mysql.PartitionByList` | PartitionByList partitions by a key's value in a list |
| `mysql.PartitionByListColumns` | PartitionByListColumns partitions by one or more columns' values in a list |
| `mysql.PartitionByHash` | PartitionByHash spreads rows over n partitions by a key's hash |
| `mysql.PartitionByKey` | PartitionByKey spreads rows over n partitions by MySQL's own hash of the columns, which takes any column type |
| `mysql.AddPartitions` | AddPartitions adds RANGE or LIST partitions at the end |
| `mysql.AddHashPartitions` | AddHashPartitions adds n partitions to a HASH or KEY partitioning |
| `mysql.DropPartitions` | DropPartitions drops RANGE or LIST partitions and every row in them |
| `mysql.TruncatePartitions` | TruncatePartitions deletes every row of the named partitions and keeps them |
| `mysql.ReorganizePartitions` | ReorganizePartitions replaces the named partitions with new definitions, moving their rows: the way to split MAXVALUE's partition when a new year arrives |
| `mysql.CoalescePartitions` | CoalescePartitions removes n partitions from a HASH or KEY partitioning, redistributing their rows |
| `mysql.RemovePartitioning` | RemovePartitioning makes the table unpartitioned, keeping every row |
| `mysql.AllPartitions` | AllPartitions names every partition of the table in a maintenance action, MySQL's ALL keyword |
| `mysql.RebuildPartitions` | RebuildPartitions rewrites the named partitions, which is how a partition left fragmented by a bulk delete is compacted without touching the rest of the table |
| `mysql.AnalyzePartitions` | AnalyzePartitions recomputes the key distribution of the named partitions, so the optimizer plans against current statistics after a bulk load |
| `mysql.CheckPartitions` | CheckPartitions looks for corruption in the named partitions |
| `mysql.OptimizePartitions` | OptimizePartitions rebuilds the named partitions and recomputes their statistics: REBUILD and ANALYZE in one pass |
| `mysql.RepairPartitions` | RepairPartitions repairs corruption CheckPartitions found |
| `mysql.ExchangePartitionClause` | EXCHANGE PARTITION .. |
| `mysql.ExchangePartition` | ExchangePartition swaps the named partition with the unpartitioned table with, validating every row |

### read

| Name | What it is |
|---|---|
| `mysql.LockOption` | what a locking read does when a row is already locked |
| `mysql.NoWait` | — |
| `mysql.SkipLocked` | — |
| `mysql.ForUpdate` | ForUpdate locks the rows read for writing until the transaction ends |
| `mysql.ForUpdateOf` | ForUpdateOf locks only the rows of the named tables in a join |
| `mysql.ForShare` | ForShare locks the rows read against writes by others |
| `mysql.WithRollup` | WithRollup adds the super-aggregate rows to a GROUP BY: one per level, with the grouping column NULL |
| `mysql.GroupConcat` | group_concat(e SEPARATOR sep), the aggregate that joins a group's values into one string |
| `mysql.IfNull` | ifnull(e, fallback) |
| `mysql.UTCNow` | utc_timestamp(6), the current instant in UTC to the microsecond, which is what a DATETIME(6) written and read in UTC wants; now() is in the session's time zone |
| `mysql.UUID` | uuid(), a version 1 UUID as text |
| `mysql.RegexpLike` | regexp_like(e, pattern), a regular-expression match |
| `mysql.CharLength` | char_length(e), the length in characters |
| `mysql.JSONExtract` | json_extract(col, path), the JSON value at a path |
| `mysql.JSONValue` | json_unquote(json_extract(col, path)), the value at a path as text, NULL when the path is absent: the ->> operator, written as the functions it abbreviates so a bound path is allowed |
| `mysql.JSONContains` | json_contains(col, candidate), whether the document contains the candidate, which must be valid JSON |
| `mysql.MatchMode` | how a full-text search reads its query |
| `mysql.NaturalLanguage` | — |
| `mysql.Boolean` | — |
| `mysql.Match` | MATCH(cols) AGAINST(query IN mode), a full-text search over columns with a FULLTEXT index, which the columns must be exactly |
| `mysql.IntDiv` | l DIV r, integer division |
| `mysql.NullSafeEq` | l <=> r, MySQL's null-safe equality: true when both are NULL, where = would be NULL |

### routines

| Name | What it is |
|---|---|
| `mysql.Event` | a MySQL event, a statement the server runs on a schedule |
| `mysql.IntervalUnit` | the unit of an event's interval |
| `mysql.UnitSecond` | — |
| `mysql.UnitMinute` | — |
| `mysql.UnitHour` | — |
| `mysql.UnitDay` | — |
| `mysql.UnitWeek` | — |
| `mysql.UnitMonth` | — |
| `mysql.UnitQuarter` | — |
| `mysql.UnitYear` | — |
| `mysql.ErrBadSchedule` | returned for a schedule outside what MySQL accepts: a zero Schedule, an interval unit outside the constants, or a quantity below one, which MySQL refuses with ER_EVENT_INTERVAL_NOT_POSITIVE |
| `mysql.Schedule` | when an event runs: once at a time, or every interval |
| `mysql.EventAt` | EventAt schedules the event to run once, at t |
| `mysql.EventEvery` | EventEvery schedules the event to run every n units, starting when it is created unless Starts says otherwise |
| `mysql.EventAfter` | EventAfter schedules the event to run once, n units from the moment the server reads the statement |
| `mysql.Completion` | what MySQL does with an event that has no run left: a one-shot event that has fired, or a repeating one past its ENDS |
| `mysql.Preserve` | Preserve keeps the event in the schema, disabled |
| `mysql.NotPreserve` | NotPreserve drops the event, which is MySQL's default |
| `mysql.EventStatus` | whether the scheduler runs the event |
| `mysql.EventEnable` | EventEnable runs the event on its schedule, which is the default |
| `mysql.EventDisable` | EventDisable keeps the event and does not run it: the way a job is turned off without losing its definition |
| `mysql.EventDisableOnReplica` | EventDisableOnReplica runs the event on the source and not on a replica, where the replicated rows arrive anyway; running it on both would apply the work twice |
| `mysql.CreateEventQuery` | CREATE EVENT with no schedule yet, which has no Build: ON SCHEDULE is not optional in MySQL's grammar, so the statement is incomplete until On has been called |
| `mysql.CreateEvent` | CreateEvent starts an event |
| `mysql.ScheduledEventQuery` | CREATE EVENT with its schedule and no body yet, which has no Build: DO is not optional either |
| `mysql.CreateEventStatement` | a complete CREATE EVENT |
| `mysql.AlterEventQuery` | ALTER EVENT with no change yet, which has no Build: MySQL refuses an ALTER EVENT naming no part to change, so the statement is incomplete until one of the methods below has been called |
| `mysql.AlterEvent` | AlterEvent starts a change to an existing event |
| `mysql.AlterEventStatement` | an ALTER EVENT with at least one change |
| `mysql.DropEvent` | DropEvent drops an event |
| `mysql.Function` | a MySQL stored function, which returns a value and can be called in an expression |
| `mysql.Procedure` | a MySQL stored procedure, which is invoked by CALL and may return result sets and OUT parameters |
| `mysql.ParamMode` | how a procedure's parameter passes its value |
| `mysql.ModeIn` | ModeIn passes a value in, which is the default and the only mode a function's parameter may have |
| `mysql.ModeOut` | ModeOut passes a value back to the caller's variable |
| `mysql.ModeInOut` | ModeInOut does both |
| `mysql.Param` | one parameter of a stored routine: a name, a SQL type and, for a procedure, a mode |
| `mysql.RoutineParam` | RoutineParam makes a parameter of the Go type T, spelled by t |
| `mysql.RoutineCharacteristic` | one of the clauses MySQL calls a routine's characteristics |
| `mysql.Deterministic` | — |
| `mysql.NotDeterministic` | — |
| `mysql.ContainsSQL` | — |
| `mysql.NoSQL` | — |
| `mysql.ReadsSQLData` | — |
| `mysql.ModifiesSQLData` | — |
| `mysql.SecurityDefiner` | — |
| `mysql.SecurityInvoker` | — |
| `mysql.LanguageSQL` | — |
| `mysql.ErrConflictingCharacteristics` | returned for two characteristics of one category, NoSQL and ReadsSQLData say, which MySQL resolves by keeping the last rather than by failing |
| `mysql.ErrCharacteristicNotAlterable` | returned for a characteristic in an ALTER that MySQL's ALTER FUNCTION and ALTER PROCEDURE do not take: determinism and the body are changed by dropping and recreating the routine |
| `mysql.CreateRoutineQuery` | a CREATE FUNCTION or CREATE PROCEDURE with no body yet, which has no Build: a routine without one is not a statement |
| `mysql.CreateRoutineStatement` | a complete CREATE FUNCTION or CREATE PROCEDURE |
| `mysql.CreateProcedure` | CreateProcedure starts a stored procedure |
| `mysql.CreateFunction` | CreateFunction starts a stored function returning the Go type T, spelled by returns |
| `mysql.ErrFunctionParamMode` | returned for a stored function whose parameter carries IN, OUT or INOUT |
| `mysql.ErrEmptyBody` | returned for a routine whose body is blank |
| `mysql.AlterRoutineQuery` | an ALTER FUNCTION or ALTER PROCEDURE with no characteristic yet, which has no Build: MySQL refuses an ALTER naming nothing to change |
| `mysql.AlterRoutineStatement` | an ALTER FUNCTION or ALTER PROCEDURE with at least one characteristic |
| `mysql.AlterFunction` | AlterFunction starts a change to a stored function's characteristics |
| `mysql.AlterProcedure` | AlterProcedure starts a change to a stored procedure's characteristics |
| `mysql.DropFunction` | DropFunction drops a stored function |
| `mysql.DropProcedure` | DropProcedure drops a stored procedure |

### table

| Name | What it is |
|---|---|
| `mysql.TableClause` | a MySQL clause for CREATE TABLE |
| `mysql.AlterClause` | a MySQL ALTER TABLE action |
| `mysql.ErrUnknownKeyword` | returned for a value outside its named constants |
| `mysql.ErrMixedReferences` | returned for a foreign key referencing two tables |
| `mysql.TableConstraint` | a named constraint of one table, declared once |
| `mysql.KeyClause` | a key in CREATE TABLE: PRIMARY KEY, UNIQUE, INDEX or FULLTEXT |
| `mysql.PrimaryKey` | PrimaryKey makes the table's primary key |
| `mysql.Unique` | Unique makes a named unique key |
| `mysql.TableIndex` | TableIndex makes a named plain index inside CREATE TABLE, which MySQL allows and the other engines do not; CreateIndex is the statement form |
| `mysql.TableFulltext` | TableFulltext makes a named FULLTEXT index inside CREATE TABLE, which Match needs over exactly these columns |
| `mysql.RefAction` | what a foreign key does when the referenced row goes |
| `mysql.RefNoAction` | — |
| `mysql.RefRestrict` | — |
| `mysql.RefCascade` | — |
| `mysql.RefSetNull` | — |
| `mysql.ColumnPair` | one column of a foreign key and the column it references |
| `mysql.RefPair` | RefPair pairs a column with the one it references, of one Go type |
| `mysql.ForeignKeyClause` | a FOREIGN KEY table constraint |
| `mysql.ForeignKey` | ForeignKey makes a named foreign key; InnoDB indexes its columns if no index does |
| `mysql.References` | the one-column foreign key |
| `mysql.CheckClause` | a named CHECK constraint, the same one ddl.Check writes, for ALTER TABLE .. |
| `mysql.Check` | Check makes a named CHECK constraint |
| `mysql.Constraint` | a MySQL table constraint ALTER TABLE can add: a unique key, a foreign key or a check |
| `mysql.AutoIncrement` | AutoIncrement makes col take the next value when an INSERT gives none |
| `mysql.ColumnCollate` | ColumnCollate gives a text column a collation, utf8mb4_bin for a case-sensitive key, say; the default utf8mb4_0900_ai_ci is case- and accent-insensitive, so a UNIQUE on it refuses "Ada" beside "ada" |
| `mysql.ColumnComment` | ColumnComment attaches a comment to a column, where MySQL keeps it |
| `mysql.GeneratedVirtual` | GeneratedVirtual makes col computed by e on read and stored nowhere |
| `mysql.GeneratedStored` | GeneratedStored makes col computed by e on write and stored |
| `mysql.InnoDB` | the storage engine a library's table wants: transactional, with foreign keys and row locks |
| `mysql.DefaultCharset` | DefaultCharset sets the table's character set, utf8mb4 for all of Unicode; the older utf8 is three bytes and cannot store an emoji |
| `mysql.TableCollate` | TableCollate sets the table's default collation |
| `mysql.TableComment` | TableComment attaches a comment to the table |
| `mysql.ModifyColumn` | ModifyColumn redefines a column in place, its type, nullability and default together: MySQL has no ALTER COLUMN .. |
| `mysql.SetDefault` | SetDefault sets a column's default to a value of its type |
| `mysql.DropDefault` | DropDefault removes a column's default |
| `mysql.AddConstraint` | AddConstraint adds a unique key, a foreign key or a check |
| `mysql.DropForeignKey` | DropForeignKey drops a declared foreign key; its index stays |
| `mysql.DropCheck` | DropCheck drops a declared CHECK constraint |
| `mysql.DropKey` | DropKey drops a declared unique key |
| `mysql.ErrRenameAcrossTables` | returned by RenameKey for two constraints of different tables: a key is renamed within its table |
| `mysql.RenameKey` | RenameKey renames a declared unique key within its table |
| `mysql.RenameIndex` | RenameIndex renames a plain index of the table, the Index counterpart of RenameKey |
| `mysql.RenameTo` | RenameTo renames the table to to's name, as its own statement |
| `mysql.ColumnInvisible` | ColumnInvisible hides a column from SELECT *, so it is returned only when named |
| `mysql.ColumnVisible` | a column to SELECT *, which is the default; it exists so a ModifyColumn that must keep the column visible can say so, since a property left out of a MySQL column redefinition is dropped |
| `mysql.SetColumnVisible` | SetColumnVisible and SetColumnInvisible change a column's visibility without restating its definition, which ModifyColumn would require |
| `mysql.SetColumnInvisible` | SetColumnInvisible hides a column from SELECT *; see ColumnInvisible |
| `mysql.Placement` | where a column goes among the table's columns |
| `mysql.First` | First places the column before every other |
| `mysql.After` | After places the column immediately after col |
| `mysql.ColumnAction` | an ALTER TABLE action that defines a column and can say where it goes: ADD COLUMN, MODIFY COLUMN and CHANGE COLUMN |
| `mysql.AddColumn` | AddColumn adds a column, with the MySQL placement the universal ddl.AddColumn cannot express |
| `mysql.ChangeColumn` | ChangeColumn renames a column and restates its definition in one action, which is the only way MySQL does both at once |
| `mysql.ModifyColumnAt` | ModifyColumn with a placement, which also moves the column: MySQL rebuilds the table to do it |
| `mysql.DropPrimaryKey` | DropPrimaryKey drops the table's primary key |
| `mysql.DropConstraint` | DropConstraint drops a declared constraint whatever its kind, which is what a migration undoing an earlier one wants when the kind is not at hand; DropCheck, DropKey and DropForeignKey are the specific forms, and MySQL refuses each of those on a constraint of another kind |
| `mysql.TableSpatial` | TableSpatial makes a named SPATIAL index inside CREATE TABLE, over geometry columns MySQL requires to be NOT NULL |

### Writes

| Name | What it is |
|---|---|
| `mysql.InsertIgnore` | InsertIgnore makes the INSERT skip a row that would violate a unique key instead of failing, turning the error into a warning |
| `mysql.OnDuplicateKeyUpdate` | MySQL's upsert: when the row would violate a unique key, the existing row is updated with the assignments instead |
| `mysql.New` | the value the INSERT tried to write to col, inside an OnDuplicateKeyUpdate: SET hits = hits + new.hits |
| `mysql.UpdateLimit` | UpdateLimit caps an UPDATE at n rows, in the order given, which is how a large backfill is done in batches without a key range |
| `mysql.DeleteLimit` | DeleteLimit caps a DELETE at n rows, in the order given: the batched purge of an old-rows table |

331 exported names.

<!-- END GENERATED REFERENCE -->
