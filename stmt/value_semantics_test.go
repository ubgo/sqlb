package stmt_test

import (
	"strings"
	"testing"

	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// TestBuilders_MethodsDoNotWriteToTheirReceiver is the contract a comparison
// with a widely used string builder bought.
//
// Every builder method returns a derived statement rather than mutating and
// returning the receiver, so deriving two queries from one base is safe by
// construction instead of by remembering Clone. The table walks every method
// that adds something, because the guarantee is only worth having if it holds
// for all of them: one method still mutating would be the one somebody uses.
func TestBuilders_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	d := postgres.Dialect{}
	tbl := expr.Table{Name: "t", Alias: "t"}
	col := expr.Col[int64](tbl, "a")
	txt := expr.Col[string](tbl, "b")

	base := func() *stmt.Select[postgres.Dialect] {
		return stmt.NewSelect(d).Exprs(col).From(tbl)
	}

	for _, tc := range []struct {
		name string
		add  func(*stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect]
	}{
		{"Where", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.Where(expr.Eq(col, 1)) }},
		{"Exprs", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.Exprs(txt) }},
		{"Join", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] {
			return s.Join(stmt.Join{Type: stmt.JoinInner, Table: expr.Table{Name: "u", Alias: "u"},
				On: []stmt.Predicate{expr.Eq(col, 1)}})
		}},
		{"Order", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] {
			return s.Order(expr.OrderBy(col, expr.SortAsc))
		}},
		{"GroupBy", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.GroupBy(col) }},
		{"Having", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] {
			return s.Having(expr.Greater(expr.Count(), 1))
		}},
		{"Limit", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.Limit(10) }},
		{"Offset", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.Offset(5) }},
		{"Distinct", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.Distinct() }},
		{"With", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.With("c", base()) }},
		{"Union", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] { return s.Union(base()) }},
		{"Apply", func(s *stmt.Select[postgres.Dialect]) *stmt.Select[postgres.Dialect] {
			return s.Apply(stmt.WithWhere(expr.Eq(col, 7)))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			before, err := s.Build()
			if err != nil {
				t.Fatal(err)
			}

			derived := tc.add(s)
			if derived == s {
				t.Error("returned the receiver, so the statement is shared")
			}

			after, err := s.Build()
			if err != nil {
				t.Fatal(err)
			}
			if after.SQL != before.SQL {
				t.Errorf("the receiver changed\n before %s\n after  %s", before.SQL, after.SQL)
			}

			out, err := derived.Build()
			if err != nil {
				t.Fatal(err)
			}
			if out.SQL == before.SQL && tc.name != "Apply" {
				t.Errorf("the derived statement is unchanged: %s", out.SQL)
			}
		})
	}
}

// TestWriteBuilders_MethodsDoNotWriteToTheirReceiver is the same contract for
// INSERT, UPDATE and DELETE.
//
// It exists because the first attempt at this change converted Select alone
// while the commit message claimed all four. A guarantee that holds for one
// builder and not its siblings is worse than none: it teaches the pattern and
// then breaks it where nobody is looking.
func TestWriteBuilders_MethodsDoNotWriteToTheirReceiver(t *testing.T) {
	d := postgres.Dialect{}
	tbl := expr.Table{Name: "t", Alias: "t"}
	col := expr.Col[int64](tbl, "a")

	t.Run("Insert", func(t *testing.T) {
		base := stmt.NewInsert(d).Into(tbl).Row(stmt.Set(col, int64(1)))
		before := mustSQL(t, base)
		if got := base.Row(stmt.Set(col, int64(2))); got == base {
			t.Error("Row returned the receiver")
		}
		if after := mustSQL(t, base); after != before {
			t.Errorf("the receiver changed: before %s, after %s", before, after)
		}
	})

	t.Run("Update", func(t *testing.T) {
		base := stmt.NewUpdate(d).Table(tbl).Set(stmt.Set(col, int64(1))).Where(expr.Eq(col, 1))
		before := mustSQL(t, base)
		if got := base.Where(expr.Eq(col, 2)); got == base {
			t.Error("Where returned the receiver")
		}
		if after := mustSQL(t, base); after != before {
			t.Errorf("the receiver changed: before %s, after %s", before, after)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		base := stmt.NewDelete(d).From(tbl).Where(expr.Eq(col, 1))
		before := mustSQL(t, base)
		if got := base.Where(expr.Eq(col, 2)); got == base {
			t.Error("Where returned the receiver")
		}
		if after := mustSQL(t, base); after != before {
			t.Errorf("the receiver changed: before %s, after %s", before, after)
		}
	})
}

// mustSQL builds anything with a Build method and returns its SQL.
func mustSQL(t *testing.T, b interface{ Build() (stmt.Query, error) }) string {
	t.Helper()
	q, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestDerived_SharesTheExecutionGuard is the part of this change that was not
// mechanical, and the reason Clone still exists.
//
// A derived statement holds the same bindings as the one it came from, and a
// binding is a pointer into a caller's variable. Two queries derived from one
// base therefore must not execute at once, exactly as two executions of one
// query must not. Giving each derivation a fresh guard would have quietly
// dropped that: the SQL would still be right and the interleaved writes would
// not. Clone is the deliberate act of starting a separate statement, and only
// Clone resets the guard.
func TestDerived_SharesTheExecutionGuard(t *testing.T) {
	d := postgres.Dialect{}
	tbl := expr.Table{Name: "t", Alias: "t"}
	col := expr.Col[int64](tbl, "a")

	var dst int64
	base := stmt.NewSelect(d).Columns(bind.Bind(col, &dst)).From(tbl)

	q1, err := base.Where(expr.Eq(col, 1)).Build()
	if err != nil {
		t.Fatal(err)
	}
	q2, err := base.Where(expr.Eq(col, 2)).Build()
	if err != nil {
		t.Fatal(err)
	}

	if !q1.Acquire() {
		t.Fatal("the first execution was refused")
	}
	defer q1.Release()

	if q2.Acquire() {
		t.Fatal("a second statement derived from the same base was allowed to run concurrently; both write through the same binding")
	}

	// A clone is a different statement and is not refused.
	q3, err := base.Clone().Where(expr.Eq(col, 3)).Build()
	if err != nil {
		t.Fatal(err)
	}
	if !q3.Acquire() {
		t.Fatal("a clone was refused; a clone is a separate statement and gets its own guard")
	}
}

// TestOption_CannotRemoveWhatTheLibraryAdded pins that options still only add,
// which is the whole reason a library hands them to Apply.
func TestOption_CannotRemoveWhatTheLibraryAdded(t *testing.T) {
	d := postgres.Dialect{}
	tbl := expr.Table{Name: "t", Alias: "t"}
	col := expr.Col[int64](tbl, "a")

	library := stmt.NewSelect(d).Exprs(col).From(tbl).Where(expr.Eq(col, 1))
	q, err := library.Apply(stmt.WithWhere(expr.Eq(col, 2))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, `"t"."a" = $1`) || !strings.Contains(q.SQL, `"t"."a" = $2`) {
		t.Fatalf("an option dropped the library's predicate: %s", q.SQL)
	}
}
