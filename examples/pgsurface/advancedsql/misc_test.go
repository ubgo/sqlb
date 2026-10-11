package advancedsql_test

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// Section 5. Misc Notes, and section 6. Codility.

// 5 quoted identifiers. sqlb quotes every identifier, so the README's point
// about case-sensitive names holds without effort.
func Example_s5_quotedIdentifiers() {
	t := sqlb.Table{Name: "table1", Alias: "t1"}
	col := sqlb.Col[string](t, "column1")
	show(sel().Columns(sqlb.Bind(col, &sink.s)).From(t).Where(sqlb.Eq(col, "name1")).Build())
	// Output:
	// SELECT "t1"."column1" FROM "table1" AS "t1" WHERE ("t1"."column1" = $1)
	// args: [name1]
}

// 5 a schema-qualified catalogue table.
func Example_s5_pgTables() {
	pgTables := sqlb.Table{Schema: "pg_catalog", Name: "pg_tables", Alias: "t"}
	schema := sqlb.Col[string](pgTables, "schemaname")
	show(sel().Columns(sqlb.Bind(schema, &sink.s), sqlb.Bind(sqlb.Col[string](pgTables, "tablename"), &sink.s2)).
		From(pgTables).Where(sqlb.NotEq(schema, "pg_catalog"), sqlb.NotEq(schema, "information_schema")).Build())
	// Output:
	// SELECT "t"."schemaname", "t"."tablename" FROM "pg_catalog"."pg_tables" AS "t" WHERE ("t"."schemaname" <> $1 AND "t"."schemaname" <> $2)
	// args: [pg_catalog information_schema]
}

// 6 the delta between an event and its predecessor, per type. lag is null
// only on the last row of each type, and that row is removed by the WHERE.
func deltaBody() *sqlb.Select[sqlb.PostgresDialect] {
	w := sqlb.NewWindow().PartitionBy(EvType).OrderBy(sqlb.OrderBy(EvTime, sqlb.SortDesc))
	delta := sqlb.Sub(sqlb.AssertNotNull(pg.Lag(EvValue, 1).Over(w)), EvValue)
	return sel().Exprs(EvType, EvValue, EvTime, sqlb.As(delta, "delta")).From(Events).Order(sqlb.OrderBy(EvType, sqlb.SortAsc))
}

func Example_s6_nthValueDerived() {
	dt := sqlb.Derived(deltaBody(), "delta")
	evType, delta := cteCol[string](dt, "event_type"), cteCol[int32](dt, "delta")
	show(sel().Distinct().Columns(
		sqlb.Bind(evType, &sink.s),
		sqlb.Bind(sqlb.As(sqlb.NthValue(delta, 1).Over(sqlb.NewWindow().PartitionBy(evType)), "value"), &sink.i32p),
	).From(dt).Where(sqlb.IsNotNull(delta)).Order(sqlb.OrderBy(evType, sqlb.SortAsc)).Build())
	// Output:
	// SELECT DISTINCT "delta"."event_type", nth_value("delta"."delta", $1) OVER (PARTITION BY "delta"."event_type") AS "value" FROM (SELECT "e"."event_type", "e"."value", "e"."time", (lag("e"."value", $2) OVER (PARTITION BY "e"."event_type" ORDER BY "e"."time" DESC) - "e"."value") AS "delta" FROM "events" AS "e" ORDER BY "e"."event_type" ASC) AS "delta" WHERE ("delta"."delta" IS NOT NULL) ORDER BY "delta"."event_type" ASC
	// args: [1 1]
}

func Example_s6_firstValueDerived() {
	dt := sqlb.Derived(deltaBody(), "delta")
	evType, delta := cteCol[string](dt, "event_type"), cteCol[int32](dt, "delta")
	show(sel().Distinct().Columns(
		sqlb.Bind(evType, &sink.s),
		sqlb.Bind(sqlb.As(sqlb.FirstValue(delta).Over(sqlb.NewWindow().PartitionBy(evType)), "value"), &sink.i32p),
	).From(dt).Where(sqlb.IsNotNull(delta)).Order(sqlb.OrderBy(evType, sqlb.SortAsc)).Build())
	// Output:
	// SELECT DISTINCT "delta"."event_type", first_value("delta"."delta") OVER (PARTITION BY "delta"."event_type") AS "value" FROM (SELECT "e"."event_type", "e"."value", "e"."time", (lag("e"."value", $1) OVER (PARTITION BY "e"."event_type" ORDER BY "e"."time" DESC) - "e"."value") AS "delta" FROM "events" AS "e" ORDER BY "e"."event_type" ASC) AS "delta" WHERE ("delta"."delta" IS NOT NULL) ORDER BY "delta"."event_type" ASC
	// args: [1]
}

func Example_s6_firstValueCTE() {
	dt := sqlb.Table{Name: "delta", Alias: "delta"}
	evType, delta := sqlb.Col[string](dt, "event_type"), sqlb.Col[int32](dt, "delta")
	show(sel().With("delta", deltaBody()).Distinct().Columns(
		sqlb.Bind(evType, &sink.s),
		sqlb.Bind(sqlb.As(sqlb.FirstValue(delta).Over(sqlb.NewWindow().PartitionBy(evType)), "value"), &sink.i32p),
	).From(dt).Where(sqlb.IsNotNull(delta)).Order(sqlb.OrderBy(evType, sqlb.SortAsc)).Build())
	// Output:
	// WITH "delta" AS (SELECT "e"."event_type", "e"."value", "e"."time", (lag("e"."value", $1) OVER (PARTITION BY "e"."event_type" ORDER BY "e"."time" DESC) - "e"."value") AS "delta" FROM "events" AS "e" ORDER BY "e"."event_type" ASC) SELECT DISTINCT "delta"."event_type", first_value("delta"."delta") OVER (PARTITION BY "delta"."event_type") AS "value" FROM "delta" AS "delta" WHERE ("delta"."delta" IS NOT NULL) ORDER BY "delta"."event_type" ASC
	// args: [1]
}
