package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The forms inside a statement that already had a constructor.
//
// The DDL detector reached zero partial and zero left-out entries on
// 2026-09-24, and that turned out to measure less than it sounded: a status of
// "built" says a statement has an entry point, and the format had nowhere to
// record a form of that statement which does not build. Reading every
// synopsis psql prints against what this package exports found the absences
// below, and one outright bug — ALTER EXTENSION accepted an Index and a
// Statistics as members, which the server refuses with "cannot add an object
// of this type to an extension", measured on 17.10. A rendering test could
// never have caught that, because the text is well formed.
//
// The rule that follows is the one this repository already had, applied one
// level down: a statement is not built until every form of it a library that
// owns tables would write is built, and the way to know which forms exist is
// to read the grammar rather than the exports.

const (
	kwCreate            = "CREATE"
	kwSequenceObject    = "SEQUENCE"
	kwWithVersion       = "VERSION"
	kwSchemaOption      = "SCHEMA"
	kwAuthorization     = "AUTHORIZATION"
	kwDependsOn         = "DEPENDS ON EXTENSION"
	kwNoDependsOn       = "NO DEPENDS ON EXTENSION"
	kwDropNotNullDomain = "DROP NOT NULL"
	kwNoMinValue        = "NO MINVALUE"
	kwNoMaxValue        = "NO MAXVALUE"
	kwOwnedByNone       = "OWNED BY NONE"
	kwTemporary         = "TEMPORARY"
	kwUnloggedSequence  = "UNLOGGED"
	kwOnDomain          = "ON DOMAIN"
	kwFromCollation     = "FROM"
	optProvider         = "provider"
	optLocale           = "locale"
	optLcCollate        = "lc_collate"
	optLcCtype          = "lc_ctype"
	optRules            = "rules"
	optDeterministic    = "deterministic"
	optVersionName      = "version"
	providerLibc        = "libc"
	providerICU         = "icu"
)

// ErrCascadeNeedsDrop is returned for a CASCADE on an ALTER DOMAIN action that
// is not a DROP CONSTRAINT, the one action Postgres allows it after.
var ErrCascadeNeedsDrop = errors.New("sqlb/pg: CASCADE follows DROP CONSTRAINT and no other domain action")

// ErrSingleExpression is returned for a one-expression statistics object given
// a second element or a kind, which Postgres's grammar has no form for: the
// single-expression form measures one expression's own distribution and the
// multi-element form measures how elements depend on each other.
var ErrSingleExpression = errors.New("sqlb/pg: a single-expression statistics object takes one element and no kinds")

// Schema is ownable: ALTER SCHEMA has an OWNER TO, and a library that installs
// its tables into a schema of its own is the thing that wants to hand that
// schema to the role which will run the application.
func (Schema) ownable() {}

// Version pins the extension version to install, rather than the newest the
// package files offer. A library that depends on a function an extension added
// in a known version pins it here and updates with UpdateExtension.
func (x *CreateExtensionStatement) Version(v string) *CreateExtensionStatement {
	c := *x
	c.version = v
	return &c
}

// Schema installs the extension's objects into a named schema rather than the
// first of the search path, which is what a library does when it keeps its
// dependencies out of the schema its caller's tables live in. An extension
// that declares itself non-relocatable has its own schema and refuses this.
func (x *CreateExtensionStatement) Schema(s Schema) *CreateExtensionStatement {
	c := *x
	c.schema = s
	return &c
}

// Cascade installs the extensions this one depends on, rather than failing on
// the first one missing.
func (x *CreateExtensionStatement) Cascade() *CreateExtensionStatement {
	c := *x
	c.cascade = true
	return &c
}

// DependsOnExtensionStatement is the [NO] DEPENDS ON EXTENSION form, which
// says an object is dropped with an extension without being a member of it.
type DependsOnExtensionStatement struct {
	kind, name string
	e          Extension
	no         bool
}

// Dependable is an object that can be made to depend on an extension. The set
// is Postgres's: an index, a trigger, a materialized view and a function, the
// objects an extension's script creates but does not own. It is not the same
// set as ExtensionMember, which is why it is its own interface.
type Dependable interface {
	commentTarget(d sqlb.Dialect) (kind, qualified, bare string, err error)
	dependable()
}

func (Index) dependable()            {}
func (Trigger) dependable()          {}
func (MaterializedView) dependable() {}
func (Function) dependable()         {}

// DependsOnExtension makes an object depend on an extension, so that dropping
// the extension drops the object too. It is the weaker half of extension
// membership and the answer for the objects that cannot be members: an index
// and a statistics object are refused by ALTER EXTENSION ADD, and an index
// created by an extension's script is tied to it this way instead.
func DependsOnExtension(o Dependable, e Extension) *DependsOnExtensionStatement {
	return dependsOn(o, e, false)
}

// NoDependsOnExtension breaks that tie, leaving the object in place when the
// extension is dropped.
func NoDependsOnExtension(o Dependable, e Extension) *DependsOnExtensionStatement {
	return dependsOn(o, e, true)
}

func dependsOn(o Dependable, e Extension, no bool) *DependsOnExtensionStatement {
	x := &DependsOnExtensionStatement{e: e, no: no}
	if o == nil {
		return x
	}
	kind, name, _, err := o.commentTarget(Dialect{})
	if err != nil {
		return x
	}
	x.kind, x.name = kind, name
	return x
}

// Build renders the statement.
func (x *DependsOnExtensionStatement) Build() (ddl.Script, error) {
	if x.kind == "" || x.name == "" {
		return nil, ddl.ErrZeroValue
	}
	kw := kwDependsOn
	if x.no {
		kw = kwNoDependsOn
	}
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		return kwAlterObject + " " + x.kind + " " + x.name + " " + kw + " " + a.Dialect().QuoteIdent(x.e.Name)
	})
}

// Authorization gives the new schema to a role, which is the form a library's
// installer runs when the role creating the schema is not the role that will
// own it. Postgres also allows the schema's name to be omitted so it takes the
// role's, which is not offered: a schema whose name is not in the declaration
// is a schema no Table value can name.
func (x *CreateSchemaStatement) Authorization(r RoleRef) *CreateSchemaStatement {
	c := *x
	c.owner = r
	return &c
}

// SetNotNull refuses NULL in every column of the domain from now on. It scans
// every table with a column of the type and fails if any row holds NULL, which
// is why the migration is the three-step one a column's NOT NULL needs.
func (x *AlterDomainQuery[T]) SetNotNull() *AlterDomainStatement[T] {
	return &AlterDomainStatement[T]{d: x.d, tail: func(*sqlb.Args) string { return kwSetNotNull }}
}

// DropNotNull allows NULL again.
func (x *AlterDomainQuery[T]) DropNotNull() *AlterDomainStatement[T] {
	return &AlterDomainStatement[T]{d: x.d, tail: func(*sqlb.Args) string { return kwDropNotNullDomain }}
}

// RenameTo renames the domain to to's name; its schema is unchanged, which is
// SetObjectSchema's job. Both are declarations of the same Go type, so a
// rename cannot change what the domain's values are.
func (x *AlterDomainQuery[T]) RenameTo(to Domain[T]) *AlterDomainStatement[T] {
	return &AlterDomainStatement[T]{d: x.d, tail: func(a *sqlb.Args) string {
		if to.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwRenameTo + " " + a.Dialect().QuoteIdent(to.Name)
	}}
}

// RenameConstraint renames one of the domain's constraints, the migration a
// declared DomainConstraint needs when its name changes. Both constraints must
// name this domain, which Build checks, because the server would report one of
// another domain as a constraint that does not exist.
func (x *AlterDomainQuery[T]) RenameConstraint(from, to DomainConstraint[T]) *AlterDomainStatement[T] {
	d := x.d
	return &AlterDomainStatement[T]{d: d, tail: func(a *sqlb.Args) string {
		if !from.check(a, d) || !to.check(a, d) {
			return ""
		}
		dl := a.Dialect()
		return kwRenameConstraint + " " + dl.QuoteIdent(from.Name) + " " + kwTo + " " + dl.QuoteIdent(to.Name)
	}}
}

// Cascade also drops what depends on the constraint being dropped. It applies
// to DropConstraint alone, which is the only ALTER DOMAIN action Postgres
// allows it after, and Build refuses it anywhere else rather than rendering a
// statement the server would reject.
func (x *AlterDomainStatement[T]) Cascade() *AlterDomainStatement[T] {
	c := *x
	c.cascade = true
	return &c
}

// Default gives the domain a default at creation, written as a literal of its
// own Go type; AlterDomain's SetDefault changes it afterwards.
func (x *CreateDomainStatement[T]) Default(v T) *CreateDomainStatement[T] {
	c := *x
	c.checks = slices.Clone(x.checks)
	c.def = func(a *sqlb.Args) string { return a.Literal(v) }
	return &c
}

// DefaultExpr gives the domain a default computed by e.
func (x *CreateDomainStatement[T]) DefaultExpr(e sqlb.Expr[T]) *CreateDomainStatement[T] {
	c := *x
	c.checks = slices.Clone(x.checks)
	c.def = func(a *sqlb.Args) string { return "(" + a.Bare(func() string { return e.SQL(a) }) + ")" }
	return &c
}

// Collate gives the domain a collation, which every column of the type then
// carries; it is how a case-insensitive text type is declared once rather than
// on each column.
func (x *CreateDomainStatement[T]) Collate(c Collation) *CreateDomainStatement[T] {
	n := *x
	n.checks = slices.Clone(x.checks)
	n.collation = c
	return &n
}

// LcCollate sets the collation's sort order separately from its character
// classification, the libc provider's two halves. It is mutually exclusive
// with the locale CreateCollation takes, which sets both.
func (x *CreateCollationStatement) LcCollate(s string) *CreateCollationStatement {
	c := *x
	c.lcCollate = s
	return &c
}

// LcCtype sets the collation's character classification.
func (x *CreateCollationStatement) LcCtype(s string) *CreateCollationStatement {
	c := *x
	c.lcCtype = s
	return &c
}

// Rules adds ICU tailoring rules on top of the locale, which is how a
// collation sorts one character out of its usual place without a locale of its
// own. Postgres 16 and later, ICU only.
func (x *CreateCollationStatement) Rules(s string) *CreateCollationStatement {
	c := *x
	c.rules = s
	return &c
}

// Version records the collation library's version the collation was made
// against, so the server can warn when an upgrade changes the sort order and
// an index built under the old one is silently wrong.
//
// It may only be given where the provider reports a version at all: ICU always
// does, and the libc C locale never does, which Postgres 18.6 refuses with
// "collation has no actual version, but a version was recorded" where 17.10 on
// macOS accepted it. Measured on both.
func (x *CreateCollationStatement) Version(s string) *CreateCollationStatement {
	c := *x
	c.version = s
	return &c
}

// CreateCollationFrom copies an existing collation under a new name, the form
// that takes no options at all. It is how a library gives a stable name of its
// own to whatever the deployment's default collation is.
func CreateCollationFrom(c, existing Collation) *CreateCollationStatement {
	return &CreateCollationStatement{c: c, from: existing}
}

// Expression adds an expression as one of the statistics object's elements,
// beside its columns. The planner keeps statistics for the expression itself,
// which is what makes a WHERE on lower(email) estimable rather than guessed.
func (x *CreateStatisticsStatement) Expression(e sqlb.SQLer) *CreateStatisticsStatement {
	c := *x
	c.kinds = slices.Clone(x.kinds)
	c.elems = append(slices.Clone(x.elems), statElement{expr: e})
	return &c
}

// CreateStatisticsExpr creates statistics for one expression, the form that
// takes a single element and no kinds. It exists beside CreateStatistics
// because Postgres's grammar separates them: two elements or more measure how
// the elements depend on each other, and one expression measures the
// expression's own distribution.
func CreateStatisticsExpr(s Statistics, t sqlb.Table, e sqlb.SQLer) *CreateStatisticsStatement {
	return &CreateStatisticsStatement{s: s, t: t, elems: []statElement{{expr: e}}, single: true}
}

// NoMinValue puts the sequence's lower bound back to the type's own, which is
// how a bound set by an earlier migration is removed rather than widened by a
// guess at what the type's minimum is.
func (x *AlterSequenceStatement[T]) NoMinValue() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwNoMinValue })
}

// NoMaxValue puts the sequence's upper bound back to the type's own.
func (x *AlterSequenceStatement[T]) NoMaxValue() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwNoMaxValue })
}

// NoMinValue starts an ALTER SEQUENCE removing the lower bound.
func (x *AlterSequenceQuery[T]) NoMinValue() *AlterSequenceStatement[T] {
	return x.start().NoMinValue()
}

// NoMaxValue starts an ALTER SEQUENCE removing the upper bound.
func (x *AlterSequenceQuery[T]) NoMaxValue() *AlterSequenceStatement[T] {
	return x.start().NoMaxValue()
}

// Cycle starts the sequence again at its minimum when it runs out. On a key
// column that means the next value collides with a row that is still there,
// which is why it is off by default and why it is worth writing out where it
// is wanted.
func (x *CreateSequenceStatement[T]) Cycle() *CreateSequenceStatement[T] {
	c := *x
	c.cycle, c.noCycle = true, false
	return &c
}

// NoCycle fails when the sequence runs out, the default.
func (x *CreateSequenceStatement[T]) NoCycle() *CreateSequenceStatement[T] {
	c := *x
	c.cycle, c.noCycle = false, true
	return &c
}

// NoMinValue leaves the lower bound at the type's own, the default, written
// out.
func (x *CreateSequenceStatement[T]) NoMinValue() *CreateSequenceStatement[T] {
	c := *x
	c.noMin = true
	return &c
}

// NoMaxValue leaves the upper bound at the type's own, the default, written
// out.
func (x *CreateSequenceStatement[T]) NoMaxValue() *CreateSequenceStatement[T] {
	c := *x
	c.noMax = true
	return &c
}

// OwnedBy ties the sequence to a column, so dropping the column or its table
// drops the sequence. It is what a declared sequence backing a column should
// almost always carry, and the reason it is not the default is that a sequence
// shared by two tables must not have it.
func (x *CreateSequenceStatement[T]) OwnedBy(col sqlb.Expr[T]) *CreateSequenceStatement[T] {
	c := *x
	c.owned = ownedBy(col)
	return &c
}

// OwnedByNone leaves the sequence standing on its own, the default, written
// out.
func (x *CreateSequenceStatement[T]) OwnedByNone() *CreateSequenceStatement[T] {
	c := *x
	c.owned = func(*sqlb.Args) string { return kwOwnedByNone }
	return &c
}

// Unlogged keeps the sequence out of the write-ahead log, which is faster and
// resets it to its start value after a crash. A logged table's identity column
// may not use one.
func (x *CreateSequenceStatement[T]) Unlogged() *CreateSequenceStatement[T] {
	c := *x
	c.modifier = kwUnloggedSequence
	return &c
}

// Temporary makes the sequence live in the session's temporary schema and
// vanish when the session ends.
func (x *CreateSequenceStatement[T]) Temporary() *CreateSequenceStatement[T] {
	c := *x
	c.modifier = kwTemporary
	return &c
}

// Restrict refuses the drop while anything depends on the object, which is
// what Postgres does anyway when Cascade is not given. It is here because the
// keyword written out is a statement of intent a reader can see, where its
// absence is indistinguishable from not having thought about it.
func (x *DropObjectStatement) Restrict() *DropObjectStatement {
	c := *x
	c.restrict = true
	return &c
}

// Cascade also drops what depends on the trigger.
func (x *DropTriggerStatement) Cascade() *DropTriggerStatement {
	c := *x
	c.cascade = true
	return &c
}

// Restrict refuses the drop while anything depends on the trigger, the
// default written out.
func (x *DropTriggerStatement) Restrict() *DropTriggerStatement {
	c := *x
	c.restrict = true
	return &c
}

// Cascade also drops what depends on the policy.
func (x *DropPolicyStatement) Cascade() *DropPolicyStatement {
	c := *x
	c.cascade = true
	return &c
}

// Restrict refuses the drop while anything depends on the policy, the default
// written out.
func (x *DropPolicyStatement) Restrict() *DropPolicyStatement {
	c := *x
	c.restrict = true
	return &c
}

// CommentOnDomainConstraint starts a comment on a domain's constraint:
// COMMENT ON CONSTRAINT name ON DOMAIN domain. It is a separate constructor
// from CommentOnConstraint because Postgres spells the two differently and a
// domain's constraint is a different object from a table's.
func CommentOnDomainConstraint[T any](c DomainConstraint[T]) *CommentQuery {
	return &CommentQuery{target: domainConstraintTarget{
		name:   c.Name,
		schema: c.Domain.Schema,
		domain: c.Domain.Name,
	}}
}

type domainConstraintTarget struct{ name, schema, domain string }

func (x domainConstraintTarget) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	if x.name == "" || x.domain == "" {
		return kwObjConstraint, "", "", nil
	}
	return kwObjConstraint, d.QuoteIdent(x.name) + " " + kwOnDomain + " " + qualified(d, x.schema, x.domain), x.name, nil
}

// AlterRangeTypeQuery is ALTER TYPE on a range type with no action yet, which
// has no Build.
type AlterRangeTypeQuery[T any] struct{ r RangeType[T] }

// AlterRangeType starts an ALTER TYPE on a range type. Moving it is
// SetObjectSchema and giving it away is SetOwner, as for every other declared
// object.
func AlterRangeType[T any](r RangeType[T]) *AlterRangeTypeQuery[T] {
	return &AlterRangeTypeQuery[T]{r: r}
}

// RenameTo renames the range type to to's name.
func (x *AlterRangeTypeQuery[T]) RenameTo(to RangeType[T]) *AlterTypeNameStatement {
	return renameType(x.r.Schema, x.r.Name, to.Name)
}

// AlterTypeNameStatement is ALTER TYPE ... RENAME TO, which every kind of type
// shares: the statement is the same whatever the type is, and the type's Go
// parameter has already done its work in the constructor that made it.
type AlterTypeNameStatement struct{ schema, from, to string }

func renameType(schema, from, to string) *AlterTypeNameStatement {
	return &AlterTypeNameStatement{schema: schema, from: from, to: to}
}

// Build renders the statement.
func (x *AlterTypeNameStatement) Build() (ddl.Script, error) {
	if x.to == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.from, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterType + " " + qualified(d, x.schema, x.from) + " " + kwRenameTo + " " + d.QuoteIdent(x.to)
	})
}

// RenameTo renames the composite type to to's name.
func (x *AlterCompositeQuery[T]) RenameTo(to Composite[T]) *AlterTypeNameStatement {
	return renameType(x.c.Schema, x.c.Name, to.Name)
}

// AlterBaseTypeQuery is ALTER TYPE on a base type with no action yet, which
// has no Build.
type AlterBaseTypeQuery[T any] struct{ b BaseType[T] }

// AlterBaseType starts an ALTER TYPE on a base type.
func AlterBaseType[T any](b BaseType[T]) *AlterBaseTypeQuery[T] {
	return &AlterBaseTypeQuery[T]{b: b}
}

// RenameTo renames the base type to to's name.
func (x *AlterBaseTypeQuery[T]) RenameTo(to BaseType[T]) *AlterTypeNameStatement {
	return renameType(x.b.Schema, x.b.Name, to.Name)
}

// SetProperties fills in the functions and the storage strategy a base type
// was created without, which is the only way to add them: the receive, send
// and type-modifier functions each take or return the type, so they cannot
// exist when CREATE TYPE runs and the type cannot be created without them
// either. It is the same shell-then-fill order CreateShellType exists for, one
// level further on.
func (x *AlterBaseTypeQuery[T]) SetProperties() *AlterBaseTypeStatement[T] {
	return &AlterBaseTypeStatement[T]{b: x.b}
}

// AlterBaseTypeStatement is ALTER TYPE ... SET ( property = value ).
type AlterBaseTypeStatement[T any] struct {
	b    BaseType[T]
	opts []func(a *sqlb.Args) string
}

func (x *AlterBaseTypeStatement[T]) with(o func(a *sqlb.Args) string) *AlterBaseTypeStatement[T] {
	c := *x
	c.opts = append(slices.Clone(x.opts), o)
	return &c
}

// Receive names the function that reads the type's binary form.
func (x *AlterBaseTypeStatement[T]) Receive(f Function) *AlterBaseTypeStatement[T] {
	return x.with(functionOption(optReceive, f))
}

// Send names the function that writes the type's binary form.
func (x *AlterBaseTypeStatement[T]) Send(f Function) *AlterBaseTypeStatement[T] {
	return x.with(functionOption(optSend, f))
}

// TypmodIn names the function that parses a type modifier.
func (x *AlterBaseTypeStatement[T]) TypmodIn(f Function) *AlterBaseTypeStatement[T] {
	return x.with(functionOption(optTypmodIn, f))
}

// TypmodOut names the function that prints a type modifier back.
func (x *AlterBaseTypeStatement[T]) TypmodOut(f Function) *AlterBaseTypeStatement[T] {
	return x.with(functionOption(optTypmodOut, f))
}

// Analyze names the function that gathers the planner's statistics.
func (x *AlterBaseTypeStatement[T]) Analyze(f Function) *AlterBaseTypeStatement[T] {
	return x.with(functionOption(optAnalyze, f))
}

// Subscript names the function that implements value[i].
func (x *AlterBaseTypeStatement[T]) Subscript(f Function) *AlterBaseTypeStatement[T] {
	return x.with(functionOption(optSubscript, f))
}

// Storage widens what the server may do with a value too large for its row.
// Postgres allows this one property to change only in the widening direction,
// plain to extended and not back, because values already stored in the old
// strategy stay as they are.
func (x *AlterBaseTypeStatement[T]) Storage(s TypeStorage) *AlterBaseTypeStatement[T] {
	return x.with(func(a *sqlb.Args) string {
		kw, ok := s.keyword()
		return optStorage + " = " + keywordOf(a, kw, ok, "Storage")
	})
}

// Build renders the statement.
func (x *AlterBaseTypeStatement[T]) Build() (ddl.Script, error) {
	if len(x.opts) == 0 {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.b.Name, true, func(a *sqlb.Args) string {
		return kwAlterType + " " + qualified(a.Dialect(), x.b.Schema, x.b.Name) + " " +
			kwSet + " (" + strings.Join(renderOptions(a, x.opts), ", ") + ")"
	})
}
