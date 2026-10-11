package sqlite

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

// Every helper, rendered and guarded.
//
// Each case pins two things: the exact SQL under the SQLite dialect, and that
// the construct records itself as unsupported under another one. The second
// half is what stops a SQLite-only spelling from leaking into a Postgres
// statement, where it would build clean and fail at the server.
//
// The SQL here is text. What proves it is correct SQLite rather than merely
// the text we expected is the execution suite in contrib/driver-sqlite, which
// runs these same constructs against a real database.
func TestHelpers_RenderAndGuard(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	doc := sqlb.Col[string](tb, "doc")
	name := sqlb.Col[string](tb, "name")
	n := sqlb.Col[int32](tb, "n")
	at := sqlb.Col[string](tb, "at")

	const prefix, suffix = "SELECT ", ` FROM "t" AS "t"`

	for _, tc := range []struct {
		name string
		expr sqlb.SQLer
		want string
	}{
		// JSON1
		{"Extract", JSONExtract[string](doc, "$.plan"), `json_extract("t"."doc", '$.plan')`},
		{"Arrow", JSONArrow(doc, "$.a"), `("t"."doc" -> '$.a')`},
		{"ArrowText", JSONArrowText(doc, "$.a"), `("t"."doc" ->> '$.a')`},
		{"Valid", JSONValid(doc), `json_valid("t"."doc")`},
		{"Type", JSONType(doc), `json_type("t"."doc")`},
		{"TypeAt", JSONTypeAt(doc, "$.a"), `json_type("t"."doc", '$.a')`},
		{"ArrayLength", JSONArrayLength(doc), `json_array_length("t"."doc")`},
		{"ArrayLengthAt", JSONArrayLengthAt(doc, "$.tags"), `json_array_length("t"."doc", '$.tags')`},
		{"Quote", JSONQuote(42), `json_quote(?)`},
		{"Array", JSONArray(name, n), `json_array("t"."name", "t"."n")`},
		{"Object", JSONObject(KV("k", name), KV("n", n)), `json_object('k', "t"."name", 'n', "t"."n")`},
		{"Set", JSONSet(doc, "$.a", sqlb.Param("x")), `json_set("t"."doc", '$.a', ?)`},
		{"Insert", JSONInsert(doc, "$.a", sqlb.Param("x")), `json_insert("t"."doc", '$.a', ?)`},
		{"Replace", JSONReplace(doc, "$.a", sqlb.Param("x")), `json_replace("t"."doc", '$.a', ?)`},
		{"Remove", JSONRemove(doc, "$.a", "$.b"), `json_remove("t"."doc", '$.a', '$.b')`},
		{"Patch", JSONPatch(doc, doc), `json_patch("t"."doc", "t"."doc")`},
		{"GroupArray", JSONGroupArray(name), `json_group_array("t"."name")`},
		{"GroupObject", JSONGroupObject(name, n), `json_group_object("t"."name", "t"."n")`},
		// text and full-text search
		{"Glob", sqlb.BoolExpr(Glob(name, "a*")), `("t"."name" GLOB ?)`},
		{"NotGlob", sqlb.BoolExpr(NotGlob(name, "a*")), `("t"."name" NOT GLOB ?)`},
		{"Match", sqlb.BoolExpr(Match(name, "hello")), `("t"."name" MATCH ?)`},
		{"Rank", Rank(tb), `"t"."rank"`},
		{"BM25", BM25(tb, 1.0, 2.0), `bm25("t", ?, ?)`},
		{"Highlight", Highlight(tb, 0, "<b>", "</b>"), `highlight("t", ?, ?, ?)`},
		{"Snippet", Snippet(tb, 0, "<b>", "</b>", "...", 10), `snippet("t", ?, ?, ?, ?, ?)`},
		{"Instr", Instr(name, "x"), `instr("t"."name", ?)`},
		{"Hex", Hex(name), `hex("t"."name")`},
		{"QuoteSQL", QuoteSQL(name), `quote("t"."name")`},
		{"Typeof", Typeof(name), `typeof("t"."name")`},
		{"IfNull", IfNull(name, sqlb.Param("z")), `ifnull("t"."name", ?)`},
		{"IIF", IIF(sqlb.Greater(n, int32(1)), name, sqlb.Param("z")), `iif("t"."n" > ?, "t"."name", ?)`},
		{"Format", Format("%s-%d", name, n), `format('%s-%d', "t"."name", "t"."n")`},
		{"GroupConcat", GroupConcat(name, ","), `group_concat("t"."name", ?)`},
		// time
		{"Date", Date(at, StartOfMonth), `date("t"."at", 'start of month')`},
		{"Time", Time(at), `time("t"."at")`},
		{"DateTime", DateTime(at, Offset(-7, Day)), `datetime("t"."at", '-7 days')`},
		{"JulianDay", JulianDay(at), `julianday("t"."at")`},
		{"UnixEpoch", UnixEpoch(at), `unixepoch("t"."at")`},
		{"Strftime", Strftime("%Y-%W", at), `strftime('%Y-%W', "t"."at")`},
		{"Now", Now(), `'now'`},
		// rowid, scalars and session
		{"Rowid", Rowid(tb), `"t"."rowid"`},
		{"LastInsertRowid", LastInsertRowid(), `last_insert_rowid()`},
		{"Changes", Changes(), `changes()`},
		{"Random", Random(), `random()`},
		{"RandomBlob", RandomBlob(16), `randomblob(?)`},
		{"Abs", Abs(n), `abs("t"."n")`},
		{"Max", Max(n, sqlb.Param(int32(5))), `max("t"."n", ?)`},
		{"Version", Version(), `sqlite_version()`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(tc.expr).From(tb).Build()
			if err != nil {
				t.Fatalf("sqlite build: %v", err)
			}
			if got := q.SQL; got != prefix+tc.want+suffix {
				t.Errorf("got  %s\nwant %s", got, prefix+tc.want+suffix)
			}
			if _, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(tc.expr).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("under postgres: want ErrUnsupportedDialect, got %v", err)
			}
		})
	}
}

// Conflict resolution renders into statement slots rather than the select
// list, so it is checked through built statements.
func TestConflictResolution(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	n := sqlb.Col[int32](tb, "n")

	// INSERT OR REPLACE sits between the keyword and INTO, which is the slot
	// the core gained for it. No other engine spells a clause there.
	t.Run("insert or", func(t *testing.T) {
		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(tb).
			Row(sqlb.Set(name, "a"), sqlb.Set(n, int32(1))).
			Clause(InsertOr(OrReplace)).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := `INSERT OR REPLACE INTO "t" ("name", "n") VALUES (?, ?)`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
		// InsertOr on a Postgres INSERT is a compile error now; examples/typecheck
		// pins it.
	})

	// The upsert, including Excluded and both WHERE positions.
	t.Run("on conflict do update", func(t *testing.T) {
		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(tb).
			Row(sqlb.Set(name, "a"), sqlb.Set(n, int32(1))).
			Clause(OnConflict(name).
				TargetWhere(sqlb.Greater(n, int32(0))).
				DoUpdate(sqlb.SetExpr(n, Excluded(n))).
				UpdateWhere(sqlb.LessExpr(n, Excluded(n))).
				Clause()).
			Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := `INSERT INTO "t" ("name", "n") VALUES (?, ?) ON CONFLICT ("name") WHERE ("t"."n" > ?) DO UPDATE SET "n" = excluded."n" WHERE ("t"."n" < excluded."n")`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})

	t.Run("on conflict do nothing", func(t *testing.T) {
		q, err := sqlb.NewInsert(sqlb.SQLiteDialect{}).Into(tb).
			Row(sqlb.Set(name, "a")).
			Clause(OnConflict().DoNothing()).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := `INSERT INTO "t" ("name") VALUES (?) ON CONFLICT DO NOTHING`
		if q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
	})
}

// The set-returning JSON functions are FROM sources.
func TestJSONSources(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	doc := sqlb.Col[string](tb, "doc")

	for name, src := range map[string]sqlb.Source{
		"Each": JSONEach(doc, "e"),
		"Tree": JSONTree(doc, "e"),
	} {
		ref := sqlb.Ref(src)
		q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
			Exprs(sqlb.Col[string](ref, "value")).From(src).Build()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := q.SQL; got == "" {
			t.Errorf("%s rendered nothing", name)
		}
		if _, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
			Exprs(sqlb.Col[string](ref, "value")).From(src).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under postgres: got %v", name, err)
		}
	}
}

// The planner hints render after the driving table.
func TestPlannerHints(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	for label, c := range map[string]SelectClause{
		"IndexedBy":  IndexedBy("idx_t_name"),
		"NotIndexed": NotIndexed(),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(name).From(tb).Clause(c).Build(); err != nil {
			t.Errorf("%s: %v", label, err)
		}
		// The same hint on a Postgres statement is a compile error now, pinned
		// in examples/typecheck.
	}
}

// Offset builds a relative time modifier. Pinned because it does its own
// integer formatting and sign handling, which is the kind of code that is
// correct for every case someone thought of and wrong for zero.
func TestOffset(t *testing.T) {
	for _, tc := range []struct {
		n    int
		unit Unit
		want Modifier
	}{
		{7, Day, "+7 days"},
		{-7, Day, "-7 days"},
		{0, Day, "+0 days"},
		{1, Month, "+1 months"},
		{-120, Second, "-120 seconds"},
		{3, Hour, "+3 hours"},
		{3, Minute, "+3 minutes"},
		{3, Year, "+3 years"},
	} {
		if got := Offset(tc.n, tc.unit); got != tc.want {
			t.Errorf("Offset(%d, %q) = %q, want %q", tc.n, tc.unit, got, tc.want)
		}
	}
	// A unit outside the six, which only a conversion can produce, makes a
	// modifier that fails at Build rather than one SQLite answers with NULL.
	if got := Offset(1, Unit("fortnights")); got != "" {
		t.Errorf("an unknown unit produced the modifier %q", got)
	}
}

// The exported constructors an application builds its own helpers on behave
// like pg's: true under SQLite, false and recorded under anything else.
func TestExportedConstructors_Guard(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	lite := func(items ...sqlb.SQLer) error {
		_, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(items...).From(tb).Build()
		return err
	}
	cases := map[string]sqlb.SQLer{
		"Expr":    Expr[string]("x.Expr", func(*sqlb.Args) string { return "1" }),
		"Pred":    sqlb.BoolExpr(Pred("x.Pred", func(*sqlb.Args) string { return "1" })),
		"BinOp":   BinOp[string]("x.BinOp", "GLOB", name, name),
		"BinPred": sqlb.BoolExpr(BinPred("x.BinPred", "MATCH", name, name)),
		"Func":    Func[string]("x.Func", "f", name),
	}
	for k, e := range cases {
		if err := lite(e); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under postgres: want ErrUnsupportedDialect, got %v", k, err)
		}
	}

	sqliteArgs := sqlb.NewArgs(sqlb.SQLiteDialect{})
	if !Guard(sqliteArgs, "x.C") || sqliteArgs.Err() != nil {
		t.Error("Guard must allow rendering under SQLite and record nothing")
	}
	pgArgs := sqlb.NewArgs(sqlb.PostgresDialect{})
	if Guard(pgArgs, "x.C") {
		t.Error("Guard must refuse under Postgres")
	}
	if !errors.Is(pgArgs.Err(), sqlb.ErrUnsupportedDialect) {
		t.Errorf("Guard must record the construct, got %v", pgArgs.Err())
	}

	q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Exprs(Func[string]("x.Func", "f", name, QuoteLit("a"))).From(tb).Build()
	if err != nil || q.SQL != "SELECT f(\"t\".\"name\", 'a') FROM \"t\" AS \"t\"" {
		t.Fatalf("render: %q %v", q.SQL, err)
	}
}
