package pg

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// Roles, and the statements that name one.
//
// A role was the last thing in Postgres's DDL this package wrote as nothing at
// all: a policy could only name PUBLIC, CURRENT_USER or SESSION_USER, and
// every OWNER TO form was recorded as the operator's on the grounds that a
// library cannot know a deployment's role names. It cannot, but its caller
// can, and the caller is the one writing the migration; what a library must
// not do is invent a role name of its own. So a role is a value here like
// every other named object, declared once by whoever knows the name and passed
// to the statements that take one, and the three keyword roles keep working
// where they already did because both spellings satisfy RoleRef.
//
// This package creates no role and grants no privilege: CREATE ROLE and GRANT
// stay the operator's, because they change who may do what to objects this
// module does not own.

const (
	kwOwnerTo = "OWNER TO"
)

// RoleRef is a role a statement can name: a Role the deployment declared, or
// one of the role-independent keywords a library can write without knowing any
// of a deployment's names. The set is closed to those two, so a statement
// cannot be handed a bare string that is interpolated into SQL.
type RoleRef interface {
	// roleSQL writes the role, failing the build on a value outside the set.
	roleSQL(a *sqlb.Args) string
}

// Role is a Postgres role, declared once as a value and named by every
// statement that gives it something to own or a policy to be subject to. The
// name is written as an identifier by the dialect, so a role name holding a
// quote stays one identifier; it is never a parameter, because no engine takes
// a placeholder where a role name goes.
type Role struct{ Name string }

// roleSQL writes the role's quoted name, failing the build on an empty one.
func (r Role) roleSQL(a *sqlb.Args) string {
	if r.Name == "" {
		a.Fail(ErrEmptyName)
		return ""
	}
	return a.Dialect().QuoteIdent(r.Name)
}

// roleSQL writes a keyword role, failing the build on a value outside the
// three constants.
func (r PolicyRole) roleSQL(a *sqlb.Args) string {
	kw, ok := r.keyword()
	return keywordOf(a, kw, ok, "Role")
}

// Ownable is a declared object OWNER TO can move to another role. The kind
// comes from the Go type, so the statement cannot say VIEW of a sequence, and
// the set is closed to the objects whose ALTER command this package builds: a
// table's owner is the operator's, since ALTER TABLE OWNER TO is one action
// among the many an operator writes on a table nobody declared here.
type Ownable interface {
	// commentTarget is the object's kind and qualified name, the same pair
	// COMMENT ON writes; Ownable does not embed Commentable for the reason
	// Movable does not, that an embedded name is claimed by this file in the
	// generated reference.
	commentTarget(d sqlb.Dialect) (kind, qualified, bare string, err error)
	ownable()
}

func (View) ownable()             {}
func (MaterializedView) ownable() {}
func (Sequence[T]) ownable()      {}
func (Enum[T]) ownable()          {}
func (Composite[T]) ownable()     {}
func (Domain[T]) ownable()        {}
func (RangeType[T]) ownable()     {}
func (BaseType[T]) ownable()      {}

// SetOwnerStatement is the OWNER TO statement of one declared object, whose
// ALTER keyword names the object kind.
type SetOwnerStatement struct {
	o  Ownable
	to RoleRef
}

// SetOwner gives a declared object to another role, the migration a library
// writes when the role that ran its install is not the role that will run it
// from then on. Ownership decides who may drop or alter the object and whose
// privileges a SECURITY DEFINER function runs with, which is why it is a
// migration rather than a grant.
func SetOwner(o Ownable, to RoleRef) *SetOwnerStatement {
	return &SetOwnerStatement{o: o, to: to}
}

// Build renders the statement.
func (x *SetOwnerStatement) Build() (ddl.Script, error) {
	if x.o == nil || x.to == nil {
		return nil, ddl.ErrZeroValue
	}
	kind, name, bare, err := x.o.commentTarget(Dialect{})
	if err != nil {
		return nil, err
	}
	return buildOne(bare, true, func(a *sqlb.Args) string {
		return kwAlterObject + " " + kind + " " + name + " " + kwOwnerTo + " " + x.to.roleSQL(a)
	})
}
