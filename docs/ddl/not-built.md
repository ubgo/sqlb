# What the DDL does not build, and how to write it anyway

The DDL builders cover what a library installs and evolves: tables, keys, indexes, views, triggers, sequences, enums, policies and partitions. Every engine has more statements than that, and this page lists all of them: every statement the three engines have that `sqlb` builds only in part or not at all, the SQL to write each one yourself, the limits no detector lists, and what may be built later. None of it is hidden or blocked. Each workaround goes through a supported escape hatch, and the statement it writes runs beside the built ones in the same `ddl.Script`.

## Contents

- [How this page is kept honest](#how-this-page-is-kept-honest)
- [The escape hatches](#the-escape-hatches)
- [Postgres commands](#postgres-commands)
- [Postgres ALTER TABLE forms](#postgres-alter-table-forms)
- [SQLite](#sqlite)
- [MySQL](#mysql)
- [Operator and not-DDL entries](#operator-and-not-ddl-entries)
- [Limits beyond the detectors](#limits-beyond-the-detectors)
- [Future work](#future-work)
- [Appendix: every detector entry](#appendix-every-detector-entry)

## How this page is kept honest

Every workaround here has been run, not just looked at. Each snippet comes from a test that runs it on its real engine and reads the result back:

| Engine | Test | Runs against |
|---|---|---|
| Postgres | `TestRawRecipes_Postgres` in `contrib/driver-pgx/raw_recipes_test.go` | Postgres 17, when `SQLB_PG_DSN` is set (`task test:pg`) |
| SQLite | `TestRawRecipes_SQLite` in `contrib/driver-sqlite/raw_recipes_test.go` | an in-memory SQLite, on every `go test` |
| MySQL | `TestRawRecipes_MySQL` in `contrib/driver-mysql/raw_recipes_test.go` | MySQL 8.4, when `SQLB_MYSQL_DSN` is set (`task test:mysql`) |

The list of statements is not written by hand either. Three detectors, one per engine, classify every statement in the engine's own command list: `pg/ddl_coverage_test.go`, `sqlite/ddl_coverage_test.go` and `mysql/ddl_coverage_test.go`. The [appendix](#appendix-every-detector-entry) is generated from them by `scripts/gen-not-built.py`. `task docs:not-built` fails in any of these cases:

- the appendix is out of date;
- an entry marked `partial` or `left-out` has no `###` section below;
- such a section has no code block;
- a section names an entry that has since been built.

Classifying a new engine version's statements therefore also means writing a section for each new gap here.

## The escape hatches

There are eight, and together they reach every statement the three engines have. Each one is typed to the dialect wherever a position has one. So a raw Postgres action on a SQLite `ALTER TABLE` is a compile error, exactly as for a built one.

| Hatch | Writes | Use it for |
|---|---|---|
| `ddl.Script{{SQL: …}}` | a whole statement | anything with no builder: a function, an event, a raw `ALTER DOMAIN` |
| `ddl.NewAlterClause[D]` | one `ALTER TABLE` action, grouped with others | an action the builders do not write |
| `ddl.NewStandaloneAlterClause[D]` | one `ALTER TABLE` action in a statement of its own | an action the engine will not combine, such as a MySQL `REORGANIZE PARTITION` |
| `ddl.NewTableClause[D]` at a `ddl.Position` | a `CREATE TABLE` modifier, element or option | a MySQL `PARTITION BY`, a raw `CHECK`, `WITHOUT ROWID` |
| `ddl.NewColumnClause[D]` at a `ddl.ColumnPart` | text inside one column's definition | a collation, a column constraint |
| `ddl.NewDropClause[D]` | an option after `DROP TABLE`'s names | an engine's drop option |
| `ddl.RawType[T]` | a column's type, spelled by you | a domain, a composite, `citext`, a MySQL `ENUM` |
| `sqlb.Raw`, `sqlb.Literal`, `pg.Func` | an expression | a default, a check, a function call |

### A whole statement

A `ddl.Statement` is SQL text plus a flag saying whether Postgres allows it inside a transaction block. Build the text yourself and put it in a `ddl.Script`, alone or appended to a built one. Quote identifiers with the dialect's `QuoteIdent`, and write a value with its `QuoteLiteral`, which refuses what it cannot write faithfully rather than guessing.

```go
d := pg.Dialect{}
lit, err := d.QuoteLiteral("unknown")
if err != nil {
	return err
}
script := ddl.Script{{SQL: `ALTER DOMAIN ` + d.QuoteIdent("status") + ` SET DEFAULT ` + lit, Transactional: true}}
```

Set `Transactional: false` only for a statement Postgres refuses in a transaction block, such as `CREATE INDEX CONCURRENTLY`. MySQL and SQLite ignore the flag: MySQL commits every DDL statement implicitly, and SQLite runs all of them in a transaction.

### An ALTER TABLE action

`ddl.NewAlterClause[D]` takes a render function and returns an action for `D`'s `ALTER TABLE`. It is grouped with the other actions under one statement wherever the engine allows that. `ddl.NewStandaloneAlterClause[D]` is the same except that the action always gets a statement of its own.

```go
force := ddl.NewAlterClause[pg.Dialect](func(*sqlb.Args) string { return `FORCE ROW LEVEL SECURITY` })
script, err := pg.AlterTable(Users).Clause(pg.EnableRowLevelSecurity(), force).Build()
// ALTER TABLE "users" ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY
```

### A CREATE TABLE clause

`ddl.NewTableClause[D]` places text at one of three positions:

- `ddl.PositionModifier`, between `CREATE` and `TABLE`;
- `ddl.PositionElement`, inside the parentheses after the columns;
- `ddl.PositionOption`, after the closing parenthesis.

`ddl.NewColumnClause[D]` places text inside one column's definition, at a `ddl.ColumnPart`:

- `ddl.PartAfterType`, where `COLLATE` goes;
- `ddl.PartConstraint`, after the default;
- `ddl.PartValueSource`, for an identity: a column with one cannot also have a default;
- `ddl.PartComputed`, for a generation expression: no default, and no `NOT NULL`.

A column clause names its column, and the statement fails at `Build` if it does not define that column.

```go
nocase := ddl.NewColumnClause[sqlite.Dialect](Name, ddl.PartAfterType, func(*sqlb.Args) string { return `COLLATE NOCASE` })
nonEmpty := ddl.NewTableClause[sqlite.Dialect](ddl.PositionElement, func(a *sqlb.Args) string {
	return `CHECK (length(` + Name.SQL(a) + `) > 0)`
})
rowless := ddl.NewTableClause[sqlite.Dialect](ddl.PositionOption, func(*sqlb.Args) string { return `WITHOUT ROWID` })
script, err := sqlite.CreateTable(Items).
	Column(ddl.Column(ID), ddl.Column(Name), ddl.Column(Payload).As(ddl.RawType[[]byte]("BLOB"))).
	Clause(sqlite.PrimaryKey(ID), nocase, nonEmpty, rowless).Build()
// CREATE TABLE "items" ("id" INTEGER NOT NULL, "name" TEXT COLLATE NOCASE NOT NULL, "payload" BLOB NOT NULL,
//   PRIMARY KEY ("id"), CHECK (length("name") > 0)) WITHOUT ROWID
```

`Name.SQL(a)` writes `"name"`, not `"i"."name"`. Nothing in `CREATE TABLE` or `ALTER TABLE` can name a column through an alias, because the statement declares none. So the statement renders every clause, raw ones included, with columns bare. Until 2026-09-22 each built clause did this itself and a raw one did not, and this recipe was the first place that failed. `TestRawClauses_RenderColumnsBare` in `ddl` pins the fix.

### A column type

`ddl.RawType[T]("…")` spells a column's type exactly as written, for a type the portable set does not name. The Go type `T` still has to match the column's `Col[T]`, so the declaration and the query keep agreeing. `contrib/vet`'s DDL check skips the type comparison for a raw spelling and still checks that the column exists and its nullability.

### An expression

Inside a default, a check or a view body, `sqlb.Literal` writes constant SQL text, and the vet's `Literal` analyzer reports any call given a variable. `sqlb.Raw` takes a render function. `pg.Func[R]` calls a function by name with typed arguments; it is how a function created raw is called from a typed query:

```go
var n int64
q, err := pg.Select().Columns(sqlb.Bind(pg.Func[int64]("rr.add", "rr_add", sqlb.Param(int64(2)), sqlb.Param(int64(3))), &n)).Build()
```

### Running it

A raw statement is a `ddl.Statement` like any other, so every runner takes it. There are four:

- `migrate.Execute` runs a `ddl.Script` on one `*sql.Conn`, grouping the transactional statements into transactions;
- `migrate.ExecuteDB` does the same on a `*sql.DB`;
- a `migrate.Ledger` runs a `migrate.Migration`'s script once per database, recording it so the next deploy skips it;
- a loop over `script` calling `db.ExecContext(ctx, st.SQL)` is all a runner has to be.

A DDL statement never takes bound values, because no engine accepts a placeholder in DDL. So `st.SQL` is complete as written.

## Postgres commands

**Empty since 2026-09-24.** Every command in the Postgres 17 and 18 command lists is now built, the operator's, or not DDL: 50 built, 94 the operator's, 40 not DDL, none partial and none left out. The forms that remain unbuilt inside a built statement are in [Limits beyond the detectors](#limits-beyond-the-detectors), because they are limits within a statement rather than a statement nobody can write.

## Postgres ALTER TABLE forms

**Empty since 2026-09-24**: 38 forms built, 18 the operator's, none partial and none left out. The escape hatch below is kept, because an operator's form is still reachable and a future Postgres will add forms before this page is updated. Each raw clause chains with built actions under one lock.

```go
func raw(sql string) pg.AlterClause {
	return ddl.NewAlterClause[pg.Dialect](func(*sqlb.Args) string { return sql })
}
```

## SQLite

**Empty since 2026-09-24.** Every statement in SQLite's language index is built, the operator's, or not DDL: 20 built, none partial, none left out. SQLite's four remaining limits are in [Limits beyond the detectors](#limits-beyond-the-detectors), and all four are the engine's rather than this module's — a clause SQLite parses and ignores, or a form 3.50.3 does not have.

## MySQL

**Empty since 2026-09-24**: every topic in MySQL 8.4's `HELP 'Data Definition'` is built or the operator's — 22 built, 17 the operator's, none partial and none left out. Events and stored routines are built, with their bodies as caller-supplied strings, because MySQL's stored-program language is not SQL a dialect can check. `database/sql` sends each statement whole, so a body with `BEGIN … END` needs no `DELIMITER`: that is a command of the `mysql` client, not SQL. MySQL's thirteen remaining limits are in [Limits beyond the detectors](#limits-beyond-the-detectors).

## Operator and not-DDL entries

These entries are classified but get no section, because nothing is missing that a library ought to write. The appendix gives each one's classification.

`operator` means the statement manages the server, not a library's schema: databases, roles and grants, tablespaces, replication, foreign servers, text-search internals, operator classes, statistics and storage tuning. A library that knows its deployment can still send any of them raw, exactly like the statements above. `TestRawRecipes_Postgres/operator` runs an `ANALYZE` that way.

`not-ddl` means the statement is not schema at all: transactions, cursors, `COPY`, `EXPLAIN`, `SET`, `LISTEN`, `VACUUM`. It belongs in the query layer or the driver, or nowhere. Two of these `pg` builds as statements, `LOCK TABLE` as `pg.NewLockTable` and `TRUNCATE` as `pg.NewTruncate`; the rest are a plain `db.ExecContext`.

`core` and `not-a-statement` are SQLite's index entries for things other than statements: `WITH`, which the query layer builds, and pages about expressions and keywords.

## Limits beyond the detectors

The detectors ask which statements an engine has. These limits are of a different kind: they sit inside statements that are built. Each one is either refused at `Build` or documented where it is used.

Since 2026-09-24 the first three subsections below are a **recorded, gated list**: `LIMITS` in `scripts/gen-not-built.py` holds every form that does not build inside a statement the detector marks `built`, and `task docs:not-built` fails when one of them is missing from this page. That gate exists because the detector's own status grammar cannot express this. An entry reads `built <Name>` and the test checks only that `<Name>` is exported, so `built` has always asserted *this statement has a constructor* rather than *every form of it builds* — and on 2026-09-24 an audit of all three engines found that hole hiding real absences behind `built`, plus one outright bug. Every reason below is the engine ignoring the form, the operator owning it, or a better spelling already built. None is "no consumer needs it", which is not a reason for a limit nobody can see.

### Postgres: forms inside a built statement

| Statement | What is missing | Why |
|---|---|---|
| CREATE TABLE | OF type_name | A typed table takes its column list from a composite type instead of declaring one, and ddl.CreateTable is a column list; AlterTable's OfType reaches the same state from a table whose columns already match. |
| CREATE TABLE | GLOBAL and LOCAL before TEMPORARY | Postgres documents both as accepted for compatibility and ignored, so a keyword that changes nothing would only mislead. |
| FOREIGN KEY | MATCH PARTIAL | Postgres parses it and refuses to implement it, so a constructor for it would be a compile-time promise the server breaks. |
| Everywhere it appears | TABLESPACE | A tablespace names a directory on a machine this code has never seen. |
| ALTER TABLE, ALTER INDEX | SET STATISTICS, attribute options, CLUSTER ON | These tune how the planner samples a table that already exists, and the value that is right depends on the data in one deployment rather than on the schema. |
| CREATE SCHEMA | Nested schema_elements | The nested form exists so the objects belong to the role AUTHORIZATION names; a library installing into its own schema runs its statements as that role or hands them over with SetOwner, and nesting every CREATE inside one statement would be a second way to write every statement this package already builds. |
| CREATE SCHEMA | AUTHORIZATION with no name | A schema whose name is not in the declaration is a schema no Table value can name. |
| CREATE FUNCTION | BEGIN ATOMIC ... END | It is a list of statements rather than a string, and the same function written with a body string under LangSQL is what this builds instead. |
| COMMENT ON | The remaining object kinds | COMMENT ON names a kind for every object Postgres has, and this package comments on the objects it can create; a comment on an object it cannot create is a comment on somebody else's object. |
| Every DROP | Several objects in one statement | SQLite's DROP TABLE takes one name, so every engine gets one statement per table and a runner can report which one failed. |

### SQLite: forms inside a built statement

| Statement | What is missing | Why |
|---|---|---|
| CHECK | The conflict clause | SQLite does not honour it: a column-level one is a syntax error and a table-level one is accepted and ignored, every action behaving as ABORT, so building it would promise behaviour the engine does not perform. |
| FOREIGN KEY | MATCH | SQLite parses MATCH and ignores it, so building it would promise an enforcement the engine does not perform. |
| PRAGMA integrity_check, quick_check | The N argument | The table-valued form the wrapped check is built on reads any argument as a table name and answers 'no such table: 5', measured on 3.50.3, and a cap on how many errors are listed is meaningless under MustReturnNoRows, which asks only whether there is one. |
| ALTER TABLE | ALTER COLUMN SET/DROP NOT NULL | SQLite's current documentation lists it and 3.50.3 does not have it; sqlite.Rebuild is how a column's nullability changes. |

### MySQL: forms inside a built statement

| Statement | What is missing | Why |
|---|---|---|
| Seven object statements | DEFINER = user | A MySQL account is an operator's grant rather than a schema a library ships; the security context a library can choose is SQL SECURITY, which is built. |
| ALTER TABLE | ORDER BY | It reorders rows physically on MyISAM and is ignored by InnoDB, the only engine a library's table uses; it is reachable through RawAlter. |
| CREATE TABLE, ALTER TABLE | The storage-tuning table options | Each is an operator's storage tuning rather than a schema a library ships, and every one is reachable through RawTableOption and RawAlter. |
| CREATE TABLE, ALTER TABLE, CREATE INDEX | ENGINE_ATTRIBUTE and SECONDARY_ENGINE_ATTRIBUTE | Each is a JSON blob whose meaning belongs to a storage engine this module does not target. |
| CREATE TABLE, ALTER TABLE | COLUMN_FORMAT and STORAGE on a column | Both are NDB Cluster storage placement and are ignored by InnoDB. |
| CREATE TABLE | The inline column UNIQUE KEY and PRIMARY KEY | The table-level PrimaryKey and Unique build the same constraint and, unlike the inline form, can be named, which is what DropKey and RenameKey need. |
| FOREIGN KEY | MATCH FULL, PARTIAL, SIMPLE | InnoDB parses it and ignores it, so building it would imply a guarantee no engine here provides. |
| FOREIGN KEY | The optional index name | MySQL names the backing index after the constraint when it is left out, which is what a declared TableConstraint already gives. |
| DROP TABLE, DROP VIEW | RESTRICT and CASCADE | MySQL parses both and does nothing with either. |
| DROP VIEW | Several views in one statement | DropTable's precedent is one statement per object, so a runner can report which one failed. |
| CREATE EVENT, ALTER EVENT | DISABLE ON SLAVE | It is the deprecated spelling of EventDisableOnReplica, which is built. |
| ALTER TABLE | Per-partition DISCARD and IMPORT TABLESPACE | Transportable tablespaces per partition are an operator's backup path; the table-level pair is built. |
| CREATE FUNCTION | The loadable-function form, RETURNS INTEGER SONAME | It is a different statement sharing the name, and it loads a shared library into the server. |
| CREATE PROCEDURE, CREATE FUNCTION | A validated routine body | The body is a caller-supplied string because MySQL's stored-program language is not SQL a dialect can check; the statement around it builds and nothing reads what is inside. |
| CREATE TRIGGER | The procedural syntax itself | StepBlock takes the IF, WHILE, REPEAT, CASE and DECLARE text as an unvalidated opener and closer, so what stays raw is the control flow; every statement inside a block is a typed step, which it was not before 2026-09-24. |

## Future work

Nothing below is scheduled. Following the repository's rule, each item gets built when a consumer needs it, and the detectors will say when it moves.

- **ClickHouse DDL**: `CREATE TABLE` with a required `ENGINE`, `ORDER BY` and `PARTITION BY`, once the core-or-separate question is decided, and after a quoting rule has been verified against a real server.

## Appendix: every detector entry

Generated by `scripts/gen-not-built.py` from the three detectors. Do not edit by hand; `task docs:not-built:gen` rewrites it.

<!-- BEGIN GENERATED DETECTOR APPENDIX -->

### Postgres commands detector

`pgCommands` in `pg/ddl_coverage_test.go` classifies every command in pg/testdata/commands-17.txt and commands-18.txt, the Postgres 17 and 18 SQL command lists: 184 entries, 50 built, 40 not-ddl, 94 operator.

| Entry | Status | Construct or reason |
|---|---|---|
| ABORT | not-ddl |  |
| ALTER AGGREGATE | operator |  |
| ALTER COLLATION | operator |  |
| ALTER CONVERSION | operator |  |
| ALTER DATABASE | operator |  |
| ALTER DEFAULT PRIVILEGES | operator |  |
| ALTER DOMAIN | built | AlterDomain |
| ALTER EVENT TRIGGER | operator |  |
| ALTER EXTENSION | built | UpdateExtension |
| ALTER FOREIGN DATA WRAPPER | operator |  |
| ALTER FOREIGN TABLE | operator |  |
| ALTER FUNCTION | operator |  |
| ALTER GROUP | operator |  |
| ALTER INDEX | built | AlterIndex |
| ALTER LANGUAGE | operator |  |
| ALTER LARGE OBJECT | operator |  |
| ALTER MATERIALIZED VIEW | built | AlterMaterializedView |
| ALTER OPERATOR | operator |  |
| ALTER OPERATOR CLASS | operator |  |
| ALTER OPERATOR FAMILY | operator |  |
| ALTER POLICY | built | AlterPolicy |
| ALTER PROCEDURE | operator |  |
| ALTER PUBLICATION | operator |  |
| ALTER ROLE | operator |  |
| ALTER ROUTINE | operator |  |
| ALTER RULE | operator |  |
| ALTER SCHEMA | built | RenameSchema |
| ALTER SEQUENCE | built | AlterSequence |
| ALTER SERVER | operator |  |
| ALTER STATISTICS | operator |  |
| ALTER SUBSCRIPTION | operator |  |
| ALTER SYSTEM | operator |  |
| ALTER TABLE | built | AlterTable |
| ALTER TABLESPACE | operator |  |
| ALTER TEXT SEARCH CONFIGURATION | operator |  |
| ALTER TEXT SEARCH DICTIONARY | operator |  |
| ALTER TEXT SEARCH PARSER | operator |  |
| ALTER TEXT SEARCH TEMPLATE | operator |  |
| ALTER TRIGGER | built | RenameTrigger |
| ALTER TYPE | built | AlterEnum |
| ALTER USER | operator |  |
| ALTER USER MAPPING | operator |  |
| ALTER VIEW | built | RenameViewColumn |
| ANALYZE | not-ddl |  |
| BEGIN | not-ddl |  |
| CALL | not-ddl |  |
| CHECKPOINT | not-ddl |  |
| CLOSE | not-ddl |  |
| CLUSTER | operator |  |
| COMMENT | built | CommentOn |
| COMMIT | not-ddl |  |
| COMMIT PREPARED | not-ddl |  |
| COPY | not-ddl |  |
| CREATE ACCESS METHOD | operator |  |
| CREATE AGGREGATE | operator |  |
| CREATE CAST | operator |  |
| CREATE COLLATION | built | CreateCollation |
| CREATE CONVERSION | operator |  |
| CREATE DATABASE | operator |  |
| CREATE DOMAIN | built | CreateDomain |
| CREATE EVENT TRIGGER | operator |  |
| CREATE EXTENSION | built | CreateExtension |
| CREATE FOREIGN DATA WRAPPER | operator |  |
| CREATE FOREIGN TABLE | operator |  |
| CREATE FUNCTION | built | CreateFunction |
| CREATE GROUP | operator |  |
| CREATE INDEX | built | CreateIndex |
| CREATE LANGUAGE | operator |  |
| CREATE MATERIALIZED VIEW | built | CreateMaterializedView |
| CREATE OPERATOR | operator |  |
| CREATE OPERATOR CLASS | operator |  |
| CREATE OPERATOR FAMILY | operator |  |
| CREATE POLICY | built | CreatePolicy |
| CREATE PROCEDURE | operator |  |
| CREATE PUBLICATION | operator |  |
| CREATE ROLE | operator |  |
| CREATE RULE | operator |  |
| CREATE SCHEMA | built | CreateSchema |
| CREATE SEQUENCE | built | CreateSequence |
| CREATE SERVER | operator |  |
| CREATE STATISTICS | built | CreateStatistics |
| CREATE SUBSCRIPTION | operator |  |
| CREATE TABLE | built | CreateTable |
| CREATE TABLE AS | built | CreateTableAs |
| CREATE TABLESPACE | operator |  |
| CREATE TEXT SEARCH CONFIGURATION TABLE | operator |  |
| CREATE TEXT SEARCH DICTIONARY | operator |  |
| CREATE TEXT SEARCH PARSER | operator |  |
| CREATE TEXT SEARCH TEMPLATE | operator |  |
| CREATE TRANSFORM | operator |  |
| CREATE TRIGGER | built | CreateTrigger |
| CREATE TYPE | built | CreateRangeType |
| CREATE USER | operator |  |
| CREATE USER MAPPING | operator |  |
| CREATE VIEW | built | CreateView |
| DEALLOCATE | not-ddl |  |
| DECLARE | not-ddl |  |
| DELETE | built | Delete |
| DISCARD | not-ddl |  |
| DO | not-ddl |  |
| DROP ACCESS METHOD | operator |  |
| DROP AGGREGATE | operator |  |
| DROP CAST | operator |  |
| DROP COLLATION | built | DropCollation |
| DROP CONVERSION | operator |  |
| DROP DATABASE | operator |  |
| DROP DOMAIN | built | DropDomain |
| DROP EVENT TRIGGER | operator |  |
| DROP EXTENSION | built | DropExtension |
| DROP FOREIGN DATA WRAPPER | operator |  |
| DROP FOREIGN TABLE | operator |  |
| DROP FUNCTION | built | DropFunction |
| DROP GROUP | operator |  |
| DROP INDEX | built | DropIndex |
| DROP LANGUAGE | operator |  |
| DROP MATERIALIZED VIEW | built | DropMaterializedView |
| DROP OPERATOR | operator |  |
| DROP OPERATOR CLASS | operator |  |
| DROP OPERATOR FAMILY | operator |  |
| DROP OWNED | operator |  |
| DROP POLICY | built | DropPolicy |
| DROP PROCEDURE | operator |  |
| DROP PUBLICATION | operator |  |
| DROP ROLE | operator |  |
| DROP ROUTINE | operator |  |
| DROP RULE | operator |  |
| DROP SCHEMA | built | DropSchema |
| DROP SEQUENCE | built | DropSequence |
| DROP SERVER | operator |  |
| DROP STATISTICS | built | DropStatistics |
| DROP SUBSCRIPTION | operator |  |
| DROP TABLE | built | DropTable |
| DROP TABLESPACE | operator |  |
| DROP TEXT SEARCH CONFIGURATION | operator |  |
| DROP TEXT SEARCH DICTIONARY | operator |  |
| DROP TEXT SEARCH PARSER | operator |  |
| DROP TEXT SEARCH TEMPLATE | operator |  |
| DROP TRANSFORM | operator |  |
| DROP TRIGGER | built | DropTrigger |
| DROP TYPE | built | DropType |
| DROP USER | operator |  |
| DROP USER MAPPING | operator |  |
| DROP VIEW | built | DropView |
| END | not-ddl |  |
| EXECUTE | not-ddl |  |
| EXPLAIN | not-ddl |  |
| FETCH | not-ddl |  |
| GRANT | operator |  |
| IMPORT FOREIGN SCHEMA | operator |  |
| INSERT | built | Insert |
| LISTEN | not-ddl |  |
| LOAD | not-ddl |  |
| LOCK | built | NewLockTable |
| MERGE | built | NewMerge |
| MOVE | not-ddl |  |
| NOTIFY | not-ddl |  |
| PREPARE | not-ddl |  |
| PREPARE TRANSACTION | not-ddl |  |
| REASSIGN OWNED | operator |  |
| REFRESH MATERIALIZED VIEW | built | RefreshMaterializedView |
| REINDEX | operator |  |
| RELEASE SAVEPOINT | not-ddl |  |
| RESET | not-ddl |  |
| REVOKE | operator |  |
| ROLLBACK | not-ddl |  |
| ROLLBACK PREPARED | not-ddl |  |
| ROLLBACK TO SAVEPOINT | not-ddl |  |
| SAVEPOINT | not-ddl |  |
| SECURITY LABEL | operator |  |
| SELECT | built | Select |
| SELECT INTO | not-ddl |  |
| SET | not-ddl |  |
| SET CONSTRAINTS | not-ddl |  |
| SET ROLE | not-ddl |  |
| SET SESSION AUTHORIZATION | not-ddl |  |
| SET TRANSACTION | not-ddl |  |
| SHOW | not-ddl |  |
| START TRANSACTION | not-ddl |  |
| TRUNCATE | built | NewTruncate |
| UNLISTEN | not-ddl |  |
| UPDATE | built | Update |
| VACUUM | not-ddl |  |
| VALUES | built | Values |
| WITH | not-ddl |  |

### Postgres ALTER TABLE forms detector

`pgAlterTable` in `pg/ddl_coverage_test.go` classifies every action in pg/testdata/alter-table-17.txt and alter-table-18.txt, the Postgres 17 and 18 ALTER TABLE synopses: 56 entries, 38 built, 18 operator.

| Entry | Status | Construct or reason |
|---|---|---|
| ADD | built | AddUniqueUsingIndex |
| ADD COLUMN IF NOT EXISTS COLLATE | built | AddColumnIfNotExists |
| ADD NOT VALID | built | AddConstraintNotValid |
| ALTER COLUMN ADD GENERATED ALWAYS BY DEFAULT AS IDENTITY | built | AddIdentity |
| ALTER COLUMN DROP DEFAULT | built | DropDefault |
| ALTER COLUMN DROP EXPRESSION IF EXISTS | built | DropExpression |
| ALTER COLUMN DROP IDENTITY IF EXISTS | built | DropIdentity |
| ALTER COLUMN RESET | operator |  |
| ALTER COLUMN SET | operator |  |
| ALTER COLUMN SET COMPRESSION | built | SetColumnCompression |
| ALTER COLUMN SET DATA TYPE COLLATE USING | built | AlterType |
| ALTER COLUMN SET DEFAULT | built | SetDefault |
| ALTER COLUMN SET DROP NOT NULL | built | SetNotNull |
| ALTER COLUMN SET EXPRESSION AS | built | SetExpression |
| ALTER COLUMN SET GENERATED ALWAYS BY DEFAULT SET RESTART WITH | built | AlterIdentitySequence |
| ALTER COLUMN SET STATISTICS DEFAULT | operator |  |
| ALTER COLUMN SET STORAGE PLAIN EXTERNAL EXTENDED MAIN DEFAULT | built | SetColumnStorage |
| ALTER CONSTRAINT DEFERRABLE NOT DEFERRABLE INITIALLY DEFERRED INITIALLY IMMEDIATE | built | AlterConstraint |
| ALTER CONSTRAINT DEFERRABLE NOT DEFERRABLE INITIALLY DEFERRED INITIALLY IMMEDIATE ENFORCED NOT ENFORCED | built | AlterConstraint |
| ALTER CONSTRAINT INHERIT NO INHERIT | built | AlterConstraint |
| ATTACH PARTITION FOR VALUES DEFAULT | built | AttachPartition |
| CLUSTER ON | operator |  |
| DETACH PARTITION CONCURRENTLY FINALIZE | built | DetachPartition |
| DISABLE ROW LEVEL SECURITY | built | DisableRowLevelSecurity |
| DISABLE RULE | built | DisableRule |
| DISABLE TRIGGER ALL USER | operator |  |
| DROP COLUMN IF EXISTS RESTRICT CASCADE | built | DropColumnIfExists |
| DROP CONSTRAINT IF EXISTS RESTRICT CASCADE | built | DropConstraint |
| ENABLE ALWAYS RULE | built | EnableAlwaysRule |
| ENABLE ALWAYS TRIGGER | operator |  |
| ENABLE REPLICA RULE | built | EnableReplicaRule |
| ENABLE REPLICA TRIGGER | operator |  |
| ENABLE ROW LEVEL SECURITY | built | EnableRowLevelSecurity |
| ENABLE RULE | built | EnableRule |
| ENABLE TRIGGER ALL USER | operator |  |
| FORCE ROW LEVEL SECURITY | built | ForceRowLevelSecurity |
| INHERIT | built | Inherit |
| NO FORCE ROW LEVEL SECURITY | built | NoForceRowLevelSecurity |
| NO INHERIT | built | NoInherit |
| NOT OF | built | NotOfType |
| OF | built | OfType |
| OWNER TO CURRENT_ROLE CURRENT_USER SESSION_USER | operator |  |
| RENAME COLUMN TO | built | RenameColumn |
| RENAME CONSTRAINT TO | built | RenameConstraint |
| RENAME TO | built | RenameTo |
| REPLICA IDENTITY DEFAULT USING INDEX FULL NOTHING | operator |  |
| RESET | operator |  |
| SET | operator |  |
| SET ACCESS METHOD DEFAULT | operator |  |
| SET LOGGED UNLOGGED | operator |  |
| SET SCHEMA | built | SetSchema |
| SET TABLESPACE | operator |  |
| SET TABLESPACE NOWAIT | operator |  |
| SET WITHOUT CLUSTER | operator |  |
| SET WITHOUT OIDS | operator |  |
| VALIDATE CONSTRAINT | built | ValidateConstraint |

### SQLite detector

`sqliteIndex` in `sqlite/ddl_coverage_test.go` classifies every entry in sqlite/testdata/lang-index.txt, SQLite's language index: 41 entries, 20 built, 1 core, 8 not-a-statement, 7 not-ddl, 5 operator.

| Entry | Status | Construct or reason |
|---|---|---|
| ALTER TABLE | built | AlterTable |
| ANALYZE | operator |  |
| ATTACH DATABASE | operator |  |
| BEGIN TRANSACTION | not-ddl |  |
| COMMIT TRANSACTION | not-ddl |  |
| CREATE INDEX | built | CreateIndex |
| CREATE TABLE | built | CreateTable |
| CREATE TRIGGER | built | CreateTrigger |
| CREATE VIEW | built | CreateView |
| CREATE VIRTUAL TABLE | built | CreateVirtualTable |
| DELETE | built | Delete |
| DETACH DATABASE | operator |  |
| DROP INDEX | built | DropIndex |
| DROP TABLE | built | DropTable |
| DROP TRIGGER | built | DropTrigger |
| DROP VIEW | built | DropView |
| END TRANSACTION | not-ddl |  |
| EXPLAIN | not-ddl |  |
| INDEXED BY | built | IndexedBy |
| INSERT | built | Insert |
| ON CONFLICT clause | built | OnConflict |
| PRAGMA | built | SetJournalMode |
| REINDEX | operator |  |
| RELEASE SAVEPOINT | not-ddl |  |
| REPLACE | built | InsertOr |
| RETURNING clause | built | InsertReturning |
| ROLLBACK TRANSACTION | not-ddl |  |
| SAVEPOINT | not-ddl |  |
| SELECT | built | Select |
| SQL keywords | not-a-statement |  |
| UPDATE | built | Update |
| UPSERT | built | OnConflict |
| VACUUM | operator |  |
| WITH clause | core | With |
| aggregate functions | not-a-statement |  |
| comment | not-a-statement |  |
| core functions | not-a-statement |  |
| date and time functions | not-a-statement |  |
| expression | not-a-statement |  |
| keywords | not-a-statement |  |
| math functions | not-a-statement |  |

### MySQL detector

`mysqlDDL` in `mysql/ddl_coverage_test.go` classifies every topic in mysql/testdata/ddl-8.4.txt, MySQL 8.4's HELP 'Data Definition': 39 entries, 22 built, 17 operator.

| Entry | Status | Construct or reason |
|---|---|---|
| ALTER DATABASE | operator |  |
| ALTER EVENT | built | AlterEvent |
| ALTER FUNCTION | built | AlterFunction |
| ALTER INSTANCE | operator |  |
| ALTER LOGFILE GROUP | operator |  |
| ALTER PROCEDURE | built | AlterProcedure |
| ALTER SCHEMA | operator |  |
| ALTER SERVER | operator |  |
| ALTER TABLE | built | AlterTable |
| ALTER TABLESPACE | operator |  |
| ALTER VIEW | built | AlterView |
| CREATE DATABASE | operator |  |
| CREATE EVENT | built | CreateEvent |
| CREATE FUNCTION | built | CreateFunction |
| CREATE INDEX | built | CreateIndex |
| CREATE LOGFILE GROUP | operator |  |
| CREATE PROCEDURE | built | CreateProcedure |
| CREATE SCHEMA | operator |  |
| CREATE SERVER | operator |  |
| CREATE SPATIAL REFERENCE SYSTEM | operator |  |
| CREATE TABLE | built | CreateTable |
| CREATE TABLESPACE | operator |  |
| CREATE TRIGGER | built | CreateTrigger |
| CREATE VIEW | built | CreateView |
| DROP DATABASE | operator |  |
| DROP EVENT | built | DropEvent |
| DROP FUNCTION | built | DropFunction |
| DROP INDEX | built | DropIndex |
| DROP PROCEDURE | built | DropProcedure |
| DROP SCHEMA | operator |  |
| DROP SERVER | operator |  |
| DROP SPATIAL REFERENCE SYSTEM | operator |  |
| DROP TABLE | built | DropTable |
| DROP TABLESPACE | operator |  |
| DROP TRIGGER | built | DropTrigger |
| DROP VIEW | built | DropView |
| FOREIGN KEY | built | ForeignKey |
| RENAME TABLE | built | RenameTable |
| TRUNCATE TABLE | built | Truncate |

<!-- END GENERATED DETECTOR APPENDIX -->
