package stmt_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// The shape checks on the statement layer's three unquotable strings: a join
// keyword, a set operation and a subquery's quantifier, plus the operator and
// function-name doors an engine package renders through.
//
// Two halves at every door, as the pg audit established: a hostile value must
// be refused, and every real one must still build. Either half alone proves
// nothing — a check that refuses everything passes the first, and no check at
// all passes the second.

// shInjection closes the construct it sits in and appends a statement. It is
// the payload the SQLite and MySQL packages found reaching statement text
// through a named string type that looked closed.
const shInjection = `INNER JOIN x ON 1=1; DROP TABLE users; --`

var (
	shT  = stmt.Table{Name: "users", Alias: "u"}
	shU  = stmt.Table{Name: "sessions", Alias: "s"}
	shID = expr.Col[int64](shT, "id")
	shSU = expr.Col[int64](shU, "user_id")
)

func shSelect() *stmt.Select[postgres.Dialect] {
	return stmt.NewSelect(postgres.Dialect{}).Exprs(shID).From(shT)
}

// TestJoinAndSetOp_TheEscapeHatchStillWorks is the evidence that adding the
// shape check did not quietly close what CLAUDE.md says is open.
//
// The decision it records is that the SET of keywords is open: FULL OUTER JOIN
// and INTERSECT ALL exist precisely because this module does not name them,
// MySQL having no full outer join and SQLite no INTERSECT ALL. This test
// builds each of them, and a lower-case spelling, and fails if any stops
// working. If a future check narrows the shape to a list, this is what says
// so.
func TestJoinAndSetOp_TheEscapeHatchStillWorks(t *testing.T) {
	cases := []struct {
		name string
		want string
		got  func() (string, error)
	}{
		{"FULL OUTER JOIN", "FULL OUTER JOIN", func() (string, error) {
			return shBuild(shSelect().Join(stmt.Join{
				Type: stmt.JoinType("FULL OUTER JOIN"), Table: shU,
				On: []stmt.Predicate{expr.EqExpr(shID, shSU)},
			}))
		}},
		{"a lower-case join keyword", "left join", func() (string, error) {
			return shBuild(shSelect().Join(stmt.Join{
				Type: stmt.JoinType("left join"), Table: shU,
				On: []stmt.Predicate{expr.EqExpr(shID, shSU)},
			}))
		}},
		{"INTERSECT ALL", "INTERSECT ALL", func() (string, error) {
			return shBuild(shSelect().SetOp("INTERSECT ALL", shSelect()))
		}},
		{"EXCEPT ALL", "EXCEPT ALL", func() (string, error) {
			return shBuild(shSelect().SetOp("EXCEPT ALL", shSelect()))
		}},
		{"a named join type", "INNER JOIN", func() (string, error) {
			return shBuild(shSelect().Join(stmt.Join{
				Type: stmt.JoinInner, Table: shU,
				On: []stmt.Predicate{expr.EqExpr(shID, shSU)},
			}))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, err := c.got()
			if err != nil {
				t.Fatalf("the escape hatch was closed: %v", err)
			}
			if !strings.Contains(sql, c.want) {
				t.Errorf("rendered %s, which does not contain %q", sql, c.want)
			}
		})
	}
}

// TestShape_EveryDoorRefusesAHostileValue is the other half: every place the
// statement layer writes a caller's string into the text unquoted.
func TestShape_EveryDoorRefusesAHostileValue(t *testing.T) {
	cases := map[string]func() (string, error){
		"JoinType": func() (string, error) {
			return shBuild(shSelect().Join(stmt.Join{
				Type: stmt.JoinType(shInjection), Table: shU,
				On: []stmt.Predicate{expr.EqExpr(shID, shSU)},
			}))
		},
		"SetOp": func() (string, error) {
			return shBuild(shSelect().SetOp("UNION; DROP TABLE users; --", shSelect()))
		},
		"SubqueryCompare/operator": func() (string, error) {
			return shBuild(shSelect().Where(stmt.SubqueryCompare(shID, "= ANY (SELECT 1); DROP TABLE users; --", "", shSelect())))
		},
		"SubqueryCompare/quantifier": func() (string, error) {
			return shBuild(shSelect().Where(stmt.SubqueryCompare(shID, "=", "ANY (SELECT 1); DROP TABLE users; --", shSelect())))
		},
		"EngineBinOp": func() (string, error) {
			return shBuild(shSelect().Where(expr.Greater(
				stmt.EngineBinOp[int64]("postgres", "probe", shInjection, shID, shID), 0)))
		},
		"EngineBinPred": func() (string, error) {
			return shBuild(shSelect().Where(stmt.EngineBinPred("postgres", "probe", shInjection, shID, shID)))
		},
		"EngineFn": func() (string, error) {
			return shBuild(shSelect().Where(expr.Greater(
				stmt.EngineFn[int64]("postgres", "probe", `f(1); DROP TABLE users; --`, shID), 0)))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			sql, err := run()
			if err == nil {
				t.Fatalf("a hostile value built: %s", sql)
			}
			if !errors.Is(err, expr.ErrUnknownValue) {
				t.Errorf("refused with %v, which is not the typed refusal", err)
			}
		})
	}
}

// TestShape_EveryDoorTakesARealValue is the honest half at the same doors.
func TestShape_EveryDoorTakesARealValue(t *testing.T) {
	cases := map[string]func() (string, error){
		"SubqueryCompare/IN": func() (string, error) {
			return shBuild(shSelect().Where(stmt.InSubquery(shID, shSelect())))
		},
		"SubqueryCompare/quantified": func() (string, error) {
			return shBuild(shSelect().Where(stmt.SubqueryCompare(shID, "=", "ANY", shSelect())))
		},
		"EngineBinOp/symbolic": func() (string, error) {
			return shBuild(shSelect().Where(expr.Greater(
				stmt.EngineBinOp[int64]("postgres", "probe", "&", shID, shID), 0)))
		},
		"EngineBinPred/word": func() (string, error) {
			return shBuild(shSelect().Where(stmt.EngineBinPred("postgres", "probe", "IS NOT DISTINCT FROM", shID, shID)))
		},
		"EngineFn/qualified": func() (string, error) {
			return shBuild(shSelect().Where(expr.Greater(
				stmt.EngineFn[int64]("postgres", "probe", "pg_catalog.abs", shID), 0)))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := run(); err != nil {
				t.Errorf("a real value was refused: %v", err)
			}
		})
	}
}

// build renders a select and returns its SQL.
func shBuild(s *stmt.Select[postgres.Dialect]) (string, error) {
	q, err := s.Build()
	if err != nil {
		return "", err
	}
	return q.SQL, nil
}
