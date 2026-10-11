package debug_test

import (
	"fmt"

	"github.com/ubgo/sqlb/contrib/debug"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

func ExampleInterpolate() {
	users := expr.Table{Name: "users", Alias: "u"}
	id := expr.Col[int64](users, "id")
	name := expr.Col[string](users, "name")

	q, _ := stmt.NewSelect(postgres.Dialect{}).
		Exprs(id, name).
		From(users).
		Where(expr.Eq(name, "O'Brien"), expr.Greater(id, 10)).
		Limit(5).
		Build()

	// What you have without this package: the shape, and the values beside it.
	fmt.Println("SQL :", q.SQL)
	fmt.Println("Args:", q.Args)

	// What you have with it: the statement as it will behave.
	fmt.Println("Read:", debug.Interpolate(postgres.Dialect{}, q))

	// Output:
	// SQL : SELECT "u"."id", "u"."name" FROM "users" AS "u" WHERE ("u"."name" = $1 AND "u"."id" > $2) LIMIT $3
	// Args: [O'Brien 10 5]
	// Read: SELECT "u"."id", "u"."name" FROM "users" AS "u" WHERE ("u"."name" = 'O''Brien' AND "u"."id" > 10) LIMIT 5
}
