package packages_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/exec"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/stmt"
)

// ExampleDBTX shows the interface the execution layer takes, and why it is the
// narrowest thing that works. Both *sql.DB and *sql.Tx satisfy it, so a
// library's query runs inside the caller's transaction alongside their own
// writes. That is the one capability an embedded library has and a remote
// service cannot offer.
func ExampleDBTX() {
	var db *sql.DB
	var tx *sql.Tx

	var _ exec.DBTX = db
	var _ exec.DBTX = tx

	fmt.Println("both *sql.DB and *sql.Tx are a DBTX")

	// Output:
	// both *sql.DB and *sql.Tx are a DBTX
}

// ExampleOne shows that nothing is passed for the destination: the query
// already carries it.
func ExampleOne() {
	var email string

	q, err := stmt.NewSelect(postgres.Dialect{}).
		Columns(bind.Bind(UserEmail, &email)).
		From(Users).
		Where(expr.Eq(UserID, "u1")).
		Build()
	if err != nil {
		panic(err)
	}

	// With a real handle this is the whole call:
	//	err = exec.One(ctx, db, q)
	// and email now holds the value, because the query carries the pointer.
	fmt.Println(len(q.Targets), "target the query carries")

	// Errors worth knowing:
	fmt.Println(errors.Is(exec.ErrNoRows, sql.ErrNoRows), "ErrNoRows aliases sql.ErrNoRows")
	fmt.Println(exec.ErrMultipleRows)
	_ = context.Background()

	// Output:
	// 1 target the query carries
	// true ErrNoRows aliases sql.ErrNoRows
	// sqlb: query matched multiple rows but expected one
}

// ExampleQuery_Acquire shows the concurrency guard. A bound query holds
// pointers into caller variables, so two goroutines executing it would
// interleave rows into the same memory. One and Each refuse the overlap.
func ExampleQuery_Acquire() {
	q := stmt.NewQuery("SELECT 1", nil, nil)

	fmt.Println(q.Acquire(), "first execution may start")
	fmt.Println(q.Acquire(), "an overlapping one is refused")
	q.Release()
	fmt.Println(q.Acquire(), "allowed again once the first finished")

	// Output:
	// true first execution may start
	// false an overlapping one is refused
	// true allowed again once the first finished
}
