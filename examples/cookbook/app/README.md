# app

The shape of **the application**: the package that owns no library, imports three of them, and consolidates what they each own into one statement.

It is nobody's dependency. Nothing imports it, which is what makes it the right place — the only place — for knowledge of the whole picture to live.

## What it knows that no library does

`app` owns `app_profile` and `app_subscription`, and it knows that [`authlite`](../authlite)'s user, [`phone`](../phone)'s number, [`workspace`](../workspace)'s membership and its own company column all describe the same person. None of the three can know that, and none of them has to.

```go
type User struct {
	authlite.User

	Phone      phone.Data
	Membership workspace.Membership
	Company    string

	workspaceID string
}
```

The layout is the convention worth copying. Columns that live **on** the driving table are embedded and flat, because they are the same row. Anything reached by a join is a **nested struct**, because it may not be there at all, and the nested type is what carries the `Exists` method that says so.

## Two methods, one slice

`Joins()` and `Bindings()` satisfy `authlite.Composite`, so the library builds the statement and the application decides its shape.

`Bindings()` is where the boundary pays off. Four packages contribute to one slice via `sqlb.Bindings`, and because the select list and the scan targets are *that same slice*, they cannot drift apart — there is no second list to keep in step, so inserting a column in the middle cannot silently shift a scan.

Two things are load-bearing and easy to get wrong:

- **Every join is `LEFT`.** An `INNER` join in a load-one-user query is a filter nobody wrote. Changing one to `INNER` makes a user with no profile row vanish, and the executed test fails with `sql: no rows in result set` — which is how that claim was verified rather than asserted.
- **`workspaceID` is unexported and set by `NewUser`.** A zero value there would join to the empty workspace and find nothing, which reads exactly like "not a member".

## The other direction

`SubscriptionJoin` is the reverse case, and the more common one: the library owns the query, and the application wants one more join, filter and column added to it through `sqlb.WithJoin`, `sqlb.WithWhere` and `sqlb.WithColumns`. The library's own `NotBanned` predicate is written before `Apply` and no option can remove it, because there is no option that subtracts.

## Where it is proven

Rendered in [`../example_test.go`](../example_test.go), recipes 1 and 2. Executed against a real database in [`contrib/driver-sqlite/crosspackage_test.go`](../../../contrib/driver-sqlite/crosspackage_test.go). The walkthrough is [`docs/cross-package.md`](../../../docs/cross-package.md).
