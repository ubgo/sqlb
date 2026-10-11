// Package workspace is the second-library shape: it owns tables that refer to
// a user by id, and it does not import the auth library. The caller supplies
// the user id column, so any identity source works.
package workspace

import "github.com/ubgo/sqlb"

// The table and column names this plugin owns. Exported so another package
// can build its own alias over the same table without copying strings.
const (
	TableNameMember  = "ws_member"
	TableAliasMember = "wm"

	ColWorkspaceID = "workspace_id"
	ColUserID      = "user_id"
	ColRole        = "role"
)

// Role is a named string type, so a role cannot be an arbitrary string
// variable at a call site.
type Role string

// The roles a membership may hold.
const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// TableMember is the membership table under this plugin's alias.
var TableMember = sqlb.Table{Name: TableNameMember, Alias: TableAliasMember}

// The typed columns of TableMember. These are the plugin's schema contract:
// another package can join on them and scan them, and can do nothing else.
var (
	MemberWorkspaceID = sqlb.Col[string](TableMember, ColWorkspaceID)
	MemberUserID      = sqlb.Col[string](TableMember, ColUserID)
	MemberRole        = sqlb.Col[Role](TableMember, ColRole)
)

// Membership is the user's row in one workspace.
type Membership struct {
	WorkspaceID string
	Role        Role
}

// Exists reports whether the LEFT join found a row.
func (m *Membership) Exists() bool { return m.WorkspaceID != "" }

// MemberJoin joins one workspace's membership to any string user id column.
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

// MemberBindings binds the membership columns to dst.
func MemberBindings(dst *Membership) []sqlb.Binding {
	return []sqlb.Binding{
		sqlb.Bind(sqlb.Coalesce(MemberWorkspaceID, sqlb.Literal[string]("''")), &dst.WorkspaceID),
		sqlb.Bind(sqlb.Coalesce(MemberRole, sqlb.Literal[Role]("''")), &dst.Role),
	}
}
