package pg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreateDomain     = "CREATE DOMAIN"
	kwAlterDomain      = "ALTER DOMAIN"
	kwDropDomain       = "DROP DOMAIN"
	kwValue            = "VALUE"
	kwCreateCollation  = "CREATE COLLATION"
	kwDropCollation    = "DROP COLLATION"
	kwCreateStatistics = "CREATE STATISTICS"
	kwDropStatistics   = "DROP STATISTICS"
	kwAlterExtension   = "ALTER EXTENSION"
	kwUpdate           = "UPDATE"
	kwAlterMatView     = "ALTER MATERIALIZED VIEW"
)

// Domain is a Postgres domain: a base type with constraints, whose values are
// T, so it is a ddl.SQLType[T] and a column of another Go type cannot use it.
type Domain[T any] struct{ Schema, Name string }

// Spelling is the domain's qualified name, as a column definition writes it.
func (d Domain[T]) Spelling(dl sqlb.Dialect) (string, error) {
	if d.Name == "" {
		return "", ErrEmptyName
	}
	return qualified(dl, d.Schema, d.Name), nil
}

// TypeOf types the domain to T for ddl.SQLType.
func (Domain[T]) TypeOf(T) {}

func (d Domain[T]) commentTarget(dl sqlb.Dialect) (string, string, string, error) {
	return kwObjDomain, qualified(dl, d.Schema, d.Name), d.Name, nil
}

// DomainValue is the VALUE keyword, the value being checked in a domain's
// CHECK, typed to the domain.
func DomainValue[T any]() sqlb.Expr[T] {
	return expr[T](constructOps, func(*sqlb.Args) string { return kwValue })
}

// DomainConstraint is a named constraint of one domain, declared once, the
// domain's counterpart of TableConstraint: CreateDomain's Check, and
// AlterDomain's AddConstraint, ValidateConstraint and DropConstraint all take
// it and refuse one declared on another domain.
type DomainConstraint[T any] struct {
	Domain Domain[T]
	Name   string
}

// ErrForeignDomain is returned for a DomainConstraint of one domain used in a
// statement about another.
var ErrForeignDomain = errors.New("sqlb/pg: constraint belongs to another domain")

// check refuses a constraint that is unnamed or of another domain than d.
func (c DomainConstraint[T]) check(a *sqlb.Args, d Domain[T]) bool {
	switch {
	case c.Name == "" || c.Domain.Name == "":
		a.Fail(ddl.ErrZeroValue)
	case c.Domain.Name != d.Name || c.Domain.Schema != d.Schema:
		a.Fail(fmt.Errorf("%w: %s is on %s, not %s", ErrForeignDomain, c.Name, c.Domain.Name, d.Name))
	default:
		return true
	}
	return false
}

// DomainCheck is one named CHECK of a domain.
type DomainCheck struct {
	name string
	pred sqlb.Predicate
	// valid is the constraint's own check, run when the domain is known.
	valid func(a *sqlb.Args) bool
}

// CreateDomainStatement is CREATE DOMAIN.
type CreateDomainStatement[T any] struct {
	d    Domain[T]
	base ddl.SQLType[T]
	// collation and def are the COLLATE and DEFAULT a domain may carry, which
	// every column of the type then has without repeating them.
	collation Collation
	def       func(a *sqlb.Args) string
	notNull   bool
	checks    []DomainCheck
}

// CreateDomain creates a domain over base, a type for the same Go type.
// Postgres has no IF NOT EXISTS for it; wrap it in Guarded.
func CreateDomain[T any](d Domain[T], base ddl.SQLType[T]) *CreateDomainStatement[T] {
	return &CreateDomainStatement[T]{d: d, base: base}
}

// Check adds a named constraint on DomainValue.
func (x *CreateDomainStatement[T]) Check(con DomainConstraint[T], p sqlb.Predicate) *CreateDomainStatement[T] {
	c := *x
	d := x.d
	c.checks = append(slices.Clone(x.checks), DomainCheck{name: con.Name, pred: p, valid: func(a *sqlb.Args) bool { return con.check(a, d) }})
	return &c
}

// NotNull refuses NULL wherever the domain is used.
func (x *CreateDomainStatement[T]) NotNull() *CreateDomainStatement[T] {
	c := *x
	c.checks = slices.Clone(x.checks)
	c.notNull = true
	return &c
}

// Build renders the statement.
func (x *CreateDomainStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.d.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		sp, err := x.base.Spelling(d)
		if err != nil {
			a.Fail(err)
			return ""
		}
		s := kwCreateDomain + " " + qualified(d, x.d.Schema, x.d.Name) + " " + kwAs + " " + sp
		if x.collation.Name != "" {
			s += " " + kwCollate + " " + qualified(d, x.collation.Schema, x.collation.Name)
		}
		if x.def != nil {
			s += " " + kwDefault + " " + x.def(a)
		}
		if x.notNull {
			s += " " + kwNotNullDomain
		}
		for _, ch := range x.checks {
			if ch.valid == nil || !ch.valid(a) {
				if ch.valid == nil {
					a.Fail(ddl.ErrZeroValue)
				}
				return ""
			}
			s += " " + constraintHead(a, ch.name) + "CHECK (" + writePred(a, ch.pred) + ")"
		}
		return s
	})
}

const kwNotNullDomain = "NOT NULL"

// duplicateCondition is what a repeated CREATE DOMAIN raises.
func (*CreateDomainStatement[T]) duplicateCondition() string { return condDuplicateObject }

// AlterDomainQuery is ALTER DOMAIN with no action yet, which has no Build.
type AlterDomainQuery[T any] struct{ d Domain[T] }

// AlterDomain starts an ALTER DOMAIN.
func AlterDomain[T any](d Domain[T]) *AlterDomainQuery[T] { return &AlterDomainQuery[T]{d: d} }

// AlterDomainStatement is a complete ALTER DOMAIN.
type AlterDomainStatement[T any] struct {
	d    Domain[T]
	tail func(a *sqlb.Args) string
	// isDrop marks the one action CASCADE may follow, DROP CONSTRAINT, so
	// Cascade on any other fails Build rather than reaching the server.
	isDrop   bool
	cascade  bool
	notValid bool
}

// AddConstraint adds a named check; NotValid skips checking existing values.
func (x *AlterDomainQuery[T]) AddConstraint(c DomainConstraint[T], p sqlb.Predicate) *AlterDomainStatement[T] {
	d := x.d
	return &AlterDomainStatement[T]{d: d, tail: func(a *sqlb.Args) string {
		if !c.check(a, d) {
			return ""
		}
		return kwAdd + " " + constraintHead(a, c.Name) + "CHECK (" + writePred(a, p) + ")"
	}}
}

// ValidateConstraint checks existing values against a constraint added NOT
// VALID.
func (x *AlterDomainQuery[T]) ValidateConstraint(c DomainConstraint[T]) *AlterDomainStatement[T] {
	d := x.d
	return &AlterDomainStatement[T]{d: d, tail: func(a *sqlb.Args) string {
		if !c.check(a, d) {
			return ""
		}
		return kwValidateConstraint + " " + a.Dialect().QuoteIdent(c.Name)
	}}
}

// DropConstraint drops a named check if it exists.
func (x *AlterDomainQuery[T]) DropConstraint(c DomainConstraint[T]) *AlterDomainStatement[T] {
	d := x.d
	return &AlterDomainStatement[T]{d: d, isDrop: true, tail: func(a *sqlb.Args) string {
		if !c.check(a, d) {
			return ""
		}
		return kwDropConstraint + " " + kwIfExists + " " + a.Dialect().QuoteIdent(c.Name)
	}}
}

// NotValid adds the constraint without checking existing values; only an
// added constraint takes it.
func (x *AlterDomainStatement[T]) NotValid() *AlterDomainStatement[T] {
	c := *x
	c.notValid = true
	return &c
}

// Build renders the statement.
func (x *AlterDomainStatement[T]) Build() (ddl.Script, error) {
	if x.cascade && !x.isDrop {
		return nil, ErrCascadeNeedsDrop
	}
	return buildOne(x.d.Name, true, func(a *sqlb.Args) string {
		s := kwAlterDomain + " " + qualified(a.Dialect(), x.d.Schema, x.d.Name) + " " + x.tail(a)
		if x.notValid {
			s += " " + kwNotValid
		}
		return s + cascade(x.cascade)
	})
}

// DropDomain drops a domain, which fails while a column uses it unless Cascade
// is given.
func DropDomain[T any](d Domain[T]) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropDomain, schema: d.Schema, name: d.Name}
}

// Collation is a Postgres collation.
type Collation struct{ Schema, Name string }

func (c Collation) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjCollation, qualified(d, c.Schema, c.Name), c.Name, nil
}

// CreateCollationStatement is CREATE COLLATION.
type CreateCollationStatement struct {
	c      Collation
	locale string
	// from is the existing collation this one copies, the FROM form, which
	// takes no other option at all.
	from                               Collation
	lcCollate, lcCtype, rules, version string
	icu, nondeterministic, ifNotExists bool
}

// CreateCollation creates a collation for locale, from the operating system's
// library unless ICU is given.
func CreateCollation(c Collation, locale string) *CreateCollationStatement {
	return &CreateCollationStatement{c: c, locale: locale}
}

// ICU takes the collation from ICU, which is the only provider that can be
// nondeterministic.
func (x *CreateCollationStatement) ICU() *CreateCollationStatement { c := *x; c.icu = true; return &c }

// Nondeterministic makes strings that compare equal under the locale equal,
// so an ICU level-2 collation makes a UNIQUE on an email case-insensitive
// without a lower() column. It needs ICU.
func (x *CreateCollationStatement) Nondeterministic() *CreateCollationStatement {
	c := *x
	c.nondeterministic = true
	return &c
}

// IfNotExists makes creating an existing collation a no-op.
func (x *CreateCollationStatement) IfNotExists() *CreateCollationStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreateCollationStatement) Build() (ddl.Script, error) {
	if x.from.Name != "" {
		return buildOne(x.c.Name, true, func(a *sqlb.Args) string {
			d := a.Dialect()
			return kwCreateCollation + ifNotExists(x.ifNotExists) + " " + qualified(d, x.c.Schema, x.c.Name) +
				" " + kwFromCollation + " " + qualified(d, x.from.Schema, x.from.Name)
		})
	}
	if x.locale == "" && x.lcCollate == "" && x.lcCtype == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.c.Name, true, func(a *sqlb.Args) string {
		provider := providerLibc
		if x.icu {
			provider = providerICU
		}
		opts := []string{optProvider + " = " + provider}
		if x.locale != "" {
			opts = append(opts, optLocale+" = "+a.Literal(x.locale))
		}
		for _, o := range []struct{ name, value string }{
			{optLcCollate, x.lcCollate}, {optLcCtype, x.lcCtype},
			{optRules, x.rules}, {optVersionName, x.version},
		} {
			if o.value != "" {
				opts = append(opts, o.name+" = "+a.Literal(o.value))
			}
		}
		if x.nondeterministic {
			opts = append(opts, optDeterministic+" = "+boolOption(false))
		}
		return kwCreateCollation + ifNotExists(x.ifNotExists) + " " + qualified(a.Dialect(), x.c.Schema, x.c.Name) + " (" + strings.Join(opts, ", ") + ")"
	})
}

// DropCollation drops a collation.
func DropCollation(c Collation) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropCollation, schema: c.Schema, name: c.Name}
}

// ColumnCollation gives a text column a declared collation, qualified by its
// schema; ColumnCollate takes a built-in collation's name.
func ColumnCollation[T collatable](col sqlb.Expr[T], c Collation) ddl.ColumnClause[Dialect] {
	return ddl.NewRankedColumnClause[Dialect](colRankCollate, col, ddl.PartAfterType, func(a *sqlb.Args) string {
		return kwCollate + " " + qualified(a.Dialect(), c.Schema, c.Name)
	})
}

// Statistics is an extended statistics object, which tells the planner that
// two columns are correlated so it stops multiplying their selectivities.
type Statistics struct{ Schema, Name string }

func (s Statistics) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjStatistics, qualified(d, s.Schema, s.Name), s.Name, nil
}

// StatKind is what an extended statistics object measures.
type StatKind int

// The kinds.
const (
	NDistinct StatKind = iota + 1
	Dependencies
	MCV
)

func (k StatKind) keyword() (string, bool) {
	switch k {
	case NDistinct:
		return "ndistinct", true
	case Dependencies:
		return "dependencies", true
	case MCV:
		return "mcv", true
	}
	return "", false
}

// CreateStatisticsStatement is CREATE STATISTICS.
type CreateStatisticsStatement struct {
	s     Statistics
	t     sqlb.Table
	kinds []StatKind
	// elems are the object's elements, each a column of the table or an
	// expression over it; single marks the one-expression form, which takes
	// no kinds and exactly one element.
	elems       []statElement
	single      bool
	ifNotExists bool
}

// statElement is one element of a statistics object: a column, or an
// expression the planner keeps its own statistics for.
type statElement struct {
	col  ddl.ColumnRef
	expr sqlb.SQLer
}

// write renders the element, a column bare and an expression parenthesised,
// which is how Postgres tells the two apart in the list.
func (e statElement) write(a *sqlb.Args) string {
	if e.expr != nil {
		return "(" + a.Bare(func() string { return e.expr.SQL(a) }) + ")"
	}
	return a.Dialect().QuoteIdent(e.col.Name())
}

// CreateStatistics creates statistics over at least two columns of t, which is
// the fewest Postgres accepts, so fewer does not compile.
func CreateStatistics(s Statistics, t sqlb.Table, a, b ddl.ColumnRef, more ...ddl.ColumnRef) *CreateStatisticsStatement {
	elems := make([]statElement, 0, len(more)+2)
	for _, c := range append([]ddl.ColumnRef{a, b}, more...) {
		elems = append(elems, statElement{col: c})
	}
	return &CreateStatisticsStatement{s: s, t: t, elems: elems}
}

// Kinds limits what is measured; all kinds when not given.
func (x *CreateStatisticsStatement) Kinds(first StatKind, more ...StatKind) *CreateStatisticsStatement {
	c := *x
	c.elems = slices.Clone(x.elems)
	c.kinds = append(append(slices.Clone(x.kinds), first), more...)
	return &c
}

// IfNotExists makes creating existing statistics a no-op.
func (x *CreateStatisticsStatement) IfNotExists() *CreateStatisticsStatement {
	c := *x
	c.elems, c.kinds = slices.Clone(x.elems), slices.Clone(x.kinds)
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreateStatisticsStatement) Build() (ddl.Script, error) {
	if len(x.elems) == 0 {
		return nil, ddl.ErrZeroValue
	}
	for _, e := range x.elems {
		if e.expr != nil {
			continue
		}
		if e.col == nil || e.col.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(e.col, x.t) {
			return nil, ddl.ErrForeignColumn
		}
	}
	if x.single && (len(x.elems) != 1 || len(x.kinds) > 0) {
		return nil, ErrSingleExpression
	}
	return buildOne(x.s.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwCreateStatistics + ifNotExists(x.ifNotExists) + " " + qualified(d, x.s.Schema, x.s.Name)
		if len(x.kinds) > 0 {
			ks := make([]string, len(x.kinds))
			for i, k := range x.kinds {
				kw, ok := k.keyword()
				ks[i] = keywordOf(a, kw, ok, "Kinds")
			}
			s += " (" + strings.Join(ks, ", ") + ")"
		}
		names := make([]string, len(x.elems))
		for i, e := range x.elems {
			names[i] = e.write(a)
		}
		return s + " " + kwOn + " " + strings.Join(names, ", ") + " " + kwFrom + " " + x.t.Qualified(d)
	})
}

// DropStatistics drops a statistics object.
func DropStatistics(s Statistics) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropStatistics, schema: s.Schema, name: s.Name}
}

// AlterExtensionStatement is ALTER EXTENSION ... UPDATE.
type AlterExtensionStatement struct {
	e  Extension
	to string
}

// UpdateExtension updates an installed extension to the version its package
// files provide, which is how a library picks up a new pgvector.
func UpdateExtension(e Extension) *AlterExtensionStatement { return &AlterExtensionStatement{e: e} }

// To names the target version rather than the newest installed.
func (x *AlterExtensionStatement) To(version string) *AlterExtensionStatement {
	c := *x
	c.to = version
	return &c
}

// Build renders the statement.
func (x *AlterExtensionStatement) Build() (ddl.Script, error) {
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		s := kwAlterExtension + " " + a.Dialect().QuoteIdent(x.e.Name) + " " + kwUpdate
		if x.to != "" {
			s += " " + kwTo + " " + a.Literal(x.to)
		}
		return s
	})
}

// RenameMaterializedViewStatement is ALTER MATERIALIZED VIEW ... RENAME TO.
type RenameMaterializedViewStatement struct {
	from, to MaterializedView
	ifExists bool
}

// RenameMaterializedView renames a materialized view; its schema is unchanged.
func RenameMaterializedView(from, to MaterializedView) *RenameMaterializedViewStatement {
	return &RenameMaterializedViewStatement{from: from, to: to}
}

// IfExists makes renaming a missing materialized view a no-op.
func (x *RenameMaterializedViewStatement) IfExists() *RenameMaterializedViewStatement {
	c := *x
	c.ifExists = true
	return &c
}

// Build renders the statement.
func (x *RenameMaterializedViewStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.from.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterMatView + ifExists(x.ifExists) + " " + qualified(d, x.from.Schema, x.from.Name) + " " + kwRenameTo + " " + d.QuoteIdent(x.to.Name)
	})
}
