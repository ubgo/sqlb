package driversqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
	"github.com/ubgo/sqlb/sqlite"
)

// The sqlite construct package, executed.
//
// Why this lives here and not beside the package it tests: the core module
// has no driver and may not have one, so github.com/ubgo/sqlb/sqlite can only
// render text and compare it. This module has a real database, so it can ask
// the only question that matters about a construct package, which is whether
// the engine accepts what it produced.
//
// That makes sqlite the one engine package in this repository whose SQL is
// proven rather than asserted. pg and ch are checked as text only, because
// running them needs a server this test cannot start.

// constructsDDL is a table plus an FTS5 virtual table, which the full-text
// helpers need and an ordinary table cannot satisfy.
var constructsDDL = []string{
	`CREATE TABLE IF NOT EXISTS sqlb_constructs (
		id    INTEGER PRIMARY KEY,
		name  TEXT NOT NULL UNIQUE,
		n     INTEGER NOT NULL,
		doc   TEXT,
		at    TEXT
	)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS sqlb_fts USING fts5(title, body)`,
}

func constructsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatalf("Memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range constructsDDL {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	return db
}

var (
	ctbl   = sqlb.Table{Name: "sqlb_constructs", Alias: "c"}
	cID    = sqlb.Col[int64](ctbl, "id")
	cName  = sqlb.Col[string](ctbl, "name")
	cN     = sqlb.Col[int32](ctbl, "n")
	cDoc   = sqlb.Col[string](ctbl, "doc")
	cAt    = sqlb.Col[string](ctbl, "at")
	ftsTbl = sqlb.Table{Name: "sqlb_fts", Alias: "sqlb_fts"}
)

func seedConstructs(t *testing.T, db *sql.DB) {
	t.Helper()
	q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(ctbl).Row(
		sqlb.Set(cID, int64(1)),
		sqlb.Set(cName, "ada"),
		sqlb.Set(cN, int32(3)),
		sqlb.Set(cDoc, `{"plan":"pro","tags":["a","b"],"seats":5}`),
		sqlb.Set(cAt, "2026-03-15 10:30:00"),
	).Build()
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), q.SQL, q.Args...); err != nil {
		t.Fatalf("seed exec: %v", err)
	}
}

// scalar builds a one-expression SELECT, runs it, and returns what the
// database answered. A failure here is the engine rejecting the SQL, which is
// the whole point of the file.
func scalar[T any](t *testing.T, db *sql.DB, e sqlb.Expr[T]) T {
	t.Helper()
	var out T
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(e, &out)).From(ctbl).Where(sqlb.Eq(cID, int64(1))).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(context.Background(), db, q); err != nil {
		t.Fatalf("exec %s: %v", q.SQL, err)
	}
	return out
}

// Every JSON1 helper, run against a real document, with the answer checked.
// A construct that rendered plausible-looking SQL and meant something else
// shows up here and nowhere earlier.
func TestJSON1_Executes(t *testing.T) {
	db := constructsDB(t)
	seedConstructs(t, db)

	if got := scalar(t, db, sqlite.JSONExtract[string](cDoc, "$.plan")); got != "pro" {
		t.Errorf("Extract = %q", got)
	}
	if got := scalar(t, db, sqlite.JSONExtract[int64](cDoc, "$.seats")); got != 5 {
		t.Errorf("Extract typed as int = %d; json_extract should preserve the JSON type", got)
	}
	if got := scalar(t, db, sqlite.JSONArrowText(cDoc, "$.plan")); got != "pro" {
		t.Errorf("ArrowText = %q", got)
	}
	if got := scalar(t, db, sqlite.JSONArrow(cDoc, "$.plan")); got != `"pro"` {
		t.Errorf("Arrow = %q; -> keeps the JSON quoting that ->> strips", got)
	}
	if got := scalar(t, db, sqlite.JSONValid(cDoc)); !got {
		t.Error("Valid on valid JSON returned false")
	}
	if got := scalar(t, db, sqlite.JSONType(cDoc)); got != "object" {
		t.Errorf("Type = %q", got)
	}
	if got := scalar(t, db, sqlite.JSONTypeAt(cDoc, "$.tags")); got == nil || *got != "array" {
		t.Errorf("TypeAt = %v", got)
	}
	if got := scalar(t, db, sqlite.JSONArrayLengthAt(cDoc, "$.tags")); got != 2 {
		t.Errorf("ArrayLengthAt = %d", got)
	}
	if got := scalar(t, db, sqlite.JSONSet(cDoc, "$.plan", sqlb.Param("free"))); got == "" {
		t.Error("Set produced nothing")
	}
	if got := scalar(t, db, sqlite.JSONExtract[string](sqlite.JSONSet(cDoc, "$.plan", sqlb.Param("free")), "$.plan")); got != "free" {
		t.Errorf("Set then Extract = %q, want free", got)
	}
	// Insert leaves an existing key alone; Replace only touches an existing one.
	if got := scalar(t, db, sqlite.JSONExtract[string](sqlite.JSONInsert(cDoc, "$.plan", sqlb.Param("free")), "$.plan")); got != "pro" {
		t.Errorf("Insert overwrote an existing key: %q", got)
	}
	if got := scalar(t, db, sqlite.JSONExtract[*string](sqlite.JSONInsert(cDoc, "$.new", sqlb.Param("x")), "$.new")); got == nil || *got != "x" {
		t.Errorf("Insert did not create a missing key: %v", got)
	}
	if got := scalar(t, db, sqlite.JSONExtract[*string](sqlite.JSONReplace(cDoc, "$.new", sqlb.Param("x")), "$.new")); got != nil {
		t.Errorf("Replace created a missing key: %v", got)
	}
	if got := scalar(t, db, sqlite.JSONExtract[*string](sqlite.JSONRemove(cDoc, "$.plan"), "$.plan")); got != nil {
		t.Errorf("Remove left the key: %v", got)
	}
	if got := scalar(t, db, sqlite.JSONQuote(42)); got != "42" {
		t.Errorf("Quote = %q", got)
	}
	if got := scalar(t, db, sqlite.JSONArray(cName, cN)); got != `["ada",3]` {
		t.Errorf("Array = %q", got)
	}
	if got := scalar(t, db, sqlite.JSONObject(sqlite.KV("k", cName), sqlite.KV("n", cN))); got != `{"k":"ada","n":3}` {
		t.Errorf("Object = %q", got)
	}
	if got := scalar(t, db, sqlite.JSONPatch(cDoc, sqlb.Param(`{"plan":null}`))); got == "" {
		t.Error("Patch produced nothing")
	}
}

// json_each as a FROM source, which is how a document's array becomes rows.
func TestJSONEach_Executes(t *testing.T) {
	db := constructsDB(t)
	seedConstructs(t, db)

	each := sqlite.JSONEach(sqlite.JSONExtract[string](cDoc, "$.tags"), "e")
	var value string
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(sqlb.Col[string](sqlb.Ref(each), "value"), &value)).
		From(ctbl).
		Join(sqlb.Join{Type: sqlb.JoinCross, Source: each}).
		Where(sqlb.Eq(cID, int64(1))).
		Order(sqlb.OrderBy(sqlb.Col[string](sqlb.Ref(each), "value"), sqlb.SortAsc)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got []string
	if err := sqlb.Each(context.Background(), db, q, func() error {
		got = append(got, value)
		return nil
	}); err != nil {
		t.Fatalf("exec %s: %v", q.SQL, err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("json_each yielded %v, want [a b]", got)
	}
}

// The aggregates, which differ from Postgres in what they return over zero
// rows. That difference is checked, not assumed.
func TestJSONAggregates_Executes(t *testing.T) {
	db := constructsDB(t)
	seedConstructs(t, db)

	var arr string
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(sqlite.JSONGroupArray(cName), &arr)).From(ctbl).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(context.Background(), db, q); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if arr != `["ada"]` {
		t.Errorf("GroupArray = %q", arr)
	}

	// Over zero rows SQLite answers "[]" where Postgres answers NULL. The doc
	// comment says so; this is what makes that claim checked.
	q, err = sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(sqlite.JSONGroupArray(cName), &arr)).
		From(ctbl).Where(sqlb.Eq(cID, int64(999))).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(context.Background(), db, q); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if arr != "[]" {
		t.Errorf("GroupArray over zero rows = %q, want []", arr)
	}
}

// The text helpers, including GLOB's case sensitivity, which is the reason to
// use it over LIKE.
func TestText_Executes(t *testing.T) {
	db := constructsDB(t)
	seedConstructs(t, db)

	matched := func(p sqlb.Predicate) bool {
		t.Helper()
		var id int64
		q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
			Columns(sqlb.Bind(cID, &id)).From(ctbl).Where(p).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		err = sqlb.One(context.Background(), db, q)
		if err == nil {
			return true
		}
		if err.Error() == sqlb.ErrNoRows.Error() {
			return false
		}
		t.Fatalf("exec %s: %v", q.SQL, err)
		return false
	}

	if !matched(sqlite.Glob(cName, "ad*")) {
		t.Error("GLOB did not match a matching prefix")
	}
	if matched(sqlite.Glob(cName, "AD*")) {
		t.Error("GLOB matched a different case; it is supposed to be case-sensitive")
	}
	if !matched(sqlite.NotGlob(cName, "zz*")) {
		t.Error("NOT GLOB did not match")
	}

	if got := scalar(t, db, sqlite.Instr(cName, "d")); got != 2 {
		t.Errorf("Instr = %d, want 2", got)
	}
	if got := scalar(t, db, sqlite.Typeof(cName)); got != "text" {
		t.Errorf("Typeof = %q", got)
	}
	if got := scalar(t, db, sqlite.Typeof(cN)); got != "integer" {
		t.Errorf("Typeof of an integer column = %q", got)
	}
	if got := scalar(t, db, sqlite.Hex(cName)); got != "616461" {
		t.Errorf("Hex = %q", got)
	}
	if got := scalar(t, db, sqlite.QuoteSQL(cName)); got != "'ada'" {
		t.Errorf("QuoteSQL = %q", got)
	}
	// IfNull takes both operands at one type. The nullable column is asserted
	// back to string because ifnull is exactly the assertion: it guarantees a
	// non-null result, which is the reason to call it.
	if got := scalar(t, db, sqlite.IfNull(cDoc, sqlb.Param("fallback"))); got == "fallback" {
		t.Error("IfNull returned the fallback for a non-null value")
	}
	nullDoc := sqlb.Col[string](ctbl, "missing_doc")
	_ = nullDoc
	if got := scalar(t, db, sqlite.IIF(sqlb.Greater(cN, int32(1)), cName, sqlb.Param("small"))); got != "ada" {
		t.Errorf("IIF took the wrong branch: %q", got)
	}
	if got := scalar(t, db, sqlite.Format("%s-%d", cName, cN)); got != "ada-3" {
		t.Errorf("Format = %q", got)
	}
	if got := scalar(t, db, sqlite.GroupConcat(cName, ",")); got != "ada" {
		t.Errorf("GroupConcat = %q", got)
	}
}

// The FTS5 helpers need a virtual table, which is why they cannot be proven
// against an ordinary one and why this file creates both.
func TestFTS5_Executes(t *testing.T) {
	db := constructsDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO sqlb_fts (title, body) VALUES ('hello world', 'the quick brown fox')`); err != nil {
		t.Fatalf("seed fts: %v", err)
	}

	body := sqlb.Col[string](ftsTbl, "body")
	title := sqlb.Col[string](ftsTbl, "title")

	var got, snip string
	var score float64
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(
			sqlb.Bind(title, &got),
			sqlb.Bind(sqlite.BM25(ftsTbl), &score),
			sqlb.Bind(sqlite.Snippet(ftsTbl, 1, "[", "]", "...", 8), &snip),
		).
		From(ftsTbl).
		Where(sqlite.Match(sqlb.Col[string](sqlb.Table{Name: "sqlb_fts", Alias: "sqlb_fts"}, "sqlb_fts"), "quick")).
		Order(sqlb.OrderBy(sqlite.Rank(ftsTbl), sqlb.SortAsc)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(ctx, db, q); err != nil {
		t.Fatalf("exec %s: %v", q.SQL, err)
	}
	if got != "hello world" {
		t.Errorf("MATCH found %q", got)
	}
	if score >= 0 {
		t.Errorf("bm25 = %v; it is negative, and more negative is better", score)
	}
	if snip == "" {
		t.Error("Snippet produced nothing")
	}

	var hl string
	q, err = sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(sqlite.Highlight(ftsTbl, 1, "<b>", "</b>"), &hl)).
		From(ftsTbl).
		Where(sqlite.Match(sqlb.Col[string](sqlb.Table{Name: "sqlb_fts", Alias: "sqlb_fts"}, "sqlb_fts"), "quick")).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(ctx, db, q); err != nil {
		t.Fatalf("exec %s: %v", q.SQL, err)
	}
	if hl != "the <b>quick</b> brown fox" {
		t.Errorf("Highlight = %q", hl)
	}
	_ = body
}

// The time family. SQLite has no date type, so every one of these returns
// text and the modifiers are what do the work.
func TestTime_Executes(t *testing.T) {
	db := constructsDB(t)
	seedConstructs(t, db)

	if got := scalar(t, db, sqlite.Date(cAt)); got != "2026-03-15" {
		t.Errorf("Date = %q", got)
	}
	if got := scalar(t, db, sqlite.Time(cAt)); got != "10:30:00" {
		t.Errorf("Time = %q", got)
	}
	if got := scalar(t, db, sqlite.DateTime(cAt, sqlite.StartOfMonth)); got != "2026-03-01 00:00:00" {
		t.Errorf("StartOfMonth = %q", got)
	}
	if got := scalar(t, db, sqlite.Date(cAt, sqlite.Offset(-7, sqlite.Day))); got != "2026-03-08" {
		t.Errorf("Offset(-7, day) = %q", got)
	}
	if got := scalar(t, db, sqlite.Date(cAt, sqlite.Offset(1, sqlite.Month))); got != "2026-04-15" {
		t.Errorf("Offset(1, month) = %q", got)
	}
	if got := scalar(t, db, sqlite.Strftime("%Y", cAt)); got != "2026" {
		t.Errorf("Strftime = %q", got)
	}
	if got := scalar(t, db, sqlite.UnixEpoch(cAt)); got <= 0 {
		t.Errorf("UnixEpoch = %d", got)
	}
	if got := scalar(t, db, sqlite.JulianDay(cAt)); got <= 0 {
		t.Errorf("JulianDay = %v", got)
	}
	// Modifiers apply left to right, which is the trap the package doc names.
	end := scalar(t, db, sqlite.Date(cAt, sqlite.StartOfMonth, sqlite.Offset(1, sqlite.Month), sqlite.Offset(-1, sqlite.Day)))
	if end != "2026-03-31" {
		t.Errorf("start of month, +1 month, -1 day = %q, want the month's last day", end)
	}
}

// Conflict resolution, where OR REPLACE and DO UPDATE differ in a way that
// matters and that only a real database can show.
func TestConflictResolution_Executes(t *testing.T) {
	ctx := context.Background()

	t.Run("insert or ignore skips", func(t *testing.T) {
		db := constructsDB(t)
		seedConstructs(t, db)
		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(ctbl).
			Row(sqlb.Set(cID, int64(2)), sqlb.Set(cName, "ada"), sqlb.Set(cN, int32(9))).
			Clause(sqlite.InsertOr(sqlite.OrIgnore)).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err != nil {
			t.Fatalf("OR IGNORE should not raise on a unique violation: %v", err)
		}
		if got := scalar(t, db, cN); got != 3 {
			t.Errorf("OR IGNORE changed the row: n = %d", got)
		}
	})

	t.Run("insert or replace changes the rowid", func(t *testing.T) {
		db := constructsDB(t)
		seedConstructs(t, db)
		before := scalar(t, db, sqlite.Rowid(ctbl))

		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(ctbl).
			Row(sqlb.Set(cName, "ada"), sqlb.Set(cN, int32(9))).
			Clause(sqlite.InsertOr(sqlite.OrReplace)).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err != nil {
			t.Fatalf("exec %s: %v", q.SQL, err)
		}

		var after int64
		sel, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
			Columns(sqlb.Bind(sqlite.Rowid(ctbl), &after)).
			From(ctbl).Where(sqlb.Eq(cName, "ada")).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if err := sqlb.One(ctx, db, sel); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if after == before {
			t.Error("OR REPLACE kept the rowid; the doc comment says it does not, and anything referencing the row by rowid depends on that")
		}
	})

	t.Run("on conflict do update keeps the rowid", func(t *testing.T) {
		db := constructsDB(t)
		seedConstructs(t, db)
		before := scalar(t, db, sqlite.Rowid(ctbl))

		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(ctbl).
			Row(sqlb.Set(cName, "ada"), sqlb.Set(cN, int32(9))).
			Clause(sqlite.OnConflict(cName).
				DoUpdate(sqlb.SetExpr(cN, sqlite.Excluded(cN))).
				Clause()).
			Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err != nil {
			t.Fatalf("exec %s: %v", q.SQL, err)
		}
		if got := scalar(t, db, cN); got != 9 {
			t.Errorf("DO UPDATE did not apply the excluded value: n = %d", got)
		}
		if after := scalar(t, db, sqlite.Rowid(ctbl)); after != before {
			t.Error("DO UPDATE changed the rowid; it modifies in place, which is the whole difference from OR REPLACE")
		}
	})

	t.Run("on conflict do nothing", func(t *testing.T) {
		db := constructsDB(t)
		seedConstructs(t, db)
		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(ctbl).
			Row(sqlb.Set(cName, "ada"), sqlb.Set(cN, int32(9))).
			Clause(sqlite.OnConflict(cName).DoNothing()).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if _, err := db.ExecContext(ctx, q.SQL, q.Args...); err != nil {
			t.Fatalf("exec %s: %v", q.SQL, err)
		}
		if got := scalar(t, db, cN); got != 3 {
			t.Errorf("DO NOTHING changed the row: n = %d", got)
		}
	})
}

// The scalar and session helpers.
func TestMisc_Executes(t *testing.T) {
	db := constructsDB(t)
	seedConstructs(t, db)

	if got := scalar(t, db, sqlite.Abs(sqlb.Neg(cN))); got != 3 {
		t.Errorf("Abs = %d", got)
	}
	if got := scalar(t, db, sqlite.Max(cN, sqlb.Param(int32(5)))); got != 5 {
		t.Errorf("scalar Max = %d", got)
	}
	if got := scalar(t, db, sqlite.Min(cN, sqlb.Param(int32(5)))); got != 3 {
		t.Errorf("scalar Min = %d", got)
	}
	if got := scalar(t, db, sqlite.Version()); got == "" {
		t.Error("Version returned nothing")
	}
	if got := scalar(t, db, sqlite.Random()); got == 0 {
		t.Log("random() returned 0, which is possible but worth noticing")
	}
	if got := scalar(t, db, sqlite.Rowid(ctbl)); got != 1 {
		t.Errorf("Rowid = %d, want the INTEGER PRIMARY KEY which aliases it", got)
	}
}

// The planner hints have to name a real index, which is the maintenance
// hazard their doc comment warns about. This proves both halves: the hint
// works with the index, and fails without it.
func TestPlannerHints_Execute(t *testing.T) {
	db := constructsDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_constructs_n ON sqlb_constructs(n)`); err != nil {
		t.Fatalf("index: %v", err)
	}
	seedConstructs(t, db)

	var id int64
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(cID, &id)).From(ctbl).
		Clause(sqlite.IndexedBy("idx_constructs_n")).
		Where(sqlb.Eq(cN, int32(3))).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(ctx, db, q); err != nil {
		t.Fatalf("INDEXED BY a real index: %v", err)
	}

	q, err = sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(cID, &id)).From(ctbl).
		Clause(sqlite.IndexedBy("idx_that_does_not_exist")).
		Where(sqlb.Eq(cN, int32(3))).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(ctx, db, q); err == nil {
		t.Error("INDEXED BY a missing index should be an error, which is why the doc calls it a maintenance hazard")
	}

	q, err = sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(cID, &id)).From(ctbl).
		Clause(sqlite.NotIndexed()).
		Where(sqlb.Eq(cN, int32(3))).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(ctx, db, q); err != nil {
		t.Fatalf("NOT INDEXED: %v", err)
	}
}

// A per-table index hint on a JOINED table, executed.
//
// This is the case that motivated Join.Clauses. INDEXED BY is per table
// reference, and a statement slot names one position per statement, so before
// the join carried its own clauses the hint attached to the driving table
// instead. That rendered FROM a INDEXED BY idx_on_b, which names an index the
// driving table does not have. SQLite rejects it, which is the good outcome;
// the bad one is a hint that silently lands on the wrong table when both
// happen to have an index of that name.
func TestIndexedBy_OnAJoinedTable_Executes(t *testing.T) {
	db := constructsDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS sqlb_join_target (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, tag TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_join_target_user ON sqlb_join_target(user_id)`,
		`INSERT INTO sqlb_join_target (id, user_id, tag) VALUES (1, 1, 'x')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	seedConstructs(t, db)

	jt := sqlb.Table{Name: "sqlb_join_target", Alias: "j"}
	jUser := sqlb.Col[int64](jt, "user_id")
	jTag := sqlb.Col[string](jt, "tag")

	var tag string
	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(jTag, &tag)).
		From(ctbl).
		Join(sqlb.Join{
			Type:    sqlb.JoinInner,
			Table:   jt,
			On:      []sqlb.Predicate{sqlb.EqExpr(cID, jUser)},
			Clauses: []sqlb.AnyClause{sqlite.IndexedBy("idx_join_target_user")},
		}).
		Where(sqlb.Eq(cID, int64(1))).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(q.SQL, `"j" INDEXED BY "idx_join_target_user"`) {
		t.Fatalf("the hint must sit on the joined table, not the driving one: %s", q.SQL)
	}
	if err := sqlb.One(ctx, db, q); err != nil {
		t.Fatalf("exec %s: %v", q.SQL, err)
	}
	if tag != "x" {
		t.Errorf("got %q", tag)
	}

	// And the failure the old shape produced: a hint naming an index the
	// driving table does not have is rejected by the engine.
	bad, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(jTag, &tag)).
		From(ctbl).
		Clause(sqlite.IndexedBy("idx_join_target_user")).
		Join(sqlb.Join{Type: sqlb.JoinInner, Table: jt, On: []sqlb.Predicate{sqlb.EqExpr(cID, jUser)}}).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := sqlb.One(ctx, db, bad); err == nil {
		t.Error("a hint naming another table's index should be rejected by SQLite")
	}
}
