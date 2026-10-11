package snippets_test

import (
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// ## 12. Locking, concurrency, queues

// ### 12.1 Job queue with SKIP LOCKED
// CREATE INDEX is DDL. The claim statement renders: a locked CTE, then UPDATE ... FROM it.
func Example_s12_01_jobQueue() {
	j := tbl("jobs", "j")
	jID, status, runAt, prio := sqlb.Col[int64](j, "id"), sqlb.Col[string](j, "status"), sqlb.Col[time.Time](j, "run_at"), sqlb.Col[int32](j, "priority")
	lockedBy, lockedAt, attempts := sqlb.Col[string](j, "locked_by"), sqlb.Col[time.Time](j, "locked_at"), sqlb.Col[int32](j, "attempts")
	next := sel().Exprs(jID).From(j).Where(sqlb.Eq(status, "queued"), sqlb.LessEqExpr(runAt, pg.Now())).
		Order(desc(prio), asc(runAt)).Limit(10).Clause(pg.ForUpdateSkipLocked())
	nextT := tbl("next", "next")
	show(sqlb.UpdateReturning(sqlb.NewUpdate(d).With("next", next).Table(j).
		Set(sqlb.Set(status, "running"), sqlb.Set(lockedBy, "worker-1"), sqlb.SetExpr(lockedAt, pg.Now()), sqlb.SetExpr(attempts, sqlb.Add(attempts, sqlb.Param[int32](1)))).
		Clause(pg.UpdateFrom(nextT)).Where(sqlb.EqExpr(jID, ref[int64](nextT, "id"))), sqlb.Bind(sqlb.Col[[]byte](j, "*"), &sink.j)).Build())
	// Recover abandoned jobs.
	show(sqlb.NewUpdate(d).Table(j).Set(sqlb.Set(status, "queued")).
		Where(sqlb.Eq(status, "running"), sqlb.LessExpr(lockedAt, pg.SubIntervalExpr(pg.Now(), pg.IntervalLit("10 minutes")))).Build())
	// Output:
	// WITH "next" AS (SELECT "j"."id" FROM "jobs" AS "j" WHERE ("j"."status" = $1 AND "j"."run_at" <= now()) ORDER BY "j"."priority" DESC, "j"."run_at" ASC LIMIT $2 FOR UPDATE SKIP LOCKED) UPDATE "jobs" AS "j" SET "status" = $3, "locked_by" = $4, "locked_at" = now(),
	// "attempts" = ("j"."attempts" + $5) FROM "next" AS "next" WHERE ("j"."id" = "next"."id") RETURNING "j"."*"
	// args: 5
	// UPDATE "jobs" AS "j" SET "status" = $1 WHERE ("j"."status" = $2 AND "j"."locked_at" < (now() - interval '10 minutes'))
	// args: 2
}

// ### 12.2 Advisory locks: transaction-scoped, never leaked
// The DO block is PL/pgSQL. The two lock calls render as FROM-less selects.
func Example_s12_02_advisoryLocks() {
	show(sel().Exprs(pg.TryAdvisoryXactLock(pg.Hashtext(sqlb.Concat(sqlb.Param("import:"), sqlb.Param("tenant-7"))))).Build())
	show(sel().Exprs(pg.AdvisoryXactLock2(42, sqlb.Cast[int32](sqlb.Param(7), "int"))).Build())
	// Output:
	// SELECT pg_try_advisory_xact_lock(hashtext(concat(CAST($1 AS text), CAST($2 AS text))))
	// args: 2
	// SELECT pg_advisory_xact_lock($1, CAST($2 AS int))
	// args: 2
}

// ### 12.3 Lock strength: FOR UPDATE versus FOR NO KEY UPDATE
// SET lock_timeout is session state. The three locking selects render.
func Example_s12_03_lockStrength() {
	o := tbl("orders", "o")
	oID, oCustomer := sqlb.Col[int64](o, "id"), sqlb.Col[int64](o, "customer_id")
	c := tbl("customers", "c")
	show(sel().Exprs(oID).From(o).Where(sqlb.Eq(oID, 1)).Clause(pg.ForNoKeyUpdate()).Build())
	show(sel().Exprs(oID).From(o).Join(inner(c, sqlb.EqExpr(sqlb.Col[int64](c, "id"), oCustomer))).Where(sqlb.Eq(oID, 1)).Clause(pg.ForUpdate(pg.Of(o))).Build())
	show(sel().Exprs(oID).From(o).Where(sqlb.Eq(oID, 1)).Clause(pg.ForUpdate(pg.NoWait())).Build())
	// Output:
	// SELECT "o"."id" FROM "orders" AS "o" WHERE ("o"."id" = $1) FOR NO KEY UPDATE
	// args: 1
	// SELECT "o"."id" FROM "orders" AS "o" INNER JOIN "customers" AS "c" ON ("c"."id" = "o"."customer_id") WHERE ("o"."id" = $1) FOR UPDATE OF "o"
	// args: 1
	// SELECT "o"."id" FROM "orders" AS "o" WHERE ("o"."id" = $1) FOR UPDATE NOWAIT
	// args: 1
}

// ### 12.4 Optimistic concurrency with xmin
func Example_s12_04_optimisticXmin() {
	docs := tbl("docs", "docs")
	id, title, xmin := sqlb.Col[int64](docs, "id"), sqlb.Col[string](docs, "title"), sqlb.Col[string](docs, "xmin")
	show(sel().Exprs(id, title, sqlb.As(sqlb.Cast[string](xmin, "text"), "version")).From(docs).Where(sqlb.Eq(id, 1)).Build())
	show(sqlb.UpdateReturning(sqlb.NewUpdate(d).Table(docs).Set(sqlb.Set(title, "New")).Where(sqlb.Eq(id, 1), sqlb.Eq(sqlb.Cast[string](xmin, "text"), "4711")), sqlb.Bind(sqlb.As(sqlb.Cast[string](xmin, "text"), "new_version"), &sink.s)).Build())
	// Output:
	// SELECT "docs"."id", "docs"."title", CAST("docs"."xmin" AS text) AS "version" FROM "docs" AS "docs" WHERE ("docs"."id" = $1)
	// args: 1
	// UPDATE "docs" AS "docs" SET "title" = $1 WHERE ("docs"."id" = $2 AND CAST("docs"."xmin" AS text) = $3) RETURNING CAST("docs"."xmin" AS text) AS "new_version"
	// args: 3
}

// ### 12.5 Gapless per-tenant sequence numbers
func Example_s12_05_gaplessCounter() {
	c := tbl("invoice_counters", "c")
	tenant, last := sqlb.Col[int64](c, "tenant_id"), sqlb.Col[int64](c, "last_no")
	show(sqlb.InsertReturning(sqlb.NewInsert(d).Into(c).Row(sqlb.Set(tenant, 7), sqlb.Set(last, 1)).
		Clause(pg.OnConflict(tenant).DoUpdate(sqlb.SetExpr(last, sqlb.Add(last, sqlb.Param[int64](1)))).Clause()), sqlb.Bind(last, &sink.i64)).Build())
	// Output:
	// INSERT INTO "invoice_counters" AS "c" ("tenant_id", "last_no") VALUES ($1, $2) ON CONFLICT ("tenant_id") DO UPDATE SET "last_no" = ("c"."last_no" + $3) RETURNING "c"."last_no"
	// args: 3
}

// ### 12.6 Serializable retry loop (client pseudocode)
// Not the query layer: BEGIN ISOLATION LEVEL, COMMIT and the retry belong to
// the application's transaction code around *sql.Tx.

// ### 12.7 Balanced double-entry ledger with a deferred constraint trigger
// Not the query layer: a PL/pgSQL trigger function and CREATE CONSTRAINT TRIGGER.

// ### 12.8 Enforcing a "max N per parent" invariant
// BEGIN and COMMIT are transaction control. The lock call and the guarded INSERT render.
func Example_s12_08_maxNPerParent() {
	tm := tbl("team_members", "team_members")
	team, user := sqlb.Col[int64](tm, "team_id"), sqlb.Col[int64](tm, "user_id")
	show(sel().Exprs(sqlb.Fn[[]byte]("pg_advisory_xact_lock", pg.Hashtext(sqlb.Param("team-members")), sqlb.Param[int64](7))).Build())
	count := sel().Exprs(sqlb.Count()).From(tm).Where(sqlb.Eq(team, 7))
	show(sqlb.NewInsert(d).Into(tm).Columns(team, user).
		Select(sel().Exprs(sqlb.Param[int64](7), sqlb.Param[int64](99)).Where(sqlb.Less(sqlb.AssertNotNull(sqlb.Subquery[int64](count)), 5))).Build())
	// Output:
	// SELECT pg_advisory_xact_lock(hashtext($1), $2)
	// args: 2
	// INSERT INTO "team_members" AS "team_members" ("team_id", "user_id")
	// SELECT $1, $2 WHERE ((SELECT count(*) FROM "team_members" AS "team_members" WHERE ("team_members"."team_id" = $3)) < $4)
	// args: 4
}

// ### 12.9 Blocking tree and how to unblock
func Example_s12_09_blockingTree() {
	blocked, blocking := tbl("pg_stat_activity", "blocked"), tbl("pg_stat_activity", "blocking")
	bPid, bStart, bQuery := sqlb.Col[int32](blocked, "pid"), sqlb.Col[time.Time](blocked, "query_start"), sqlb.Col[string](blocked, "query")
	kPid, kState, kQuery := sqlb.Col[int32](blocking, "pid"), sqlb.Col[string](blocking, "state"), sqlb.Col[string](blocking, "query")
	pids := pg.FuncSource(sqlb.Fn[int32]("unnest", pg.BlockingPids(bPid)), "b", "pid")
	show(sel().Exprs(sqlb.As(bPid, "blocked_pid"), sqlb.As(pg.Diff(pg.Now(), bStart), "waiting_for"), sqlb.As(pg.Left(bQuery, 80), "blocked_query"),
		sqlb.As(kPid, "blocking_pid"), sqlb.As(kState, "blocking_state"), sqlb.As(pg.Left(kQuery, 80), "blocking_query")).
		From(blocked).Join(sqlb.Join{Type: sqlb.JoinInner, Source: pg.Lateral(pids), On: []sqlb.Predicate{sqlb.True()}},
		inner(blocking, sqlb.EqExpr(kPid, ref[int32](pids, "pid")))).Build())
	show(sel().Exprs(pg.CancelBackend(sqlb.Param[int32](4242))).Build())
	act := tbl("pg_stat_activity", "pg_stat_activity")
	pid, state, change, query := sqlb.Col[int32](act, "pid"), sqlb.Col[string](act, "state"), sqlb.Col[time.Time](act, "state_change"), sqlb.Col[string](act, "query")
	show(sel().Exprs(pid, sqlb.As(pg.Diff(pg.Now(), change), "idle_for"), pg.Left(query, 80)).From(act).
		Where(sqlb.Eq(state, "idle in transaction"), sqlb.GreaterExpr(pg.Diff(pg.Now(), change), pg.IntervalLit("5 minutes"))).Build())
	// Output:
	// SELECT "blocked"."pid" AS "blocked_pid", (now() - "blocked"."query_start") AS "waiting_for", left("blocked"."query", $1) AS "blocked_query", "blocking"."pid" AS "blocking_pid", "blocking"."state" AS "blocking_state", left("blocking"."query", $2) AS "blocking_query" FROM "pg_stat_activity" AS "blocked" INNER JOIN LATERAL unnest(pg_blocking_pids("blocked"."pid")) AS "b" ("pid") ON (TRUE) INNER JOIN "pg_stat_activity" AS "blocking" ON ("blocking"."pid" = "b"."pid")
	// args: 2
	// SELECT pg_cancel_backend($1)
	// args: 1
	// SELECT "pg_stat_activity"."pid", (now() - "pg_stat_activity"."state_change") AS "idle_for", left("pg_stat_activity"."query", $1) FROM "pg_stat_activity" AS "pg_stat_activity" WHERE ("pg_stat_activity"."state" = $2 AND (now() - "pg_stat_activity"."state_change") > interval '5 minutes')
	// args: 2
}

// ### 12.10 Subtransaction cost
// Prose only.
