package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 5. Writes: upsert, MERGE, data-modifying CTEs

// ### 5.1 Upsert with a conditional update and inserted/updated detection
func Example_s5_01_upsertConditional() {
	inv := tbl("inventory", "i")
	sku, qty, updated := sqlb.Col[string](inv, "sku"), sqlb.Col[int64](inv, "qty"), sqlb.Col[time.Time](inv, "updated_at")
	xmax := sqlb.Col[int64](inv, "xmax")
	newQty := sqlb.Add(qty, pg.Excluded(qty))
	show(sqlb.InsertReturning(sqlb.NewInsert(d).Into(inv).Row(sqlb.Set(sku, "A1"), sqlb.Set(qty, 5), sqlb.SetExpr(updated, pg.Now())).
		Clause(pg.OnConflict(sku).DoUpdate(sqlb.SetExpr(qty, newQty), sqlb.SetExpr(updated, pg.Excluded(updated))).
			UpdateWhere(sqlb.GreaterEq(newQty, 0)).Clause()), sqlb.Bind(sku, &sink.s), sqlb.Bind(qty, &sink.i64), sqlb.Bind(sqlb.As(sqlb.BoolExpr(sqlb.Eq(xmax, 0)), "inserted"), &sink.b)).Build())
	// Output:
	// INSERT INTO "inventory" AS "i" ("sku", "qty", "updated_at") VALUES ($1, $2, now()) ON CONFLICT ("sku") DO UPDATE SET "qty" = ("i"."qty" + EXCLUDED."qty"),
	// "updated_at" = EXCLUDED."updated_at" WHERE (("i"."qty" + EXCLUDED."qty") >= $3) RETURNING "i"."sku", "i"."qty", "i"."xmax" = $4 AS "inserted"
	// args: 4
}

// ### 5.2 Partial unique index as the conflict arbiter
// CREATE UNIQUE INDEX is DDL. The INSERT repeats the index predicate as the arbiter.
func Example_s5_02_partialIndexArbiter() {
	u := tbl("users", "u")
	email, name, deleted := sqlb.Col[string](u, "email"), sqlb.Col[string](u, "name"), sqlb.Col[*time.Time](u, "deleted_at")
	show(sqlb.NewInsert(d).Into(u).Row(sqlb.Set(email, "a@x.com"), sqlb.Set(name, "Ada")).
		Clause(pg.OnConflict(email).TargetWhere(sqlb.IsNull(deleted)).DoUpdate(sqlb.SetExpr(name, pg.Excluded(name))).Clause()).Build())
	// Output:
	// INSERT INTO "users" AS "u" ("email", "name") VALUES ($1, $2) ON CONFLICT ("email") WHERE ("u"."deleted_at" IS NULL) DO UPDATE SET "name" = EXCLUDED."name"
	// args: 2
}

// ### 5.3 Get-or-create returning the existing row, and its race
func Example_s5_03_getOrCreate() {
	tags := tbl("tags", "tags")
	tID, tName := sqlb.Col[int64](tags, "id"), sqlb.Col[string](tags, "name")
	ins := sqlb.InsertReturning(sqlb.NewInsert(d).Into(tags).Row(sqlb.Set(tName, "go")).Clause(pg.OnConflict(tName).DoNothing()), sqlb.Bind(tID, &sink.i64))
	insT := tbl("ins", "ins")
	show(sel().With("ins", ins).Exprs(ref[int64](insT, "id")).From(insT).
		UnionAll(sel().Exprs(tID).From(tags).Where(sqlb.Eq(tName, "go"))).Limit(1).Build())
	// Output:
	// WITH "ins" AS (INSERT INTO "tags" AS "tags" ("name") VALUES ($1) ON CONFLICT ("name") DO NOTHING RETURNING "tags"."id")
	// SELECT "ins"."id" FROM "ins" AS "ins" UNION ALL SELECT "tags"."id" FROM "tags" AS "tags" WHERE ("tags"."name" = $2) LIMIT $3
	// args: 3
}

// ### 5.4 Bulk upsert from a JSON parameter with source dedup
func Example_s5_04_bulkUpsertFromJSON() {
	p := tbl("products", "products")
	sku, price := sqlb.Col[string](p, "sku"), sqlb.Col[float64](p, "price")
	x := pg.RecordSet(sqlb.Fn[[]byte]("jsonb_to_recordset", sqlb.Cast[[]byte](sqlb.Param([]byte(`[{"sku":"A1","price":9}]`)), "jsonb")), "x",
		pg.ColumnDef{Name: "sku", Type: "text"}, pg.ColumnDef{Name: "price", Type: "numeric"})
	xSku, xPrice := ref[string](x, "sku"), ref[float64](x, "price")
	src := sel().Clause(pg.DistinctOn(xSku)).Exprs(xSku, xPrice).From(x).Order(asc(xSku), desc(xPrice))
	show(sqlb.NewInsert(d).Into(p).Columns(sku, price).Select(src).
		Clause(pg.OnConflict(sku).DoUpdate(sqlb.SetExpr(price, pg.Excluded(price))).
			UpdateWhere(pg.IsDistinctFrom(price, pg.Excluded(price))).Clause()).Build())
	// Output:
	// INSERT INTO "products" AS "products" ("sku", "price")
	// SELECT DISTINCT ON ("x"."sku") "x"."sku", "x"."price" FROM jsonb_to_recordset(CAST($1 AS jsonb)) AS "x" ("sku" text, "price" numeric) ORDER BY "x"."sku" ASC, "x"."price" DESC ON CONFLICT ("sku") DO UPDATE SET "price" = EXCLUDED."price" WHERE (("products"."price" IS DISTINCT FROM EXCLUDED."price"))
	// args: 1
}

// ### 5.5 MERGE with delete, conditional insert, and RETURNING (PG15, RETURNING PG17)
func Example_s5_05_merge() {
	stock := tbl("stock", "s")
	sSku, sQty, sUpdated := sqlb.Col[string](stock, "sku"), sqlb.Col[int64](stock, "qty"), sqlb.Col[time.Time](stock, "updated_at")
	mv := tbl("movements", "movements")
	mvSku, mvDelta, mvBatch := sqlb.Col[string](mv, "sku"), sqlb.Col[int64](mv, "delta"), sqlb.Col[int64](mv, "batch_id")
	m := sqlb.Derived(sel().Exprs(mvSku, sqlb.As(sqlb.Sum(mvDelta), "delta")).From(mv).Where(sqlb.Eq(mvBatch, 42)).GroupBy(mvSku), "m")
	mSku, mDelta := ref[string](m, "sku"), ref[int64](m, "delta")
	show(pg.NewMerge(d).Into(stock).Using(m, sqlb.EqExpr(sSku, mSku)).
		WhenMatched(sqlb.LessEq(sqlb.Add(sQty, mDelta), 0)).ThenDelete().
		WhenMatched().ThenUpdate(sqlb.SetExpr(sQty, sqlb.Add(sQty, mDelta)), sqlb.SetExpr(sUpdated, pg.Now())).
		WhenNotMatched(sqlb.Greater(mDelta, 0)).ThenInsert(sqlb.SetExpr(sSku, mSku), sqlb.SetExpr(sQty, mDelta)).
		WhenNotMatched().ThenDoNothing().
		Returning(sqlb.Bind(pg.MergeAction(), &sink.s), sqlb.Bind(sSku, &sink.s), sqlb.Bind(sQty, &sink.i64)).Build())
	// Output:
	// MERGE INTO "stock" AS "s" USING (SELECT "movements"."sku", sum("movements"."delta") AS "delta" FROM "movements" AS "movements" WHERE ("movements"."batch_id" = $1) GROUP BY "movements"."sku") AS "m" ON ("s"."sku" = "m"."sku") WHEN MATCHED AND (("s"."qty" + "m"."delta") <= $2) THEN DELETE WHEN MATCHED THEN UPDATE SET "qty" = ("s"."qty" + "m"."delta"),
	// "updated_at" = now() WHEN NOT MATCHED AND ("m"."delta" > $3) THEN INSERT ("sku", "qty") VALUES ("m"."sku", "m"."delta") WHEN NOT MATCHED THEN DO NOTHING RETURNING merge_action(),
	// "s"."sku", "s"."qty"
	// args: 3
}

// ### 5.6 Move rows between tables atomically
func Example_s5_06_moveRows() {
	jobs := tbl("jobs", "j")
	status, finished := sqlb.Col[string](jobs, "status"), sqlb.Col[time.Time](jobs, "finished_at")
	moved := sqlb.DeleteReturning(sqlb.NewDelete(d).From(jobs).Where(sqlb.Eq(status, "done"), sqlb.LessExpr(finished, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("7 days")))), sqlb.Bind(sqlb.Col[[]byte](jobs, "*"), &sink.j))
	movedT := tbl("moved", "moved")
	show(sqlb.NewInsert(d).With("moved", moved).Into(tbl("jobs_archive", "jobs_archive")).
		Select(sel().Exprs(raw("*")).From(movedT)).Build())
	// Output:
	// WITH "moved" AS (DELETE FROM "jobs" AS "j" WHERE ("j"."status" = $1 AND "j"."finished_at" < (now() - interval '7 days')) RETURNING "j"."*") INSERT INTO "jobs_archive" AS "jobs_archive" SELECT * FROM "moved" AS "moved"
	// args: 1
}

// ### 5.7 Data-modifying CTE snapshot rule
func Example_s5_07_snapshotRule() {
	t := tbl("t", "t")
	v := sqlb.Col[int32](t, "v")
	ins := sqlb.InsertReturning(sqlb.NewInsert(d).Into(t).Row(sqlb.Set(v, 1)), sqlb.Bind(sqlb.Col[[]byte](t, "*"), &sink.j))
	checkIt := sel().Exprs(sqlb.As(sqlb.Count(), "n")).From(t)
	insT, checkT := tbl("ins", "ins"), tbl("check_it", "check_it")
	show(sel().With("ins", ins).With("check_it", checkIt).
		Exprs(sqlb.Subquery[int64](sel().Exprs(ref[int64](checkT, "n")).From(checkT)), sqlb.Subquery[int64](sel().Exprs(sqlb.Count()).From(insT))).Build())
	// Output:
	// WITH "ins" AS (INSERT INTO "t" AS "t" ("v") VALUES ($1) RETURNING "t"."*"),
	// "check_it" AS (SELECT count(*) AS "n" FROM "t" AS "t")
	// SELECT (SELECT "check_it"."n" FROM "check_it" AS "check_it"), (SELECT count(*) FROM "ins" AS "ins")
	// args: 1
}

// ### 5.8 UPDATE ... FROM and its silent multiple-match hazard
func Example_s5_08_updateFromAggregated() {
	a := tbl("accounts", "a")
	aID, aBalance := sqlb.Col[int64](a, "id"), sqlb.Col[float64](a, "balance")
	pending := tbl("pending", "pending")
	pAccount, pDelta := sqlb.Col[int64](pending, "account_id"), sqlb.Col[float64](pending, "delta")
	s := sqlb.Derived(sel().Exprs(pAccount, sqlb.As(sqlb.Sum(pDelta), "delta")).From(pending).GroupBy(pAccount), "s")
	show(sqlb.NewUpdate(d).Table(a).Set(sqlb.SetExpr(aBalance, sqlb.Add(aBalance, ref[float64](s, "delta")))).
		Clause(pg.UpdateFrom(s)).Where(sqlb.EqExpr(aID, ref[int64](s, "account_id"))).Build())
	// Output:
	// UPDATE "accounts" AS "a" SET "balance" = ("a"."balance" + "s"."delta") FROM (SELECT "pending"."account_id", sum("pending"."delta") AS "delta" FROM "pending" AS "pending" GROUP BY "pending"."account_id") AS "s" WHERE ("a"."id" = "s"."account_id")
}

// ### 5.9 Lock rows in a fixed order before a multi-row update (deadlock avoidance)
func Example_s5_09_lockInOrder() {
	a := tbl("accounts", "a")
	aID, aBalance := sqlb.Col[int64](a, "id"), sqlb.Col[float64](a, "balance")
	locked := sel().Exprs(aID).From(a).Where(pg.InArray(aID, []int64{1, 2, 3})).Order(asc(aID)).Clause(pg.ForUpdate())
	lockedT := tbl("locked", "locked")
	show(sqlb.NewUpdate(d).With("locked", locked).Table(a).Set(sqlb.SetExpr(aBalance, sqlb.Sub(aBalance, sqlb.Param(10.0)))).
		Clause(pg.UpdateFrom(lockedT)).Where(sqlb.EqExpr(aID, ref[int64](lockedT, "id"))).Build())
	// Output:
	// WITH "locked" AS (SELECT "a"."id" FROM "accounts" AS "a" WHERE ("a"."id" = ANY($1)) ORDER BY "a"."id" ASC FOR UPDATE) UPDATE "accounts" AS "a" SET "balance" = ("a"."balance" - $2) FROM "locked" AS "locked" WHERE ("a"."id" = "locked"."id")
	// args: 2
}

// ### 5.10 Delete duplicates keeping one
func Example_s5_10_deleteDuplicates() {
	c := tbl("contacts", "c")
	cID, cEmail := sqlb.Col[int64](c, "id"), sqlb.Col[string](c, "email")
	dd := tbl("contacts", "d")
	show(sqlb.NewDelete(d).From(c).Clause(pg.DeleteUsing(dd)).
		Where(sqlb.EqExpr(cEmail, sqlb.Col[string](dd, "email")), sqlb.GreaterExpr(cID, sqlb.Col[int64](dd, "id"))).Build())
	// By physical row id when there is no usable id.
	contacts := tbl("contacts", "contacts")
	ctid, email, created := sqlb.Col[string](contacts, "ctid"), sqlb.Col[string](contacts, "email"), sqlb.Col[time.Time](contacts, "created_at")
	numbered := sqlb.Derived(sel().Exprs(ctid, sqlb.As(sqlb.RowNumber().Over(sqlb.NewWindow().PartitionBy(email).OrderBy(asc(created), asc(ctid))), "rn")).From(contacts), "s")
	dups := sel().Exprs(ref[string](numbered, "ctid")).From(numbered).Where(sqlb.Greater(ref[int64](numbered, "rn"), 1))
	show(sqlb.NewDelete(d).From(contacts).Where(sqlb.InSubquery(ctid, dups)).Build())
	// Output:
	// DELETE FROM "contacts" AS "c" USING "contacts" AS "d" WHERE ("c"."email" = "d"."email" AND "c"."id" > "d"."id")
	// DELETE FROM "contacts" AS "contacts" WHERE ("contacts"."ctid" IN (SELECT "s"."ctid" FROM (SELECT "contacts"."ctid", row_number() OVER (PARTITION BY "contacts"."email" ORDER BY "contacts"."created_at" ASC, "contacts"."ctid" ASC) AS "rn" FROM "contacts" AS "contacts") AS "s" WHERE ("s"."rn" > $1)))
	// args: 1
}

// ### 5.11 Batched delete without a long lock (procedure with COMMIT, PG11)
// Not the query layer: a PL/pgSQL procedure with COMMIT inside a loop. The
// batch it runs is a DELETE with ctid = ANY(ARRAY(SELECT ... LIMIT n)),
// which sqlb renders; the loop and the commits belong to the caller.

// ### 5.12 Map generated ids back to input rows
func Example_s5_12_mapGeneratedIds() {
	names := pg.FuncSource(sqlb.Fn[string]("unnest", sqlb.Cast[[]string](sqlb.Param([]string{"a", "b"}), "text[]")), "u", "name", "ord")
	input := sel().Exprs(ref[string](names, "name"), ref[int64](names, "ord")).From(pg.WithOrdinality(names))
	inputT := tbl("input", "i")
	items := tbl("items", "items")
	itID, itName := sqlb.Col[int64](items, "id"), sqlb.Col[string](items, "name")
	ins := sqlb.InsertReturning(sqlb.NewInsert(d).Into(items).Columns(itName).
		Select(sel().Exprs(ref[string](inputT, "name")).From(inputT).Order(asc(ref[int64](inputT, "ord")))), sqlb.Bind(itID, &sink.i64), sqlb.Bind(itName, &sink.s))
	insT := tbl("ins", "ins")
	show(sel().With("input", input).With("ins", ins).
		Exprs(ref[int64](inputT, "ord"), ref[int64](insT, "id")).From(inputT).
		Join(inner(insT, sqlb.EqExpr(ref[string](insT, "name"), ref[string](inputT, "name")))).
		Order(asc(ref[int64](inputT, "ord"))).Build())
	// Output:
	// WITH "input" AS (SELECT "u"."name", "u"."ord" FROM unnest(CAST($1 AS text[])) WITH ORDINALITY AS "u" ("name", "ord")),
	// "ins" AS (INSERT INTO "items" AS "items" ("name")
	// SELECT "i"."name" FROM "input" AS "i" ORDER BY "i"."ord" ASC RETURNING "items"."id", "items"."name")
	// SELECT "i"."ord", "ins"."id" FROM "input" AS "i" INNER JOIN "ins" AS "ins" ON ("ins"."name" = "i"."name") ORDER BY "i"."ord" ASC
	// args: 1
}

// ### 5.13 Identity columns: OVERRIDING SYSTEM VALUE and resetting after a bulk load
func Example_s5_13_overridingSystemValue() {
	t := tbl("t", "t")
	id, v := sqlb.Col[int64](t, "id"), sqlb.Col[string](t, "v")
	show(sqlb.NewInsert(d).Into(t).Row(sqlb.Set(id, 42), sqlb.Set(v, "x")).Clause(pg.OverridingSystemValue()).Build())
	// setval(pg_get_serial_sequence(...)) is a maintenance call; it renders as a FROM-less SELECT.
	show(sel().Exprs(pg.Setval(pg.GetSerialSequence("t", "id"), sqlb.Subquery[int64](sel().Exprs(sqlb.Max(id)).From(t)))).Build())
	// ALTER TABLE ... RESTART WITH is DDL.
	// Output:
}

// ### 5.14 COPY tricks
// Not the query layer: COPY is a protocol-level bulk statement that the
// driver runs (pgx CopyFrom). TRUNCATE is administration.
