//go:build typecheck

// Package main must NOT compile. The gate builds it with -tags typecheck and
// fails if the build succeeds. Each statement below is a mistake the type
// system is claimed to reject; if one of them starts compiling, the claim is
// false and the gate says so.
package main

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ch"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/vector"
	"github.com/ubgo/sqlb/sqlite"
)

type status string

var t = sqlb.Table{Name: "t", Alias: "t"}

var (
	name = sqlb.Col[string](t, "name")
	at   = sqlb.Col[time.Time](t, "at")
	flag = sqlb.Col[bool](t, "flag")
	emb  = sqlb.Col[vector.Vector](t, "emb")
)

func main() {
	var n int
	_ = sqlb.Bind(name, &n)                           // string column into *int
	_ = sqlb.Eq(at, "yesterday")                      // time column compared with a string
	_ = sqlb.EqExpr(name, flag)                       // join between string and bool
	_ = vector.L2(name, emb)                          // distance over a string column
	_ = sqlb.Coalesce(at, sqlb.Literal[string]("''")) // mismatched fallback
	_ = sqlb.Bind(sqlb.RowNumber(), &n)               // window function without OVER is not an Expr
	_ = sqlb.Set(at, "2026-01-01")                    // assignment of a string to a time column
	_ = sqlb.Sum(name)                                // sum over a non-numeric column

	// Typestate on writes: Build exists only on a complete statement.
	var d sqlb.PostgresDialect
	_, _ = sqlb.NewUpdate(d).Table(t).Set(sqlb.Set(flag, true)).Build() // UPDATE with no WHERE and no All()
	_, _ = sqlb.NewDelete(d).From(t).Build()                            // DELETE with no WHERE and no All()
	_, _ = sqlb.NewInsert(d).Into(t).Build()                            // INSERT with no rows, no query, no body

	// Engine clauses are typed to their dialect and statement kind.
	var lite sqlb.SQLiteDialect
	_ = sqlb.NewSelect(lite).Exprs(name).From(t).Clause(pg.ForUpdate())                        // Postgres clause on a SQLite statement
	_ = sqlb.NewSelect(d).Exprs(name).From(t).Clause(pg.UpdateFrom(t))                         // UPDATE clause on a SELECT
	_ = sqlb.NewInsert(lite).Into(t).Row(sqlb.Set(name, "a")).Clause(pg.OverridingUserValue()) // Postgres INSERT clause on SQLite
	_ = sqlb.NewSelect(d).Exprs(name).From(t).Clause(sqlite.IndexedBy("i"))                    // SQLite hint on a Postgres statement
	_ = sqlb.NewSelect(d).Exprs(name).From(t).Clause(ch.Final())                               // ClickHouse clause on a Postgres statement
	var runtime sqlb.Dialect = d
	_ = sqlb.NewSelect(runtime).Exprs(name).From(t).Clause(pg.ForUpdate()) // engine clause on a runtime-chosen dialect

	// Engine statement kinds are typed to their dialect, like engine clauses.
	_ = pg.NewTruncate(lite, t)  // TRUNCATE for SQLite, which has none
	_ = pg.NewLockTable(lite, t) // LOCK TABLE for SQLite, which has no table locks
	_ = pg.NewMerge(runtime)     // MERGE for a dialect chosen at runtime

	// DDL: an incomplete statement has no Build, and every value and clause is typed.
	_, _ = pg.CreateTable(t).Build()                                                                                                        // CREATE TABLE with no column
	_, _ = pg.AlterTable(t).Build()                                                                                                         // ALTER TABLE with no action
	_ = pg.CreateTable(t).Column()                                                                                                          // Column needs at least one
	_ = ddl.Column(name).Default(42)                                                                                                        // an int default on a string column
	_ = ddl.Column(name).As(ddl.RawType[int]("integer"))                                                                                    // an int type on a string column
	_ = ddl.RenameColumn(name, flag)                                                                                                        // a rename that changes the Go type
	_ = pg.CreateTable(t).Column(ddl.Column(name)).Clause(ddl.NewTableClause[sqlb.SQLiteDialect](ddl.PositionOption, nil))                  // a SQLite table clause on Postgres
	_ = pg.AlterTable(t).Clause(ddl.NewAlterClause[sqlb.SQLiteDialect](nil))                                                                // a SQLite ALTER action on Postgres
	_ = sqlite.CreateTable(t).Column(ddl.Column(name)).Clause(pg.PrimaryKey(name))                                                          // a Postgres key on a SQLite table
	_ = pg.RefPair(name, flag)                                                                                                              // a text column referencing a boolean one
	_ = pg.References(name, at)                                                                                                             // a text column referencing a timestamp
	_ = pg.Identity(name, pg.Always)                                                                                                        // an identity on a text column
	_ = pg.GeneratedStored(name, flag)                                                                                                      // a generated value of another Go type
	_ = pg.ColumnCollate(flag, "C")                                                                                                         // a collation on a boolean column
	_ = sqlite.DropTable(t).Clause(pg.Cascade())                                                                                            // CASCADE on SQLite, which has none
	_ = pg.AddConstraintNotValid(pg.Unique(pg.TableConstraint{Table: name.Table(), Name: "k"}, name))                                       // NOT VALID on a unique key, which Postgres refuses
	_ = pg.SetDefault(name, 42)                                                                                                             // an int default on a text column
	_ = pg.AlterType(name, ddl.RawType[int]("integer"))                                                                                     // an int type for a text column
	_ = pg.AlterType(name, ddl.RawType[string]("text")).Using(flag)                                                                         // a USING of another Go type
	_ = pg.AddIdentity(name, pg.Always)                                                                                                     // an identity added to a text column
	_ = sqlite.AlterTable(t).Clause(pg.SetNotNull(name))                                                                                    // a Postgres action on a SQLite table
	_ = pg.CreateEnum(pg.Enum[status]{Name: "s"}, "active", 42)                                                                             // a label that is not the enum's Go type
	_ = ddl.Column(name).As(pg.Enum[status]{Name: "s"})                                                                                     // an enum type on a column of another Go type
	_, _ = pg.CreateIndex(pg.Index{Name: "i"}, t).Build()                                                                                   // CREATE INDEX with no key
	_ = pg.IndexKey(name).Opclass(pg.JSONBPathOps)                                                                                          // a jsonb operator class on a text column
	_ = pg.IndexKey(emb).Opclass(vector.HalfCosineOps)                                                                                      // a halfvec operator class on a vector column
	_, _ = pg.CommentOnTable(t).Build()                                                                                                     // COMMENT ON with no text
	_ = pg.Guarded(pg.CreateSchema(pg.Schema{Name: "s"}))                                                                                   // Guarded on a statement that has IF NOT EXISTS
	_ = pg.AddConstraintNotValid(pg.Exclude(pg.TableConstraint{Table: name.Table(), Name: "x"}, pg.GiST, pg.ExcludeOn(name, pg.OpEquals)))  // NOT VALID on an exclusion constraint, which Postgres refuses
	_ = pg.Sequence[string]{Name: "s"}                                                                                                      // a sequence of text
	_ = ddl.Column(name).DefaultExpr(pg.NextVal(pg.Sequence[int64]{Name: "s"}))                                                             // a bigint sequence as a text default
	_ = pg.AlterSequence(pg.Sequence[int64]{Name: "s"}).OwnedBy(name)                                                                       // a sequence owned by a text column
	_ = pg.CreateView(pg.View{Name: "v"}).As(sqlite.Select(name).From(t))                                                                   // a SQLite body for a Postgres view
	_, _ = pg.CreateView(pg.View{Name: "v"}).Build()                                                                                        // CREATE VIEW with no body
	_, _ = pg.AlterSequence(pg.Sequence[int64]{Name: "s"}).Build()                                                                          // ALTER SEQUENCE with no action
	_ = pg.FromTo(at, 1, 2)                                                                                                                 // numeric bounds on a timestamp partition key
	_ = pg.ListValues(name, 42)                                                                                                             // a number in a text key's list
	_ = pg.IsDistinctFrom(pg.Old(name), pg.New(flag))                                                                                       // OLD text against NEW boolean
	_, _ = pg.CreateTrigger(pg.Trigger{Name: "tr", Table: t}).After(pg.OnInsert()).Build()                                                  // a trigger with no function
	_, _ = pg.CreatePartition(t, t).Build()                                                                                                 // a partition with no bounds
	_ = sqlite.CreateTable(t).Column(ddl.Column(name)).Clause(pg.PartitionByList(name))                                                     // Postgres partitioning on a SQLite table
	_ = ddl.Column(name).As(pg.Domain[int64]{Name: "d"})                                                                                    // an integer domain on a text column
	_ = pg.CreateDomain(pg.Domain[string]{Name: "d"}, ddl.RawType[int64]("bigint"))                                                         // a domain over a base of another Go type
	_ = pg.CreateStatistics(pg.Statistics{Name: "s"}, t, name)                                                                              // statistics on one column, which Postgres refuses
	_ = pg.CreatePolicy(pg.Policy{Name: "p", Table: t}).Using(sqlb.EqExpr(name, flag))                                                      // a policy comparing text with a boolean
	_ = pg.ColumnCollation(flag, pg.Collation{Name: "c"})                                                                                   // a collation on a boolean column
	_, _ = pg.AlterPolicy(pg.Policy{Name: "p", Table: t}).Build()                                                                           // ALTER POLICY with no action
	_ = pg.CreateTable(t).Column(ddl.Column(name)).Clause(sqlite.Strict())                                                                  // STRICT on a Postgres table
	_ = sqlite.AutoincrementKey(name)                                                                                                       // AUTOINCREMENT on a text column
	_ = sqlite.ColumnCollate(flag, sqlite.NoCase)                                                                                           // a collation on a boolean column
	_ = sqlite.References(name, flag)                                                                                                       // a text column referencing a boolean
	_ = sqlite.GeneratedVirtual(name, flag)                                                                                                 // a generated value of another Go type
	_ = pg.AlterTable(t).Clause(sqlite.AddColumn(ddl.Column(name)))                                                                         // a SQLite action on a Postgres table
	_ = sqlite.CreateView(sqlite.View{Name: "v"}).OrReplace()                                                                               // OR REPLACE, which SQLite's CREATE VIEW does not have
	_ = sqlite.StepUpdate(sqlite.Update(t).Set(sqlb.Set(name, "x")))                                                                        // an unfiltered UPDATE in a trigger body
	_ = sqlite.StepInsert(pg.Insert(t).Row(sqlb.Set(name, "x")))                                                                            // a Postgres statement in a SQLite trigger
	_ = sqlite.CreateTrigger(sqlite.Trigger{Name: "tr"}).After(sqlite.OnInsert(), t).When(sqlite.IsNot(sqlite.Old(name), sqlite.New(flag))) // OLD text IS NOT NEW boolean
	_ = sqlite.CreateView(sqlite.View{Name: "v"}).As(pg.Select(name).From(t))                                                               // a Postgres body for a SQLite view
	_ = sqlite.CopyAs(name, flag)                                                                                                           // a rebuild copying a boolean into a text column
	_ = sqlite.Rebuild(t).Into(pg.CreateTable(t).Column(ddl.Column(name)))                                                                  // a Postgres definition for a SQLite rebuild
	_, _ = sqlite.CreateFTS5(t).Build()                                                                                                     // an FTS5 table with no columns
	_ = pg.Insert(t).Row(sqlb.Set(name, "a")).Clause(mysql.OnDuplicateKeyUpdate(sqlb.Set(name, "b")))                                       // a MySQL upsert on a Postgres INSERT
	_ = mysql.Select(name).From(t).Clause(pg.ForUpdate())                                                                                   // a Postgres lock on a MySQL SELECT
	_ = mysql.Update(t).Set(sqlb.Set(name, "b")).All().Clause(mysql.DeleteLimit(1))                                                         // a DELETE clause on an UPDATE
	_ = mysql.AutoIncrement(name)                                                                                                           // AUTO_INCREMENT on a text column
	_ = mysql.CreateIndex(mysql.Index{Name: "i"}, t).IfNotExists()                                                                          // IF NOT EXISTS, which MySQL's CREATE INDEX lacks
	_ = mysql.StepUpdate(mysql.Update(t).Set(sqlb.Set(name, "b")))                                                                          // an unfiltered UPDATE in a trigger body
	_ = mysql.IndexPrefix(flag, 10)                                                                                                         // a prefix key on a boolean column
	_ = mysql.CreateTable(t).Column(ddl.Column(name)).Clause(pg.Unlogged())                                                                 // a Postgres table clause on MySQL
	_, _ = pg.AlterEnum(pg.Enum[status]{Name: "s"}).Build()                                                                                 // ALTER TYPE with no action

	// RETURNING is a capability, checked by a type constraint.
	var s string
	_ = sqlb.InsertReturning(sqlb.NewInsert(sqlb.ClickHouseDialect{}).Into(t).Row(sqlb.Set(name, "a")), sqlb.Bind(name, &s)) // ClickHouse has no RETURNING
	_ = sqlb.InsertReturning(sqlb.NewInsert(runtime).Into(t).Row(sqlb.Set(name, "a")), sqlb.Bind(name, &s))                  // capability unknown at compile time
	_ = sqlb.InsertReturning(sqlb.NewInsert(sqlb.MySQLDialect{}).Into(t).Row(sqlb.Set(name, "a")), sqlb.Bind(name, &s))      // MySQL has no RETURNING

}
