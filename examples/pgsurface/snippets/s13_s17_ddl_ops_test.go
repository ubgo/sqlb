package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 13. Partitioning and zero-downtime DDL
//
// Every statement in 13.1, 13.2, 13.4 to 13.12 is DDL, a PL/pgSQL block or
// a maintenance command. The DDL is in scope as authored DDL, typed builders
// over the same Col[T] values the queries use, decided on 2026-09-21; the
// items below become examples as those builders land. Until then they keep
// their order here, and the PL/pgSQL blocks and maintenance commands stay the
// operator's:
//
// ### 13.1 Range partitioning with a default partition and fast attach — DDL
// ### 13.2 Automatic monthly partition creation — PL/pgSQL function
// ### 13.3 Partition pruning only works with plannable predicates — queries, below
// ### 13.4 Add NOT NULL without a long lock — DDL
// ### 13.5 Which ADD COLUMN rewrites the table — DDL
// ### 13.6 DDL with a lock timeout and retries — PL/pgSQL block
// ### 13.7 int to bigint primary key without downtime (outline) — DDL and trigger
// ### 13.8 CREATE INDEX CONCURRENTLY: invalid leftovers — DDL, plus one catalog query below
// ### 13.9 Table swap for a full rewrite — DDL
// ### 13.10 Generated columns, identity, LIKE, unlogged — DDL
// ### 13.11 Idempotent migration block — PL/pgSQL block
// ### 13.12 Foreign key details worth knowing — DDL

// ### 13.3 Partition pruning only works with plannable predicates
func Example_s13_03_partitionPruning() {
	ev := tbl("events", "e")
	created, tenant := sqlb.Col[time.Time](ev, "created_at"), sqlb.Col[int64](ev, "tenant_id")
	show(sel().Exprs(created).From(ev).Where(sqlb.GreaterEqExpr(created, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("1 day")))).Build())
	show(sel().Exprs(created).From(ev).Where(sqlb.GreaterEq(created, t0)).Build())
	show(sel().Exprs(created).From(ev).Where(sqlb.EqExpr(sqlb.Cast[time.Time](created, "date"), pg.CurrentDate())).Build())
	t := tbl("tenants", "t")
	show(sel().Exprs(created).From(ev).Join(inner(t, sqlb.EqExpr(sqlb.Col[int64](t, "id"), tenant))).Where(sqlb.Eq(sqlb.Col[string](t, "slug"), "x")).Build())
	// Output:
	// SELECT "e"."created_at" FROM "events" AS "e" WHERE ("e"."created_at" >= (now() - interval '1 day'))
	// SELECT "e"."created_at" FROM "events" AS "e" WHERE ("e"."created_at" >= $1)
	// args: 1
	// SELECT "e"."created_at" FROM "events" AS "e" WHERE (CAST("e"."created_at" AS date) = current_date)
	// SELECT "e"."created_at" FROM "events" AS "e" INNER JOIN "tenants" AS "t" ON ("t"."id" = "e"."tenant_id") WHERE ("t"."slug" = $1)
	// args: 1
}

// ### 13.8 CREATE INDEX CONCURRENTLY: invalid leftovers (the catalog query)
func Example_s13_08_invalidIndexes() {
	idx := tbl("pg_index", "pg_index")
	show(sel().Exprs(sqlb.Cast[string](sqlb.Col[int64](idx, "indexrelid"), "regclass")).From(idx).
		Where(sqlb.Not(sqlb.PredicateOf(sqlb.Col[bool](idx, "indisvalid").SQL))).Build())
	// Output:
	// SELECT CAST("pg_index"."indexrelid" AS regclass) FROM "pg_index" AS "pg_index" WHERE (NOT ("pg_index"."indisvalid"))
}

// ## 14. Triggers, PL/pgSQL, dynamic SQL
//
// ### 14.1 Generic audit trigger with a per-column diff — PL/pgSQL trigger
// ### 14.2 Statement-level triggers with transition tables — PL/pgSQL trigger
// ### 14.3 updated_at, immutable columns, recursion guard — PL/pgSQL trigger
// ### 14.4 Event trigger: block dangerous DDL — event trigger
// ### 14.5 Dynamic SQL done safely — PL/pgSQL; sqlb is the Go-side answer to the same rule
// ### 14.6 Exception handling with full diagnostics — PL/pgSQL
// ### 14.7 Polymorphic, variadic, table-returning, named arguments — DDL, plus the calls below
// ### 14.8 Volatility, SECURITY DEFINER, and SQL-standard bodies — DDL
// ### 14.9 LISTEN / NOTIFY from a trigger — trigger and session command
// ### 14.10 Autonomous transaction via dblink — extension call
// ### 14.11 PL/pgSQL gotchas — prose

// ### 14.7 Polymorphic, variadic, table-returning, named arguments (the calls)
func Example_s14_07_functionCalls() {
	top := pg.FuncSource(sqlb.Fn[[]byte]("top_customers", sqlb.Param(5), sqlb.Param("EU"), sqlb.Param("US")), "tc", "customer_id", "revenue")
	show(sel().Exprs(ref[int64](top, "customer_id"), ref[float64](top, "revenue")).From(top).Build())
	variadic := pg.FuncSource(sqlb.Fn[[]byte]("top_customers", sqlb.Param(5), pg.Variadic(sqlb.Param([]string{"EU"}))), "tc", "customer_id", "revenue")
	show(sel().Exprs(ref[int64](variadic, "customer_id")).From(variadic).Build())
	show(sel().Exprs(sqlb.Fn[int64]("count_where", pg.Named("p_schema", sqlb.Param("public")), pg.Named("p_table", sqlb.Param("users")), pg.Named("p_col", sqlb.Param("plan")), pg.Named("p_val", sqlb.Param("pro")))).Build())
	// Output:
	// SELECT "tc"."customer_id", "tc"."revenue" FROM top_customers($1, $2, $3) AS "tc" ("customer_id", "revenue")
	// args: 3
	// SELECT "tc"."customer_id" FROM top_customers($1, VARIADIC $2) AS "tc" ("customer_id", "revenue")
	// args: 2
	// SELECT count_where("p_schema" => $1, "p_table" => $2, "p_col" => $3, "p_val" => $4)
	// args: 4
}

// ### 14.10 Autonomous transaction via dblink (the call)
func Example_s14_10_dblink() {
	show(sel().Exprs(sqlb.Fn[string]("dblink_exec", sqlb.Concat(sqlb.Param("dbname="), pg.CurrentDatabase()),
		pg.Format("INSERT INTO attempt_log (msg, at) VALUES (%L, now())", sqlb.Param("about to try")))).Build())
	// Output:
	// SELECT dblink_exec(concat(CAST($1 AS text), current_database()), format($2, $3))
	// args: 3
}

// ## 15. Security: row-level security and privileges
//
// ### 15.1 Multi-tenant row-level security driven by a session setting — DDL and SET LOCAL
// ### 15.2 Column masking through a view — DDL; the masking expression is below
// ### 15.3 Least-privilege setup — GRANT and REVOKE

// ### 15.2 Column masking through a view (the masking expression)
func Example_s15_02_columnMasking() {
	u := tbl("users", "users")
	id, email, ssn := sqlb.Col[int64](u, "id"), sqlb.Col[string](u, "email"), sqlb.Col[string](u, "ssn")
	canSee := sqlb.PredicateOf(pg.HasRole(pg.CurrentUser(), "pii_reader", "member").SQL)
	show(sel().Exprs(id, email, sqlb.As(sqlb.Case[string]().When(canSee, ssn).Else(pg.ConcatOp(sqlb.Param("***-**-"), pg.Right(ssn, 4))), "ssn")).From(u).Build())
	// Output:
	// SELECT "users"."id", "users"."email", CASE WHEN pg_has_role(current_user, $1, $2) THEN "users"."ssn" ELSE ($3 || right("users"."ssn", $4)) END AS "ssn" FROM "users" AS "users"
	// args: 4
}

// ## 16. Performance and indexing

// ### 16.1 Keyset pagination
func Example_s16_01_keysetPagination() {
	p := tbl("posts", "posts")
	id, created, title := sqlb.Col[int64](p, "id"), sqlb.Col[time.Time](p, "created_at"), sqlb.Col[string](p, "title")
	show(sel().Exprs(id, created, title).From(p).
		Where(sqlb.RowLess(sqlb.Row(created, id), sqlb.Row(sqlb.Param(t1), sqlb.Param[int64](900)))).
		Order(desc(created), desc(id)).Limit(20).Build())
	// Output:
	// SELECT "posts"."id", "posts"."created_at", "posts"."title" FROM "posts" AS "posts" WHERE (("posts"."created_at", "posts"."id") < ($1, $2)) ORDER BY "posts"."created_at" DESC, "posts"."id" DESC LIMIT $3
	// args: 3
}

// ### 16.2 Index types and when each applies — DDL
// ### 16.3 Reading EXPLAIN properly — EXPLAIN is a planner command, not a query the builder renders
// ### 16.4 Fixing bad estimates — ANALYZE, ALTER and SET

// ### 16.5 Fast approximate count
// The count_estimate function is PL/pgSQL. The catalog estimate renders.
func Example_s16_05_approximateCount() {
	pc := tbl("pg_class", "pg_class")
	show(sel().Exprs(sqlb.As(sqlb.Cast[int64](sqlb.Col[float64](pc, "reltuples"), "bigint"), "estimate")).From(pc).
		Where(sqlb.EqExpr(sqlb.Col[int64](pc, "oid"), pg.Regclass("orders"))).Build())
	// Output:
	// SELECT CAST("pg_class"."reltuples" AS bigint) AS "estimate" FROM "pg_class" AS "pg_class" WHERE ("pg_class"."oid" = 'orders'::regclass)
}

// ### 16.6 Loose index scan (DISTINCT on a low-cardinality indexed column)
func Example_s16_06_looseIndexScan() {
	big := tbl("big", "big")
	col := sqlb.Col[string](big, "col")
	t := tbl("t", "t")
	tCol := ref[*string](t, "col")
	seed := sel().Exprs(sqlb.As(sqlb.Min(col), "col")).From(big)
	next := sel().Exprs(sqlb.Min(col)).From(big).Where(sqlb.GreaterExpr(col, sqlb.AssertNotNull(tCol)))
	step := sel().Exprs(sqlb.Subquery[string](next)).From(t).Where(sqlb.IsNotNull(tCol))
	show(sel().WithRecursive("t", seed, step).Exprs(tCol).From(t).Where(sqlb.IsNotNull(tCol)).Build())
	// Output:
	// WITH RECURSIVE "t" AS (SELECT min("big"."col") AS "col" FROM "big" AS "big" UNION ALL SELECT (SELECT min("big"."col") FROM "big" AS "big" WHERE ("big"."col" > "t"."col")) FROM "t" AS "t" WHERE ("t"."col" IS NOT NULL))
	// SELECT "t"."col" FROM "t" AS "t" WHERE ("t"."col" IS NOT NULL)
}

// ### 16.7 Query rewrites that change the plan
func Example_s16_07_queryRewrites() {
	o := tbl("orders", "orders")
	customer, rep := sqlb.Col[int64](o, "customer_id"), sqlb.Col[int64](o, "sales_rep_id")
	star := sqlb.Col[[]byte](o, "*")
	show(sel().Exprs(star).From(o).Where(sqlb.Eq(customer, 5)).Union(sel().Exprs(star).From(o).Where(sqlb.Eq(rep, 5))).Build())
	show(sel().Exprs(sqlb.BoolExpr(sqlb.Exists(sel().Exprs(sqlb.Param(1)).From(o).Where(sqlb.Eq(customer, 5))))).Build())
	ev := tbl("events", "events")
	ts, created, tenant := sqlb.Col[time.Time](ev, "ts"), sqlb.Col[time.Time](ev, "created_at"), sqlb.Col[int64](ev, "tenant_id")
	recent := sel().Exprs(sqlb.Col[[]byte](ev, "*")).From(ev).Where(sqlb.GreaterExpr(ts, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("1 hour"))))
	r := tbl("recent", "r")
	show(sel().WithCTE(sqlb.CTE{Name: "recent", Body: recent, Prefix: pg.Materialized(true)}).Exprs(sqlb.Col[[]byte](r, "*")).From(r).Build())
	show(sel().WithCTE(sqlb.CTE{Name: "recent", Body: recent, Prefix: pg.Materialized(false)}).Exprs(sqlb.Col[[]byte](r, "*")).From(r).Build())
	show(sel().Exprs(created).From(ev).Where(sqlb.GreaterExpr(created, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("1 day")))).Build())
	show(sel().Exprs(created).From(ev).Where(sqlb.GreaterExpr(created, pg.SubIntervalExpr(pg.ClockTimestamp(), pg.IntervalLit("1 day")))).Build())
	show(sel().Exprs(created).From(ev).Where(sqlb.Eq(tenant, 7)).Order(desc(created)).Limit(10).Build())
	// Output:
	// SELECT "orders"."*" FROM "orders" AS "orders" WHERE ("orders"."customer_id" = $1) UNION SELECT "orders"."*" FROM "orders" AS "orders" WHERE ("orders"."sales_rep_id" = $2)
	// args: 2
	// SELECT EXISTS (SELECT $1 FROM "orders" AS "orders" WHERE ("orders"."customer_id" = $2))
	// args: 2
	// WITH "recent" AS MATERIALIZED (SELECT "events"."*" FROM "events" AS "events" WHERE ("events"."ts" > (now() - interval '1 hour')))
	// SELECT "r"."*" FROM "recent" AS "r"
	// WITH "recent" AS NOT MATERIALIZED (SELECT "events"."*" FROM "events" AS "events" WHERE ("events"."ts" > (now() - interval '1 hour')))
	// SELECT "r"."*" FROM "recent" AS "r"
	// SELECT "events"."created_at" FROM "events" AS "events" WHERE ("events"."created_at" > (now() - interval '1 day'))
	// SELECT "events"."created_at" FROM "events" AS "events" WHERE ("events"."created_at" > (clock_timestamp() - interval '1 day'))
	// SELECT "events"."created_at" FROM "events" AS "events" WHERE ("events"."tenant_id" = $1) ORDER BY "events"."created_at" DESC LIMIT $2
	// args: 2
}

// ### 16.8 pg_stat_statements: where the time goes
func Example_s16_08_pgStatStatements() {
	s := tbl("pg_stat_statements", "pg_stat_statements")
	calls, total, mean, rows := sqlb.Col[int64](s, "calls"), sqlb.Col[float64](s, "total_exec_time"), sqlb.Col[float64](s, "mean_exec_time"), sqlb.Col[int64](s, "rows")
	hit, read, query := sqlb.Col[float64](s, "shared_blks_hit"), sqlb.Col[float64](s, "shared_blks_read"), sqlb.Col[string](s, "query")
	show(sel().Exprs(calls, sqlb.As(pg.Round(total, 1), "total_ms"), sqlb.As(pg.Round(mean, 2), "mean_ms"), rows,
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), hit), sqlb.AssertNotNull(sqlb.NullIf(sqlb.Add(hit, read), sqlb.Param(0.0)))), 1), "cache_hit_pct"),
		sqlb.As(pg.Left(query, 120), "query")).From(s).Order(desc(total)).Limit(20).Build())
	// Output:
	// SELECT "pg_stat_statements"."calls", round("pg_stat_statements"."total_exec_time"::numeric, $1) AS "total_ms", round("pg_stat_statements"."mean_exec_time"::numeric, $2) AS "mean_ms", "pg_stat_statements"."rows", round((($3 * "pg_stat_statements"."shared_blks_hit") / NULLIF(("pg_stat_statements"."shared_blks_hit" + "pg_stat_statements"."shared_blks_read"), $4))::numeric, $5) AS "cache_hit_pct", left("pg_stat_statements"."query", $6) AS "query" FROM "pg_stat_statements" AS "pg_stat_statements" ORDER BY "pg_stat_statements"."total_exec_time" DESC LIMIT $7
	// args: 7
}

// ### 16.9 TOAST and column storage
// The ALTER statements are DDL. The size probe renders.
func Example_s16_09_toast() {
	ev := tbl("events", "events")
	payload, id := sqlb.Col[[]byte](ev, "payload"), sqlb.Col[int64](ev, "id")
	show(sel().Exprs(sqlb.As(pg.ColumnSize(payload), "stored_bytes"), sqlb.As(pg.OctetLength(sqlb.Cast[string](payload, "text")), "raw_bytes")).
		From(ev).Where(sqlb.Eq(id, 1)).Build())
	// Output:
	// SELECT pg_column_size("events"."payload") AS "stored_bytes", octet_length(CAST("events"."payload" AS text)) AS "raw_bytes" FROM "events" AS "events" WHERE ("events"."id" = $1)
	// args: 1
}

// ### 16.10 HOT updates and fillfactor
// The ALTER statements are DDL. The HOT ratio probe renders.
func Example_s16_10_hotUpdates() {
	t := tbl("pg_stat_user_tables", "pg_stat_user_tables")
	upd, hot, rel := sqlb.Col[float64](t, "n_tup_upd"), sqlb.Col[float64](t, "n_tup_hot_upd"), sqlb.Col[string](t, "relname")
	show(sel().Exprs(upd, hot, sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), hot), sqlb.AssertNotNull(sqlb.NullIf(upd, sqlb.Param(0.0)))), 1), "hot_pct")).
		From(t).Where(sqlb.Eq(rel, "counters")).Build())
	// Output:
	// SELECT "pg_stat_user_tables"."n_tup_upd", "pg_stat_user_tables"."n_tup_hot_upd", round((($1 * "pg_stat_user_tables"."n_tup_hot_upd") / NULLIF("pg_stat_user_tables"."n_tup_upd", $2))::numeric, $3) AS "hot_pct" FROM "pg_stat_user_tables" AS "pg_stat_user_tables" WHERE ("pg_stat_user_tables"."relname" = $4)
	// args: 4
}

// ### 16.11 Sampling instead of ORDER BY random()
func Example_s16_11_tablesample() {
	big := tbl("big", "big")
	star := sqlb.Col[[]byte](big, "*")
	show(sel().Exprs(star).From(big).Clause(pg.TableSample(pg.SampleSystem, 0.1, 42)).Build())
	show(sel().Exprs(star).From(big).Clause(pg.TableSample(pg.SampleBernoulli, 1, -1)).Build())
	show(sel().Exprs(star).From(big).Clause(pg.TableSample(pg.SampleMethod("SYSTEM_ROWS"), 100, -1)).Build())
	// Output:
	// SELECT "big"."*" FROM "big" AS "big" TABLESAMPLE SYSTEM (0.1) REPEATABLE (42)
	// SELECT "big"."*" FROM "big" AS "big" TABLESAMPLE BERNOULLI (1)
	// SELECT "big"."*" FROM "big" AS "big" TABLESAMPLE SYSTEM_ROWS (100)
}

// ## 17. Operations and catalog queries
// Each is a SELECT over the catalog and renders. The typed door carries the
// catalog functions; 17.11 is a psql command and stays a comment.

// ### 17.1 Table sizes with heap, indexes, and TOAST split out
func Example_s17_01_tableSizes() {
	c, n := tbl("pg_class", "c"), tbl("pg_namespace", "n")
	oid, relname, toast, reltuples, relkind, relns := sqlb.Col[int64](c, "oid"), sqlb.Col[string](c, "relname"), sqlb.Col[int64](c, "reltoastrelid"), sqlb.Col[float64](c, "reltuples"), sqlb.Col[string](c, "relkind"), sqlb.Col[int64](c, "relnamespace")
	nspname, noid := sqlb.Col[string](n, "nspname"), sqlb.Col[int64](n, "oid")
	show(sel().Exprs(sqlb.As(pg.ConcatOp(pg.ConcatOp(nspname, sqlb.Param(".")), relname), "relation"),
		sqlb.As(pg.SizePretty(pg.TotalRelationSize(oid)), "total"), sqlb.As(pg.SizePretty(pg.RelationSize(oid)), "heap"),
		sqlb.As(pg.SizePretty(pg.IndexesSize(oid)), "indexes"),
		sqlb.As(pg.SizePretty(sqlb.Case[int64]().When(sqlb.NotEq(toast, 0), pg.TotalRelationSize(toast)).Else(sqlb.Param[int64](0))), "toast"),
		sqlb.As(sqlb.Cast[int64](reltuples, "bigint"), "est_rows")).
		From(c).Join(inner(n, sqlb.EqExpr(noid, relns))).
		Where(sqlb.In(relkind, "r", "m", "p"), sqlb.NotIn(nspname, "pg_catalog", "information_schema")).
		Order(desc(pg.TotalRelationSize(oid))).Limit(25).Build())
	// Output:
	// SELECT (("n"."nspname" || $1) || "c"."relname") AS "relation", pg_size_pretty(pg_total_relation_size("c"."oid")) AS "total", pg_size_pretty(pg_relation_size("c"."oid")) AS "heap", pg_size_pretty(pg_indexes_size("c"."oid")) AS "indexes", pg_size_pretty(CASE WHEN "c"."reltoastrelid" <> $2 THEN pg_total_relation_size("c"."reltoastrelid") ELSE $3 END) AS "toast", CAST("c"."reltuples" AS bigint) AS "est_rows" FROM "pg_class" AS "c" INNER JOIN "pg_namespace" AS "n" ON ("n"."oid" = "c"."relnamespace") WHERE ("c"."relkind" IN ($4, $5, $6) AND "n"."nspname" NOT IN ($7, $8)) ORDER BY pg_total_relation_size("c"."oid") DESC LIMIT $9
	// args: 9
}

// ### 17.2 Dead tuples, bloat, and vacuum progress
func Example_s17_02_deadTuples() {
	t := tbl("pg_stat_user_tables", "pg_stat_user_tables")
	rel, live, dead := sqlb.Col[string](t, "relname"), sqlb.Col[float64](t, "n_live_tup"), sqlb.Col[float64](t, "n_dead_tup")
	show(sel().Exprs(rel, live, dead,
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), dead), sqlb.AssertNotNull(sqlb.NullIf(sqlb.Add(live, dead), sqlb.Param(0.0)))), 1), "dead_pct"),
		sqlb.Col[*time.Time](t, "last_autovacuum"), sqlb.Col[*time.Time](t, "last_autoanalyze")).From(t).Order(desc(dead)).Limit(20).Build())
	// pgstattuple is an extension function source; pg_stat_progress_vacuum is a view.
	st := pg.FuncSource(sqlb.Fn[[]byte]("pgstattuple", sqlb.Param("orders")), "st")
	show(sel().Exprs(sqlb.Col[[]byte](sqlb.Ref(st), "*")).From(st).Build())
	// Output:
	// SELECT "pg_stat_user_tables"."relname", "pg_stat_user_tables"."n_live_tup", "pg_stat_user_tables"."n_dead_tup", round((($1 * "pg_stat_user_tables"."n_dead_tup") / NULLIF(("pg_stat_user_tables"."n_live_tup" + "pg_stat_user_tables"."n_dead_tup"), $2))::numeric, $3) AS "dead_pct", "pg_stat_user_tables"."last_autovacuum", "pg_stat_user_tables"."last_autoanalyze" FROM "pg_stat_user_tables" AS "pg_stat_user_tables" ORDER BY "pg_stat_user_tables"."n_dead_tup" DESC LIMIT $4
	// args: 4
	// SELECT "st"."*" FROM pgstattuple($1) AS "st"
	// args: 1
}

// ### 17.3 Foreign keys with no supporting index
func Example_s17_03_unindexedForeignKeys() {
	c, a, i := tbl("pg_constraint", "c"), tbl("pg_attribute", "a"), tbl("pg_index", "i")
	conrelid, conkey, conname, contype := sqlb.Col[int64](c, "conrelid"), sqlb.Col[[]int16](c, "conkey"), sqlb.Col[string](c, "conname"), sqlb.Col[string](c, "contype")
	x := pg.WithOrdinality(pg.FuncSource(sqlb.Fn[int16]("unnest", conkey), "x", "attnum", "n"))
	xAttnum, xN := ref[int16](x, "attnum"), ref[int64](x, "n")
	attname, attnum, attrelid := sqlb.Col[string](a, "attname"), sqlb.Col[int16](a, "attnum"), sqlb.Col[int64](a, "attrelid")
	indrelid, indpred, indkey := sqlb.Col[int64](i, "indrelid"), sqlb.Col[*string](i, "indpred"), sqlb.Col[string](i, "indkey")
	leading := pg.SliceExprs(sqlb.Cast[[]int16](indkey, "smallint[]"), sqlb.Param(0), sqlb.Sub(pg.Cardinality(conkey), sqlb.Param[int32](1)))
	covered := sel().Exprs(sqlb.Param(1)).From(i).Where(sqlb.EqExpr(indrelid, conrelid), sqlb.IsNull(indpred), pg.ArrayContainsExpr(leading, conkey))
	show(sel().Exprs(sqlb.As(sqlb.Cast[string](conrelid, "regclass"), "table"),
		sqlb.As(pg.StringAgg(attname, ", ", pg.OrderWithin(asc(xN))), "fk_columns"), conname,
		sqlb.As(pg.SizePretty(pg.RelationSize(conrelid)), "table_size")).
		From(c).Join(crossLateral(x), inner(a, sqlb.EqExpr(attnum, xAttnum), sqlb.EqExpr(attrelid, conrelid))).
		Where(sqlb.Eq(contype, "f"), sqlb.NotExists(covered)).
		GroupBy(conrelid, conname).Order(desc(pg.RelationSize(conrelid))).Build())
	// Output:
	// SELECT CAST("c"."conrelid" AS regclass) AS "table", string_agg("a"."attname", $1 ORDER BY "x"."n" ASC) AS "fk_columns", "c"."conname", pg_size_pretty(pg_relation_size("c"."conrelid")) AS "table_size" FROM "pg_constraint" AS "c" CROSS JOIN LATERAL unnest("c"."conkey") WITH ORDINALITY AS "x" ("attnum", "n") INNER JOIN "pg_attribute" AS "a" ON ("a"."attnum" = "x"."attnum" AND "a"."attrelid" = "c"."conrelid") WHERE ("c"."contype" = $2 AND NOT EXISTS (SELECT $3 FROM "pg_index" AS "i" WHERE ("i"."indrelid" = "c"."conrelid" AND "i"."indpred" IS NULL AND ((CAST("i"."indkey" AS smallint[]))[$4:(cardinality("c"."conkey") - $5)] @> "c"."conkey")))) GROUP BY "c"."conrelid", "c"."conname" ORDER BY pg_relation_size("c"."conrelid") DESC
	// args: 5
}

// ### 17.4 Unused and duplicate indexes
func Example_s17_04_unusedAndDuplicateIndexes() {
	s, i := tbl("pg_stat_user_indexes", "s"), tbl("pg_index", "i")
	sIdx, sScan := sqlb.Col[int64](s, "indexrelid"), sqlb.Col[int64](s, "idx_scan")
	iIdx, unique, primary := sqlb.Col[int64](i, "indexrelid"), sqlb.Col[bool](i, "indisunique"), sqlb.Col[bool](i, "indisprimary")
	show(sel().Exprs(sqlb.Col[string](s, "schemaname"), sqlb.Col[string](s, "relname"), sqlb.Col[string](s, "indexrelname"),
		sqlb.As(pg.SizePretty(pg.RelationSize(sIdx)), "size"), sScan).
		From(s).Join(inner(i, sqlb.EqExpr(iIdx, sIdx))).
		Where(sqlb.Eq(sScan, 0), sqlb.Not(sqlb.PredicateOf(unique.SQL)), sqlb.Not(sqlb.PredicateOf(primary.SQL))).
		Order(desc(pg.RelationSize(sIdx))).Build())
	idx := tbl("pg_index", "pg_index")
	indrelid, indexrelid := sqlb.Col[int64](idx, "indrelid"), sqlb.Col[int64](idx, "indexrelid")
	text := func(col string) sqlb.SQLer { return sqlb.Cast[string](sqlb.Col[string](idx, col), "text") }
	show(sel().Exprs(sqlb.As(sqlb.Cast[string](indrelid, "regclass"), "table"), sqlb.As(pg.ArrayAgg(sqlb.Cast[string](indexrelid, "regclass")), "duplicates"),
		sqlb.As(pg.SizePretty(sqlb.Sum(pg.RelationSize(indexrelid))), "total_size")).
		From(idx).GroupBy(indrelid, text("indkey"), text("indclass"), text("indoption"),
		sqlb.Coalesce(sqlb.Cast[string](sqlb.Col[*string](idx, "indexprs"), "text"), sqlb.Param("")), sqlb.Coalesce(sqlb.Cast[string](sqlb.Col[*string](idx, "indpred"), "text"), sqlb.Param(""))).
		Having(sqlb.Greater(sqlb.Count(), 1)).Build())
	// Output:
	// SELECT "s"."schemaname", "s"."relname", "s"."indexrelname", pg_size_pretty(pg_relation_size("s"."indexrelid")) AS "size", "s"."idx_scan" FROM "pg_stat_user_indexes" AS "s" INNER JOIN "pg_index" AS "i" ON ("i"."indexrelid" = "s"."indexrelid") WHERE ("s"."idx_scan" = $1 AND NOT ("i"."indisunique") AND NOT ("i"."indisprimary")) ORDER BY pg_relation_size("s"."indexrelid") DESC
	// args: 1
	// SELECT CAST("pg_index"."indrelid" AS regclass) AS "table", array_agg(CAST("pg_index"."indexrelid" AS regclass)) AS "duplicates", pg_size_pretty(sum(pg_relation_size("pg_index"."indexrelid"))) AS "total_size" FROM "pg_index" AS "pg_index" GROUP BY "pg_index"."indrelid", CAST("pg_index"."indkey" AS text), CAST("pg_index"."indclass" AS text), CAST("pg_index"."indoption" AS text), COALESCE(CAST("pg_index"."indexprs" AS text), $1), COALESCE(CAST("pg_index"."indpred" AS text), $2) HAVING (count(*) > $3)
	// args: 3
}

// ### 17.5 Sequences about to overflow their column
func Example_s17_05_sequenceOverflow() {
	dep, seq, table, col, ps := tbl("pg_depend", "d"), tbl("pg_class", "seq"), tbl("pg_class", "tbl"), tbl("pg_attribute", "col"), tbl("pg_sequences", "ps")
	typ := pg.FormatType(sqlb.Col[int64](col, "atttypid"), sqlb.Col[int32](col, "atttypmod"))
	colMax := sqlb.Case[int64]().When(sqlb.Eq(typ, "smallint"), sqlb.Param[int64](32767)).When(sqlb.Eq(typ, "integer"), sqlb.Param[int64](2147483647)).Else(sqlb.Param[int64](9223372036854775807))
	last := sqlb.Col[int64](ps, "last_value")
	show(sel().Exprs(sqlb.As(sqlb.Col[string](seq, "relname"), "sequence"), sqlb.As(sqlb.Col[string](table, "relname"), "table"), sqlb.As(sqlb.Col[string](col, "attname"), "column"),
		sqlb.As(typ, "type"), last, sqlb.As(colMax, "column_max"),
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), sqlb.Cast[float64](last, "float8")), sqlb.Cast[float64](colMax, "float8")), 2), "pct_used")).
		From(dep).
		Join(inner(seq, sqlb.EqExpr(sqlb.Col[int64](seq, "oid"), sqlb.Col[int64](dep, "objid")), sqlb.Eq(sqlb.Col[string](seq, "relkind"), "S")),
			inner(table, sqlb.EqExpr(sqlb.Col[int64](table, "oid"), sqlb.Col[int64](dep, "refobjid"))),
			inner(col, sqlb.EqExpr(sqlb.Col[int64](col, "attrelid"), sqlb.Col[int64](dep, "refobjid")), sqlb.EqExpr(sqlb.Col[int32](col, "attnum"), sqlb.Col[int32](dep, "refobjsubid"))),
			inner(ps, sqlb.EqExpr(sqlb.Col[string](ps, "schemaname"), sqlb.Cast[string](sqlb.Cast[string](sqlb.Col[int64](seq, "relnamespace"), "regnamespace"), "text")), sqlb.EqExpr(sqlb.Col[string](ps, "sequencename"), sqlb.Col[string](seq, "relname")))).
		Where(sqlb.In(sqlb.Col[string](dep, "deptype"), "a", "i"), sqlb.IsNotNull(last)).
		Order(desc(sqlb.OutputCol[float64]("pct_used"))).Build())
	// Output:
	// SELECT "seq"."relname" AS "sequence", "tbl"."relname" AS "table", "col"."attname" AS "column", format_type("col"."atttypid", "col"."atttypmod") AS "type", "ps"."last_value", CASE WHEN format_type("col"."atttypid", "col"."atttypmod") = $1 THEN $2 WHEN format_type("col"."atttypid", "col"."atttypmod") = $3 THEN $4 ELSE $5 END AS "column_max", round((($6 * CAST("ps"."last_value" AS float8)) / CAST(CASE WHEN format_type("col"."atttypid", "col"."atttypmod") = $7 THEN $8 WHEN format_type("col"."atttypid", "col"."atttypmod") = $9 THEN $10 ELSE $11 END AS float8))::numeric, $12) AS "pct_used" FROM "pg_depend" AS "d" INNER JOIN "pg_class" AS "seq" ON ("seq"."oid" = "d"."objid" AND "seq"."relkind" = $13) INNER JOIN "pg_class" AS "tbl" ON ("tbl"."oid" = "d"."refobjid") INNER JOIN "pg_attribute" AS "col" ON ("col"."attrelid" = "d"."refobjid" AND "col"."attnum" = "d"."refobjsubid") INNER JOIN "pg_sequences" AS "ps" ON ("ps"."schemaname" = CAST(CAST("seq"."relnamespace" AS regnamespace) AS text) AND "ps"."sequencename" = "seq"."relname") WHERE ("d"."deptype" IN ($14, $15) AND "ps"."last_value" IS NOT NULL) ORDER BY "pct_used" DESC
	// args: 15
}

// ### 17.6 Transaction ID wraparound distance
func Example_s17_06_wraparound() {
	db := tbl("pg_database", "pg_database")
	frozen := pg.XidAge(sqlb.Col[int64](db, "datfrozenxid"))
	show(sel().Exprs(sqlb.Col[string](db, "datname"), sqlb.As(frozen, "xid_age"),
		sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), sqlb.Cast[float64](frozen, "float8")), sqlb.Param(2147483648.0)), 1), "pct_to_wraparound")).
		From(db).Order(desc(sqlb.OutputCol[int64]("xid_age"))).Build())
	pc := tbl("pg_class", "pg_class")
	show(sel().Exprs(sqlb.Col[string](pc, "relname"), pg.XidAge(sqlb.Col[int64](pc, "relfrozenxid"))).From(pc).
		Where(sqlb.Eq(sqlb.Col[string](pc, "relkind"), "r")).Order(desc(pg.XidAge(sqlb.Col[int64](pc, "relfrozenxid")))).Limit(10).Build())
	px := tbl("pg_prepared_xacts", "pg_prepared_xacts")
	show(sel().Exprs(sqlb.Col[[]byte](px, "*")).From(px).Build())
	// Output:
	// SELECT "pg_database"."datname", age("pg_database"."datfrozenxid") AS "xid_age", round((($1 * CAST(age("pg_database"."datfrozenxid") AS float8)) / $2)::numeric, $3) AS "pct_to_wraparound" FROM "pg_database" AS "pg_database" ORDER BY "xid_age" DESC
	// args: 3
	// SELECT "pg_class"."relname", age("pg_class"."relfrozenxid") FROM "pg_class" AS "pg_class" WHERE ("pg_class"."relkind" = $1) ORDER BY age("pg_class"."relfrozenxid") DESC LIMIT $2
	// args: 2
	// SELECT "pg_prepared_xacts"."*" FROM "pg_prepared_xacts" AS "pg_prepared_xacts"
}

// ### 17.7 What is running, what is idle, what is waiting
func Example_s17_07_activity() {
	a := tbl("pg_stat_activity", "pg_stat_activity")
	pid, state, xact, qstart, query := sqlb.Col[int32](a, "pid"), sqlb.Col[string](a, "state"), sqlb.Col[time.Time](a, "xact_start"), sqlb.Col[time.Time](a, "query_start"), sqlb.Col[string](a, "query")
	show(sel().Exprs(pid, sqlb.Col[string](a, "usename"), sqlb.Col[string](a, "application_name"), state, sqlb.Col[string](a, "wait_event_type"), sqlb.Col[string](a, "wait_event"),
		sqlb.As(pg.Diff(pg.Now(), xact), "xact_age"), sqlb.As(pg.Diff(pg.Now(), qstart), "query_age"), sqlb.As(pg.Left(query, 100), "query")).
		From(a).Where(sqlb.NotEq(state, "idle"), sqlb.NotEqExpr(pid, pg.BackendPID())).Order(pg.OrderByNulls(xact, sqlb.SortAsc, false)).Build())
	show(sel().Exprs(state, sqlb.Count()).From(a).GroupBy(state).Build())
	show(sel().Exprs(pg.TerminateBackend(pid)).From(a).Where(sqlb.Eq(sqlb.Col[string](a, "datname"), "app"), sqlb.NotEqExpr(pid, pg.BackendPID())).Build())
	// Output:
	// SELECT "pg_stat_activity"."pid", "pg_stat_activity"."usename", "pg_stat_activity"."application_name", "pg_stat_activity"."state", "pg_stat_activity"."wait_event_type", "pg_stat_activity"."wait_event", (now() - "pg_stat_activity"."xact_start") AS "xact_age", (now() - "pg_stat_activity"."query_start") AS "query_age", left("pg_stat_activity"."query", $1) AS "query" FROM "pg_stat_activity" AS "pg_stat_activity" WHERE ("pg_stat_activity"."state" <> $2 AND "pg_stat_activity"."pid" <> pg_backend_pid()) ORDER BY "pg_stat_activity"."xact_start" ASC NULLS LAST
	// args: 2
	// SELECT "pg_stat_activity"."state", count(*) FROM "pg_stat_activity" AS "pg_stat_activity" GROUP BY "pg_stat_activity"."state"
	// SELECT pg_terminate_backend("pg_stat_activity"."pid") FROM "pg_stat_activity" AS "pg_stat_activity" WHERE ("pg_stat_activity"."datname" = $1 AND "pg_stat_activity"."pid" <> pg_backend_pid())
	// args: 1
}

// ### 17.8 Replication lag
func Example_s17_08_replicationLag() {
	r := tbl("pg_stat_replication", "pg_stat_replication")
	lsnDiff := pg.WalLsnDiff(pg.CurrentWalLsn(), sqlb.Col[string](r, "replay_lsn"))
	show(sel().Exprs(sqlb.Col[string](r, "client_addr"), sqlb.Col[string](r, "state"), sqlb.Col[string](r, "sync_state"), sqlb.As(lsnDiff, "replay_lag_bytes"),
		sqlb.Col[pg.Interval](r, "write_lag"), sqlb.Col[pg.Interval](r, "flush_lag"), sqlb.Col[pg.Interval](r, "replay_lag")).From(r).Build())
	show(sel().Exprs(sqlb.As(pg.Diff(pg.Now(), pg.LastXactReplayTimestamp()), "lag")).Build())
	s := tbl("pg_replication_slots", "pg_replication_slots")
	show(sel().Exprs(sqlb.Col[string](s, "slot_name"), sqlb.Col[bool](s, "active"),
		sqlb.As(pg.SizePretty(pg.WalLsnDiff(pg.CurrentWalLsn(), sqlb.Col[string](s, "restart_lsn"))), "retained_wal")).From(s).Build())
	// Output:
	// SELECT "pg_stat_replication"."client_addr", "pg_stat_replication"."state", "pg_stat_replication"."sync_state", pg_wal_lsn_diff(pg_current_wal_lsn(),
	// "pg_stat_replication"."replay_lsn") AS "replay_lag_bytes", "pg_stat_replication"."write_lag", "pg_stat_replication"."flush_lag", "pg_stat_replication"."replay_lag" FROM "pg_stat_replication" AS "pg_stat_replication"
	// SELECT (now() - pg_last_xact_replay_timestamp()) AS "lag"
	// SELECT "pg_replication_slots"."slot_name", "pg_replication_slots"."active", pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(),
	// "pg_replication_slots"."restart_lsn")) AS "retained_wal" FROM "pg_replication_slots" AS "pg_replication_slots"
}

// ### 17.9 What depends on this column (why ALTER fails)
func Example_s17_09_columnDependencies() {
	dep, rw, v := tbl("pg_depend", "d"), tbl("pg_rewrite", "r"), tbl("pg_class", "v")
	att := tbl("pg_attribute", "pg_attribute")
	attnum := sel().Exprs(sqlb.Col[int32](att, "attnum")).From(att).
		Where(sqlb.EqExpr(sqlb.Col[int64](att, "attrelid"), pg.Regclass("orders")), sqlb.Eq(sqlb.Col[string](att, "attname"), "total"))
	show(sel().Distinct().Exprs(sqlb.As(sqlb.Cast[string](sqlb.Col[int64](v, "oid"), "regclass"), "dependent_view")).From(dep).
		Join(inner(rw, sqlb.EqExpr(sqlb.Col[int64](rw, "oid"), sqlb.Col[int64](dep, "objid"))), inner(v, sqlb.EqExpr(sqlb.Col[int64](v, "oid"), sqlb.Col[int64](rw, "ev_class")))).
		Where(sqlb.EqExpr(sqlb.Col[int64](dep, "refobjid"), pg.Regclass("orders")),
			sqlb.EqExpr(sqlb.Col[int32](dep, "refobjsubid"), sqlb.AssertNotNull(sqlb.Subquery[int32](attnum))),
			sqlb.In(sqlb.Col[string](v, "relkind"), "v", "m")).Build())
	// Output:
	// SELECT DISTINCT CAST("v"."oid" AS regclass) AS "dependent_view" FROM "pg_depend" AS "d" INNER JOIN "pg_rewrite" AS "r" ON ("r"."oid" = "d"."objid") INNER JOIN "pg_class" AS "v" ON ("v"."oid" = "r"."ev_class") WHERE ("d"."refobjid" = 'orders'::regclass AND "d"."refobjsubid" = (SELECT "pg_attribute"."attnum" FROM "pg_attribute" AS "pg_attribute" WHERE ("pg_attribute"."attrelid" = 'orders'::regclass AND "pg_attribute"."attname" = $1)) AND "v"."relkind" IN ($2, $3))
	// args: 3
}

// ### 17.10 Config drift and cache hit ratio
func Example_s17_10_configDrift() {
	s := tbl("pg_settings", "pg_settings")
	show(sel().Exprs(sqlb.Col[string](s, "name"), sqlb.Col[string](s, "setting"), sqlb.Col[*string](s, "unit"), sqlb.Col[string](s, "source"), sqlb.Col[*string](s, "sourcefile"), sqlb.Col[*int32](s, "sourceline")).
		From(s).Where(sqlb.NotIn(sqlb.Col[string](s, "source"), "default", "override")).Order(asc(sqlb.Col[string](s, "name"))).Build())
	f := tbl("pg_file_settings", "pg_file_settings")
	show(sel().Exprs(sqlb.Col[[]byte](f, "*")).From(f).Where(sqlb.IsNotNull(sqlb.Col[*string](f, "error"))).Build())
	io := tbl("pg_statio_user_tables", "pg_statio_user_tables")
	hit, read := sqlb.Col[float64](io, "heap_blks_hit"), sqlb.Col[float64](io, "heap_blks_read")
	show(sel().Exprs(sqlb.As(pg.Round(sqlb.Div(sqlb.Mul(sqlb.Param(100.0), sqlb.Sum(hit)), sqlb.AssertNotNull(sqlb.NullIf(sqlb.Sum(sqlb.Add(hit, read)), sqlb.Param(0.0)))), 2), "heap_cache_hit_pct")).From(io).Build())
	sio := tbl("pg_stat_io", "pg_stat_io")
	reads := sqlb.Col[int64](sio, "reads")
	show(sel().Exprs(sqlb.Col[string](sio, "backend_type"), sqlb.Col[string](sio, "object"), sqlb.Col[string](sio, "context"), reads, sqlb.Col[int64](sio, "writes"), sqlb.Col[int64](sio, "extends"), sqlb.Col[int64](sio, "evictions")).
		From(sio).Where(sqlb.Greater(reads, 0)).Order(desc(reads)).Build())
	// Output:
	// SELECT "pg_settings"."name", "pg_settings"."setting", "pg_settings"."unit", "pg_settings"."source", "pg_settings"."sourcefile", "pg_settings"."sourceline" FROM "pg_settings" AS "pg_settings" WHERE ("pg_settings"."source" NOT IN ($1, $2)) ORDER BY "pg_settings"."name" ASC
	// args: 2
	// SELECT "pg_file_settings"."*" FROM "pg_file_settings" AS "pg_file_settings" WHERE ("pg_file_settings"."error" IS NOT NULL)
	// SELECT round((($1 * sum("pg_statio_user_tables"."heap_blks_hit")) / NULLIF(sum(("pg_statio_user_tables"."heap_blks_hit" + "pg_statio_user_tables"."heap_blks_read")), $2))::numeric, $3) AS "heap_cache_hit_pct" FROM "pg_statio_user_tables" AS "pg_statio_user_tables"
	// args: 3
	// SELECT "pg_stat_io"."backend_type", "pg_stat_io"."object", "pg_stat_io"."context", "pg_stat_io"."reads", "pg_stat_io"."writes", "pg_stat_io"."extends", "pg_stat_io"."evictions" FROM "pg_stat_io" AS "pg_stat_io" WHERE ("pg_stat_io"."reads" > $1) ORDER BY "pg_stat_io"."reads" DESC
	// args: 1
}

// ### 17.11 Generate and run DDL with \gexec (psql)
// Not the query layer: \gexec is a psql command. The generating SELECT is a
// format() over the catalog and renders like 18.2 below.
