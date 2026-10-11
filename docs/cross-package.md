# Across a package boundary

This is the problem `sqlb` was built for, and the one page that shows all of it at once.

A library that ships its own tables cannot serve the application that embeds it. The application will want the library's rows joined to tables the library has never heard of, and it will want that in one statement rather than three round trips stitched together in Go. The library cannot write that join, because it does not know the application's schema. The application cannot write it either, unless the library hands over something to join *on*.

What it hands over is the choice. A library can export **SQL strings**, which gives the application no type safety and the library no control over what is done with them; it can export a **query API** for every join it can imagine, which is a combinatorial explosion it will never finish; or it can export **typed columns** and let the application compose them. `sqlb` exists to make the third one cheap.

## Contents

- [The four packages](#the-four-packages)
- [The three ownership shapes](#the-three-ownership-shapes)
- [The composite](#the-composite)
- [What one statement looks like](#what-one-statement-looks-like)
- [The other direction: extending a library's own query](#the-other-direction-extending-a-librarys-own-query)
- [What is proven, and where](#what-is-proven-and-where)

## The four packages

The worked example lives under [`examples/cookbook/`](../examples/cookbook) and is four directories, which is the point: four owners, one statement, and no import cycle anywhere.

| Package | Owns | Imports |
|---|---|---|
| `authlite` | `auth_user`, `auth_identity` | `sqlb` |
| `phone` | nothing — it borrows `auth_identity` under its own alias | `sqlb`, `authlite` |
| `workspace` | `ws_member` | `sqlb` |
| `app` | `app_profile`, `app_subscription` | `sqlb`, and all three above |

`workspace` never learns that `authlite` exists. `authlite` never learns that any of the other three exist. Only `app` — the application, the package that is nobody's dependency — knows the whole picture, which is exactly the right place for that knowledge to live.

## The three ownership shapes

**A library that owns tables** exports the tables, the typed columns, and a bindings function. That is the whole public contract; adding a column is a minor version, renaming one is a major version.

```go
var TableUser = sqlb.Table{Name: TableNameUser, Alias: TableAliasUser}

var (
	UserID        = sqlb.Col[string](TableUser, ColID)
	UserEmail     = sqlb.Col[string](TableUser, ColEmail)
	UserBannedAt  = sqlb.Col[*time.Time](TableUser, ColBannedAt)
	UserCreatedAt = sqlb.Col[time.Time](TableUser, ColCreatedAt)
)

// A function, not a slice, because each binding holds a pointer into dst.
func UserBindings(dst *User) []sqlb.Binding {
	return []sqlb.Binding{
		sqlb.Bind(UserID, &dst.ID),
		sqlb.Bind(UserEmail, &dst.Email),
		sqlb.Bind(UserBannedAt, &dst.BannedAt),
		sqlb.Bind(UserCreatedAt, &dst.CreatedAt),
	}
}
```

**A plugin that borrows a table** declares its own alias over a table someone else owns, so it can join to that table without the owner knowing it exists. The alias is the whole trick, and it must differ from the owner's: `authlite` uses `i` for `auth_identity`, so `phone` uses `phi`, and a statement can join the same physical table twice without ambiguity.

```go
const TableAliasIdentity = "phi"

var TableIdentity = sqlb.Table{Name: authlite.TableNameIdentity, Alias: TableAliasIdentity}

func UserJoin() sqlb.Join {
	return sqlb.Join{
		Type:  sqlb.JoinLeft,
		Table: TableIdentity,
		On: []sqlb.Predicate{
			sqlb.EqExpr(IdentityUserID, authlite.UserID),
			sqlb.Eq(IdentityKind, authlite.KindPhone),
		},
	}
}
```

**A library that owns tables and imports nothing** is the shape worth copying furthest. `workspace` refers to a user by id, and it takes the id column as an argument rather than importing an identity library, so it composes with any identity source at all:

```go
// This package has no import of the auth library; the application passes
// auth's UserID here.
func MemberJoin(userID sqlb.Expr[string], workspaceID string) sqlb.Join {
	return sqlb.Join{
		Type:  sqlb.JoinLeft,
		Table: TableMember,
		On: []sqlb.Predicate{
			sqlb.EqExpr(MemberUserID, userID),
			sqlb.Eq(MemberWorkspaceID, workspaceID),
		},
	}
}
```

`MemberJoin` takes an `sqlb.Expr[string]`, so a caller passing a `time.Time` column, or an `Expr[int64]` from a schema whose ids are integers, does not compile. That is the boundary being type-checked with no shared type and no shared import.

## The composite

The application declares one struct for what it wants back, and two methods that say how to get it. Columns that live on the driving table are embedded and flat, because they are the same row; anything reached by a join is a nested struct, because it may not be there at all.

```go
type User struct {
	authlite.User

	Phone      phone.Data
	Membership workspace.Membership
	Company    string

	workspaceID string
}

func (u *User) Joins() []sqlb.Join {
	return []sqlb.Join{
		phone.UserJoin(),
		workspace.MemberJoin(authlite.UserID, u.workspaceID),
		{
			Type:  sqlb.JoinLeft,
			Table: TableProfile,
			On:    []sqlb.Predicate{sqlb.EqExpr(ProfileUserID, authlite.UserID)},
		},
	}
}

func (u *User) Bindings() []sqlb.Binding {
	return sqlb.Bindings(
		authlite.UserBindings(&u.User),
		phone.UserBindings(&u.Phone),
		workspace.MemberBindings(&u.Membership),
		[]sqlb.Binding{
			sqlb.Bind(sqlb.Coalesce(ProfileCompany, sqlb.Literal[string]("''")), &u.Company),
		},
	)
}
```

`sqlb.Bindings` concatenates the groups in order. Four packages contribute to one slice, and because the select list and the scan targets are *that same slice*, they cannot drift apart: there is no second list to keep in step, so inserting a column in the middle cannot silently shift a scan.

Two details are load-bearing and easy to get wrong:

- **Every join is LEFT.** An `INNER` join in a load-one-user query is a filter nobody wrote: a user with no phone, or no membership, or no profile row, disappears from a query that was supposed to return them. The executed test catches this — switching one join to `INNER` makes the bare user vanish and the test fails with `sql: no rows in result set`.
- **A miss and an empty value are not the same thing.** The bindings `COALESCE` a missing row to `''` so the scan cannot fail on a `NULL`, which means the scanned struct alone can no longer tell the two apart. That is what `Exists` is for, and why each nested type has one:

```go
// Exists reports whether the LEFT join found a row. A miss scans the
// COALESCE fallback, so a zero Data is indistinguishable from an empty
// number without this.
func (d *Data) Exists() bool { return d.Number != "" }
```

## What one statement looks like

The library builds it, from the joins and bindings the application supplied:

```go
func (a *Auth) LoadUserQuery(userID string, dst Composite) (sqlb.Query, error) {
	return a.Select().
		Columns(dst.Bindings()...).
		From(TableUser).
		Join(dst.Joins()...).
		Where(sqlb.Eq(UserID, userID)).
		Limit(singleRow).
		Build()
}
```

Four tables, three owners, one round trip:

```sql
SELECT "u"."id", "u"."primary_email", "u"."banned_at", "u"."created_at",
       COALESCE("phi"."value", ''), COALESCE("wm"."workspace_id", ''),
       COALESCE("wm"."role", ''), COALESCE("p"."company", '')
FROM "auth_user" AS "u"
  LEFT JOIN "auth_identity" AS "phi" ON ("phi"."user_id" = "u"."id" AND "phi"."kind" = $1)
  LEFT JOIN "ws_member" AS "wm" ON ("wm"."user_id" = "u"."id" AND "wm"."workspace_id" = $2)
  LEFT JOIN "app_profile" AS "p" ON ("p"."user_id" = "u"."id")
WHERE ("u"."id" = $3) LIMIT $4
```

`auth_identity` appears once, under the plugin's alias. The core's own alias for that table is untouched and available, so a statement that wants both the plugin's phone row and the core's identity rows gets them without either side coordinating with the other.

## The other direction: extending a library's own query

The composite is the application supplying a shape to the library. The reverse case is more common and matters just as much: the library owns the query, and the application wants one more join, one more filter and one more column, without the library's own rules being lost.

`sqlb.Option` is that door. The library applies the options *after* its own predicates, and options only ever add:

```go
func (a *Auth) AdminUserListQuery(limit, offset int, dst *User, opts ...sqlb.Option) (sqlb.Query, error) {
	return a.Select().
		Columns(UserBindings(dst)...).
		From(TableUser).
		Where(NotBanned()).
		Order(sqlb.OrderBy(UserCreatedAt, sqlb.SortDesc)).
		Limit(limit).
		Offset(offset).
		Apply(opts...).
		Build()
}
```

```go
show(a.AdminUserListQuery(50, 0, &row.User,
	sqlb.WithJoin(app.SubscriptionJoin(authlite.UserID)),
	sqlb.WithWhere(sqlb.Eq(app.SubState, app.SubActive)),
	sqlb.WithColumns(sqlb.Bind(app.SubPlan, &row.Plan)),
))
```

`NotBanned` is written before `Apply` and no option can remove it, because there is no option that subtracts. The library keeps the rule it must keep; the application gets the query it wanted.

## What is proven, and where

Rendering and running are different claims, so this story is pinned in two places.

| Where | What it proves |
|---|---|
| `examples/cookbook/example_test.go` | The exact SQL. Each recipe is an `Example` whose `// Output:` block is the rendered statement, so a change in rendering fails the build. |
| `contrib/driver-sqlite/crosspackage_test.go` | That SQLite accepts it, that the joins do not fan out, and that the bindings land in the right fields — including a LEFT-join miss reported as a miss rather than an empty value. |

The second is the one that cannot be faked. `TestTask_LoadOneUserAcrossFourPackages` loads a fully-populated user and asserts every field; `TestTask_CompositeDistinguishesAMissFromAnEmptyValue` loads a user with no phone, no membership and no profile and asserts that each nested type reports the miss; `TestTask_TheSameUserInADifferentWorkspace` loads one user in two workspaces and asserts the role differs, which is what says the membership join is scoped by the value the application supplied.

Run them:

```sh
go test ./examples/cookbook -run Example_loadComposite -v
go test ./contrib/driver-sqlite -run TestTask_ -v
```
