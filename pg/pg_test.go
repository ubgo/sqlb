package pg

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
)

// Every construct in the package must fail closed under a non-Postgres dialect.
func TestGuards_FailUnderSQLite(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	tags := sqlb.Col[[]string](tb, "tags")
	var s string
	cases := map[string]func() (sqlb.Query, error){
		"ILike": func() (sqlb.Query, error) {
			return sqlb.NewSelect(sqlb.SQLiteDialect{}).Columns(sqlb.Bind(name, &s)).From(tb).Where(ILike(name, "a%")).Build()
		},
		"Any": func() (sqlb.Query, error) {
			return sqlb.NewSelect(sqlb.SQLiteDialect{}).Columns(sqlb.Bind(name, &s)).From(tb).Where(Any(tags, "x")).Build()
		},
	}
	for n, build := range cases {
		if _, err := build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: want ErrUnsupportedDialect, got %v", n, err)
		}
	}
}

// Every helper family added for the snippet catalogue must fail closed under
// a non-Postgres dialect, like the first ones.
func TestGuards_NewHelperFamiliesFailUnderSQLite(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	name := sqlb.Col[string](tb, "name")
	day := sqlb.Col[time.Time](tb, "day")
	n := sqlb.Col[int32](tb, "n")
	var s string
	lite := func(items ...sqlb.SQLer) (sqlb.Query, error) {
		return sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(items...).From(tb).Build()
	}
	cases := map[string]func() (sqlb.Query, error){
		"JSONTable": func() (sqlb.Query, error) {
			jt := JSONTable(doc, "$.a", "jt", JSONTableColumn{Name: "x", Type: "text", Path: "$"})
			return sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(sqlb.Col[string](sqlb.Ref(jt), "x")).From(jt).Build()
		},
		"JSONObject": func() (sqlb.Query, error) { return lite(JSONObject([]JSONPair{Pair("k", name)})) },
		"JSONValue":  func() (sqlb.Query, error) { return lite(JSONValue[string](doc, "$.a")) },
		"SetSubscript": func() (sqlb.Query, error) {
			return sqlb.NewUpdate(sqlb.SQLiteDialect{}).Table(tb).Set(SetSubscript(doc, []string{"a"}, sqlb.Param("1"))).All().Build()
		},
		"RowsFrom": func() (sqlb.Query, error) {
			r := RowsFrom("r", []sqlb.SQLer{GenerateSeriesScalar(sqlb.Param(1), sqlb.Param(2))}, false, "n")
			return sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(sqlb.Col[int64](sqlb.Ref(r), "n")).From(r).Build()
		},
		"IntervalLit": func() (sqlb.Query, error) { return lite(IntervalLit("1 day")) },
		"DaysBetween": func() (sqlb.Query, error) { return lite(DaysBetween(day, day)) },
		"BitAnd":      func() (sqlb.Query, error) { return lite(BitAnd(n, n)) },
		"Encode":      func() (sqlb.Query, error) { return lite(Encode(sqlb.Col[[]byte](tb, "b"), "hex")) },
		"UUIDv7":      func() (sqlb.Query, error) { return lite(UUIDv7Timestamp(name)) },
		"Regclass": func() (sqlb.Query, error) {
			return lite(sqlb.BoolExpr(sqlb.EqExpr(sqlb.Cast[int64](n, "bigint"), Regclass("t"))))
		},
	}
	_ = s
	// Regclass used to be skipped here, with a comment calling it universal
	// text. It is not: SQLite rejects 'x'::regclass with a syntax error, so
	// it is guarded now like everything else and the exemption is gone.
	for k, build := range cases {
		if _, err := build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: want ErrUnsupportedDialect, got %v", k, err)
		}
	}
}

// The exported constructors an extension package builds on guard the same
// way as the helpers here, so a sub-package cannot leak Postgres syntax
// into another engine.
func TestExportedConstructors_Guard(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	name := sqlb.Col[string](tb, "name")
	lite := func(items ...sqlb.SQLer) (sqlb.Query, error) {
		return sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(items...).From(tb).Build()
	}
	cases := map[string]func() (sqlb.Query, error){
		"Expr": func() (sqlb.Query, error) {
			return lite(Expr[string]("x.Expr", func(*sqlb.Args) string { return "1" }))
		},
		"Pred": func() (sqlb.Query, error) {
			return lite(sqlb.BoolExpr(Pred("x.Pred", func(*sqlb.Args) string { return "true" })))
		},
		"BinOp":   func() (sqlb.Query, error) { return lite(BinOp[string]("x.BinOp", "<->", name, name)) },
		"BinPred": func() (sqlb.Query, error) { return lite(sqlb.BoolExpr(BinPred("x.BinPred", "%", name, name))) },
		"Func":    func() (sqlb.Query, error) { return lite(Func[string]("x.Func", "f", name)) },
	}
	for k, build := range cases {
		if _, err := build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: want ErrUnsupportedDialect, got %v", k, err)
		}
	}
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(Func[string]("x.Func", "f", name, QuoteLit("a"))).From(tb).Build()
	if err != nil || q.SQL != `SELECT f("t"."name", 'a') FROM "t" AS "t"` {
		t.Fatalf("postgres render: %q %v", q.SQL, err)
	}
}

func TestSettings(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(SetLocal("statement_timeout", "5s"), SetConfig("work_mem", "64MB", false), CurrentSetting("work_mem")).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT set_config('statement_timeout', $1, true), set_config('work_mem', $2, false), current_setting('work_mem', true)`
	if q.SQL != want || len(q.Args) != 2 || q.Args[0] != "5s" {
		t.Fatalf("got %q %v", q.SQL, q.Args)
	}
	for k, e := range map[string]sqlb.SQLer{"SetLocal": SetLocal("a", "b"), "CurrentSetting": CurrentSetting("a")} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
