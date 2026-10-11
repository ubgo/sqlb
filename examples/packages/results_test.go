package packages_test

import (
	"context"
	"fmt"

	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// ExampleBind shows the shorter result strategy. The column list and the scan
// targets come from one slice, in one order, so they cannot drift.
func ExampleBind() {
	var email string
	var age int

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Columns(bind.Bind(UserEmail, &email), bind.Bind(UserAge, &age)).
		From(Users).
		Where(expr.Eq(UserID, "u1")).
		Build()
	if err != nil {
		panic(err)
	}

	fmt.Println(q.SQL)
	fmt.Println(len(q.Targets), "targets, in select-list order")

	// Nothing is passed to One for the destination; the query carries it:
	//	err = exec.One(ctx, db, q)

	// Output:
	// SELECT "u"."email", "u"."age" FROM "app_user" AS "u" WHERE ("u"."id" = $1)
	// 2 targets, in select-list order
}

// ExampleBindings shows why this library exists. Two packages that do not
// import each other each contribute a group, and one query scans into both.
func ExampleBindings() {
	var libEmail string
	var appTotal int

	fromTheLibrary := []stmt.Binding{bind.Bind(UserEmail, &libEmail)}
	fromTheApp := []stmt.Binding{bind.Bind(InvTotal, &appTotal)}

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Columns(bind.Bindings(fromTheLibrary, fromTheApp)...).
		From(Users).
		Join(stmt.Join{
			Type: stmt.JoinInner, Table: Invoices, Source: Invoices,
			On: []expr.Predicate{expr.EqExpr(UserID, InvUserID)},
		}).
		Build()
	if err != nil {
		panic(err)
	}
	fmt.Println(q.SQL)

	// Output:
	// SELECT "u"."email", "i"."total" FROM "app_user" AS "u" INNER JOIN "invoice" AS "i" ON ("u"."id" = "i"."user_id")
}

type person struct {
	Email string
	Age   int
}

// personReader is composed pairwise. Map2 is the only combinator, so a reader
// of any width is Map2 applied repeatedly.
var personReader = scan.Map2(
	scan.Field(UserEmail),
	scan.Field(UserAge),
	func(e string, a int) person { return person{Email: e, Age: a} },
)

// ExampleReader shows the other result strategy. A Reader captures no memory:
// it allocates destinations per row and returns values, so one Reader is safe
// to share across any number of concurrent executions.
func ExampleReader() {
	a := postgres.Dialect{}
	sel := stmt.NewSelect(a).From(Users).Where(expr.Greater(UserAge, 30))

	// All builds the statement with the reader's columns and returns values:
	//	people, err := scan.All(ctx, db, sel, personReader)
	_ = context.Background()

	q, err := sel.Exprs(personReader.Columns()...).Build()
	if err != nil {
		panic(err)
	}
	fmt.Println(q.SQL)
	fmt.Println(len(q.Targets), "targets: a Reader holds no caller memory")

	// Output:
	// SELECT "u"."email", "u"."age" FROM "app_user" AS "u" WHERE ("u"."age" > $1)
	// 0 targets: a Reader holds no caller memory
}

// ExampleMap2 shows composition reaching three columns, which is Map2 applied
// twice rather than a Map3 that does not exist.
func ExampleMap2() {
	type wide struct {
		person
		ID string
	}
	r := scan.Map2(personReader, scan.Field(UserID),
		func(p person, id string) wide { return wide{p, id} })

	a := postgres.Dialect{}
	q, err := stmt.NewSelect(a).Exprs(r.Columns()...).From(Users).Build()
	if err != nil {
		panic(err)
	}
	fmt.Println(q.SQL)

	// Output:
	// SELECT "u"."email", "u"."age", "u"."id" FROM "app_user" AS "u"
}
