// Package app is the application side of the cross-package story: the code
// that owns no library, imports three of them, and consolidates what they
// each own into one statement.
//
// The three it composes are deliberately three different shapes, because a
// real application meets all three:
//
//   - authlite owns tables and exports typed columns for them.
//   - phone borrows one of authlite's tables under its own alias, so it can
//     join to a row authlite already knows about without authlite knowing
//     the plugin exists.
//   - workspace owns its own tables and imports nothing. It takes the user
//     id column as an argument, so any identity source works.
//
// None of them knows about app_profile or app_subscription, which this
// package owns, and none of them can be made to. That is the boundary sqlb
// exists for: the application supplies the joins and the scan targets, and
// the compiler checks every one of them.
package app

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/examples/cookbook/authlite"
	"github.com/ubgo/sqlb/examples/cookbook/phone"
	"github.com/ubgo/sqlb/examples/cookbook/workspace"
)

// The application's own table and column names. Constants for the same
// reason the libraries use them: the migration and the column declarations
// spell the name once.
const (
	TableNameProfile  = "app_profile"
	TableAliasProfile = "p"

	ColProfileUserID  = "user_id"
	ColProfileCompany = "company"

	TableNameSubscription  = "app_subscription"
	TableAliasSubscription = "s"

	ColSubUserID = "user_id"
	ColSubStatus = "status"
	ColSubPlan   = "plan"
)

// TableProfile is an application table. No library imports this package, so
// no library can join to it; the application does the joining.
var TableProfile = sqlb.Table{Name: TableNameProfile, Alias: TableAliasProfile}

// The typed columns of app_profile.
var (
	ProfileUserID  = sqlb.Col[string](TableProfile, ColProfileUserID)
	ProfileCompany = sqlb.Col[string](TableProfile, ColProfileCompany)
)

// TableSubscription is the second application table, used to extend a
// library's own query rather than to build one.
var TableSubscription = sqlb.Table{Name: TableNameSubscription, Alias: TableAliasSubscription}

// SubStatus is a named string type, so the closed set of states cannot be
// widened by passing an arbitrary string at a call site.
type SubStatus string

// The subscription states this application knows.
const (
	SubActive   SubStatus = "active"
	SubCanceled SubStatus = "canceled"
)

// The typed columns of app_subscription.
var (
	SubUserID = sqlb.Col[string](TableSubscription, ColSubUserID)
	SubState  = sqlb.Col[SubStatus](TableSubscription, ColSubStatus)
	SubPlan   = sqlb.Col[string](TableSubscription, ColSubPlan)
)

// User is the composite the application actually wants: the auth library's
// own row, plus everything reached across a package boundary.
//
// The shape is the convention worth copying. Columns that live ON auth_user
// are embedded and flat, because they are the same row; anything reached by
// a join is a nested struct, because it may not be there at all and the
// nested type is what carries the Exists method that says so.
type User struct {
	authlite.User

	Phone      phone.Data
	Membership workspace.Membership
	Company    string

	// workspaceID scopes the membership join. It is unexported and set by
	// NewUser because a zero value here would silently join to the empty
	// workspace and find nothing, which reads exactly like "not a member".
	workspaceID string
}

// NewUser returns a composite destination scoped to one workspace.
func NewUser(workspaceID string) *User { return &User{workspaceID: workspaceID} }

// WorkspaceID reports the workspace the membership join is scoped to.
func (u *User) WorkspaceID() string { return u.workspaceID }

// Joins satisfies authlite.Composite. The application decides the shape of
// the statement the library builds: which tables are reached, in what order,
// and by which join type.
//
// Every one is LEFT. An INNER join here would silently drop every user
// without a phone, without a membership, or without a profile row, turning a
// load-one-user query into a filter nobody wrote.
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

// Bindings satisfies authlite.Composite: every column the statement selects,
// in order, each bound to the field it scans into.
//
// This is where the boundary pays off. Four packages contribute bindings to
// one slice, the select list and the scan targets come from that one slice,
// and the compiler has checked every column against the field it writes to.
// Inserting a column in the middle cannot shift a scan, because there is no
// second list to keep in step.
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

// SubscriptionJoin joins the application's subscription table to any string
// user id column. It is what the application passes to a library query
// through sqlb.WithJoin, which is the other direction of the boundary: the
// library's query, extended by the application, with the library's own
// predicates still in place.
func SubscriptionJoin(userID sqlb.Expr[string]) sqlb.Join {
	return sqlb.Join{
		Type:  sqlb.JoinInner,
		Table: TableSubscription,
		On:    []sqlb.Predicate{sqlb.EqExpr(SubUserID, userID)},
	}
}
