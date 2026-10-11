package ch

import (
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
)

func TestDialect_BacktickQuoting(t *testing.T) {
	if got := (Dialect{}).QuoteIdent("a`b"); got != "`a``b`" {
		t.Fatalf("quote %q", got)
	}
	if _, ok := any(Dialect{}).(sqlb.Returning); ok {
		t.Fatal("ClickHouse must not claim RETURNING")
	}
	if sqlb.DialectName(Dialect{}) != DialectClickHouse {
		t.Fatal("name")
	}
}

func TestSettings_RenderInGivenOrder(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	c := sqlb.Col[string](tb, "c")
	var s string
	q, err := sqlb.NewSelect(Dialect{}).Columns(sqlb.Bind(c, &s)).From(tb).
		Clause(Settings(Setting{SettingMaxThreads, 4}, JoinUseNulls())).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT `t`.`c` FROM `t` AS `t` SETTINGS max_threads = ?, join_use_nulls = ?"
	if q.SQL != want || q.Args[0] != 4 || q.Args[1] != 1 {
		t.Fatalf("got %s %v", q.SQL, q.Args)
	}
}

func TestGuards_FailUnderPostgres(t *testing.T) {
	tb := sqlb.Table{Name: "t", Alias: "t"}
	at := sqlb.Col[time.Time](tb, "at")
	var when time.Time
	// Final() on a Postgres statement is a compile error now; the expression
	// guard is what this still exercises.
	_, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Columns(sqlb.Bind(ToStartOfHour(at), &when)).From(tb).Build()
	if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", err)
	}
}
