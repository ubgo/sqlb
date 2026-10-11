# authlite

The shape of **a library that owns tables**. A deliberately small stand-in for a real one, small enough to read in a sitting and complete enough that every extension mechanism is visible.

It owns `auth_user` and `auth_identity`, renders SQL for whatever dialect it is given, and executes only through the `DBTX` the caller hands it — so the recipes run without a driver.

## What a library exports

Four things, and they are the whole public contract:

- **Tables and typed columns**, as package-level values. `sqlb.Col[string](TableUser, ColID)` is an `Expr[string]`; another package can compare it, join on it and bind it, and can do nothing else with it. Adding a column is a minor version; renaming one is a major version.
- **A bindings function**, `UserBindings(dst *User) []sqlb.Binding`. A function rather than a slice, because each binding captures a pointer into `dst`.
- **Name constants**, so the migration file and the column declarations spell `auth_user` once, and a rename is one edit.
- **Query methods** that accept `sqlb.Option`, so the application can add a join, a filter or a column the library has never heard of.

## The three mechanisms worth reading

**A rule no option can remove.** `NotBanned()` is the single definition of "which users are visible", written into the `WHERE` before `Apply(opts...)` runs. Options only ever add, so there is no option that takes it back out. That is the property a library needs before it can safely accept extensions at all.

**A destination the application declares.** `Composite` is an interface with `Joins()` and `Bindings()`. `LoadUserQuery` takes one and builds a statement over whatever tables the application named — so the library joins to tables it cannot see, and the compiler still checks every condition. [`app`](../app) is the implementation.

**Hooks inside the transaction.** `SignUp` takes the caller's `DBTX` and passes that same `DBTX` to `AfterSignUp`, so the application's row and the library's row commit or roll back together. The caller owns the transaction; the library never begins one.

## What it is not

Not an auth library. There is no password handling, no session, no token, and the one `INSERT` is a raw string because the recipe it serves is about the hook, not about the write. Read [`ubgo/auth`](https://github.com/ubgo/auth) for the real thing; read this for the shape.
