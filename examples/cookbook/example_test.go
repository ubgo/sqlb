// Package cookbook_test is the cookbook. Each Example is one recipe: complete
// code, and the SQL it renders as the checked Output. `go test` fails when a
// recipe's SQL drifts from what the doc claims. docsi/COOKBOOK.md is written
// from these outputs and must not say anything this file does not prove.
package cookbook_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ch"
	"github.com/ubgo/sqlb/examples/cookbook/app"
	"github.com/ubgo/sqlb/examples/cookbook/authlite"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/vector"
)

// show prints a rendered query the way every recipe reports it.
func show(q sqlb.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(q.SQL)
	fmt.Println("args:", jsonArgsAsText(q.Args))
}

// jsonArgsAsText returns args with every json.RawMessage converted to a
// string. fmt prints a RawMessage as its bytes on Go 1.24, the module's floor,
// and as its text on Go 1.27, so printing args directly made the Output of
// every recipe with a JSON argument depend on the toolchain. As a string it
// prints the same on both.
func jsonArgsAsText(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		if raw, ok := a.(json.RawMessage); ok {
			a = string(raw)
		}
		out[i] = a
	}
	return out
}

// ---------------------------------------------------------------------------
// The application's own tables and the composite live in examples/cookbook/app,
// so the four owners in this story are four directories a reader can list:
// authlite (owns tables), phone (borrows one under its own alias), workspace
// (owns tables, imports nothing) and app (owns tables, imports the other
// three and joins them). docs/cross-package.md walks through it.
// ---------------------------------------------------------------------------

// Recipe 1. One statement loads the core user, a plugin's data, a second
// library's data and the application's own table. Four tables, three owners.
func Example_loadComposite() {
	a := authlite.New(authlite.WithDialect(sqlb.PostgresDialect{}))
	u := app.NewUser("ws_42")
	show(a.LoadUserQuery("usr_123", u))
	// Output:
	// SELECT "u"."id", "u"."primary_email", "u"."banned_at", "u"."created_at", COALESCE("phi"."value", ''), COALESCE("wm"."workspace_id", ''), COALESCE("wm"."role", ''), COALESCE("p"."company", '') FROM "auth_user" AS "u" LEFT JOIN "auth_identity" AS "phi" ON ("phi"."user_id" = "u"."id" AND "phi"."kind" = $1) LEFT JOIN "ws_member" AS "wm" ON ("wm"."user_id" = "u"."id" AND "wm"."workspace_id" = $2) LEFT JOIN "app_profile" AS "p" ON ("p"."user_id" = "u"."id") WHERE ("u"."id" = $3) LIMIT $4
	// args: [phone ws_42 usr_123 1]
}

// Recipe 2. The application extends a library query with a join, a filter
// and a column. The library's NotBanned predicate survives.
func Example_extendLibraryQuery() {
	a := authlite.New(authlite.WithDialect(sqlb.PostgresDialect{}))

	type AdminRow struct {
		authlite.User
		Plan string
	}
	var row AdminRow

	show(a.AdminUserListQuery(50, 0, &row.User,
		sqlb.WithJoin(app.SubscriptionJoin(authlite.UserID)),
		sqlb.WithWhere(sqlb.Eq(app.SubState, app.SubActive)),
		sqlb.WithColumns(sqlb.Bind(app.SubPlan, &row.Plan)),
	))
	// Output:
	// SELECT "u"."id", "u"."primary_email", "u"."banned_at", "u"."created_at", "s"."plan" FROM "auth_user" AS "u" INNER JOIN "app_subscription" AS "s" ON ("s"."user_id" = "u"."id") WHERE ("u"."banned_at" IS NULL AND "s"."status" = $1) ORDER BY "u"."created_at" DESC LIMIT $2 OFFSET $3
	// args: [active 50 0]
}

// Recipe 3. The same library query renders for SQLite with no code change.
func Example_dialectSwitch() {
	a := authlite.New(authlite.WithDialect(sqlb.SQLiteDialect{}))
	var u authlite.User
	show(a.UserByEmailQuery("person@example.com", &u))
	// Output:
	// SELECT "u"."id", "u"."primary_email", "u"."banned_at", "u"."created_at" FROM "auth_user" AS "u" WHERE (lower("u"."primary_email") = ? AND "u"."banned_at" IS NULL) LIMIT ?
	// args: [person@example.com 1]
}

// Recipe 4. Ad hoc use: inline tables, no package-level declarations, still
// type-checked.
func Example_adHoc() {
	t := sqlb.Table{Name: "events", Alias: "e"}
	kind := sqlb.Col[string](t, "kind")
	at := sqlb.Col[time.Time](t, "created_at")
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	var k string
	var when time.Time
	show(sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(kind, &k), sqlb.Bind(at, &when)).
		From(t).
		Where(sqlb.Greater(at, since), sqlb.In(kind, "login", "logout")).
		Order(sqlb.OrderBy(at, sqlb.SortDesc)).
		Limit(20).
		Build())
	// Output:
	// SELECT "e"."kind", "e"."created_at" FROM "events" AS "e" WHERE ("e"."created_at" > $1 AND "e"."kind" IN ($2, $3)) ORDER BY "e"."created_at" DESC LIMIT $4
	// args: [2026-09-01 00:00:00 +0000 UTC login logout 20]
}

// Recipe 5. pgvector nearest-neighbour search. The distance is one typed
// expression used as a column, a filter and a sort key.
func Example_vectorSearch() {
	doc := sqlb.Table{Name: "app_doc", Alias: "doc"}
	docID := sqlb.Col[string](doc, "id")
	embedding := sqlb.Col[vector.Vector](doc, "embedding")

	query := vector.Vector{0.1, 0.2, 0.3}
	dist := vector.L2(embedding, sqlb.Param(query))

	var id string
	var d float64
	show(sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(docID, &id), sqlb.Bind(dist, &d)).
		From(doc).
		Where(sqlb.Less(dist, 0.5)).
		Order(sqlb.OrderBy(dist, sqlb.SortAsc)).
		Limit(10).
		Build())
	// Output:
	// SELECT "doc"."id", ("doc"."embedding" <-> $1) FROM "app_doc" AS "doc" WHERE (("doc"."embedding" <-> $2) < $3) ORDER BY ("doc"."embedding" <-> $4) ASC LIMIT $5
	// args: [[0.1 0.2 0.3] [0.1 0.2 0.3] 0.5 [0.1 0.2 0.3] 10]
}

// Recipe 6. A Postgres construct under the SQLite dialect fails at Build,
// never at the database.
func Example_unsupportedDialect() {
	doc := sqlb.Table{Name: "app_doc", Alias: "doc"}
	embedding := sqlb.Col[vector.Vector](doc, "embedding")
	dist := vector.L2(embedding, sqlb.Param(vector.Vector{1, 2}))

	var d float64
	_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(dist, &d)).
		From(doc).
		Build()
	fmt.Println(errors.Is(err, sqlb.ErrUnsupportedDialect))
	fmt.Println(err)
	// Output:
	// true
	// sqlb: construct is not supported by this dialect
	// vector.L2 under dialect "sqlite"
}

// Recipe 7. JSONB: a bound key path and a containment filter that can use a
// GIN index.
func Example_jsonb() {
	doc := sqlb.Table{Name: "app_profile", Alias: "p"}
	prefs := sqlb.Col[json.RawMessage](doc, "prefs")
	userID := sqlb.Col[string](doc, "user_id")

	var theme string
	show(sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(pg.JSONText(prefs, "theme"), &theme)).
		From(doc).
		Where(pg.Contains(prefs, json.RawMessage(`{"beta":true}`)), sqlb.Eq(userID, "usr_123")).
		Build())
	// Output:
	// SELECT ("p"."prefs" ->> $1) FROM "app_profile" AS "p" WHERE (("p"."prefs" @> $2) AND "p"."user_id" = $3)
	// args: [theme {"beta":true} usr_123]
}

// Recipe 8. A job queue: DISTINCT ON and FOR UPDATE SKIP LOCKED through the
// clause slots.
func Example_jobQueue() {
	job := sqlb.Table{Name: "app_job", Alias: "j"}
	jobID := sqlb.Col[string](job, "id")
	queue := sqlb.Col[string](job, "queue")
	runAt := sqlb.Col[time.Time](job, "run_at")
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	var id string
	show(sqlb.NewSelect(sqlb.PostgresDialect{}).
		Clause(pg.DistinctOn(queue), pg.ForUpdateSkipLocked()).
		Columns(sqlb.Bind(jobID, &id)).
		From(job).
		Where(sqlb.LessEq(runAt, now)).
		Order(sqlb.OrderBy(queue, sqlb.SortAsc), sqlb.OrderBy(runAt, sqlb.SortAsc)).
		Limit(1).
		Build())
	// Output:
	// SELECT DISTINCT ON ("j"."queue") "j"."id" FROM "app_job" AS "j" WHERE ("j"."run_at" <= $1) ORDER BY "j"."queue" ASC, "j"."run_at" ASC LIMIT $2 FOR UPDATE SKIP LOCKED
	// args: [2026-09-16 12:00:00 +0000 UTC 1]
}

// Recipe 9. ClickHouse: latest event per user from a ReplacingMergeTree,
// PREWHERE on the partition column, LIMIT BY instead of a window function,
// and join_use_nulls set so a LEFT JOIN miss scans as NULL.
func Example_clickhouseLatestPerUser() {
	ev := sqlb.Table{Name: "events", Alias: "e"}
	userID := sqlb.Col[string](ev, "user_id")
	kind := sqlb.Col[string](ev, "kind")
	at := sqlb.Col[time.Time](ev, "at")
	day := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)

	var uid, k string
	var when time.Time
	show(sqlb.NewSelect(ch.Dialect{}).
		Columns(sqlb.Bind(userID, &uid), sqlb.Bind(kind, &k), sqlb.Bind(at, &when)).
		From(ev).
		Clause(ch.Final(), ch.Prewhere(sqlb.GreaterEq(at, day))).
		Where(sqlb.Eq(kind, "purchase")).
		Order(sqlb.OrderBy(userID, sqlb.SortAsc), sqlb.OrderBy(at, sqlb.SortDesc)).
		Clause(ch.LimitBy(1, userID), ch.Settings(ch.JoinUseNulls())).
		Limit(1000).
		Build())
	// Output:
	// SELECT `e`.`user_id`, `e`.`kind`, `e`.`at` FROM `events` AS `e` FINAL PREWHERE (`e`.`at` >= ?) WHERE (`e`.`kind` = ?) ORDER BY `e`.`user_id` ASC, `e`.`at` DESC LIMIT ? BY `e`.`user_id` LIMIT ? SETTINGS join_use_nulls = ?
	// args: [2026-09-16 00:00:00 +0000 UTC purchase 1 1000 1]
}

// Recipe 10. ClickHouse aggregates: hourly buckets, approximate uniques, a
// conditional sum, and argMax. Every function is typed.
func Example_clickhouseHourly() {
	ev := sqlb.Table{Name: "events", Alias: "e"}
	userID := sqlb.Col[string](ev, "user_id")
	kind := sqlb.Col[string](ev, "kind")
	amount := sqlb.Col[float64](ev, "amount")
	at := sqlb.Col[time.Time](ev, "at")

	hour := ch.ToStartOfHour(at)
	var h time.Time
	var users, purchases uint64
	var revenue float64
	var lastKind string
	show(sqlb.NewSelect(ch.Dialect{}).
		Columns(
			sqlb.Bind(hour, &h),
			sqlb.Bind(ch.Uniq(userID), &users),
			sqlb.Bind(ch.CountIf(sqlb.Eq(kind, "purchase")), &purchases),
			sqlb.Bind(ch.SumIf(amount, sqlb.Eq(kind, "purchase")), &revenue),
			sqlb.Bind(ch.ArgMax(kind, at), &lastKind),
		).
		From(ev).
		GroupBy(hour).
		Order(sqlb.OrderBy(hour, sqlb.SortAsc)).
		Build())
	// Output:
	// SELECT toStartOfHour(`e`.`at`), uniq(`e`.`user_id`), countIf(`e`.`kind` = ?), sumIf(`e`.`amount`, `e`.`kind` = ?), argMax(`e`.`kind`, `e`.`at`) FROM `events` AS `e` GROUP BY toStartOfHour(`e`.`at`) ORDER BY toStartOfHour(`e`.`at`) ASC
	// args: [purchase purchase]
}

// Recipe 11. ARRAY JOIN returns the clause and the typed element in one call.
func Example_clickhouseArrayJoin() {
	ev := sqlb.Table{Name: "events", Alias: "e"}
	tags := sqlb.Col[[]string](ev, "tags")
	arrayJoin, tag := ch.ArrayJoin(tags, "tag")

	var t string
	var n uint64
	show(sqlb.NewSelect(ch.Dialect{}).
		Columns(sqlb.Bind(tag, &t), sqlb.Bind(ch.Count(), &n)).
		From(ev).
		Clause(arrayJoin).
		Where(sqlb.NotEq(tag, "")).
		GroupBy(tag).
		Build())
	// Output:
	// SELECT `tag`, count() FROM `events` AS `e` ARRAY JOIN `e`.`tags` AS `tag` WHERE (`tag` <> ?) GROUP BY `tag`
	// args: []
}

// recordingTx is a DBTX that records what it was asked to execute. It stands
// in for *sql.Tx so the hook recipe runs without a driver.
type recordingTx struct{ log []string }

func (r *recordingTx) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.log = append(r.log, fmt.Sprintf("%s %v", query, args))
	return sqlResult{}, nil
}

// Recipe 12. The application writes its own row in the hook, through the
// same transaction the library is writing through.
func Example_hookInTransaction() {
	a := authlite.New(
		authlite.WithDialect(sqlb.PostgresDialect{}),
		authlite.WithHooks(authlite.Hooks{
			AfterSignUp: func(ctx context.Context, tx sqlb.DBTX, e *authlite.SignUpEvent) error {
				_, err := tx.ExecContext(ctx,
					`INSERT INTO app_profile (user_id, company) VALUES ($1, $2)`,
					e.User.ID, "")
				return err
			},
		}),
	)

	tx := &recordingTx{}
	if _, err := a.SignUp(context.Background(), tx, "usr_123", "person@example.com"); err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, line := range tx.log {
		fmt.Println(line)
	}
	// Output:
	// INSERT INTO auth_user (id, primary_email) VALUES ($1, $2) [usr_123 person@example.com]
	// INSERT INTO app_profile (user_id, company) VALUES ($1, $2) [usr_123 ]
}
