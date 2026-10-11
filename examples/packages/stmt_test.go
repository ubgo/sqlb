package packages_test

import (
	"fmt"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// Invoices is the application's own table, joined against the library's.
var (
	Invoices  = expr.Table{Name: "invoice", Alias: "i"}
	InvUserID = expr.Col[string](Invoices, "user_id")
	InvTotal  = expr.Col[int](Invoices, "total")
)

func show(q stmt.Query, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(q.SQL)
	if len(q.Args) > 0 {
		fmt.Println(q.Args)
	}
}

// ExampleSelect shows the shape of a statement. Exprs names select items with
// no scan destination, which is what a subquery, a CTE body or a caller that
// scans on its own needs.
func ExampleSelect() {
	show(stmt.NewSelect(postgres.Dialect{}).
		Exprs(UserEmail, UserAge).
		From(Users).
		Where(expr.Greater(UserAge, 30)).
		Order(expr.OrderBy(UserAge, expr.SortDesc)).
		Limit(10).
		Build())

	// Output:
	// SELECT "u"."email", "u"."age" FROM "app_user" AS "u" WHERE ("u"."age" > $1) ORDER BY "u"."age" DESC LIMIT $2
	// [30 10]
}

// ExampleSelect_join shows the boundary this library exists for: two packages
// declare their own tables, and a third joins them with the compiler checking
// the condition.
func ExampleSelect_join() {
	show(stmt.NewSelect(postgres.Dialect{}).
		Exprs(UserEmail, InvTotal).
		From(Users).
		Join(stmt.Join{
			Type:   stmt.JoinInner,
			Table:  Invoices,
			Source: Invoices,
			On:     []expr.Predicate{expr.EqExpr(UserID, InvUserID)},
		}).
		Build())

	// Output:
	// SELECT "u"."email", "i"."total" FROM "app_user" AS "u" INNER JOIN "invoice" AS "i" ON ("u"."id" = "i"."user_id")
}

// ExampleSelect_groupBy shows aggregation with a HAVING clause.
func ExampleSelect_groupBy() {
	show(stmt.NewSelect(postgres.Dialect{}).
		Exprs(InvUserID, expr.Sum(InvTotal)).
		From(Invoices).
		GroupBy(InvUserID).
		Having(expr.Greater(expr.Sum(InvTotal), 100)).
		Build())

	// Output:
	// SELECT "i"."user_id", sum("i"."total") FROM "invoice" AS "i" GROUP BY "i"."user_id" HAVING (sum("i"."total") > $1)
	// [100]
}

// ExampleInsert shows a write. Values are bound, never interpolated.
func ExampleInsert() {
	show(stmt.NewInsert(postgres.Dialect{}).
		Into(Users).
		Row(stmt.Set(UserID, "u1"), stmt.Set(UserEmail, "ada@example.com")).
		Build())

	// Output:
	// INSERT INTO "app_user" AS "u" ("id", "email") VALUES ($1, $2)
	// [u1 ada@example.com]
}

// ExampleUpdate shows an update with a WHERE clause. An update with no WHERE
// is a decision this package makes you write out, not an accident.
func ExampleUpdate() {
	show(stmt.NewUpdate(postgres.Dialect{}).
		Table(Users).
		Set(stmt.Set(UserEmail, "new@example.com")).
		Where(expr.Eq(UserID, "u1")).
		Build())

	// Output:
	// UPDATE "app_user" AS "u" SET "email" = $1 WHERE ("u"."id" = $2)
	// [new@example.com u1]
}

// ExampleDelete shows a delete.
func ExampleDelete() {
	show(stmt.NewDelete(postgres.Dialect{}).
		From(Users).
		Where(expr.Eq(UserID, "u1")).
		Build())

	// Output:
	// DELETE FROM "app_user" AS "u" WHERE ("u"."id" = $1)
	// [u1]
}
