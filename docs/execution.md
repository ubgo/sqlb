# Execution

The core speaks `database/sql` directly, because it exists for libraries that cannot carry an ORM.

## DBTX

```go
type DBTX interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
```

Both `*sql.DB` and `*sql.Tx` satisfy it. Taking the interface rather than a concrete type is what lets a caller run a library's query inside their own transaction, alongside their own writes. That is the one capability an embedded library has that a remote service can never offer, so the interface is deliberately the narrowest thing that provides it.

## One and Each

```go
q, err := sel.Build()
err = sqlb.One(ctx, db, q)          // exactly one row
err = sqlb.Each(ctx, db, q, func() error {
	// the bound targets hold this row
	return nil
})
```

Nothing is passed for the destination, because the query already carries it. This is the whole point of `Bind`: the column list and the scan targets are derived from the same slice, in the same order.

`Each` reuses the same targets for every row, so a callback that wants to keep a row must copy it. That is deliberate: the alternative allocates a struct per row for callers who are about to aggregate and throw them away.

## Concurrency: a Query is not safe for concurrent use

**Executing one `Query` from two goroutines is a data race.** If you arrived here from a race detector report naming a variable you passed to `Bind`, this is why.

It is the one sharp edge in the design, and it is sharp because a `Query` reads like inert data: a string, some values, done. It is not. `Targets` holds pointers into your variables, so two goroutines executing one `Query` write to the same memory and interleave each other's rows. Nothing crashes, and the wrong values appear intermittently under load only. `go test -race` reports it as a data race on the bound variables.

Build the query where its destination lives, per call. That is what every cookbook recipe does, and it costs nothing because building is just assembling a string.

```go
// Wrong: one query, one set of variables, many goroutines.
var shared, _ = userQuery(&id, &email)   // package scope

// Right: the destination and the query are made together, per call.
func handler() {
	var id, email string
	q, err := userQuery(&id, &email)
	...
}
```

`One` and `Each` refuse an overlapping execution with `ErrQueryInUse` rather than corrupting the result. A lock would be worse: it would serialise the executions and hand the second caller the first caller's rows in its own variables, with no sign anything happened.

The check is a best effort, not a guarantee. It catches a shared `Query` and two queries built from one shared builder, which are the realistic mistakes. It does not catch the same destination pointer bound into two separate builders, and a `Query` assembled as a struct literal carries no guard at all. Sequential reuse of one query is fine and stays fine.

## The two errors One returns

`ErrNoRows` aliases `sql.ErrNoRows`, so an existing `errors.Is` check keeps working and nobody has to learn a second spelling of the same condition.

`ErrMultipleRows` is its own error, and it exists because taking the first row would be worse. Every `One` caller is resolving a single thing, so a second row means a uniqueness index is missing or a join fans out. Silently picking one of them is the worst available failure mode for an identity lookup.

## Transactions

There is no transaction helper, and there will not be one. `DBTX` already accepts a `*sql.Tx`, so the application opens the transaction, decides its isolation level and decides when to commit. A library that opened its own would be deciding those on the application's behalf.

## Drivers

The core has no driver and no opinion about one. Driver integrations are separate modules under `contrib/`, so importing `sqlb` never puts a driver, cgo or a version conflict into an application.
