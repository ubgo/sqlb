package packages_test

import (
	"errors"
	"fmt"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/sqlite"
)

// The tables these examples create. Declared here rather than beside the
// accounts table, because the point of each example is one ordering rule and
// a second table keeps the output short enough to read.
var (
	events    = expr.Table{Name: "event", Alias: "e"}
	evtID     = expr.Col[int64](events, "id")
	evtBody   = expr.Col[string](events, "body")
	evtParent = expr.Table{Name: "event_base"}
	notes     = expr.Table{Name: "note"}
	noteID    = expr.Col[int64](notes, "id")
	noteBody  = expr.Col[string](notes, "body")
)

// ExampleOptionRank shows the fix for clause order on Postgres. Every table
// option and every column clause below is passed in REVERSE of the order
// Postgres's grammar requires, and the statement still comes out in that
// order: INHERITS, USING, WITH after the parenthesis, and STORAGE,
// COMPRESSION, COLLATE inside the column. Written as passed, Postgres would
// answer `syntax error at or near "USING"`. See "Clause order is the engine's" in
// docs/ddl/README.md, and ddl.OptionRank, for why the statement sorts
// rather than refusing.
func ExampleOptionRank() {
	printScript(pg.CreateTable(events).Column(ddl.Column(evtID), ddl.Column(evtBody)).Clause(
		pg.TableStorage(pg.IntParameter("fillfactor", 70)),
		pg.TableAccessMethod(pg.Heap),
		pg.Inherits(evtParent),
		pg.ColumnCollate(evtBody, "C"),
		pg.ColumnCompression(evtBody, "pglz"),
		pg.ColumnStorage(evtBody, pg.StorageExternal),
	).Build())
	// Output:
	// CREATE TABLE "event" ("id" BIGINT NOT NULL, "body" TEXT STORAGE external COMPRESSION "pglz" COLLATE "C" NOT NULL) INHERITS ("event_base") USING "heap" WITH ("fillfactor" = 70)
}

// ExampleOptionRank_mysql shows the MySQL half. PARTITION BY is passed first
// and MySQL takes it only last, so it moves there. The three table options
// are left in the order they were passed, because MySQL takes table options in
// any order: they share one rank, and the sort is stable.
func ExampleOptionRank_mysql() {
	printScript(mysql.CreateTable(events).Column(ddl.Column(evtID)).Clause(
		mysql.PartitionByHash(mysql.PartitionOn(evtID), 4),
		mysql.TableComment("audit trail"),
		mysql.DefaultCharset("utf8mb4"),
		mysql.InnoDB(),
	).Build())
	// Output:
	// CREATE TABLE `event` (`id` BIGINT NOT NULL) COMMENT = 'audit trail' DEFAULT CHARSET = `utf8mb4` ENGINE = InnoDB PARTITION BY HASH (`id`) PARTITIONS 4
}

// ExampleOptionRank_unranked shows the rule for an option the module does not
// understand: a clause made with NewTableClause carries no rank, so it stays
// exactly where the caller put it and only the ranked options move around it.
// Here the raw TABLESPACE is placed last by the caller and stays last, which
// is where Postgres needs it; the two ranked options between them are sorted.
func ExampleOptionRank_unranked() {
	tablespace := ddl.NewTableClause[postgres.Dialect](ddl.PositionOption, func(*render.Args) string { return "TABLESPACE fast_ssd" })
	printScript(pg.CreateTable(events).Column(ddl.Column(evtID)).Clause(
		pg.TableStorage(pg.IntParameter("fillfactor", 70)),
		pg.TableAccessMethod(pg.Heap),
		tablespace,
	).Build())
	// Output:
	// CREATE TABLE "event" ("id" BIGINT NOT NULL) USING "heap" WITH ("fillfactor" = 70) TABLESPACE fast_ssd
}

// ExampleErrNoDefault shows the refusal for SQLite's NOT NULL ON CONFLICT
// REPLACE on a column with no default. REPLACE writes the default in place of
// a NULL; with none, SQLite silently behaves as ABORT, and it accepts the
// definition, so the mistake would only show on the first NULL written in
// production. Build refuses it instead, and the same clause on a column that
// declares a default builds. See ddl.DefaultRequirer for why this is
// refused at Build rather than documented.
func ExampleErrNoDefault() {
	_, err := sqlite.CreateTable(notes).Column(ddl.Column(noteID), ddl.Column(noteBody)).
		Clause(sqlite.NotNullOnConflict(noteBody, sqlite.OrReplace)).Build()
	fmt.Println(errors.Is(err, ddl.ErrNoDefault))

	printScript(sqlite.CreateTable(notes).Column(ddl.Column(noteID), ddl.Column(noteBody).Default("untitled")).
		Clause(sqlite.NotNullOnConflict(noteBody, sqlite.OrReplace)).Build())
	// Output:
	// true
	// CREATE TABLE "note" ("id" INTEGER NOT NULL, "body" TEXT NOT NULL ON CONFLICT REPLACE DEFAULT 'untitled')
}
