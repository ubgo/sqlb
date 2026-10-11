# workspace

The shape of **a library that owns tables and imports nothing**. Of the four packages in this story it is the one worth copying furthest.

It owns `ws_member`, it refers to a user by id, and it has no import of [`authlite`](../authlite) or of any other identity library. It cannot: nothing in it names one.

## Taking the column as an argument

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

That signature is the entire design. The join condition is written by the package that owns the table, so it cannot be got wrong by a caller; the *other side* of it is supplied by whoever is doing the joining. Any identity source works — this one, a different auth library, or a `users` table an application wrote by hand.

And it is still type-checked. `MemberJoin` takes an `sqlb.Expr[string]`, so a caller passing a `time.Time` column, or an `Expr[int64]` from a schema whose ids are integers, does not compile. Two packages that share no type and no import, and the boundary between them is checked by the compiler.

## The rest

`Role` is a named string type over a closed set, so a role cannot be an arbitrary string variable at a call site. `MemberBindings` binds through `COALESCE` because the join is `LEFT`, and `Membership.Exists()` is what tells a non-member from a member with empty values — the same pattern, and the same reason, as in [`phone`](../phone).

`workspaceID` is scoped per call rather than per package: the same user in two workspaces is two different memberships, which `TestTask_TheSameUserInADifferentWorkspace` in [`contrib/driver-sqlite`](../../../contrib/driver-sqlite) executes against a real database.
