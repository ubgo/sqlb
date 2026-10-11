package packages_test

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/dialect/sqlite"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/vector"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// The columns a library already exports for its queries. The DDL snippets
// create the table from these same values, which is the point: there is no
// second declaration of the table to drift from the first.
var (
	accounts   = expr.Table{Name: "account", Alias: "a"}
	accID      = expr.Col[int64](accounts, "id")
	accEmail   = expr.Col[string](accounts, "email")
	accNick    = expr.Col[*string](accounts, "nick")
	accHandle  = expr.Col[*string](accounts, "handle")
	accStatus  = expr.Col[string](accounts, "status")
	accPhone   = expr.Col[*string](accounts, "phone")
	accBalance = expr.Col[float64](accounts, "balance")
	accCreated = expr.Col[time.Time](accounts, "created_at")
)

func printScript(s ddl.Script, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(strings.Join(s.SQL(), ";\n"))
}

// ExampleNewCreateTable renders one CREATE TABLE for two engines from one
// declaration. The column types come from the Go types, nullability from
// whether the type is a pointer, and the default is written as a literal
// because no engine accepts a placeholder in DDL.
func ExampleNewCreateTable() {
	cols := []ddl.ColumnSpec{
		ddl.Column(accID), ddl.Column(accEmail), ddl.Column(accNick),
		ddl.Column(accStatus).Default("active"),
	}
	printScript(ddl.NewCreateTable(postgres.Dialect{}, accounts).IfNotExists().Column(cols[0], cols[1:]...).Build())
	printScript(ddl.NewCreateTable(sqlite.Dialect{}, accounts).IfNotExists().Column(cols[0], cols[1:]...).Build())
	// Output:
	// CREATE TABLE IF NOT EXISTS "account" ("id" BIGINT NOT NULL, "email" TEXT NOT NULL, "nick" TEXT, "status" TEXT NOT NULL DEFAULT 'active')
	// CREATE TABLE IF NOT EXISTS "account" ("id" INTEGER NOT NULL, "email" TEXT NOT NULL, "nick" TEXT, "status" TEXT NOT NULL DEFAULT 'active')
}

// ExampleColumnDef_As overrides the type inferred from Go: a portable name the
// schema table spells per engine, or a spelling written out for one engine.
func ExampleColumnDef_As() {
	printScript(ddl.NewCreateTable(postgres.Dialect{}, accounts).Column(
		ddl.Column(accBalance).As(ddl.Portable[float64](schema.TypeDecimal)),
		ddl.Column(accEmail).As(ddl.RawType[string]("citext")),
	).Build())
	// Output:
	// CREATE TABLE "account" ("balance" NUMERIC NOT NULL, "email" citext NOT NULL)
}

// ExampleColumnDef_DefaultExpr takes an expression rather than a value. It is
// parenthesised, which SQLite requires of any default that is not a literal
// and Postgres accepts.
func ExampleColumnDef_DefaultExpr() {
	printScript(ddl.NewCreateTable(sqlite.Dialect{}, accounts).Column(
		ddl.Column(accCreated).DefaultExpr(expr.Literal[time.Time]("CURRENT_TIMESTAMP")),
	).Build())
	// Output:
	// CREATE TABLE "account" ("created_at" TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP))
}

// ExampleOneOf writes a CHECK constraint from a column and the values it may
// hold. Inside a CHECK the column is written bare, because a table's own
// constraint cannot name it through an alias.
func ExampleOneOf() {
	printScript(ddl.NewCreateTable(postgres.Dialect{}, accounts).
		Column(ddl.Column(accStatus)).
		Constraint(ddl.OneOf(ddl.TableConstraint{Table: accStatus.Table(), Name: "account_status_known"}, accStatus, "active", "banned")).Build())
	// Output:
	// CREATE TABLE "account" ("status" TEXT NOT NULL, CONSTRAINT "account_status_known" CHECK ("status" IN ('active', 'banned')))
}

// ExampleNewAlterTable evolves the table. Postgres joins actions into one
// statement, but never a RENAME, which its grammar keeps apart; SQLite takes
// one action per statement. The code is the same for both.
func ExampleNewAlterTable() {
	actions := []ddl.Action{
		ddl.AddColumn(ddl.Column(accPhone)),
		ddl.AddColumn(ddl.Column(accBalance).Default(0)),
		ddl.RenameColumn(accNick, accHandle),
		ddl.DropColumn(accEmail),
	}
	printScript(ddl.NewAlterTable(postgres.Dialect{}, accounts).Action(actions[0], actions[1:]...).Build())
	fmt.Println("--")
	printScript(ddl.NewAlterTable(sqlite.Dialect{}, accounts).Action(actions[0], actions[1:]...).Build())
	// Output:
	// ALTER TABLE "account" ADD COLUMN "phone" TEXT, ADD COLUMN "balance" DOUBLE PRECISION NOT NULL DEFAULT 0;
	// ALTER TABLE "account" RENAME COLUMN "nick" TO "handle";
	// ALTER TABLE "account" DROP COLUMN "email"
	// --
	// ALTER TABLE "account" ADD COLUMN "phone" TEXT;
	// ALTER TABLE "account" ADD COLUMN "balance" REAL NOT NULL DEFAULT 0;
	// ALTER TABLE "account" RENAME COLUMN "nick" TO "handle";
	// ALTER TABLE "account" DROP COLUMN "email"
}

// ExampleNewDropTable drops each table in its own statement, because SQLite's
// DROP TABLE takes one name.
func ExampleNewDropTable() {
	sessions := expr.Table{Name: "session"}
	printScript(ddl.NewDropTable(sqlite.Dialect{}, sessions, accounts).IfExists().Build())
	// Output:
	// DROP TABLE IF EXISTS "session";
	// DROP TABLE IF EXISTS "account"
}

// ExampleColumnDef_Default_refused shows a value the dialect cannot write as a
// literal. SQLite has no time type, and the literal it would get matches no
// time the driver stores, so Build refuses instead of rendering a default that
// never compares equal.
func ExampleColumnDef_Default_refused() {
	_, err := ddl.NewCreateTable(sqlite.Dialect{}, accounts).
		Column(ddl.Column(accCreated).Default(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))).Build()
	fmt.Println(errors.Is(err, render.ErrNotInlinable))
	// Output:
	// true
}

// ExampleNewTableClause is how an engine package adds a clause to CREATE TABLE
// without this package knowing it exists. The dialect is in the type, so a
// Postgres clause on a SQLite statement does not compile.
func ExampleNewTableClause() {
	unlogged := ddl.NewTableClause[postgres.Dialect](ddl.PositionModifier, func(*render.Args) string { return "UNLOGGED" })
	printScript(ddl.NewCreateTable(postgres.Dialect{}, accounts).Clause(unlogged).Column(ddl.Column(accID)).Build())
	// Output:
	// CREATE UNLOGGED TABLE "account" ("id" BIGINT NOT NULL)
}

// ExampleNewColumnClause is how an engine package adds something to one
// column's definition. The clause names its column, the statement routes it
// there, and a clause naming a column the table does not define fails Build.
func ExampleNewColumnClause() {
	collate := ddl.NewColumnClause[postgres.Dialect](accEmail, ddl.PartAfterType, func(*render.Args) string { return `COLLATE "C"` })
	printScript(ddl.NewCreateTable(postgres.Dialect{}, accounts).Column(ddl.Column(accEmail)).Clause(collate).Build())
	_, err := ddl.NewCreateTable(postgres.Dialect{}, accounts).Column(ddl.Column(accID)).Clause(collate).Build()
	fmt.Println(errors.Is(err, ddl.ErrUnknownColumn))
	// Output:
	// CREATE TABLE "account" ("email" TEXT COLLATE "C" NOT NULL)
	// true
}

// Example_postgresKeys is the Postgres table a library actually ships: an
// identity key, a generated column, a unique constraint and a foreign key,
// with every column named once, in its Col declaration.
func Example_postgresKeys() {
	users := expr.Table{Name: "users"}
	userID := expr.Col[int64](users, "id")
	email := expr.Col[string](users, "email")
	emailLower := expr.Col[string](users, "email_lower")
	sessions := expr.Table{Name: "sessions"}
	sessionUser := expr.Col[int64](sessions, "user_id")

	printScript(pg.CreateTable(users).IfNotExists().
		Column(ddl.Column(userID), ddl.Column(email), ddl.Column(emailLower)).
		Clause(
			pg.Identity(userID, pg.Always),
			pg.GeneratedStored(emailLower, expr.Lower(email)),
			pg.PrimaryKey(userID),
			pg.Unique(pg.TableConstraint{Table: emailLower.Table(), Name: "users_email_lower_key"}, emailLower),
		).Build())
	printScript(pg.CreateTable(sessions).IfNotExists().
		Column(ddl.Column(sessionUser)).
		Clause(pg.References(sessionUser, userID).OnDelete(pg.RefCascade)).Build())
	// Output:
	// CREATE TABLE IF NOT EXISTS "users" ("id" BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY, "email" TEXT NOT NULL, "email_lower" TEXT GENERATED ALWAYS AS (lower("email")) STORED, PRIMARY KEY ("id"), CONSTRAINT "users_email_lower_key" UNIQUE ("email_lower"))
	// CREATE TABLE IF NOT EXISTS "sessions" ("user_id" BIGINT NOT NULL, FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE CASCADE)
}

// Example_postgresAlter adds a NOT NULL column to a table that has rows, the
// three steps it takes, and adds a foreign key without the long lock. Joinable
// actions share one statement; a rename is always its own.
func Example_postgresAlter() {
	users := expr.Table{Name: "users"}
	userID := expr.Col[int64](users, "id")
	tzNullable := expr.Col[*string](users, "tz")
	tz := expr.Col[string](users, "tz")
	sessions := expr.Table{Name: "sessions"}
	sessionUser := expr.Col[int64](sessions, "user_id")

	printScript(pg.AlterTable(users).Clause(pg.AddColumnIfNotExists(ddl.Column(tzNullable))).Build())
	// ... backfill with pg.Update(users).Set(sqlb.Set(tz, "UTC")) ...
	printScript(pg.AlterTable(users).Clause(pg.SetNotNull(tz), pg.SetDefault(tz, "UTC")).Build())
	// Two builds, run as two transactions: one statement would hold the strong
	// lock through the validating scan, which is what NOT VALID exists to avoid.
	printScript(pg.AlterTable(sessions).Clause(
		pg.AddConstraintNotValid(pg.ForeignKey(pg.TableConstraint{Table: sessionUser.Table(), Name: "sessions_user_fk"}, pg.RefPair(sessionUser, userID))),
	).Build())
	printScript(pg.AlterTable(sessions).Clause(
		pg.ValidateConstraint(pg.TableConstraint{Table: sessions, Name: "sessions_user_fk"}),
		pg.RenameConstraint(pg.TableConstraint{Table: sessions, Name: "sessions_user_fk"}, pg.TableConstraint{Table: sessions, Name: "sessions_owner_fk"}),
	).Build())
	// Output:
	// ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "tz" TEXT
	// ALTER TABLE "users" ALTER COLUMN "tz" SET NOT NULL, ALTER COLUMN "tz" SET DEFAULT 'UTC'
	// ALTER TABLE "sessions" ADD CONSTRAINT "sessions_user_fk" FOREIGN KEY ("user_id") REFERENCES "users" ("id") NOT VALID
	// ALTER TABLE "sessions" VALIDATE CONSTRAINT "sessions_user_fk";
	// ALTER TABLE "sessions" RENAME CONSTRAINT "sessions_user_fk" TO "sessions_owner_fk"
}

// status is a closed set a library ships as a Postgres enum.
type status string

const (
	statusActive status = "active"
	statusBanned status = "banned"
)

// Example_postgresObjects installs what a library's tables need around them:
// a schema, an extension, an enum made safe to repeat with Guarded, a table
// using the enum, a partial index, a vector index and a comment.
func Example_postgresObjects() {
	auth := pg.Schema{Name: "auth"}
	statusType := pg.Enum[status]{Schema: "auth", Name: "user_status"}
	users := expr.Table{Schema: "auth", Name: "users"}
	email := expr.Col[string](users, "email")
	stat := expr.Col[status](users, "status")
	deletedAt := expr.Col[*time.Time](users, "deleted_at")
	docs := expr.Table{Schema: "auth", Name: "documents"}
	embedding := expr.Col[vector.Vector](docs, "embedding")

	for _, b := range []interface{ Build() (ddl.Script, error) }{
		pg.CreateSchema(auth).IfNotExists(),
		pg.CreateExtension(vector.Extension).IfNotExists(),
		pg.Guarded(pg.CreateEnum(statusType, statusActive, statusBanned)),
		pg.CreateTable(users).IfNotExists().Column(ddl.Column(email), ddl.Column(stat).As(statusType).Default(statusActive), ddl.Column(deletedAt)),
		pg.CreateIndex(pg.Index{Schema: "auth", Name: "users_live_email"}, users).Unique().IfNotExists().
			On(pg.IndexExpr(expr.Lower(email))).Where(expr.IsNull(deletedAt)),
		pg.CreateIndex(pg.Index{Schema: "auth", Name: "documents_embedding"}, docs).IfNotExists().
			Using(vector.HNSW).On(pg.IndexKey(embedding).Opclass(vector.CosineOps)).With(vector.M(16)),
		pg.CommentOn(statusType).Is("Account lifecycle; add a value with AlterEnum."),
	} {
		printScript(b.Build())
	}
	// Output:
	// CREATE SCHEMA IF NOT EXISTS "auth"
	// CREATE EXTENSION IF NOT EXISTS "vector"
	// DO $sqlb$ BEGIN CREATE TYPE "auth"."user_status" AS ENUM ('active', 'banned'); EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
	// CREATE TABLE IF NOT EXISTS "auth"."users" ("email" TEXT NOT NULL, "status" "auth"."user_status" NOT NULL DEFAULT 'active', "deleted_at" TIMESTAMPTZ)
	// CREATE UNIQUE INDEX IF NOT EXISTS "users_live_email" ON "auth"."users" ((lower("email"))) WHERE "deleted_at" IS NULL
	// CREATE INDEX IF NOT EXISTS "documents_embedding" ON "auth"."documents" USING "hnsw" ("embedding" "vector_cosine_ops") WITH ("m" = 16)
	// COMMENT ON TYPE "auth"."user_status" IS 'Account lifecycle; add a value with AlterEnum.'
}
