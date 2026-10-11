package ch

import (
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
)

// Pinning tests for the ClickHouse helpers no example reaches.
//
// Why they exist: each is a one-line guarded constructor, and the package's
// invariant is that every construct here fails closed under another dialect.
// A helper that forgets its guard renders ClickHouse-only syntax into a
// Postgres statement, which the builder would happily hand to the database.
func TestHelpers_RenderAndGuard(t *testing.T) {
	tb := sqlb.Table{Name: "events", Alias: "e"}
	id := sqlb.Col[string](tb, "id")
	at := sqlb.Col[time.Time](tb, "at")

	// SAMPLE sits in its own statement slot rather than in the column list,
	// so it is checked through a built statement rather than as an expression.
	t.Run("Sample", func(t *testing.T) {
		q, err := sqlb.NewSelect(Dialect{}).Exprs(id).From(tb).Clause(Sample(0.1)).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if want := "SELECT `e`.`id` FROM `events` AS `e` SAMPLE 0.1"; q.SQL != want {
			t.Fatalf("got  %s\nwant %s", q.SQL, want)
		}
		// Sample on a Postgres statement is a compile error now, pinned in
		// examples/typecheck.
	})

	for _, tc := range []struct {
		name string
		expr sqlb.SQLer
		want string
	}{
		// uniqExact is the exact counterpart of uniq, which trades accuracy
		// for memory. Pinned because rendering one where the caller asked for
		// the other changes the answer rather than failing.
		{"UniqExact", UniqExact(id), "uniqExact(`e`.`id`)"},
		// toStartOfDay is the bucketing function a time series groups by.
		{"ToStartOfDay", ToStartOfDay(at), "toStartOfDay(`e`.`at`)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := sqlb.NewSelect(Dialect{}).Exprs(tc.expr).From(tb).Build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if want := "SELECT " + tc.want + " FROM `events` AS `e`"; q.SQL != want {
				t.Fatalf("got  %s\nwant %s", q.SQL, want)
			}
			if _, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(tc.expr).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Fatalf("under postgres: want ErrUnsupportedDialect, got %v", err)
			}
		})
	}
}

// The guard arms of the conditional aggregates.
//
// ClickHouse spells a conditional aggregate as a function suffix (sumIf,
// countIf) where other engines use FILTER (WHERE ...). Nothing else in the
// package renders a predicate inside an aggregate, so these guard arms have
// their own path through the renderer and their own chance to be missing.
func TestConditionalAggregates_Guard(t *testing.T) {
	tb := sqlb.Table{Name: "events", Alias: "e"}
	id := sqlb.Col[string](tb, "id")
	n := sqlb.Col[int32](tb, "n")
	hot := sqlb.Greater(n, int32(10))

	for label, e := range map[string]sqlb.SQLer{
		"ArgMax":  ArgMax(id, n),
		"SumIf":   SumIf(n, hot),
		"CountIf": CountIf(hot),
		"Count":   Count(),
	} {
		if _, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(e).From(tb).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under postgres: want ErrUnsupportedDialect, got %v", label, err)
		}
		if _, err := sqlb.NewSelect(Dialect{}).Exprs(e).From(tb).Build(); err != nil {
			t.Errorf("%s under clickhouse: %v", label, err)
		}
	}
}
