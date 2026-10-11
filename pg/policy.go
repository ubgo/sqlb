package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreatePolicy = "CREATE POLICY"
	kwAlterPolicy  = "ALTER POLICY"
	kwDropPolicy   = "DROP POLICY"
	kwAsPermissive = "AS PERMISSIVE"
	kwAsRestrict   = "AS RESTRICTIVE"
	kwToRole       = "TO"
	kwWithCheck    = "WITH CHECK"
)

// Policy is a row-level security policy on one table; its name is unique per
// table, which is why the table is part of the value.
type Policy struct {
	Name  string
	Table sqlb.Table
}

func (p Policy) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjPolicy, d.QuoteIdent(p.Name) + " " + kwOn + " " + p.Table.Qualified(d), p.Name, nil
}

// PolicyCommand is the command a policy applies to.
type PolicyCommand int

// The commands. AllCommands is the default.
const (
	AllCommands PolicyCommand = iota + 1
	SelectCommand
	InsertCommand
	UpdateCommand
	DeleteCommand
)

func (c PolicyCommand) keyword() (string, bool) {
	switch c {
	case AllCommands:
		return "ALL", true
	case SelectCommand:
		return "SELECT", true
	case InsertCommand:
		return "INSERT", true
	case UpdateCommand:
		return "UPDATE", true
	case DeleteCommand:
		return "DELETE", true
	}
	return "", false
}

// PolicyRole is one of the role-independent targets a policy can name, which
// a library can write without knowing any of a deployment's role names. A
// named role is a Role, and both satisfy RoleRef, so To takes either.
type PolicyRole int

// The roles a library can name without knowing the deployment's roles.
const (
	RolePublic PolicyRole = iota + 1
	RoleCurrentUser
	RoleSessionUser
)

func (r PolicyRole) keyword() (string, bool) {
	switch r {
	case RolePublic:
		return "PUBLIC", true
	case RoleCurrentUser:
		return "CURRENT_USER", true
	case RoleSessionUser:
		return "SESSION_USER", true
	}
	return "", false
}

// writePred renders a predicate with columns bare, as a policy's USING and
// WITH CHECK name the table's columns.
func writePred(a *sqlb.Args, p sqlb.Predicate) string {
	return a.Bare(func() string {
		var b strings.Builder
		p.WriteSQL(&b, a)
		return b.String()
	})
}

// CreatePolicyStatement is CREATE POLICY.
type CreatePolicyStatement struct {
	p           Policy
	restrictive bool
	cmd         PolicyCommand
	roles       []RoleRef
	using       sqlb.Predicate
	check       sqlb.Predicate
}

// CreatePolicy creates a policy. Postgres has no IF NOT EXISTS for it; wrap it
// in Guarded. It has no effect until row security is enabled on the table
// with EnableRowLevelSecurity, and none on the table's owner unless forced.
func CreatePolicy(p Policy) *CreatePolicyStatement { return &CreatePolicyStatement{p: p} }

func (x *CreatePolicyStatement) derive() *CreatePolicyStatement {
	c := *x
	c.roles = slices.Clone(x.roles)
	return &c
}

// Permissive makes the policy one of those any of which may grant a row, the
// default.
func (x *CreatePolicyStatement) Permissive() *CreatePolicyStatement {
	c := x.derive()
	c.restrictive = false
	return c
}

// Restrictive makes the policy one every row must also pass.
func (x *CreatePolicyStatement) Restrictive() *CreatePolicyStatement {
	c := x.derive()
	c.restrictive = true
	return c
}

// For limits the policy to one command.
func (x *CreatePolicyStatement) For(cmd PolicyCommand) *CreatePolicyStatement {
	c := x.derive()
	c.cmd = cmd
	return c
}

// To names who the policy applies to, a declared Role or one of the keyword
// roles; PUBLIC when not given.
func (x *CreatePolicyStatement) To(first RoleRef, more ...RoleRef) *CreatePolicyStatement {
	c := x.derive()
	c.roles = append(append(c.roles, first), more...)
	return c
}

// Using is which existing rows the policy shows or lets a command change.
func (x *CreatePolicyStatement) Using(p sqlb.Predicate) *CreatePolicyStatement {
	c := x.derive()
	c.using = p
	return c
}

// WithCheck is which new rows an INSERT or UPDATE may write.
func (x *CreatePolicyStatement) WithCheck(p sqlb.Predicate) *CreatePolicyStatement {
	c := x.derive()
	c.check = p
	return c
}

// Build renders the statement.
func (x *CreatePolicyStatement) Build() (ddl.Script, error) {
	return buildOne(x.p.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwCreatePolicy + " " + d.QuoteIdent(x.p.Name) + " " + kwOn + " " + x.p.Table.Qualified(d)
		if x.restrictive {
			s += " " + kwAsRestrict
		} else {
			s += " " + kwAsPermissive
		}
		if x.cmd != 0 {
			kw, ok := x.cmd.keyword()
			s += " " + kwFor + " " + keywordOf(a, kw, ok, "For")
		}
		if len(x.roles) > 0 {
			rs := make([]string, len(x.roles))
			for i, r := range x.roles {
				rs[i] = r.roleSQL(a)
			}
			s += " " + kwToRole + " " + strings.Join(rs, ", ")
		}
		if x.using != nil {
			s += " " + kwUsing + " (" + writePred(a, x.using) + ")"
		}
		if x.check != nil {
			s += " " + kwWithCheck + " (" + writePred(a, x.check) + ")"
		}
		return s
	})
}

// duplicateCondition is what a repeated CREATE POLICY raises.
func (*CreatePolicyStatement) duplicateCondition() string { return condDuplicateObject }

// AlterPolicyQuery is ALTER POLICY with no action yet, which has no Build.
type AlterPolicyQuery struct{ p Policy }

// AlterPolicy starts an ALTER POLICY.
func AlterPolicy(p Policy) *AlterPolicyQuery { return &AlterPolicyQuery{p: p} }

// AlterPolicyStatement is a complete ALTER POLICY.
//
// Its three changeable parts accumulate into one statement rather than one
// each, because Postgres's grammar takes them together and a policy changed in
// three statements has two windows in which it is half changed: the roles are
// the new ones and the predicate is still the old one. RenameTo is the
// exception and is a form of its own in the grammar, so it cannot be combined
// and Build refuses the combination.
type AlterPolicyStatement struct {
	p      Policy
	rename func(a *sqlb.Args) string
	parts  []func(a *sqlb.Args) string
}

// ErrRenamePolicyAlone is returned for an ALTER POLICY that both renames the
// policy and changes it, which Postgres has no form for.
var ErrRenamePolicyAlone = errors.New("sqlb/pg: a policy rename takes no other change")

// with derives a statement carrying one more change.
func (x *AlterPolicyStatement) with(o func(a *sqlb.Args) string) *AlterPolicyStatement {
	c := *x
	c.parts = append(slices.Clone(x.parts), o)
	return &c
}

// RenameTo renames the policy; to names the same table.
func (x *AlterPolicyQuery) RenameTo(to Policy) *AlterPolicyStatement {
	return &AlterPolicyStatement{p: x.p, rename: func(a *sqlb.Args) string {
		if to.Table.Name != x.p.Table.Name || to.Table.Schema != x.p.Table.Schema {
			a.Fail(ddl.ErrForeignColumn)
		}
		return kwRenameTo + " " + a.Dialect().QuoteIdent(to.Name)
	}}
}

// To replaces the set of roles the policy applies to, which is the whole set
// and not an addition: a policy that named PUBLIC and is given a Role here
// stops applying to everyone else. It is how a library hands its tables to a
// named application role after an install ran as somebody else.
func (x *AlterPolicyQuery) To(first RoleRef, more ...RoleRef) *AlterPolicyStatement {
	return (&AlterPolicyStatement{p: x.p}).To(first, more...)
}

// Using replaces the policy's USING predicate, which is how a policy's rule
// changes without a window in which it is dropped.
func (x *AlterPolicyQuery) Using(p sqlb.Predicate) *AlterPolicyStatement {
	return (&AlterPolicyStatement{p: x.p}).Using(p)
}

// WithCheck replaces the policy's WITH CHECK predicate.
func (x *AlterPolicyQuery) WithCheck(p sqlb.Predicate) *AlterPolicyStatement {
	return (&AlterPolicyStatement{p: x.p}).WithCheck(p)
}

// To replaces the roles, beside whatever else this statement changes.
func (x *AlterPolicyStatement) To(first RoleRef, more ...RoleRef) *AlterPolicyStatement {
	roles := append([]RoleRef{first}, more...)
	return x.with(func(a *sqlb.Args) string {
		rs := make([]string, len(roles))
		for i, r := range roles {
			if r == nil {
				a.Fail(ddl.ErrZeroValue)
				return ""
			}
			rs[i] = r.roleSQL(a)
		}
		return kwToRole + " " + strings.Join(rs, ", ")
	})
}

// Using replaces the policy's USING predicate, beside whatever else this
// statement changes.
func (x *AlterPolicyStatement) Using(p sqlb.Predicate) *AlterPolicyStatement {
	return x.with(func(a *sqlb.Args) string { return kwUsing + " (" + writePred(a, p) + ")" })
}

// WithCheck replaces the policy's WITH CHECK predicate, beside whatever else
// this statement changes.
func (x *AlterPolicyStatement) WithCheck(p sqlb.Predicate) *AlterPolicyStatement {
	return x.with(func(a *sqlb.Args) string { return kwWithCheck + " (" + writePred(a, p) + ")" })
}

// Build renders the statement.
func (x *AlterPolicyStatement) Build() (ddl.Script, error) {
	if x.rename != nil && len(x.parts) > 0 {
		return nil, ErrRenamePolicyAlone
	}
	if x.rename == nil && len(x.parts) == 0 {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.p.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwAlterPolicy + " " + d.QuoteIdent(x.p.Name) + " " + kwOn + " " + x.p.Table.Qualified(d)
		if x.rename != nil {
			return s + " " + x.rename(a)
		}
		for _, part := range x.parts {
			s += " " + part(a)
		}
		return s
	})
}

// DropPolicyStatement is DROP POLICY ... ON.
type DropPolicyStatement struct {
	p                           Policy
	ifExists, cascade, restrict bool
}

// DropPolicy drops a policy from its table.
func DropPolicy(p Policy) *DropPolicyStatement { return &DropPolicyStatement{p: p} }

// IfExists makes dropping a missing policy a no-op.
func (x *DropPolicyStatement) IfExists() *DropPolicyStatement { c := *x; c.ifExists = true; return &c }

// Build renders the statement.
func (x *DropPolicyStatement) Build() (ddl.Script, error) {
	return buildOne(x.p.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwDropPolicy + ifExists(x.ifExists) + " " + d.QuoteIdent(x.p.Name) + " " + kwOn + " " + x.p.Table.Qualified(d) +
			cascade(x.cascade) + restrict(x.restrict)
	})
}
