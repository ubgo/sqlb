package snippets_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 11. NULL, numeric, and string edge cases

// ### 11.1 NOT IN with a NULL returns nothing
func Example_s11_01_notInWithNull() {
	u := tbl("users", "u")
	uID := sqlb.Col[int64](u, "id")
	t := tbl("teams", "t")
	mgr := sqlb.Col[*int64](t, "manager_id")
	show(sel().Exprs(uID).From(u).Where(sqlb.NotInSubquery(sqlb.Nullable(uID), sel().Exprs(mgr).From(t))).Build())
	show(sel().Exprs(uID).From(u).Where(sqlb.NotExists(sel().Exprs(sqlb.Param(1)).From(t).Where(sqlb.EqExpr(mgr, sqlb.Nullable(uID))))).Build())
	// Output:
	// SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."id" NOT IN (SELECT "t"."manager_id" FROM "teams" AS "t"))
	// SELECT "u"."id" FROM "users" AS "u" WHERE (NOT EXISTS (SELECT $1 FROM "teams" AS "t" WHERE ("t"."manager_id" = "u"."id")))
	// args: 1
}

// ### 11.2 NULL-safe comparison, unique NULLs, NULL ordering
// The two CREATE statements are DDL. The three queries render.
func Example_s11_02_nullSafe() {
	a, b := tbl("a", "a"), tbl("b", "b")
	aCode, bCode := sqlb.Col[*string](a, "code"), sqlb.Col[*string](b, "code")
	show(sel().Exprs(aCode).From(a).Join(inner(b, pg.IsNotDistinctFrom(aCode, bCode))).Build())
	t := tbl("t", "t")
	score, flag := sqlb.Col[*float64](t, "score"), sqlb.Col[bool](t, "flag")
	show(sel().Exprs(score).From(t).Order(pg.OrderByNulls(score, sqlb.SortDesc, false)).Build())
	show(sel().Exprs(sqlb.BoolExpr(pg.IsTrue(flag)), sqlb.BoolExpr(sqlb.Eq(flag, true))).From(t).Build())
	// Output:
	// SELECT "a"."code" FROM "a" AS "a" INNER JOIN "b" AS "b" ON (("a"."code" IS NOT DISTINCT FROM "b"."code"))
	// SELECT "t"."score" FROM "t" AS "t" ORDER BY "t"."score" DESC NULLS LAST
	// SELECT "t"."flag" IS TRUE, "t"."flag" = $1 FROM "t" AS "t"
	// args: 1
}

// ### 11.3 Aggregates over empty input
func Example_s11_03_aggregatesOverEmpty() {
	t := tbl("t", "t")
	x := sqlb.Col[int64](t, "x")
	show(sel().Exprs(sqlb.Count(), sqlb.CountOf(x), sqlb.Sum(x), sqlb.Avg(x), pg.BoolAnd(sqlb.BoolExpr(sqlb.Greater(x, 0))), pg.ArrayAgg(x),
		pg.StringAgg(sqlb.Cast[string](x, "text"), ",")).From(t).Where(sqlb.False()).Build())
	// Output:
	// SELECT count(*), count("t"."x"), sum("t"."x"), avg("t"."x"), bool_and("t"."x" > $1), array_agg("t"."x"), string_agg(CAST("t"."x" AS text), $2) FROM "t" AS "t" WHERE (FALSE)
	// args: 2
}

// ### 11.4 Integer division, modulo sign, rounding modes
func Example_s11_04_divisionAndRounding() {
	ten, four := sqlb.Param[int32](10), sqlb.Param[int32](4)
	show(sel().Exprs(sqlb.Div(ten, four), sqlb.Div(sqlb.Cast[float64](ten, "float8"), sqlb.Param(4.0)), sqlb.Div(sqlb.Cast[float64](ten, "numeric"), sqlb.Cast[float64](four, "numeric")),
		pg.Div(ten, four), pg.ModFn(ten, four),
		sqlb.Mod(ten, sqlb.Param[int32](-3)), sqlb.Mod(sqlb.Param[int32](-10), sqlb.Param[int32](3)),
		pg.RoundHalf(sqlb.Literal[float64]("2.5::numeric")), pg.RoundHalf(sqlb.Literal[float64]("2.5::float8")), pg.RoundHalf(sqlb.Literal[float64]("0.5::float8")),
		pg.Round(sqlb.Param(1234.5678), -2),
		pg.TruncZero(sqlb.Param(-1.7)), pg.Ceil(sqlb.Param(-1.7)), pg.Floor(sqlb.Param(-1.7))).Build())
	t := tbl("t", "t")
	x, divisor := sqlb.Col[float64](t, "x"), sqlb.Col[float64](t, "divisor")
	show(sel().Exprs(pg.Round(x, 2)).From(t).Build())
	show(sel().Exprs(sqlb.Div(sqlb.Param(1.0), sqlb.AssertNotNull(sqlb.NullIf(divisor, sqlb.Param(0.0))))).From(t).Build())
	// Output:
	// SELECT ($1 / $2), (CAST($3 AS float8) / $4), (CAST($5 AS numeric) / CAST($6 AS numeric)), div($7, $8), mod($9, $10), ($11 % $12), ($13 % $14), round(2.5::numeric), round(2.5::float8), round(0.5::float8), round($15::numeric, $16), trunc($17), ceil($18), floor($19)
	// args: 19
	// SELECT round("t"."x"::numeric, $1) FROM "t" AS "t"
	// args: 1
	// SELECT ($1 / NULLIF("t"."divisor", $2)) FROM "t" AS "t"
	// args: 2
}

// ### 11.5 Float and bigint precision
func Example_s11_05_precision() {
	show(sel().Exprs(
		sqlb.BoolExpr(sqlb.EqExpr(sqlb.Add(sqlb.Literal[float64]("0.1::float8"), sqlb.Literal[float64]("0.2::float8")), sqlb.Literal[float64]("0.3::float8"))),
		sqlb.BoolExpr(sqlb.EqExpr(sqlb.Add(sqlb.Literal[float64]("0.1::numeric"), sqlb.Literal[float64]("0.2")), sqlb.Literal[float64]("0.3"))),
		sqlb.Literal[int64]("9007199254740993::float8::bigint"),
		sqlb.BoolExpr(sqlb.EqExpr(sqlb.Literal[float64]("'NaN'::float8"), sqlb.Literal[float64]("'NaN'::float8"))),
		sqlb.BoolExpr(sqlb.GreaterExpr(sqlb.Literal[float64]("'NaN'::numeric"), sqlb.Literal[float64]("1e30::numeric"))),
	).Build())
	show(sel().Exprs(sqlb.Add(sqlb.Param[int32](2147483647), sqlb.Param[int32](1))).Build())
	show(sel().Exprs(sqlb.Add(sqlb.Cast[int64](sqlb.Param[int32](2147483647), "bigint"), sqlb.Param[int64](1))).Build())
	// Output:
	// SELECT (0.1::float8 + 0.2::float8) = 0.3::float8, (0.1::numeric + 0.2) = 0.3, 9007199254740993::float8::bigint, 'NaN'::float8 = 'NaN'::float8, 'NaN'::numeric > 1e30::numeric
	// SELECT ($1 + $2)
	// args: 2
	// SELECT (CAST($1 AS bigint) + $2)
	// args: 2
}

// ### 11.6 Splitting money so the parts sum exactly
func Example_s11_06_splitMoney() {
	g, part := pg.GenerateSeries(1, 3, 0, "g")
	inner := sel().Exprs(sqlb.As(part, "part"), sqlb.As(pg.Round(sqlb.Div(sqlb.Literal[float64]("100.00"), sqlb.Param(3.0)), 2), "share"),
		sqlb.As(sqlb.Literal[float64]("100.00"), "total"), sqlb.As(sqlb.Param[int32](3), "n")).From(g)
	s := sqlb.Derived(inner, "s")
	sPart, share, total, n := ref[int64](s, "part"), ref[float64](s, "share"), ref[float64](s, "total"), ref[int32](s, "n")
	remainder := sqlb.Case[float64]().When(sqlb.Eq(sPart, 1), sqlb.Sub(total, sqlb.Mul(share, sqlb.Cast[float64](n, "numeric")))).Else(sqlb.Param(0.0))
	show(sel().Exprs(sPart, sqlb.As(sqlb.Add(share, remainder), "adjusted")).From(s).Build())
	// Output:
	// SELECT "s"."part", ("s"."share" + CASE WHEN "s"."part" = $1 THEN ("s"."total" - ("s"."share" * CAST("s"."n" AS numeric))) ELSE $2 END) AS "adjusted" FROM (SELECT "g"."n" AS "part", round((100.00 / $3)::numeric, $4) AS "share", 100.00 AS "total", $5 AS "n" FROM generate_series(CAST($6 AS bigint), CAST($7 AS bigint)) AS "g" ("n")) AS "s"
	// args: 7
}

// ### 11.7 Regex: scalar match, global replace, backrefs, split, count
func Example_s11_07_regex() {
	t := tbl("t", "t")
	s := sqlb.Col[string](t, "s")
	show(sel().Exprs(
		sqlb.As(pg.Subscript(pg.RegexpMatch(s, `(\d+)-(\d+)`), 1), "first_group"),
		sqlb.As(pg.RegexpReplace(s, `(\w+)@(\w+)`, `\2 at \1`, "g"), "swapped"),
		sqlb.As(pg.RegexpReplace(s, `\s+`, " ", "g"), "squeezed"),
		sqlb.As(pg.RegexpSplitToArray(s, `\s*,\s*`), "csv_parts"),
		pg.RegexpCount(s, `\d`),
		pg.RegexpInstr(s, "@"), pg.RegexpSubstr(s, `[a-z]+@[a-z.]+`),
		sqlb.BoolExpr(pg.RegexI(s, "^hello")),
		sqlb.As(pg.SubstringRegex(s, `#(\d+)`), "issue_number"),
	).From(t).Build())
	// Output:
	// SELECT regexp_match("t"."s", $1)[$2] AS "first_group", regexp_replace("t"."s", $3, $4, $5) AS "swapped", regexp_replace("t"."s", $6, $7, $8) AS "squeezed", regexp_split_to_array("t"."s", $9) AS "csv_parts", regexp_count("t"."s", $10), regexp_instr("t"."s", $11), regexp_substr("t"."s", $12), ("t"."s" ~* $13), substring("t"."s" FROM $14) AS "issue_number" FROM "t" AS "t"
	// args: 14
}

// ### 11.8 Natural (version-aware) sort
func Example_s11_08_naturalSort() {
	f := tbl("files", "files")
	name := sqlb.Col[string](f, "name")
	numbered := pg.WithOrdinality(pg.FuncSource(sqlb.Fn[string]("regexp_split_to_table", name, sqlb.Param(`(?<=\D)(?=\d)|(?<=\d)(?=\D)`)), "p", "part", "i"))
	pPart, pI := ref[string](numbered, "part"), ref[int64](numbered, "i")
	padded := sqlb.Case[string]().When(pg.Regex(pPart, `^\d+$`), pg.LPad(pPart, 20, "0")).Else(pPart)
	key := sel().Exprs(pg.ArrayAgg(padded, pg.OrderWithin(asc(pI)))).From(numbered)
	show(sel().Exprs(name).From(f).Order(asc(sqlb.Subquery[[]string](key))).Build())
	r := tbl("releases", "releases")
	version := sqlb.Col[string](r, "version")
	show(sel().Exprs(version).From(r).Order(asc(sqlb.Cast[[]int32](pg.StringToArray(version, "."), "int[]"))).Build())
	// Output:
	// SELECT "files"."name" FROM "files" AS "files" ORDER BY (SELECT array_agg(CASE WHEN ("p"."part" ~ $1) THEN lpad("p"."part", $2, $3) ELSE "p"."part" END ORDER BY "p"."i" ASC) FROM regexp_split_to_table("files"."name", $4) WITH ORDINALITY AS "p" ("part", "i")) ASC
	// args: 4
	// SELECT "releases"."version" FROM "releases" AS "releases" ORDER BY CAST(string_to_array("releases"."version", $1) AS int[]) ASC
	// args: 1
}

// ### 11.9 Collation: byte order, case-insensitive, and speed
// CREATE COLLATION and CREATE TABLE are DDL. The two comparisons render.
func Example_s11_09_collation() {
	t := tbl("t", "t")
	name := sqlb.Col[string](t, "name")
	show(sel().Exprs(name).From(t).Order(asc(pg.Collate(name, "C"))).Build())
	show(sel().Exprs(sqlb.BoolExpr(sqlb.LessExpr(sqlb.Param("a"), pg.Collate(sqlb.Param("B"), "C"))), sqlb.BoolExpr(sqlb.LessExpr(sqlb.Param("a"), pg.Collate(sqlb.Param("B"), "en_US")))).Build())
	// Output:
	// SELECT "t"."name" FROM "t" AS "t" ORDER BY "t"."name" COLLATE "C" ASC
	// SELECT $1 < $2 COLLATE "C", $3 < $4 COLLATE "en_US"
	// args: 4
}

// ### 11.10 String functions that surprise
func Example_s11_10_stringFunctions() {
	show(sel().Exprs(
		pg.Left(sqlb.Param("abcdef"), -2),
		pg.ConcatOp(sqlb.Param("a"), sqlb.Literal[string]("NULL")),
		pg.ConcatWs(", ", sqlb.Param("a"), sqlb.Literal[string]("NULL"), sqlb.Param("b")),
		pg.Format("%I = %L (%s)", sqlb.Param("col name"), sqlb.Param("it's"), sqlb.Param(42)),
		pg.Length(sqlb.Param("héllo")), pg.OctetLength(sqlb.Param("héllo")),
		sqlb.BoolExpr(sqlb.EqExpr(pg.Normalize(sqlb.Param("é"), pg.NFD), sqlb.Param("é"))),
		sqlb.BoolExpr(sqlb.Like(sqlb.Param("abc"), `a\_c`)),
		sqlb.BoolExpr(pg.SimilarTo(sqlb.Param("a.b"), "a.b")),
		pg.SplitPart(sqlb.Param("a-b-c"), "-", -1),
		sqlb.Cast[int32](sqlb.AssertNotNull(sqlb.NullIf(pg.Trim(sqlb.Param(" 42 ")), sqlb.Param(""))), "int"),
		pg.QuoteIdent(sqlb.Param("weird col")), pg.QuoteLiteral(sqlb.Param("a'b")),
	).Build())
	// Output:
	// SELECT left($1, $2), ($3 || NULL), concat_ws($4, $5, NULL, $6), format($7, $8, $9, $10), length($11), octet_length($12), normalize($13, NFD) = $14, $15 LIKE $16, ($17 SIMILAR TO $18), split_part($19, $20, $21), CAST(NULLIF(btrim($22), $23) AS int), quote_ident($24), quote_literal($25)
	// args: 25
}

// ### 11.11 Row-value comparison and set-operation quirks
func Example_s11_11_rowValuesAndSetOps() {
	show(sel().Exprs(sqlb.BoolExpr(sqlb.RowLess(sqlb.Row(sqlb.Param(1), sqlb.Param("b")), sqlb.Row(sqlb.Param(1), sqlb.Param("c")))),
		sqlb.BoolExpr(sqlb.RowEq(sqlb.Row(sqlb.Param(1), sqlb.Literal[string]("NULL")), sqlb.Row(sqlb.Param(1), sqlb.Literal[string]("NULL"))))).Build())
	a, b, c := tbl("a", "a"), tbl("b", "b"), tbl("c", "c")
	star := func(t sqlb.Table) sqlb.SQLer { return sqlb.Col[[]byte](t, "*") }
	show(pg.ExceptAll(sel().Exprs(star(a)).From(a), sel().Exprs(star(b)).From(b)).Build())
	show(sel().Exprs(star(a)).From(a).Intersect(sel().Exprs(star(b)).From(b)).Union(sel().Exprs(star(c)).From(c)).Build())
	v := pg.Values("v", []string{"id", "name"}, []sqlb.SQLer{sqlb.Cast[int64](sqlb.Param(1), "bigint"), sqlb.Cast[*string](sqlb.Literal[*string]("NULL"), "text")}, []sqlb.SQLer{sqlb.Param(2), sqlb.Param("x")})
	show(sel().Exprs(ref[int64](v, "id"), ref[*string](v, "name")).From(v).Build())
	// Output:
	// SELECT ($1, $2) < ($3, $4), ($5, NULL) = ($6, NULL)
	// args: 6
	// SELECT "a"."*" FROM "a" AS "a" EXCEPT ALL SELECT "b"."*" FROM "b" AS "b"
	// SELECT "a"."*" FROM "a" AS "a" INTERSECT SELECT "b"."*" FROM "b" AS "b" UNION SELECT "c"."*" FROM "c" AS "c"
	// SELECT "v"."id", "v"."name" FROM (VALUES (CAST($1 AS bigint), CAST(NULL AS text)), ($2, $3)) AS "v" ("id", "name")
	// args: 3
}

// ### 11.12 Enum caveats
// ALTER TYPE, CREATE TYPE and ALTER TABLE are DDL. The ORDER BY renders and
// sorts by declaration order, which the builder cannot know or check.
func Example_s11_12_enumOrder() {
	items := tbl("items", "items")
	status := sqlb.Col[string](items, "status")
	show(sel().Exprs(status).From(items).Order(asc(status)).Build())
	// Output:
	// SELECT "items"."status" FROM "items" AS "items" ORDER BY "items"."status" ASC
}
