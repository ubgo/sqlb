package packages_test

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// qualify is a clause sqlb does not have, written the way a package in another
// repository would write it.
//
// QUALIFY filters on a window function, and DuckDB, Snowflake and BigQuery
// have it. Nothing here is privileged: this is the same Clause interface the
// pg, sqlite and ch packages implement.
type qualify struct{ cond expr.Predicate }

// Slot names the position the renderer writes it at. An outside package can
// choose a slot but cannot invent one, because a position only exists if the
// renderer visits it, which is why the slot set in stmt is complete rather
// than minimal.
func (qualify) Slot() stmt.Slot { return stmt.SlotBeforeOrder }

// ClauseOf is the marker that types the clause to an engine and a statement
// kind. It does nothing; its signature is what lets a Postgres SELECT accept
// this clause and a SQLite one refuse it at compile time. An outside package
// writes exactly this line, or calls stmt.EngineClause, which writes it and
// adds the Build-time guard.
func (qualify) ClauseOf(postgres.Dialect, stmt.SelectKind) {}

// WriteSQL renders the clause. Note there is no leading space: the renderer
// supplies the separator before calling this, and a clause that adds its own
// produces a double space. Every clause in this repository follows that rule.
func (q qualify) WriteSQL(b *strings.Builder, a *render.Args) {
	b.WriteString("QUALIFY ")
	q.cond.WriteSQL(b, a)
}

// Qualify is what the outside package exports.
func Qualify(cond expr.Predicate) stmt.Clause[postgres.Dialect, stmt.SelectKind] {
	return qualify{cond}
}

// ExampleSlot shows a whole clause added from outside, with no change to sqlb.
func ExampleSlot() {
	events := expr.Table{Name: "event", Alias: "e"}
	userID := expr.Col[string](events, "user_id")
	at := expr.Col[int](events, "at")

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Exprs(userID).
		From(events).
		Where(expr.Greater(at, 100)).
		Clause(Qualify(expr.Eq(expr.Fn[int]("row_number"), 1))).
		Order(expr.OrderBy(at, expr.SortDesc)).
		Build()
	if err != nil {
		panic(err)
	}

	fmt.Println(q.SQL)
	fmt.Println(q.Args)

	// Output:
	// SELECT "e"."user_id" FROM "event" AS "e" WHERE ("e"."at" > $1) QUALIFY row_number() = $2 ORDER BY "e"."at" DESC
	// [100 1]
}

// ExampleQuery_targets shows what Query.Targets actually holds: the caller's
// own pointers, in select-list order, which is exactly what rows.Scan is given.
func ExampleQuery_targets() {
	var email string
	var age int

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Columns(bind.Bind(UserEmail, &email), bind.Bind(UserAge, &age)).
		From(Users).
		Build()
	if err != nil {
		panic(err)
	}

	fmt.Println(len(q.Targets), "targets")
	fmt.Println("[0] is &email:", q.Targets[0] == any(&email))
	fmt.Println("[1] is &age:  ", q.Targets[1] == any(&age))

	// Built with Exprs instead of Columns, a query carries none:
	noTargets, _ := stmt.NewSelect(postgres.Dialect{}).Exprs(UserEmail).From(Users).Build()
	fmt.Println("from Exprs:", len(noTargets.Targets), "targets")

	// Output:
	// 2 targets
	// [0] is &email: true
	// [1] is &age:   true
	// from Exprs: 0 targets
}
