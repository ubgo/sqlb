# `bind` — results through caller pointers

```go
import "github.com/ubgo/sqlb/bind"
```

One of two result strategies, and the shorter one to write. `Bind(col, &dst)` pairs a column with a destination; the column list and the scan targets come from the same slice, in the same order, so nothing has to be kept in step by hand.

That property is the reason the library exists in this shape. The other strategy is [`scan`](../scan/README.md), which returns values instead. Neither is deprecated.

## Contents

- [Setup](#setup)
- [Binding a column](#binding-a-column)
- [Composing across packages](#composing-across-packages)
- [Not safe for concurrent use](#not-safe-for-concurrent-use)
- [Why Binding is an interface](#why-binding-is-an-interface)

## Binding a column

```go
var email string
var age int

q, err := stmt.NewSelect(postgres.Dialect{}).
	Columns(bind.Bind(UserEmail, &email), bind.Bind(UserAge, &age)).
	From(Users).
	Where(expr.Eq(UserID, "u1")).
	Build()
```

```
SELECT "u"."email", "u"."age" FROM "app_user" AS "u" WHERE ("u"."id" = $1)
2 targets, in select-list order
```

The compiler rejects a destination whose type is not `*T`: `bind.Bind(UserEmail, &age)` does not compile, because the column is an `Expr[string]` and `age` is an `int`.

Then nothing is passed for the destination at execution time:

```go
err = exec.One(ctx, db, q)
```

Inserting a column in the middle cannot silently shift a scan, because there is no second list to keep in step.

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

// The application declares its own, and never imports the library's schema.
var (
	Invoices  = expr.Table{Name: "invoice", Alias: "i"}
	InvUserID = expr.Col[string](Invoices, "user_id")
	InvTotal  = expr.Col[int](Invoices, "total")
)

// Supplied by the caller in the snippets below.
var (
	ctx = context.Background()
	db  *sql.DB              // any *sql.DB, *sql.Tx, or other exec.DBTX
	d   = postgres.Dialect{} // the engine being rendered for
)

// The destinations the snippets bind into.
var (
	libEmail string
	appTotal int
)
```

## Composing across packages

This is the case the library exists for. Two packages that do not import each other each contribute a group, and one query scans into both:

```go
fromTheLibrary := []stmt.Binding{bind.Bind(UserEmail, &libEmail)}
fromTheApp     := []stmt.Binding{bind.Bind(InvTotal, &appTotal)}

stmt.NewSelect(postgres.Dialect{}).
	Columns(bind.Bindings(fromTheLibrary, fromTheApp)...).
	From(Users).
	Join(/* ... */).
	Build()
```

```
SELECT "u"."email", "i"."total" FROM "app_user" AS "u" INNER JOIN "invoice" AS "i" ON ("u"."id" = "i"."user_id")
```

A library exports a function returning its own `[]stmt.Binding` for a struct it owns; the application concatenates that with its own and never learns the library's column layout.

## Not safe for concurrent use

The pointer is captured, not the value, and it lives as long as the query does. **A query built this way is NOT SAFE FOR CONCURRENT USE.** Two goroutines executing it write through the same pointers at once, which is a DATA RACE that reads like inert data: no panic, no error, just wrong values.

`One` and `Each` refuse an overlapping execution with `ErrQueryInUse`, but that is detection, not prevention — a struct-literal `Query` has no guard. Build the query where its destination lives, per call. Reusing one sequentially is fine.

Two more consequences worth knowing before they bite:

- `Each` writes **every** row through the same pointer, so a callback keeping a row must copy it.
- A query held in a struct field, cached, or shared between goroutines is the case this strategy cannot serve. Use [`scan`](../scan/README.md).

## Why Binding is an interface

`Binding` is defined by [`stmt`](../stmt/README.md), not here:

```go
type Binding interface {
	SQL(a *Args) string
	Target() any
	ColumnName() string
}
```

The statement layer has to put *something* in `Query.Targets`, but it must not know that the something is a pointer into a caller's variable. The moment it does, the hazard belongs to that layer, and a second result strategy has to fight a statement that already took an opinion.

So `stmt` sees an opaque `Target() any` it cannot dereference, retain, or reason about. That the target is a pointer is this package's decision, made one layer up, which is also why the concurrency warning lives here: this is the layer that captured the memory.
