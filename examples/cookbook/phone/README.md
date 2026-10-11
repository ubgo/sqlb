# phone

The shape of **a plugin that borrows a table it does not own**.

`phone` stores nothing. It reaches into `auth_identity` — a table [`authlite`](../authlite) owns — and contributes a join and a binding to a composite user, so an application gets a phone number alongside the core user's columns without `authlite` knowing this package exists.

## The alias is the whole trick

```go
const TableAliasIdentity = "phi"

var TableIdentity = sqlb.Table{Name: authlite.TableNameIdentity, Alias: TableAliasIdentity}
```

The core uses `i` for `auth_identity`. This plugin must not, or a statement that joins the table twice is ambiguous and the database says so. Declaring its own alias over the same physical table is what lets both appear in one statement — and it is why the table name is a constant the owner exports rather than a string copied here.

A plugin adding a kind adds it to `authlite.IdentifierKind`, so the closed set stays in one place. `IdentityKind` is typed as that named string type, which is what stops an arbitrary string variable being passed as a kind.

## LEFT, and what that costs

`UserJoin` is a `LEFT` join. `INNER` would silently drop every user who has no phone from a query that was supposed to load one user — a filter nobody wrote.

The price of `LEFT` is that a miss and an empty value become indistinguishable: the bindings `COALESCE` a missing row to `''` so the scan cannot fail on a `NULL`, and afterwards a zero `Data` could mean either. `Exists` is what carries the difference:

```go
// Exists reports whether the LEFT join found a row. A miss scans the
// COALESCE fallback, so a zero Data is indistinguishable from an empty
// number without this.
func (d *Data) Exists() bool { return d.Number != "" }
```

That distinction is executed, not assumed: `TestTask_CompositeDistinguishesAMissFromAnEmptyValue` in [`contrib/driver-sqlite`](../../../contrib/driver-sqlite) loads a user with no phone row from a real database and asserts the miss is reported as a miss.
