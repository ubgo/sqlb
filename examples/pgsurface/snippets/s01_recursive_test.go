package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 1. Recursive CTEs and graphs

// ### 1.1 Tree walk with a manual cycle guard (any version)
func Example_s1_01_treeWalkCycleGuard() {
	emp := tbl("employees", "e")
	eID, eMgr, eName := sqlb.Col[int64](emp, "id"), sqlb.Col[*int64](emp, "manager_id"), sqlb.Col[string](emp, "name")
	org := tbl("org", "o")
	oID, oDepth, oPath := ref[int64](org, "id"), ref[int32](org, "depth"), ref[[]int64](org, "path")

	// The anchor uses the bare table (no alias), so the step's alias e is free.
	anchorT := tbl("employees", "employees")
	aID, aMgr, aName := sqlb.Col[int64](anchorT, "id"), sqlb.Col[*int64](anchorT, "manager_id"), sqlb.Col[string](anchorT, "name")
	seed := sel().Exprs(aID, aMgr, aName, sqlb.As(sqlb.Param[int32](0), "depth"), sqlb.As(pg.ArrayOf(aID), "path")).
		From(anchorT).Where(sqlb.IsNull(aMgr))
	step := sel().Exprs(eID, eMgr, eName, sqlb.Add(oDepth, sqlb.Param[int32](1)), pg.ArrayConcat(oPath, pg.ArrayOf(eID))).
		From(emp).Join(inner(org, sqlb.EqExpr(eMgr, sqlb.Nullable(oID)))).
		Where(pg.AllExpr(eID, sqlb.OpNotEq, oPath)) // cycle guard

	show(sel().WithRecursive("org", seed, step).
		Exprs(sqlb.As(pg.ConcatOp(pg.RepeatExpr(sqlb.Param("    "), oDepth), ref[string](org, "name")), "tree"), oDepth, oPath).
		From(org).Order(asc(oPath)).Build())
	// Output:
	// WITH RECURSIVE "org" AS (SELECT "employees"."id", "employees"."manager_id", "employees"."name", $1 AS "depth", ARRAY["employees"."id"] AS "path" FROM "employees" AS "employees" WHERE ("employees"."manager_id" IS NULL) UNION ALL SELECT "e"."id", "e"."manager_id", "e"."name", ("o"."depth" + $2), ("o"."path" || ARRAY["e"."id"]) FROM "employees" AS "e" INNER JOIN "org" AS "o" ON ("e"."manager_id" = "o"."id") WHERE ("e"."id" <> ALL("o"."path")))
	// SELECT (repeat($3, "o"."depth") || "o"."name") AS "tree", "o"."depth", "o"."path" FROM "org" AS "o" ORDER BY "o"."path" ASC
	// args: 3
}

// ### 1.2 Built-in SEARCH and CYCLE clauses (PG14)
func Example_s1_02_searchAndCycle() {
	emp := tbl("employees", "e")
	eID, eMgr, eName := sqlb.Col[int64](emp, "id"), sqlb.Col[*int64](emp, "manager_id"), sqlb.Col[string](emp, "name")
	org := tbl("org", "o")
	anchorT := tbl("employees", "employees")
	seed := sel().Exprs(sqlb.Col[int64](anchorT, "id"), sqlb.Col[*int64](anchorT, "manager_id"), sqlb.Col[string](anchorT, "name")).
		From(anchorT).Where(sqlb.Eq(sqlb.Col[int64](anchorT, "id"), 1))
	step := sel().Exprs(eID, eMgr, eName).From(emp).Join(inner(org, sqlb.EqExpr(eMgr, sqlb.Nullable(ref[int64](org, "id")))))

	show(sel().WithRecursiveCTE(sqlb.CTE{
		Name:   "org",
		Body:   seed.UnionAll(step),
		Suffix: pg.Both(pg.SearchDepthFirst("ordercol", "name"), pg.Cycle("is_cycle", "cycle_path", "id")),
	}).
		Exprs(ref[string](org, "name"), ref[bool](org, "is_cycle"), ref[[]byte](org, "cycle_path")).
		From(org).Where(sqlb.Not(sqlb.PredicateOf(ref[bool](org, "is_cycle").SQL))).
		Order(asc(ref[string](org, "ordercol"))).Build())
	// Output:
	// WITH RECURSIVE "org" AS (SELECT "employees"."id", "employees"."manager_id", "employees"."name" FROM "employees" AS "employees" WHERE ("employees"."id" = $1) UNION ALL SELECT "e"."id", "e"."manager_id", "e"."name" FROM "employees" AS "e" INNER JOIN "org" AS "o" ON ("e"."manager_id" = "o"."id")) SEARCH DEPTH FIRST BY "name" SET "ordercol" CYCLE "id" SET "is_cycle" USING "cycle_path" SELECT "o"."name", "o"."is_cycle", "o"."cycle_path" FROM "org" AS "o" WHERE (NOT ("o"."is_cycle")) ORDER BY "o"."ordercol" ASC
	// args: 1
}

// ### 1.3 Bill of materials with quantity multiplication
func Example_s1_03_billOfMaterials() {
	pt := tbl("parts_tree", "pt")
	ptParent, ptChild, ptQty := sqlb.Col[string](pt, "parent_id"), sqlb.Col[string](pt, "child_id"), sqlb.Col[float64](pt, "qty")
	bom := tbl("bom", "b")
	bRoot, bChild, bTotal, bLevel, bPath := ref[string](bom, "root"), ref[string](bom, "child_id"), ref[float64](bom, "total_qty"), ref[int32](bom, "level"), ref[[]string](bom, "path")
	anchor := tbl("parts_tree", "parts_tree")
	aParent, aChild, aQty := sqlb.Col[string](anchor, "parent_id"), sqlb.Col[string](anchor, "child_id"), sqlb.Col[float64](anchor, "qty")

	seed := sel().Exprs(sqlb.As(aParent, "root"), aChild, sqlb.As(sqlb.Cast[float64](aQty, "numeric"), "total_qty"),
		sqlb.As(sqlb.Param[int32](1), "level"), sqlb.As(pg.ArrayOf(aParent, aChild), "path")).
		From(anchor).Where(sqlb.Eq(aParent, "BIKE"))
	step := sel().Exprs(bRoot, ptChild, sqlb.Mul(bTotal, ptQty), sqlb.Add(bLevel, sqlb.Param[int32](1)), pg.ArrayConcat(bPath, pg.ArrayOf(ptChild))).
		From(bom).Join(inner(pt, sqlb.EqExpr(ptParent, bChild))).
		Where(pg.AllExpr(ptChild, sqlb.OpNotEq, bPath))

	show(sel().WithRecursive("bom", seed, step).
		Exprs(sqlb.As(bChild, "part"), sqlb.As(sqlb.Sum(bTotal), "needed"), sqlb.As(sqlb.Max(bLevel), "deepest_level")).
		From(bom).GroupBy(bChild).Order(desc(sqlb.OutputCol[float64]("needed"))).Build())
	// Output:
	// WITH RECURSIVE "bom" AS (SELECT "parts_tree"."parent_id" AS "root", "parts_tree"."child_id", CAST("parts_tree"."qty" AS numeric) AS "total_qty", $1 AS "level", ARRAY["parts_tree"."parent_id", "parts_tree"."child_id"] AS "path" FROM "parts_tree" AS "parts_tree" WHERE ("parts_tree"."parent_id" = $2) UNION ALL SELECT "b"."root", "pt"."child_id", ("b"."total_qty" * "pt"."qty"), ("b"."level" + $3), ("b"."path" || ARRAY["pt"."child_id"]) FROM "bom" AS "b" INNER JOIN "parts_tree" AS "pt" ON ("pt"."parent_id" = "b"."child_id") WHERE ("pt"."child_id" <> ALL("b"."path")))
	// SELECT "b"."child_id" AS "part", sum("b"."total_qty") AS "needed", max("b"."level") AS "deepest_level" FROM "bom" AS "b" GROUP BY "b"."child_id" ORDER BY "needed" DESC
	// args: 3
}

// ### 1.4 Cheapest path in a weighted graph
func Example_s1_04_cheapestPath() {
	edges := tbl("edges", "e")
	eSrc, eDst, eCost := sqlb.Col[string](edges, "src"), sqlb.Col[string](edges, "dst"), sqlb.Col[float64](edges, "cost")
	paths := tbl("paths", "p")
	pSrc, pDst, pCost, pPath := ref[string](paths, "src"), ref[string](paths, "dst"), ref[float64](paths, "cost"), ref[[]string](paths, "path")
	anchor := tbl("edges", "edges")
	aSrc, aDst, aCost := sqlb.Col[string](anchor, "src"), sqlb.Col[string](anchor, "dst"), sqlb.Col[float64](anchor, "cost")

	seed := sel().Exprs(aSrc, aDst, aCost, sqlb.As(pg.ArrayOf(aSrc, aDst), "path")).From(anchor).Where(sqlb.Eq(aSrc, "A"))
	step := sel().Exprs(pSrc, eDst, sqlb.Add(pCost, eCost), pg.ArrayConcat(pPath, pg.ArrayOf(eDst))).
		From(paths).Join(inner(edges, sqlb.EqExpr(eSrc, pDst))).
		Where(pg.AllExpr(eDst, sqlb.OpNotEq, pPath), sqlb.Less(pg.Cardinality(pPath), 10)) // hop cap

	show(sel().WithRecursive("paths", seed, step).Clause(pg.DistinctOn(pDst)).
		Exprs(pDst, pCost, pPath).From(paths).Order(asc(pDst), asc(pCost)).Build())
	// Output:
	// WITH RECURSIVE "paths" AS (SELECT "edges"."src", "edges"."dst", "edges"."cost", ARRAY["edges"."src", "edges"."dst"] AS "path" FROM "edges" AS "edges" WHERE ("edges"."src" = $1) UNION ALL SELECT "p"."src", "e"."dst", ("p"."cost" + "e"."cost"), ("p"."path" || ARRAY["e"."dst"]) FROM "paths" AS "p" INNER JOIN "edges" AS "e" ON ("e"."src" = "p"."dst") WHERE ("e"."dst" <> ALL("p"."path") AND cardinality("p"."path") < $2))
	// SELECT DISTINCT ON ("p"."dst") "p"."dst", "p"."cost", "p"."path" FROM "paths" AS "p" ORDER BY "p"."dst" ASC, "p"."cost" ASC
	// args: 2
}

// ### 1.5 Connected components (label propagation)
func Example_s1_05_connectedComponents() {
	nodes := tbl("nodes", "nodes")
	nID := sqlb.Col[int64](nodes, "id")
	edges := tbl("edges", "e")
	eA, eB := sqlb.Col[int64](edges, "a"), sqlb.Col[int64](edges, "b")
	reach := tbl("reach", "r")
	rNode, rReached := ref[int64](reach, "node"), ref[int64](reach, "reached")

	seed := sel().Exprs(sqlb.As(nID, "node"), sqlb.As(nID, "reached")).From(nodes)
	other := sqlb.Case[int64]().When(sqlb.EqExpr(eA, rReached), eB).Else(eA)
	step := sel().Exprs(rNode, other).From(reach).Join(inner(edges, sqlb.InExprs(rReached, eA, eB)))
	// UNION, not UNION ALL, is what terminates this recursion.
	show(sel().WithRecursiveCTE(sqlb.CTE{Name: "reach", Body: seed.Union(step)}).
		Exprs(rNode, sqlb.As(sqlb.Min(rReached), "component")).From(reach).GroupBy(rNode).
		Order(asc(sqlb.OutputCol[int64]("component")), asc(rNode)).Build())
	// Output:
	// WITH RECURSIVE "reach" AS (SELECT "nodes"."id" AS "node", "nodes"."id" AS "reached" FROM "nodes" AS "nodes" UNION SELECT "r"."node", CASE WHEN "e"."a" = "r"."reached" THEN "e"."b" ELSE "e"."a" END FROM "reach" AS "r" INNER JOIN "edges" AS "e" ON ("r"."reached" IN ("e"."a", "e"."b")))
	// SELECT "r"."node", min("r"."reached") AS "component" FROM "reach" AS "r" GROUP BY "r"."node" ORDER BY "component" ASC, "r"."node" ASC
}

// ### 1.6 Subtree totals for every node at once
func Example_s1_06_subtreeTotals() {
	cats := tbl("categories", "c")
	cID, cParent, cAmount := sqlb.Col[int64](cats, "id"), sqlb.Col[*int64](cats, "parent_id"), sqlb.Col[float64](cats, "amount")
	t := tbl("t", "t")
	tID, tPath := ref[int64](t, "id"), ref[[]int64](t, "path")
	anchor := tbl("categories", "categories")
	aID, aParent, aAmount := sqlb.Col[int64](anchor, "id"), sqlb.Col[*int64](anchor, "parent_id"), sqlb.Col[float64](anchor, "amount")

	seed := sel().Exprs(aID, aParent, aAmount, sqlb.As(pg.ArrayOf(aID), "path")).From(anchor).Where(sqlb.IsNull(aParent))
	step := sel().Exprs(cID, cParent, cAmount, pg.ArrayConcat(tPath, pg.ArrayOf(cID))).
		From(cats).Join(inner(t, sqlb.EqExpr(cParent, sqlb.Nullable(tID))))

	a := tbl("t", "a")
	dd := tbl("t", "d")
	show(sel().WithRecursive("t", seed, step).
		Exprs(sqlb.Col[int64](a, "id"), sqlb.As(sqlb.Sum(sqlb.Col[float64](dd, "amount")), "subtree_total"),
			sqlb.As(sqlb.Sub(sqlb.Count(), sqlb.Param[int64](1)), "descendants")).
		From(a).Join(inner(dd, pg.AnyExpr(sqlb.Col[int64](a, "id"), sqlb.OpEq, sqlb.Col[[]int64](dd, "path")))).
		GroupBy(sqlb.Col[int64](a, "id")).Build())
	// Output:
	// WITH RECURSIVE "t" AS (SELECT "categories"."id", "categories"."parent_id", "categories"."amount", ARRAY["categories"."id"] AS "path" FROM "categories" AS "categories" WHERE ("categories"."parent_id" IS NULL) UNION ALL SELECT "c"."id", "c"."parent_id", "c"."amount", ("t"."path" || ARRAY["c"."id"]) FROM "categories" AS "c" INNER JOIN "t" AS "t" ON ("c"."parent_id" = "t"."id"))
	// SELECT "a"."id", sum("d"."amount") AS "subtree_total", (count(*) - $1) AS "descendants" FROM "t" AS "a" INNER JOIN "t" AS "d" ON ("a"."id" = ANY("d"."path")) GROUP BY "a"."id"
	// args: 1
}

// ### 1.7 Ancestors of many starting nodes in one query
func Example_s1_07_ancestorsOfMany() {
	c := tbl("categories", "c")
	cID, cParent := sqlb.Col[int64](c, "id"), sqlb.Col[*int64](c, "parent_id")
	up := tbl("up", "u")
	uStart, uParent, uDist := ref[int64](up, "start_id"), ref[*int64](up, "parent_id"), ref[int32](up, "dist")
	p := tbl("categories", "p")
	pID, pParent := sqlb.Col[int64](p, "id"), sqlb.Col[*int64](p, "parent_id")

	seed := sel().Exprs(sqlb.As(cID, "start_id"), cID, cParent, sqlb.As(sqlb.Param[int32](0), "dist")).
		From(c).Where(pg.InArray(cID, []int64{3, 7}))
	step := sel().Exprs(uStart, pID, pParent, sqlb.Add(uDist, sqlb.Param[int32](1))).
		From(up).Join(inner(p, sqlb.EqExpr(sqlb.Nullable(pID), uParent)))

	show(sel().WithRecursive("up", seed, step).
		Exprs(uStart, sqlb.As(pg.ArrayAgg(ref[int64](up, "id"), pg.OrderWithin(desc(uDist))), "ancestors_root_first")).
		From(up).GroupBy(uStart).Build())
	// Output:
	// WITH RECURSIVE "up" AS (SELECT "c"."id" AS "start_id", "c"."id", "c"."parent_id", $1 AS "dist" FROM "categories" AS "c" WHERE ("c"."id" = ANY($2)) UNION ALL SELECT "u"."start_id", "p"."id", "p"."parent_id", ("u"."dist" + $3) FROM "up" AS "u" INNER JOIN "categories" AS "p" ON ("p"."id" = "u"."parent_id"))
	// SELECT "u"."start_id", array_agg("u"."id" ORDER BY "u"."dist" DESC) AS "ancestors_root_first" FROM "up" AS "u" GROUP BY "u"."start_id"
	// args: 3
}

// ### 1.8 Loan amortization schedule (row depends on previous row)
func Example_s1_08_amortization() {
	params := tbl("params", "pr")
	principal, r, n := ref[float64](params, "principal"), ref[float64](params, "r"), ref[int32](params, "n")
	pmtT := tbl("pmt", "p")
	pmt := ref[float64](pmtT, "pmt")
	sched := tbl("sched", "s")
	sPeriod, sBalance := ref[int32](sched, "period"), ref[float64](sched, "balance")

	paramsBody := sel().Exprs(sqlb.As(sqlb.Literal[float64]("250000::numeric"), "principal"), sqlb.As(sqlb.Literal[float64]("0.05::numeric / 12"), "r"), sqlb.As(sqlb.Param[int32](360), "n"))
	// round(principal * r / (1 - power(1 + r, -n)), 2)
	pmtBody := sel().Exprs(sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(principal, r),
		sqlb.Sub(sqlb.Param(1.0), pg.PowerExpr(sqlb.Add(sqlb.Param(1.0), r), sqlb.Neg(n)))), 2), "pmt")).From(params)
	interest := pg.Round(sqlb.Mul(sBalance, r), 2)
	seed := sel().Exprs(sqlb.As(sqlb.Param[int32](0), "period"), sqlb.As(principal, "balance"),
		sqlb.As(sqlb.Literal[float64]("0::numeric"), "interest"), sqlb.As(sqlb.Literal[float64]("0::numeric"), "principal_paid")).From(params)
	step := sel().Exprs(sqlb.Add(sPeriod, sqlb.Param[int32](1)),
		pg.Round(sqlb.Sub(sBalance, sqlb.Sub(pmt, interest)), 2), interest, sqlb.Sub(pmt, interest)).
		From(sched).Join(cross(pmtT), cross(params)).Where(sqlb.LessExpr(sPeriod, n))

	// WITH RECURSIVE applies to the whole list, so the plain CTEs sit before it.
	show(sel().With("params", paramsBody).With("pmt", pmtBody).WithRecursive("sched", seed, step).
		Exprs(sPeriod, sBalance, ref[float64](sched, "interest"), ref[float64](sched, "principal_paid")).From(sched).Build())
	// Output:
	// WITH RECURSIVE "params" AS (SELECT 250000::numeric AS "principal", 0.05::numeric / 12 AS "r", $1 AS "n"),
	// "pmt" AS (SELECT round((("pr"."principal" * "pr"."r") / ($2 - power(($3 + "pr"."r"), (-"pr"."n"))))::numeric, $4) AS "pmt" FROM "params" AS "pr"),
	// "sched" AS (SELECT $5 AS "period", "pr"."principal" AS "balance", 0::numeric AS "interest", 0::numeric AS "principal_paid" FROM "params" AS "pr" UNION ALL SELECT ("s"."period" + $6), round(("s"."balance" - ("p"."pmt" - round(("s"."balance" * "pr"."r")::numeric, $7)))::numeric, $8), round(("s"."balance" * "pr"."r")::numeric, $9), ("p"."pmt" - round(("s"."balance" * "pr"."r")::numeric, $10)) FROM "sched" AS "s" CROSS JOIN "pmt" AS "p" CROSS JOIN "params" AS "pr" WHERE ("s"."period" < "pr"."n"))
	// SELECT "s"."period", "s"."balance", "s"."interest", "s"."principal_paid" FROM "sched" AS "s"
	// args: 10
}

// ### 1.9 Split a date interval into calendar-month chunks (proration)
func Example_s1_09_monthChunks() {
	subs := tbl("subscriptions", "subscriptions")
	sID, sStart, sEnd := sqlb.Col[int64](subs, "id"), sqlb.Col[time.Time](subs, "start_date"), sqlb.Col[time.Time](subs, "end_date")
	chunks := tbl("chunks", "c")
	cID, cStart, cEnd, cEndDate := ref[int64](chunks, "id"), ref[time.Time](chunks, "chunk_start"), ref[time.Time](chunks, "chunk_end"), ref[time.Time](chunks, "end_date")
	month := pg.IntervalLit("1 month")

	seed := sel().Exprs(sID, sqlb.As(sStart, "chunk_start"),
		sqlb.As(pg.Least(sEnd, sqlb.Cast[time.Time](pg.AddIntervalExpr(pg.DateTrunc(pg.FieldMonth, sStart), month), "date")), "chunk_end"),
		sEnd).From(subs)
	step := sel().Exprs(cID, cEnd, pg.Least(cEndDate, sqlb.Cast[time.Time](pg.AddIntervalExpr(cEnd, month), "date")), cEndDate).
		From(chunks).Where(sqlb.LessExpr(cEnd, cEndDate))

	show(sel().WithRecursive("chunks", seed, step).
		Exprs(cID, cStart, cEnd, sqlb.As(pg.DaysBetween(cEnd, cStart), "days")).
		From(chunks).Order(asc(cID), asc(cStart)).Build())
	// Output:
	// WITH RECURSIVE "chunks" AS (SELECT "subscriptions"."id", "subscriptions"."start_date" AS "chunk_start", LEAST("subscriptions"."end_date", CAST((date_trunc('month', "subscriptions"."start_date") + interval '1 month') AS date)) AS "chunk_end", "subscriptions"."end_date" FROM "subscriptions" AS "subscriptions" UNION ALL SELECT "c"."id", "c"."chunk_end", LEAST("c"."end_date", CAST(("c"."chunk_end" + interval '1 month') AS date)),
	// "c"."end_date" FROM "chunks" AS "c" WHERE ("c"."chunk_end" < "c"."end_date"))
	// SELECT "c"."id", "c"."chunk_start", "c"."chunk_end", ("c"."chunk_end" - "c"."chunk_start") AS "days" FROM "chunks" AS "c" ORDER BY "c"."id" ASC, "c"."chunk_start" ASC
}

// ### 1.10 Recursive CTE rules that bite
// Prose only: no SQL to render. The rules hold for sqlb-built recursions
// too: one recursive reference per step, no aggregate or LIMIT on it, cast
// in the anchor because it fixes the column types.
