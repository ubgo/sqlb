# `exec` — running a statement

```go
import "github.com/ubgo/sqlb/exec"
```

Runs a built statement against a database and iterates its rows.

It is a layer because execution has nothing to do with how results come back. Taking a handle, carrying the context, looping rows and telling "no rows" from "more than one row" are the same work whichever result strategy sits above, so doing them once is what keeps a second strategy from re-implementing all of it.

## Contents

- [Setup](#setup)
- [DBTX](#dbtx)
- [One and Each](#one-and-each)
- [Errors](#errors)
- [The concurrency guard](#the-concurrency-guard)

## DBTX

```go
type DBTX interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
```

Two methods, and both `*sql.DB` and `*sql.Tx` satisfy them:

```go
var _ exec.DBTX = db   // *sql.DB
var _ exec.DBTX = tx   // *sql.Tx
```

Taking this rather than a concrete type is what lets a caller run a library's query **inside their own transaction**, alongside their own writes. That is the single capability an embedded library has and a remote identity service can never offer, so the interface is deliberately the narrowest thing that provides it.

Anything else satisfying those two methods works too: a pooled proxy, a retrying wrapper, an instrumented handle. No layer is needed for those.

## Setup

Every snippet on this page uses these declarations.

```go
import (
	"context"
	"database/sql"

	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/expr"
)

// A library declares its table and its typed columns, and exports them.
var (
	Users     = expr.Table{Name: "app_user", Alias: "u"}
	UserID    = expr.Col[string](Users, "id")
	UserEmail = expr.Col[string](Users, "email")
	UserAge   = expr.Col[int](Users, "age")
)

// Supplied by the caller in the snippets below.
var (
	ctx = context.Background()
	db  *sql.DB              // any *sql.DB, *sql.Tx, or other exec.DBTX
	d   = postgres.Dialect{} // the engine being rendered for
)
```

## One and Each

Nothing is passed for the destination, because the query already carries it:

```go
var email string
q, _ := stmt.NewSelect(d).Columns(bind.Bind(UserEmail, &email)).From(Users).Build()

err := exec.One(ctx, db, q)
// email now holds the value
```

`Each` runs the same way and calls your function once per row:

```go
err := exec.Each(ctx, db, q, func() error {
	fmt.Println(email)   // overwritten every row
	return nil
})
```

**The invariant that will bite you if you skip it:** every row scans into the *same* destinations, because the bindings hold fixed pointers. The struct is overwritten each iteration, so a callback keeping a row must copy it. Appending a pointer to the destination yields N identical values, all equal to the last row.

Returning an error from the callback stops iteration and returns that error.

## Errors

| Error | When |
|---|---|
| `ErrNoRows` | `One` matched nothing. Aliases `sql.ErrNoRows`, so an existing `errors.Is` check keeps working. |
| `ErrMultipleRows` | `One` matched more than one row. |
| `ErrNoTargets` | The query bound no destinations, for instance it was built with `Exprs`. |
| `ErrQueryInUse` | The same statement is already executing. |

`ErrMultipleRows` is worth its own error rather than silently taking the first. Every `One` caller is resolving an identity, so a second row means a uniqueness index is missing or a join fans out, and picking an arbitrary row is the worst possible failure mode for a login lookup.

## The concurrency guard

A query built with bindings holds pointers into caller variables, so **it is not safe for concurrent use.** Two goroutines executing one query interleave each other's rows into the same memory, which is a data race that reads like inert data.

`One` and `Each` refuse an overlapping execution:

```go
q.Acquire()   // true  — first execution may start
q.Acquire()   // false — an overlapping one is refused
q.Release()
q.Acquire()   // true  — allowed again
```

This is **detection, not prevention.** A `Query` built as a struct literal has no guard and is always allowed to proceed, which is a documented hole rather than a bug. Build the query where its destination lives, per call; reusing one sequentially is fine.

[`scan`](../scan/README.md) is the strategy without this hazard: it returns values rather than writing through pointers, so one reader is safe to share across any number of concurrent executions.
