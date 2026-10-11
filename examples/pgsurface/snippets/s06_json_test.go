package snippets_test

import (
	"encoding/json"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 6. JSON and JSONB

// ### 6.1 JSONPath queries, predicates, and variables
func Example_s6_01_jsonpath() {
	o := tbl("orders", "orders")
	oID, doc := sqlb.Col[int64](o, "id"), sqlb.Col[json.RawMessage](o, "doc")
	items, item := pg.PathQueryVars(doc, `$.items[*] ? (@.price > $min && @.sku like_regex "^A" flag "i")`, json.RawMessage(`{"min": 10}`), "item")
	show(sel().Exprs(oID, sqlb.As(item, "item")).From(o).Join(cross(items)).
		Where(pg.PathExists(doc, `$.items[*] ? (@.qty > 100)`)).Build())
	show(sel().Exprs(
		sqlb.As(pg.PathQueryArray(doc, `$.items[*].price`), "prices"),
		sqlb.As(pg.PathQueryFirst(doc, `$.items[*] ? (@.sku == "A1")`), "first_a1"),
		sqlb.As(sqlb.BoolExpr(pg.PathExists(doc, `$.customer.vip ? (@ == true)`)), "is_vip"),
		sqlb.As(pg.PathQueryScalar(doc, `$.items.size()`), "n_items"),
	).From(o).Build())
	// Output:
	// SELECT "orders"."id", "item"."value" AS "item" FROM "orders" AS "orders" CROSS JOIN jsonb_path_query("orders"."doc", $1::jsonpath, $2::jsonb) AS "item" ("value") WHERE ("orders"."doc" @? $3::jsonpath)
	// args: 3
	// SELECT jsonb_path_query_array("orders"."doc", $1::jsonpath) AS "prices", jsonb_path_query_first("orders"."doc", $2::jsonpath) AS "first_a1", "orders"."doc" @? $3::jsonpath AS "is_vip", jsonb_path_query("orders"."doc", $4::jsonpath) AS "n_items" FROM "orders" AS "orders"
	// args: 4
}

// ### 6.2 Deep updates: jsonb_set, subscripting, delete-by-path, shallow merge
func Example_s6_02_deepUpdates() {
	o := tbl("orders", "o")
	doc := sqlb.Col[json.RawMessage](o, "doc")
	upd := func(as sqlb.Assignment) { show(sqlb.NewUpdate(d).Table(o).Set(as).All().Build()) }
	upd(sqlb.SetExpr(doc, pg.JSONBSet(doc, []string{"customer", "address", "zip"}, json.RawMessage(`"10001"`), true)))
	upd(sqlb.SetExpr(doc, pg.JSONBSetLax(doc, []string{"note"}, sqlb.Literal[json.RawMessage]("NULL"), true, "delete_key")))
	upd(sqlb.SetExpr(doc, pg.JSONConcat(doc, sqlb.Param(json.RawMessage(`{"status": "shipped", "shipped_at": "2024-09-16"}`)))))
	upd(sqlb.SetExpr(doc, pg.DeletePath(doc, []string{"customer", "ssn"})))
	upd(pg.SetSubscript(doc, []string{"customer", "tier"}, sqlb.Param(json.RawMessage(`"gold"`))))
	show(sel().Exprs(pg.JSONSubscript(doc, "customer", "address", "zip")).From(o).Build())
	// Output:
	// UPDATE "orders" AS "o" SET "doc" = jsonb_set("o"."doc", $1, $2, $3)
	// args: 3
	// UPDATE "orders" AS "o" SET "doc" = jsonb_set_lax("o"."doc", $1, NULL, $2, $3)
	// args: 3
	// UPDATE "orders" AS "o" SET "doc" = ("o"."doc" || $1)
	// args: 1
	// UPDATE "orders" AS "o" SET "doc" = ("o"."doc" #- $1)
	// args: 1
	// UPDATE "orders" AS "o" SET "doc"[$1][$2] = $3
	// args: 3
	// SELECT "o"."doc"[$1][$2][$3] FROM "orders" AS "o"
	// args: 3
}

// ### 6.3 Flatten arbitrary JSON to key paths (recursive walk)
func Example_s6_03_flattenJSON() {
	docs := tbl("docs", "docs")
	dID, doc := sqlb.Col[int64](docs, "id"), sqlb.Col[json.RawMessage](docs, "doc")
	each, k, v := pg.Each(doc, "e")
	seed := sel().Exprs(dID, sqlb.As(k, "key"), sqlb.As(v, "value"), sqlb.As(pg.ArrayOf(k), "path")).From(docs).Join(cross(each))

	flat := tbl("flat", "f")
	fID, fValue, fPath := ref[int64](flat, "id"), ref[json.RawMessage](flat, "value"), ref[[]string](flat, "path")
	objEach, ok, ov := pg.Each(fValue, "oe")
	objects := sel().Exprs(ok, ov).From(objEach).Where(sqlb.Eq(pg.JSONBTypeof(fValue), "object"))
	arr := pg.WithOrdinality(pg.FuncSource(sqlb.Fn[json.RawMessage]("jsonb_array_elements", fValue), "a", "value", "ord"))
	arrays := sel().Exprs(sqlb.Cast[string](sqlb.Sub(ref[int64](arr, "ord"), sqlb.Param[int64](1)), "text"), ref[json.RawMessage](arr, "value")).From(arr).
		Where(sqlb.Eq(pg.JSONBTypeof(fValue), "array"))
	c := sqlb.Derived(objects.UnionAll(arrays), "c")
	step := sel().Exprs(fID, ref[string](c, "key"), ref[json.RawMessage](c, "value"), pg.ArrayConcat(fPath, pg.ArrayOf(ref[string](c, "key")))).
		From(flat).Join(crossLateral(c))

	show(sel().WithRecursive("flat", seed, step).
		Exprs(fID, sqlb.As(pg.ArrayToString(fPath, "."), "json_path"), fValue).From(flat).
		Where(sqlb.NotIn(pg.JSONBTypeof(fValue), "object", "array")).Build())
	// Output:
	// WITH RECURSIVE "flat" AS (SELECT "docs"."id", "e"."key" AS "key", "e"."value" AS "value", ARRAY["e"."key"] AS "path" FROM "docs" AS "docs" CROSS JOIN jsonb_each("docs"."doc") AS "e" ("key", "value") UNION ALL SELECT "f"."id", "c"."key", "c"."value", ("f"."path" || ARRAY["c"."key"]) FROM "flat" AS "f" CROSS JOIN LATERAL (SELECT "oe"."key", "oe"."value" FROM jsonb_each("f"."value") AS "oe" ("key", "value") WHERE (jsonb_typeof("f"."value") = $1) UNION ALL SELECT CAST(("a"."ord" - $2) AS text),
	// "a"."value" FROM jsonb_array_elements("f"."value") WITH ORDINALITY AS "a" ("value", "ord") WHERE (jsonb_typeof("f"."value") = $3)) AS "c")
	// SELECT "f"."id", array_to_string("f"."path", $4) AS "json_path", "f"."value" FROM "flat" AS "f" WHERE (jsonb_typeof("f"."value") NOT IN ($5, $6))
	// args: 6
}

// ### 6.4 JSON_TABLE (PG17)
func Example_s6_04_jsonTable() {
	o := tbl("orders", "o")
	oID, doc := sqlb.Col[int64](o, "id"), sqlb.Col[json.RawMessage](o, "doc")
	jt := pg.JSONTable(doc, "$.items[*]", "jt",
		pg.JSONTableColumn{Name: "item_no", Ordinality: true},
		pg.JSONTableColumn{Name: "sku", Type: "text", Path: "$.sku"},
		pg.JSONTableColumn{Name: "qty", Type: "int", Path: "$.qty", DefaultOnEmpty: "0"},
		pg.JSONTableColumn{Name: "price", Type: "numeric", Path: "$.price", DefaultOnError: "0"},
		pg.JSONTableColumn{Nested: &pg.JSONTableNested{Path: "$.tags[*]", Columns: []pg.JSONTableColumn{{Name: "tag", Type: "text", Path: "$"}}}},
	)
	show(sel().Exprs(oID, ref[int64](jt, "item_no"), ref[string](jt, "sku"), ref[int32](jt, "qty"), ref[float64](jt, "price"), ref[*string](jt, "tag")).
		From(o).Join(cross(jt)).Build())
	// Output:
	// SELECT "o"."id", "jt"."item_no", "jt"."sku", "jt"."qty", "jt"."price", "jt"."tag" FROM "orders" AS "o" CROSS JOIN JSON_TABLE("o"."doc", '$.items[*]' COLUMNS ("item_no" FOR ORDINALITY, "sku" text PATH '$.sku', "qty" int PATH '$.qty' DEFAULT 0 ON EMPTY, "price" numeric PATH '$.price' DEFAULT 0 ON ERROR, NESTED PATH '$.tags[*]' COLUMNS ("tag" text PATH '$'))) AS "jt"
}

// ### 6.5 SQL/JSON constructors and predicates (PG16 constructors, PG17 query functions)
func Example_s6_05_sqlJSON() {
	u := tbl("users", "u")
	uID, uEmail, uPhone, uTag := sqlb.Col[int64](u, "id"), sqlb.Col[string](u, "email"), sqlb.Col[*string](u, "phone"), sqlb.Col[string](u, "tag")
	show(sel().Exprs(
		pg.JSONObject([]pg.JSONPair{pg.Pair("id", uID), pg.Pair("email", uEmail), pg.Pair("phone", uPhone)}, pg.AbsentOnNull(), pg.WithUniqueKeys(), pg.ReturningJSONB()),
		pg.JSONArrayAgg(uTag, pg.JSONOrderBy(asc(uTag)), pg.AbsentOnNull()),
		pg.JSONObjectAgg(sqlb.Col[string](u, "k"), sqlb.Col[string](u, "v"), pg.AbsentOnNull()),
	).From(u).Build())
	o := tbl("orders", "o")
	doc, rawText := sqlb.Col[json.RawMessage](o, "doc"), sqlb.Col[string](o, "raw_text")
	show(sel().Exprs(sqlb.BoolExpr(pg.IsJSON(doc, pg.JSONObj, false)), sqlb.BoolExpr(pg.IsJSON(doc, pg.JSONArr, false)), sqlb.BoolExpr(pg.IsJSON(rawText, pg.JSONAny, true))).From(o).Build())
	show(sel().Exprs(
		pg.JSONValue[int32](doc, "$.qty", pg.Returning("int"), pg.DefaultOnEmpty("0")),
		pg.JSONQuery(doc, "$.items[*].sku", pg.WithWrapper()),
		sqlb.BoolExpr(pg.JSONExists(doc, "$.customer.vip")),
	).From(o).Build())
	// Output:
	// SELECT JSON_OBJECT(CAST($1 AS text): "u"."id", CAST($2 AS text): "u"."email", CAST($3 AS text): "u"."phone" ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb), JSON_ARRAYAGG("u"."tag" ORDER BY "u"."tag" ASC ABSENT ON NULL), JSON_OBJECTAGG("u"."k": "u"."v" ABSENT ON NULL) FROM "users" AS "u"
	// args: 3
	// SELECT "o"."doc" IS JSON OBJECT, "o"."doc" IS JSON ARRAY, "o"."raw_text" IS JSON WITH UNIQUE KEYS FROM "orders" AS "o"
	// SELECT JSON_VALUE("o"."doc", '$.qty' RETURNING int DEFAULT 0 ON EMPTY), JSON_QUERY("o"."doc", '$.items[*].sku' WITH WRAPPER), JSON_EXISTS("o"."doc", '$.customer.vip') FROM "orders" AS "o"
}

// ### 6.6 Nested API document in one query
func Example_s6_06_nestedDocument() {
	c := tbl("customers", "c")
	cID, cName := sqlb.Col[int64](c, "id"), sqlb.Col[string](c, "name")
	o := tbl("orders", "o")
	oID, oCustomer, oTotal, oCreated := sqlb.Col[int64](o, "id"), sqlb.Col[int64](o, "customer_id"), sqlb.Col[float64](o, "total"), sqlb.Col[time.Time](o, "created_at")
	l := tbl("order_lines", "l")
	lOrder, lLine := sqlb.Col[int64](l, "order_id"), sqlb.Col[int32](l, "line_no")
	lRow := pg.RowRef(l)

	items := sqlb.Derived(sel().Exprs(sqlb.As(pg.JSONBAgg(pg.DeleteKey(pg.ToJSONB(lRow), "order_id"), pg.OrderWithin(asc(lLine))), "items")).
		From(l).Where(sqlb.EqExpr(lOrder, oID)), "i")
	emptyArr := pg.EmptyJSONArray()
	orders := sqlb.Derived(sel().Exprs(sqlb.As(pg.JSONBAgg(
		pg.BuildObject(pg.Key("id"), oID, pg.Key("total"), oTotal, pg.Key("items"), sqlb.Coalesce(ref[json.RawMessage](items, "items"), emptyArr)),
		pg.OrderWithin(desc(oCreated))), "orders")).
		From(o).Join(leftLateral(items)).Where(sqlb.EqExpr(oCustomer, cID)), "o")
	show(sel().Exprs(sqlb.As(pg.BuildObject(pg.Key("id"), cID, pg.Key("name"), cName, pg.Key("orders"), sqlb.Coalesce(ref[json.RawMessage](orders, "orders"), emptyArr)), "customer")).
		From(c).Join(leftLateral(orders)).Where(sqlb.Eq(cID, 7)).Build())
	// Output:
	// SELECT jsonb_build_object(CAST($1 AS text),
	// "c"."id", CAST($2 AS text),
	// "c"."name", CAST($3 AS text), COALESCE("o"."orders", '[]'::jsonb)) AS "customer" FROM "customers" AS "c" LEFT JOIN LATERAL (SELECT jsonb_agg(jsonb_build_object(CAST($4 AS text),
	// "o"."id", CAST($5 AS text),
	// "o"."total", CAST($6 AS text), COALESCE("i"."items", '[]'::jsonb)) ORDER BY "o"."created_at" DESC) AS "orders" FROM "orders" AS "o" LEFT JOIN LATERAL (SELECT jsonb_agg((to_jsonb("l") - $7) ORDER BY "l"."line_no" ASC) AS "items" FROM "order_lines" AS "l" WHERE ("l"."order_id" = "o"."id")) AS "i" ON (TRUE) WHERE ("o"."customer_id" = "c"."id")) AS "o" ON (TRUE) WHERE ("c"."id" = $8)
	// args: 8
}

// ### 6.7 Indexing JSONB correctly
// Not the query layer: CREATE INDEX statements. The planner note applies to
// the queries above: a ->> equality needs the expression index, containment
// uses GIN.

// ### 6.8 JSON null versus SQL NULL versus missing key
func Example_s6_08_jsonNullVersusSQLNull() {
	docs := tbl("docs", "docs")
	doc := sqlb.Col[json.RawMessage](docs, "doc")
	show(sel().Exprs(
		sqlb.As(sqlb.BoolExpr(sqlb.IsNull(pg.Get(doc, "x"))), "key_missing"),
		sqlb.As(sqlb.BoolExpr(sqlb.EqExpr(pg.Get(doc, "x"), pg.JSONNull())), "json_null"),
		sqlb.As(sqlb.BoolExpr(sqlb.IsNull(pg.JSONText(doc, "x"))), "missing_or_null"),
		sqlb.As(sqlb.BoolExpr(pg.HasKey(doc, "x")), "key_present"),
	).From(docs).Build())
	show(sel().Exprs(pg.StripNulls(sqlb.Param(json.RawMessage(`{"a": null, "b": {"c": null}}`)))).Build())
	// Output:
	// SELECT ("docs"."doc" -> $1) IS NULL AS "key_missing", ("docs"."doc" -> $2) = 'null'::jsonb AS "json_null", ("docs"."doc" ->> $3) IS NULL AS "missing_or_null", ("docs"."doc" ? $4) AS "key_present" FROM "docs" AS "docs"
	// args: 4
	// SELECT jsonb_strip_nulls($1)
	// args: 1
}

// ### 6.9 Diff two JSONB objects
func Example_s6_09_diffJSONB() {
	a, aKey, aVal := pg.Each(sqlb.Param(json.RawMessage(`{"x":1}`)), "a")
	b, _, bVal := pg.Each(sqlb.Param(json.RawMessage(`{"x":2}`)), "b")
	show(sel().Exprs(aKey, sqlb.As(aVal, "old_value"), sqlb.As(bVal, "new_value")).From(a).
		Join(sqlb.Join{Type: pg.JoinFull, Source: b, Using: []string{"key"}}).
		Where(pg.IsDistinctFrom(aVal, bVal)).Build())
	// Output:
	// SELECT "a"."key", "a"."value" AS "old_value", "b"."value" AS "new_value" FROM jsonb_each($1) AS "a" ("key", "value") FULL JOIN jsonb_each($2) AS "b" ("key", "value") USING ("key") WHERE (("a"."value" IS DISTINCT FROM "b"."value"))
	// args: 2
}

// ### 6.10 Update one element inside a JSON array by predicate
func Example_s6_10_updateArrayElement() {
	o := tbl("orders", "orders")
	doc := sqlb.Col[json.RawMessage](o, "doc")
	elems := pg.WithOrdinality(pg.FuncSource(sqlb.Fn[json.RawMessage]("jsonb_array_elements", pg.Get(doc, "items")), "t", "item", "ord"))
	item, ord := ref[json.RawMessage](elems, "item"), ref[int64](elems, "ord")
	rebuilt := sqlb.Case[json.RawMessage]().When(sqlb.Eq(pg.JSONText(item, "sku"), "A1"), pg.JSONConcat(item, sqlb.Param(json.RawMessage(`{"qty": 5}`)))).Else(item)
	newItems := sel().Exprs(pg.JSONBAgg(rebuilt, pg.OrderWithin(asc(ord)))).From(elems)
	show(sqlb.NewUpdate(d).Table(o).
		Set(sqlb.SetExpr(doc, pg.JSONBSetExpr(doc, []string{"items"}, sqlb.Subquery[json.RawMessage](newItems), true))).
		Where(pg.Contains(doc, json.RawMessage(`{"items": [{"sku": "A1"}]}`))).Build())
	// Output:
	// UPDATE "orders" AS "orders" SET "doc" = jsonb_set("orders"."doc", $1, (SELECT jsonb_agg(CASE WHEN ("t"."item" ->> $2) = $3 THEN ("t"."item" || $4) ELSE "t"."item" END ORDER BY "t"."ord" ASC) FROM jsonb_array_elements(("orders"."doc" -> $5)) WITH ORDINALITY AS "t" ("item", "ord")), $6) WHERE (("orders"."doc" @> $7))
	// args: 7
}

// ### 6.11 JSONB facts that surprise people
// Prose only. Two of them shape sqlb code: jsonb_agg over zero rows is NULL,
// so 6.6 wraps it in COALESCE; big ids are sent as strings.
