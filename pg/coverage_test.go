package pg

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
)

// Pinning tests for the helpers no catalogue example happens to use.
//
// Why they exist: every one of these is a one-line guarded constructor, and a
// one-line constructor is exactly where a wrong operator, a wrong function
// name or a missing guard survives review. The catalogues under examples/
// cover the constructs their source documents happened to contain; these pin
// the rest, so the package has no helper whose rendered SQL nothing asserts.
//
// Each case pins two things at once: the exact SQL under Postgres, and that
// the construct records itself as unsupported under another dialect. The
// second half is what stops an unguarded helper from leaking Postgres syntax
// into a SQLite or ClickHouse statement.
func TestHelpers_RenderAndGuard(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	n := sqlb.Col[int32](tb, "n")
	b := sqlb.Col[bool](tb, "b")
	day := sqlb.Col[time.Time](tb, "day")
	tags := sqlb.Col[[]string](tb, "tags")
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	rng := sqlb.Col[Range[time.Time]](tb, "rng")
	tsv := sqlb.Col[TSVector](tb, "tsv")
	tsq := sqlb.Col[TSQuery](tb, "tsq")

	// The rendered statement is always SELECT <expr> FROM "t" AS "t", so the
	// case only has to name the expression between them.
	const prefix, suffix = "SELECT ", ` FROM "t" AS "t"`

	for _, tc := range []struct {
		name string
		expr sqlb.SQLer
		want string
	}{
		// aggregates
		{"BoolOr", BoolOr(b), `bool_or("t"."b")`},
		{"Variance", Variance(n), `variance("t"."n")`},
		{"RangeIntersectAgg", RangeIntersectAgg(rng), `range_intersect_agg("t"."rng")`},
		// arrays
		{"ArrayContainedBy", sqlb.BoolExpr(ArrayContainedBy(tags, []string{"a"})), `("t"."tags" <@ $1)`},
		{"ArrayPrepend", ArrayPrepend("x", tags), `array_prepend($1, "t"."tags")`},
		{"ArrayCat", ArrayCat(tags, tags), `array_cat("t"."tags", "t"."tags")`},
		{"ArrayElementsScalar", ArrayElementsScalar(doc), `jsonb_array_elements("t"."doc")`},
		// full text
		{"Strip", Strip(tsv), `strip("t"."tsv")`},
		{"TSOr", TSOr(tsq, tsq), `("t"."tsq" || "t"."tsq")`},
		{"TSFollowedBy", TSFollowedBy(tsq, tsq), `("t"."tsq" <-> "t"."tsq")`},
		// json
		{"JSONBArrayLength", JSONBArrayLength(doc), `jsonb_array_length("t"."doc")`},
		{"Pretty", Pretty(doc), `jsonb_pretty("t"."doc")`},
		{"JSONValue+opts", JSONValue[string](doc, "$.a", DefaultOnError("''"), WithoutWrapper()), `JSON_VALUE("t"."doc", '$.a' WITHOUT WRAPPER DEFAULT '' ON ERROR)`},
		{"JSONValue+cond", JSONValue[string](doc, "$.a", WithConditionalWrapper()), `JSON_VALUE("t"."doc", '$.a' WITH CONDITIONAL WRAPPER)`},
		{"JSONObject+null", JSONObject([]JSONPair{Pair("k", name)}, NullOnNull()), `JSON_OBJECT(CAST($1 AS text): "t"."name" NULL ON NULL)`},
		// math and bits
		{"Sign", Sign(n), `sign("t"."n")`},
		{"Exp", Exp(n), `exp("t"."n")`},
		{"Log10", Log10(n), `log("t"."n")`},
		{"GCD", GCD(n, n), `gcd("t"."n", "t"."n")`},
		{"BitXor", BitXor(n, n), `("t"."n" # "t"."n")`},
		{"ShiftLeft", ShiftLeft(n, 2), `("t"."n" << $1)`},
		{"ShiftRight", ShiftRight(n, 2), `("t"."n" >> $1)`},
		// ranges
		{"Int4Range", Int4Range(1, 10, BoundsIncExc), `int4range($1, $2, '[)')`},
		{"LowerInf", sqlb.BoolExpr(LowerInf(rng)), `lower_inf("t"."rng")`},
		{"RangeContainsRange", sqlb.BoolExpr(RangeContainsRange(rng, rng)), `("t"."rng" @> "t"."rng")`},
		{"RangeContainedBy", sqlb.BoolExpr(RangeContainedBy(rng, rng)), `("t"."rng" <@ "t"."rng")`},
		{"StrictlyRight", sqlb.BoolExpr(StrictlyRight(rng, rng)), `("t"."rng" >> "t"."rng")`},
		{"RangeDiff", RangeDiff(rng, rng), `("t"."rng" - "t"."rng")`},
		{"UpperInc", UpperInc(rng), `upper_inc("t"."rng")`},
		{"RangeMerge", RangeMerge(rng, rng), `range_merge("t"."rng", "t"."rng")`},
		// text
		{"NotILike", sqlb.BoolExpr(NotILike(name, "a%")), `("t"."name" NOT ILIKE $1)`},
		{"NotRegexI", sqlb.BoolExpr(NotRegexI(name, "^a")), `("t"."name" !~* $1)`},
		{"Initcap", Initcap(name), `initcap("t"."name")`},
		{"Reverse", Reverse(name), `reverse("t"."name")`},
		{"LTrim", LTrim(name), `ltrim("t"."name")`},
		{"RTrim", RTrim(name), `rtrim("t"."name")`},
		{"RPad", RPad(name, 10, " "), `rpad("t"."name", $1, $2)`},
		{"StartsWith", sqlb.BoolExpr(StartsWith(name, "a")), `starts_with("t"."name", $1)`},
		{"Repeat", Repeat(name, 3), `repeat("t"."name", $1)`},
		// time
		{"SubInterval", SubInterval(day, Interval("1 day")), `("t"."day" - $1::interval)`},
		{"MakeInterval", MakeInterval(1, 2), `make_interval(days => $1, hours => $2)`},
		{"ToDate", ToDate(name, "YYYY-MM-DD"), `to_date("t"."name", $1)`},
		{"DatePlusDays", DatePlusDays(day, sqlb.Param(7)), `("t"."day" + $1)`},
		// session and catalog
		{"AdvisoryXactLock", AdvisoryXactLock(sqlb.Param(int64(7))), `pg_advisory_xact_lock($1)`},
		{"GetSerialSequence", GetSerialSequence("t", "id"), `pg_get_serial_sequence($1, $2)`},
		{"Setval", Setval(Regclass("t_id_seq"), sqlb.Param(int64(5))), `setval('t_id_seq'::regclass, $1)`},
		// booleans
		{"IsFalse", sqlb.BoolExpr(IsFalse(b)), `"t"."b" IS FALSE`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(tc.expr).From(tb).Build()
			if err != nil {
				t.Fatalf("postgres build: %v", err)
			}
			if got := q.SQL; got != prefix+tc.want+suffix {
				t.Errorf("got  %s\nwant %s", got, prefix+tc.want+suffix)
			}
			// The same construct under another dialect must fail closed.
			if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(tc.expr).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("under sqlite: want ErrUnsupportedDialect, got %v", err)
			}
		})
	}
}

// Guard is the constructor an extension package calls when a helper renders
// in several pieces and cannot use Func or BinOp. It is exported, so its
// contract is pinned here rather than only through its callers: true under
// Postgres, false under anything else, and a false result has already
// recorded the construct.
func TestGuard_ReportsAndRecords(t *testing.T) {
	pgArgs := sqlb.NewArgs(sqlb.PostgresDialect{})
	if !Guard(pgArgs, "x.Construct") {
		t.Error("Guard must allow rendering under Postgres")
	}
	if err := pgArgs.Err(); err != nil {
		t.Errorf("Guard recorded an error under Postgres: %v", err)
	}

	liteArgs := sqlb.NewArgs(sqlb.SQLiteDialect{})
	if Guard(liteArgs, "x.Construct") {
		t.Error("Guard must refuse rendering under SQLite")
	}
	if err := liteArgs.Err(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Errorf("Guard must record the construct as unsupported, got %v", err)
	}
}

// SearchBreadthFirst and OverridingUserValue render into statement slots
// rather than into an expression, so they cannot go in the table above.
func TestClauseHelpers(t *testing.T) {
	// SEARCH BREADTH FIRST orders a recursive CTE level by level. Pinned
	// because its sibling SearchDepthFirst is covered by a catalogue example
	// and this one is not, and the two differ by a single keyword.
	got := renderSQLer(t, SearchBreadthFirst("ord", "id"))
	if want := `SEARCH BREADTH FIRST BY "id" SET "ord"`; got != want {
		t.Errorf("SearchBreadthFirst\n got  %s\n want %s", got, want)
	}

	// OVERRIDING USER VALUE makes an INSERT write the identity column the
	// table generates instead of the value supplied. Pinned because the
	// clause is easy to render into the wrong slot, where it would be
	// accepted by the builder and rejected by the database.
	ins := sqlb.NewInsert(sqlb.PostgresDialect{}).Into(sqlb.Table{Name: "t", Alias: "t"}).
		Row(sqlb.Set(sqlb.Col[int32](sqlb.Table{Name: "t", Alias: "t"}, "id"), int32(1))).
		Clause(OverridingUserValue())
	q, err := ins.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if want := `INSERT INTO "t" AS "t" ("id") OVERRIDING USER VALUE VALUES ($1)`; q.SQL != want {
		t.Errorf("OverridingUserValue\n got  %s\n want %s", q.SQL, want)
	}
	// OverridingUserValue on a SQLite INSERT no longer builds: it is a compile
	// error, pinned in examples/typecheck.
}

// renderSQLer renders a bare SQLer, for the helpers that are statement
// fragments rather than expressions.
func renderSQLer(t *testing.T, s sqlb.SQLer) string {
	t.Helper()
	a := sqlb.NewArgs(sqlb.PostgresDialect{})
	out := s.SQL(a)
	if err := a.Err(); err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

// JSON[T] is the scan-and-value wrapper for a jsonb column holding a Go
// value. Its round trip is pinned here because a driver only ever exercises
// one direction per test, and the failure mode of the other is silent: a
// Value that emits the wrong shape stores valid JSON that Scan cannot read
// back.
func TestJSON_RoundTrip(t *testing.T) {
	type payload struct {
		Plan  string `json:"plan"`
		Seats int    `json:"seats"`
	}

	in := JSON[payload]{V: payload{Plan: "pro", Seats: 5}}
	v, err := in.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	// Value returns a string, not []byte. That is the documented contract:
	// driver.Value permits both and the string form is what every driver
	// this package targets accepts for a json or jsonb parameter.
	text, ok := v.(string)
	if !ok {
		t.Fatalf("Value must produce a string, got %T", v)
	}
	if text != `{"plan":"pro","seats":5}` {
		t.Fatalf("Value produced %s", text)
	}

	var out JSON[payload]
	if err := out.Scan([]byte(text)); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if out.V != in.V {
		t.Fatalf("round trip changed the value: %+v", out.V)
	}
	if err := out.Scan(text); err != nil {
		t.Fatalf("Scan(string): %v", err)
	}
	if out.V != in.V {
		t.Fatalf("round trip from string changed the value: %+v", out.V)
	}

	// A NULL column must clear the value rather than leave the previous row's.
	if err := out.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if out.V != (payload{}) {
		t.Fatalf("NULL left a stale value: %+v", out.V)
	}

	// Anything that is not text is a driver mismatch, and the error says so
	// rather than leaving the caller with a zero value that looks real.
	err = out.Scan(42)
	if err == nil {
		t.Fatal("Scan of a non-text value must fail")
	}
	if err.Error() == "" {
		t.Fatal("the scan error must describe itself")
	}
}

// The constants that used to render without a dialect guard.
//
// They were built on sqlb.Literal, which renders a fragment with no dialect
// to consult, so every one of them built clean under SQLite and was rejected
// by the database at run time: '{}'::jsonb, B'1011' and the rest all answer
// with a syntax error. That was measured against a real SQLite database
// through contrib/driver-sqlite, not inferred.
//
// They now go through litExpr, which guards like every other construct here.
// This test is what keeps them that way: it asserts both the exact Postgres
// rendering and the dialect error, so a future constant added with
// sqlb.Literal fails here rather than at someone's database.
func TestGuardedLiterals(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	for _, tc := range []struct {
		name string
		expr sqlb.SQLer
		want string
	}{
		{"EmptyJSONObject", EmptyJSONObject(), `'{}'::jsonb`},
		{"EmptyJSONArray", EmptyJSONArray(), `'[]'::jsonb`},
		{"JSONNull", JSONNull(), `'null'::jsonb`},
		{"EmptyArray", EmptyArray[string]("text"), `'{}'::text[]`},
		{"ArrayLit", ArrayLit[int32]("{1,2}", "int"), `'{1,2}'::int[]`},
		{"TimestampLit", TimestampLit("2026-01-01"), `timestamp '2026-01-01'`},
		{"TimestamptzLit", TimestamptzLit("2026-01-01Z"), `'2026-01-01Z'::timestamptz`},
		{"DateLit", DateLit("2026-01-01"), `date '2026-01-01'`},
		{"Infinity", Infinity(), `'infinity'::timestamptz`},
		{"Regclass", Regclass("t_id_seq"), `'t_id_seq'::regclass`},
		{"BitLit", BitLit("1011"), `B'1011'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(tc.expr).From(tb).Build()
			if err != nil {
				t.Fatalf("postgres: %v", err)
			}
			if want := `SELECT ` + tc.want + ` FROM "t" AS "t"`; q.SQL != want {
				t.Errorf("got  %s\nwant %s", q.SQL, want)
			}
			if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(tc.expr).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Errorf("under sqlite: want ErrUnsupportedDialect, got %v", err)
			}
		})
	}
}

// Variadic and Named decorate an argument rather than being expressions, and
// both render Postgres-only syntax: VARIADIC has no equivalent elsewhere, and
// SQLite answers `k => ?` with a syntax error. RowRef is deliberately not
// here: it renders a bare quoted identifier, which parses anywhere, and the
// Postgres-specific part is whichever guarded function wraps it.
func TestArgumentDecorators_Guard(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	for name, e := range map[string]sqlb.SQLer{
		"Variadic": Variadic(sqlb.Param([]string{"a"})),
		"Named":    Named("k", sqlb.Param(1)),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(sqlb.Fn[string]("f", e)).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: want ErrUnsupportedDialect, got %v", name, err)
		}
	}
}

// Both carries no syntax of its own, so its guard comes from its operands.
// Pinned because "the guard is somewhere else" is exactly the reasoning that
// leaves a real hole, and the only way to know it holds is to build one.
func TestBoth_InheritsItsOperandsGuard(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	body := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(sqlb.Col[string](tb, "name")).From(tb)
	cte := sqlb.CTE{
		Name:   "c",
		Body:   body,
		Suffix: Both(SearchDepthFirst("ord", "id"), Cycle("cyc", "path", "id")),
	}
	ref := sqlb.Table{Name: "c", Alias: "c"}
	if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).WithCTE(cte).
		Exprs(sqlb.Col[string](ref, "name")).From(ref).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", err)
	}
}
