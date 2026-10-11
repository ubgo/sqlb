package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 3. LATERAL and set-returning functions

// ### 3.1 Top 3 per group, keeping groups with no rows
func Example_s3_01_topNKeepingEmptyGroups() {
	c := tbl("customers", "c")
	cID, cName := sqlb.Col[int64](c, "id"), sqlb.Col[string](c, "name")
	o := tbl("orders", "o")
	oID, oCustomer, oTotal, oCreated := sqlb.Col[int64](o, "id"), sqlb.Col[int64](o, "customer_id"), sqlb.Col[float64](o, "total"), sqlb.Col[time.Time](o, "created_at")
	last3 := sqlb.Derived(sel().Exprs(oID, oTotal, oCreated).From(o).Where(sqlb.EqExpr(oCustomer, cID)).Order(desc(oCreated)).Limit(3), "o")
	show(sel().Exprs(cID, cName, sqlb.As(ref[int64](last3, "id"), "order_id"), ref[float64](last3, "total"), ref[time.Time](last3, "created_at")).
		From(c).Join(leftLateral(last3)).Order(asc(cID), desc(ref[time.Time](last3, "created_at"))).Build())
	// Output:
	// SELECT "c"."id", "c"."name", "o"."id" AS "order_id", "o"."total", "o"."created_at" FROM "customers" AS "c" LEFT JOIN LATERAL (SELECT "o"."id", "o"."total", "o"."created_at" FROM "orders" AS "o" WHERE ("o"."customer_id" = "c"."id") ORDER BY "o"."created_at" DESC LIMIT $1) AS "o" ON (TRUE) ORDER BY "c"."id" ASC, "o"."created_at" DESC
	// args: 1
}

// ### 3.2 Zip arrays and set-returning functions with WITH ORDINALITY
func Example_s3_02_zipWithOrdinality() {
	t := pg.UnnestMulti("t", []sqlb.SQLer{pg.Array("a", "b", "c"), pg.Array(1, 2)}, true, "letter", "num", "ord")
	show(sel().Exprs(ref[string](t, "letter"), ref[*int32](t, "num"), ref[int64](t, "ord")).From(t).Build())
	rows := pg.RowsFrom("t", []sqlb.SQLer{
		sqlb.Fn[[]byte]("jsonb_array_elements", sqlb.Cast[[]byte](sqlb.Param([]byte(`[10,20]`)), "jsonb")),
		sqlb.Fn[int32]("generate_series", sqlb.Param(1), sqlb.Param(3)),
	}, true, "j", "n", "ord")
	show(sel().Exprs(ref[[]byte](rows, "j"), ref[*int32](rows, "n"), ref[int64](rows, "ord")).From(rows).Build())
	// Output:
	// SELECT "t"."letter", "t"."num", "t"."ord" FROM unnest(ARRAY[CAST($1 AS text), CAST($2 AS text), CAST($3 AS text)], ARRAY[CAST($4 AS bigint), CAST($5 AS bigint)]) WITH ORDINALITY AS "t" ("letter", "num", "ord")
	// args: 5
	// SELECT "t"."j", "t"."n", "t"."ord" FROM ROWS FROM (jsonb_array_elements(CAST($1 AS jsonb)), generate_series($2, $3)) WITH ORDINALITY AS "t" ("j", "n", "ord")
	// args: 3
}

// ### 3.3 Fill missing days per entity with zeros
func Example_s3_03_fillMissingDays() {
	s := tbl("stores", "s")
	sStore := sqlb.Col[int64](s, "store_id")
	days, day := pg.GenerateSeriesTime(t0, t1, "1 day", "d")
	o := tbl("orders", "o")
	oStore, oCreated, oTotal := sqlb.Col[int64](o, "store_id"), sqlb.Col[time.Time](o, "created_at"), sqlb.Col[float64](o, "total")
	show(sel().Exprs(sStore, sqlb.As(sqlb.Cast[time.Time](day, "date"), "day"), sqlb.As(sqlb.Coalesce(sqlb.Sum(oTotal), sqlb.Param(0.0)), "revenue")).
		From(s).Join(cross(days), left(o, sqlb.EqExpr(oStore, sStore), sqlb.GreaterEqExpr(oCreated, day),
		sqlb.LessExpr(oCreated, pg.AddIntervalExpr(day, pg.IntervalLit("1 day"))))).
		GroupBy(sStore, day).Order(asc(sStore), asc(day)).Build())
	// Output:
	// SELECT "s"."store_id", CAST("d"."ts" AS date) AS "day", COALESCE(sum("o"."total"), $1) AS "revenue" FROM "stores" AS "s" CROSS JOIN generate_series($2, $3, interval '1 day') AS "d" ("ts") LEFT JOIN "orders" AS "o" ON ("o"."store_id" = "s"."store_id" AND "o"."created_at" >= "d"."ts" AND "o"."created_at" < ("d"."ts" + interval '1 day')) GROUP BY "s"."store_id", "d"."ts" ORDER BY "s"."store_id" ASC, "d"."ts" ASC
	// args: 3
}

// ### 3.4 LATERAL as a LET binding
func Example_s3_04_lateralAsLet() {
	p := tbl("products", "p")
	pID, price, qty := sqlb.Col[int64](p, "id"), sqlb.Col[float64](p, "price"), sqlb.Col[float64](p, "qty")
	calc := sqlb.Derived(sel().Exprs(sqlb.As(sqlb.Mul(price, qty), "subtotal")), "calc")
	subtotal := ref[float64](calc, "subtotal")
	show(sel().Exprs(pID, subtotal, sqlb.As(sqlb.Mul(subtotal, sqlb.Param(0.2)), "tax"), sqlb.As(sqlb.Mul(subtotal, sqlb.Param(1.2)), "total")).
		From(p).Join(crossLateral(calc)).Where(sqlb.Greater(subtotal, 100)).Build())
	// Output:
	// SELECT "p"."id", "calc"."subtotal", ("calc"."subtotal" * $1) AS "tax", ("calc"."subtotal" * $2) AS "total" FROM "products" AS "p" CROSS JOIN LATERAL (SELECT ("p"."price" * "p"."qty") AS "subtotal") AS "calc" WHERE ("calc"."subtotal" > $3)
	// args: 3
}

// ### 3.5 Multiple set-returning functions in the SELECT list
func Example_s3_05_multipleSRFsInSelect() {
	show(sel().Exprs(sqlb.As(pg.GenerateSeriesScalar(sqlb.Param(1), sqlb.Param(3)), "a"), sqlb.As(pg.GenerateSeriesScalar(sqlb.Param(1), sqlb.Param(2)), "b")).Build())
	// Output:
	// SELECT generate_series(CAST($1 AS bigint), CAST($2 AS bigint)) AS "a", generate_series(CAST($3 AS bigint), CAST($4 AS bigint)) AS "b"
	// args: 4
}

// ### 3.6 Exists-with-detail
func Example_s3_06_existsWithDetail() {
	u := tbl("users", "u")
	uID, uEmail := sqlb.Col[int64](u, "id"), sqlb.Col[string](u, "email")
	a := tbl("login_attempts", "a")
	aUser, aAt, aOK := sqlb.Col[int64](a, "user_id"), sqlb.Col[time.Time](a, "attempted_at"), sqlb.Col[bool](a, "success")
	f := sqlb.Derived(sel().Exprs(aAt).From(a).Where(sqlb.EqExpr(aUser, uID), sqlb.Not(sqlb.PredicateOf(aOK.SQL))).Order(desc(aAt)).Limit(1), "f")
	fAt := ref[time.Time](f, "attempted_at")
	show(sel().Exprs(uID, uEmail, sqlb.As(fAt, "last_failed_login")).From(u).Join(leftLateral(f)).
		Where(sqlb.GreaterExpr(fAt, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("1 hour")))).Build())
	// Output:
	// SELECT "u"."id", "u"."email", "f"."attempted_at" AS "last_failed_login" FROM "users" AS "u" LEFT JOIN LATERAL (SELECT "a"."attempted_at" FROM "login_attempts" AS "a" WHERE ("a"."user_id" = "u"."id" AND NOT ("a"."success")) ORDER BY "a"."attempted_at" DESC LIMIT $1) AS "f" ON (TRUE) WHERE ("f"."attempted_at" > (now() - interval '1 hour'))
	// args: 1
}
