package pgsurface_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

func ins() *sqlb.Insert[sqlb.PostgresDialect] { return sqlb.NewInsert(d) }
func upd() *sqlb.Update[sqlb.PostgresDialect] { return sqlb.NewUpdate(d) }
func del() *sqlb.Delete[sqlb.PostgresDialect] { return sqlb.NewDelete(d) }

// 7. INSERT

func Example_insert_oneRowReturning() {
	show(sqlb.InsertReturning(ins().Into(Users).
		Row(sqlb.Set(UEmail, "a@example.com"), sqlb.Set(UName, "Ada"), sqlb.Set(UAge, 36)), sqlb.Bind(UID, &sink.s), sqlb.Bind(UCreatedAt, &sink.t)).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email", "name", "age") VALUES ($1, $2, $3) RETURNING "u"."id", "u"."created_at"
	// args: [a@example.com Ada 36]
}

func Example_insert_manyRows() {
	show(ins().Into(Users).
		Row(sqlb.Set(UEmail, "a@example.com"), sqlb.Set(UName, "Ada")).
		Row(sqlb.Set(UEmail, "b@example.com"), sqlb.Set(UName, "Bob")).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email", "name") VALUES ($1, $2), ($3, $4)
	// args: [a@example.com Ada b@example.com Bob]
}

func Example_insert_defaultValuesAndDefaultColumn() {
	show(ins().Into(Users).Body(pg.DefaultValues()).Build())
	show(ins().Into(Users).Row(sqlb.SetDefault(UID), sqlb.Set(UEmail, "c@example.com")).Build())
	// Output:
	// INSERT INTO "users" AS "u" DEFAULT VALUES
	// INSERT INTO "users" AS "u" ("id", "email") VALUES (DEFAULT, $1)
	// args: [c@example.com]
}

func Example_insert_fromSelect() {
	src := sel().Exprs(UEmail, UName).From(Users).Where(sqlb.Eq(UActive, false))
	archive := sqlb.Table{Name: "users_archive", Alias: "ua"}
	aEmail := sqlb.Col[string](archive, "email")
	aName := sqlb.Col[string](archive, "name")
	show(ins().Into(archive).Columns(aEmail, aName).Select(src).Build())
	// Output:
	// INSERT INTO "users_archive" AS "ua" ("email", "name") SELECT "u"."email", "u"."name" FROM "users" AS "u" WHERE ("u"."active" = $1)
	// args: [false]
}

func Example_insert_overriding() {
	show(ins().Into(Users).Row(sqlb.Set(UID, "usr_1"), sqlb.Set(UEmail, "d@example.com")).
		Clause(pg.OverridingSystemValue()).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("id", "email") OVERRIDING SYSTEM VALUE VALUES ($1, $2)
	// args: [usr_1 d@example.com]
}

func Example_insert_onConflictDoNothing() {
	show(ins().Into(Users).Row(sqlb.Set(UEmail, "a@example.com")).
		Clause(pg.OnConflict(UEmail).DoNothing()).Build())
	show(ins().Into(Users).Row(sqlb.Set(UEmail, "a@example.com")).
		Clause(pg.OnConflict().DoNothing()).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email") VALUES ($1) ON CONFLICT ("email") DO NOTHING
	// args: [a@example.com]
	// INSERT INTO "users" AS "u" ("email") VALUES ($1) ON CONFLICT DO NOTHING
	// args: [a@example.com]
}

func Example_insert_onConstraint() {
	show(ins().Into(Users).Row(sqlb.Set(UEmail, "a@example.com")).
		Clause(pg.OnConstraint(pg.TableConstraint{Table: Users, Name: "users_email_key"}).DoNothing()).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email") VALUES ($1) ON CONFLICT ON CONSTRAINT "users_email_key" DO NOTHING
	// args: [a@example.com]
}

func Example_insert_upsertExcluded() {
	show(sqlb.InsertReturning(ins().Into(Users).
		Row(sqlb.Set(UEmail, "a@example.com"), sqlb.Set(UName, "Ada")).
		Clause(pg.OnConflict(UEmail).
			DoUpdate(sqlb.SetExpr(UName, pg.Excluded(UName)), sqlb.SetExpr(UCreatedAt, pg.Now())).
			UpdateWhere(pg.IsDistinctFrom(UName, pg.Excluded(UName))).
			Clause()), sqlb.Bind(UID, &sink.s), sqlb.Bind(UEmail, &sink.s2)).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email", "name") VALUES ($1, $2) ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name", "created_at" = now() WHERE (("u"."name" IS DISTINCT FROM EXCLUDED."name")) RETURNING "u"."id", "u"."email"
	// args: [a@example.com Ada]
}

func Example_insert_partialIndexTarget() {
	show(ins().Into(Users).Row(sqlb.Set(UEmail, "a@example.com")).
		Clause(pg.OnConflict(UEmail).TargetWhere(sqlb.IsNull(UDeletedAt)).DoNothing()).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email") VALUES ($1) ON CONFLICT ("email") WHERE ("u"."deleted_at" IS NULL) DO NOTHING
	// args: [a@example.com]
}

// 8. UPDATE

func Example_update_setAndExpression() {
	show(sqlb.UpdateReturning(upd().Table(Posts).
		Set(sqlb.Set(PTitle, "New"), sqlb.SetExpr(PViews, sqlb.Add(PViews, sqlb.Param[int64](1)))).
		Where(sqlb.Eq(PID, "post_1")), sqlb.Bind(PViews, &sink.i64)).Build())
	// Output:
	// UPDATE "posts" AS "p" SET "title" = $1, "views" = ("p"."views" + $2) WHERE ("p"."id" = $3) RETURNING "p"."views"
	// args: [New 1 post_1]
}

func Example_update_from() {
	show(upd().Table(Posts).
		Set(sqlb.SetExpr(PTitle, sqlb.Concat(UName, sqlb.Param(": "), PTitle))).
		Clause(pg.UpdateFrom(Users)).
		Where(sqlb.EqExpr(PUserID, UID), sqlb.Eq(UActive, false)).Build())
	// Output:
	// UPDATE "posts" AS "p" SET "title" = concat("u"."name", CAST($1 AS text), "p"."title") FROM "users" AS "u" WHERE ("p"."user_id" = "u"."id" AND "u"."active" = $2)
	// args: [:  false]
}

// An UPDATE with no WHERE does not compile: Build is only defined on the
// type Where and All return. The whole-table case is written down with All.
func Example_update_allRowsExplicit() {
	show(upd().Table(Users).Set(sqlb.Set(UActive, false)).All().Build())
	// Output:
	// UPDATE "users" AS "u" SET "active" = $1
	// args: [false]
}

func Example_update_withCTE() {
	top := sel().Exprs(PUserID).From(Posts).GroupBy(PUserID).Having(sqlb.Greater(sqlb.Count(), 10))
	t := sqlb.Table{Name: "top", Alias: "t"}
	show(upd().With("top", top).Table(Users).Set(sqlb.Set(UPlan, PlanPro)).
		Where(sqlb.InSubquery(UID, sel().Exprs(sqlb.Col[string](t, "user_id")).From(t))).Build())
	// Output:
	// WITH "top" AS (SELECT "p"."user_id" FROM "posts" AS "p" GROUP BY "p"."user_id" HAVING (count(*) > $1)) UPDATE "users" AS "u" SET "plan" = $2 WHERE ("u"."id" IN (SELECT "t"."user_id" FROM "top" AS "t"))
	// args: [10 pro]
}

// 9. DELETE

func Example_delete_whereReturning() {
	show(sqlb.DeleteReturning(del().From(Posts).Where(sqlb.Eq(PPublished, false), sqlb.Less(PCreatedAt, t0)), sqlb.Bind(PID, &sink.s)).Build())
	// Output:
	// DELETE FROM "posts" AS "p" WHERE ("p"."published" = $1 AND "p"."created_at" < $2) RETURNING "p"."id"
	// args: [false 2026-09-01 00:00:00 +0000 UTC]
}

func Example_delete_using() {
	show(del().From(Posts).Clause(pg.DeleteUsing(Users)).
		Where(sqlb.EqExpr(PUserID, UID), sqlb.IsNotNull(UDeletedAt)).Build())
	// Output:
	// DELETE FROM "posts" AS "p" USING "users" AS "u" WHERE ("p"."user_id" = "u"."id" AND "u"."deleted_at" IS NOT NULL)
}

// A DELETE with no WHERE does not compile; the whole table is written down
// with All.
func Example_delete_allRowsExplicit() {
	show(del().From(Events).All().Build())
	// Output:
	// DELETE FROM "events" AS "e"
}

// 10. MERGE

func Example_merge_full() {
	src := pg.Values("s", []string{"email", "name"},
		[]sqlb.SQLer{sqlb.Param("a@example.com"), sqlb.Param("Ada")},
	)
	sEmail := sqlb.Col[string](sqlb.Ref(src), "email")
	sName := sqlb.Col[string](sqlb.Ref(src), "name")
	show(pg.NewMerge(d).Into(Users).Using(src, sqlb.EqExpr(UEmail, sEmail)).
		WhenMatched(sqlb.IsNotNull(UDeletedAt)).ThenDelete().
		WhenMatched().ThenUpdate(sqlb.SetExpr(UName, sName)).
		WhenNotMatched().ThenInsert(sqlb.SetExpr(UEmail, sEmail), sqlb.SetExpr(UName, sName)).
		WhenNotMatchedBySource().ThenDoNothing().
		Returning(sqlb.Bind(pg.MergeAction(), &sink.s), sqlb.Bind(UID, &sink.s2)).Build())
	// Output:
	// MERGE INTO "users" AS "u" USING (VALUES ($1, $2)) AS "s" ("email", "name") ON ("u"."email" = "s"."email") WHEN MATCHED AND ("u"."deleted_at" IS NOT NULL) THEN DELETE WHEN MATCHED THEN UPDATE SET "name" = "s"."name" WHEN NOT MATCHED THEN INSERT ("email", "name") VALUES ("s"."email", "s"."name") WHEN NOT MATCHED BY SOURCE THEN DO NOTHING RETURNING merge_action(), "u"."id"
	// args: [a@example.com Ada]
}

// Example_merge_underSQLiteFails shows the one path by which another engine's
// dialect still reaches a MERGE. pg.NewMerge takes pg.Dialect, so passing
// SQLite's does not compile; but a Merge is a Statement and can be a CTE body
// in a SQLite statement, and there Build refuses it, naming the construct.
func Example_merge_underSQLiteFails() {
	src := pg.Values("s", []string{"email"}, []sqlb.SQLer{sqlb.Param("a@example.com")})
	merge := pg.NewMerge(pg.Dialect{}).Into(Users).Using(src, sqlb.True()).WhenMatched().ThenDelete()
	show(sqlb.NewSelect(sqlb.SQLiteDialect{}).With("m", merge).Exprs(sqlb.Literal[int]("1")).Build())
	// Output:
	// error: sqlb: construct is not supported by this dialect
	// pg.Merge under dialect "sqlite"
}

// Typed JSON column through JSON[T].

type Prefs struct {
	Theme string `json:"theme"`
	Beta  bool   `json:"beta"`
}

func Example_insert_typedJSONColumn() {
	prefs := sqlb.Col[pg.JSON[Prefs]](Profiles, "prefs")
	show(ins().Into(Profiles).Row(sqlb.Set(PrUserID, "usr_1"), sqlb.Set(prefs, pg.JSON[Prefs]{V: Prefs{Theme: "dark", Beta: true}})).Build())
	// Output:
	// INSERT INTO "profiles" AS "pr" ("user_id", "prefs") VALUES ($1, $2)
	// args: [usr_1 {{dark true}}]
}
