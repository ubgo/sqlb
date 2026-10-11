package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The ALTER forms of a declared object that are neither a rename nor a move.
//
// Each of these was recorded by the DDL detector as the operator's, on the
// reading that a storage parameter, an access method or an extension's
// membership list is a deployment's business rather than a library's. Half of
// that is true and half of it was a line drawn in the wrong place: a library
// owning a table owns that table's indexes, so it owns the fillfactor it chose
// for one, and an extension that ships objects has to be able to say which
// objects it ships. What stays the operator's is the tablespace, which names a
// directory on a machine this code has never seen.
//
// A storage parameter is the one open set here: its names come from the access
// method and from extensions, so the name is written as an identifier and the
// value as a literal by the dialect, never by concatenation.

const (
	kwAttachIndex     = "ATTACH PARTITION"
	kwRenameColumn    = "RENAME COLUMN"
	kwSetAccessMethod = "SET ACCESS METHOD"
	kwAddMember       = "ADD"
	kwDropMember      = "DROP"
)

// StorageParameter is one storage parameter of an index, a table or a
// materialized view: a name and a typed value. The names are an open set,
// because an access method and an extension each add their own, so the name is
// written as an identifier; the value is written by the dialect as a literal
// of its Go type, which is what keeps a text parameter one string.
type StorageParameter struct {
	name  string
	value func(a *sqlb.Args) string
}

// IntParameter is a whole-numbered parameter, fillfactor being the usual one.
func IntParameter(name string, v int64) StorageParameter {
	return StorageParameter{name: name, value: func(a *sqlb.Args) string { return a.Literal(v) }}
}

// FloatParameter is a fractional parameter, an autovacuum scale factor being
// the usual one.
func FloatParameter(name string, v float64) StorageParameter {
	return StorageParameter{name: name, value: func(a *sqlb.Args) string { return a.Literal(v) }}
}

// BoolParameter is an on-or-off parameter.
func BoolParameter(name string, v bool) StorageParameter {
	return StorageParameter{name: name, value: func(*sqlb.Args) string { return boolOption(v) }}
}

// TextParameter is a parameter whose value is a word or a phrase.
func TextParameter(name string, v string) StorageParameter {
	return StorageParameter{name: name, value: func(a *sqlb.Args) string { return a.Literal(v) }}
}

// write renders the parameter as an assignment, failing the build on a zero
// value rather than writing a name with nothing after the equals sign.
func (p StorageParameter) write(a *sqlb.Args) string {
	if p.name == "" || p.value == nil {
		a.Fail(ddl.ErrZeroValue)
		return ""
	}
	return a.Dialect().QuoteIdent(p.name) + " = " + p.value(a)
}

// writeParameters renders a SET (...) list, which is how ALTER writes storage
// parameters; a CREATE writes the same list after WITH, which is
// writeWithParameters. The two are separate because the keyword is the whole
// difference and a statement that uses the wrong one is a syntax error the
// server reports rather than a wrong result.
func writeParameters(a *sqlb.Args, ps []StorageParameter) string {
	return kwSet + " (" + strings.Join(parameterList(a, ps), ", ") + ")"
}

// writeWithParameters renders a WITH (...) list, which is how CREATE writes
// storage parameters.
func writeWithParameters(a *sqlb.Args, ps []StorageParameter) string {
	return kwWith + " (" + strings.Join(parameterList(a, ps), ", ") + ")"
}

// writeParameterNames renders a RESET (...) list, whose members are names with
// no value.
func writeParameterNames(a *sqlb.Args, names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		if n == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		parts[i] = a.Dialect().QuoteIdent(n)
	}
	return kwReset + " (" + strings.Join(parts, ", ") + ")"
}

// AccessMethod is a table access method, the storage engine a table or a
// materialized view's rows are kept in. It is an open set for the reason
// IndexMethod is: an extension adds one.
type AccessMethod string

// Heap is the access method a stock Postgres has and every table uses unless
// an extension provides another.
const Heap AccessMethod = "heap"

// AlterIndexQuery is ALTER INDEX with no action yet, which has no Build.
type AlterIndexQuery struct{ ix Index }

// AlterIndex starts an ALTER INDEX on a declared index. A rename is
// RenameIndex, which is a statement of its own as every RENAME is.
func AlterIndex(ix Index) *AlterIndexQuery { return &AlterIndexQuery{ix: ix} }

// AlterIndexStatement is a complete ALTER INDEX.
type AlterIndexStatement struct {
	ix   Index
	tail func(a *sqlb.Args) string
}

// SetStorage sets storage parameters on the index. They are the index's own,
// not its table's: fillfactor on a b-tree decides how full a page is packed
// when the index is built, which is a trade a library makes for its own
// read-heavy or write-heavy index and cannot make for a table it does not own.
// A change takes effect as pages are rewritten, so it is usually followed by a
// REINDEX, which is the operator's.
func (x *AlterIndexQuery) SetStorage(first StorageParameter, more ...StorageParameter) *AlterIndexStatement {
	ps := append([]StorageParameter{first}, more...)
	return &AlterIndexStatement{ix: x.ix, tail: func(a *sqlb.Args) string { return writeParameters(a, ps) }}
}

// ResetStorage puts the named storage parameters back to the access method's
// defaults.
func (x *AlterIndexQuery) ResetStorage(first string, more ...string) *AlterIndexStatement {
	names := append([]string{first}, more...)
	return &AlterIndexStatement{ix: x.ix, tail: func(a *sqlb.Args) string { return writeParameterNames(a, names) }}
}

// AttachPartition makes an existing index on a partition the partition's copy
// of this partitioned index. It is how a partitioned index is built without a
// long lock: create the index on each partition, attach each one here, and the
// parent index becomes valid when the last partition has been attached. The
// partition's index must match this one in columns, operator classes and
// uniqueness, which Postgres checks.
func (x *AlterIndexQuery) AttachPartition(part Index) *AlterIndexStatement {
	return &AlterIndexStatement{ix: x.ix, tail: func(a *sqlb.Args) string {
		if part.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwAttachIndex + " " + qualified(a.Dialect(), part.Schema, part.Name)
	}}
}

// Build renders the statement.
func (x *AlterIndexStatement) Build() (ddl.Script, error) {
	if x.tail == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.ix.Name, true, func(a *sqlb.Args) string {
		return kwAlterIndex + " " + qualified(a.Dialect(), x.ix.Schema, x.ix.Name) + " " + x.tail(a)
	})
}

// AlterMaterializedViewQuery is ALTER MATERIALIZED VIEW with no action yet,
// which has no Build.
type AlterMaterializedViewQuery struct{ v MaterializedView }

// AlterMaterializedView starts an ALTER MATERIALIZED VIEW. Renaming the view
// is RenameMaterializedView, moving it is SetObjectSchema and giving it away
// is SetOwner, each a standalone form in Postgres's grammar.
func AlterMaterializedView(v MaterializedView) *AlterMaterializedViewQuery {
	return &AlterMaterializedViewQuery{v: v}
}

// AlterMaterializedViewStatement is a complete ALTER MATERIALIZED VIEW.
type AlterMaterializedViewStatement struct {
	v    MaterializedView
	tail func(a *sqlb.Args) string
}

// RenameColumn renames one of the view's output columns. The names are
// strings rather than column values because a materialized view's columns are
// named by its body's select list, which is where their declaration lives.
func (x *AlterMaterializedViewQuery) RenameColumn(from, to string) *AlterMaterializedViewStatement {
	return &AlterMaterializedViewStatement{v: x.v, tail: func(a *sqlb.Args) string {
		return renameColumn(a, from, to)
	}}
}

// SetStorage sets storage parameters on the view's own table, a fillfactor or
// an autovacuum setting, which a view refreshed in full wants set differently
// from a table written a row at a time.
func (x *AlterMaterializedViewQuery) SetStorage(first StorageParameter, more ...StorageParameter) *AlterMaterializedViewStatement {
	ps := append([]StorageParameter{first}, more...)
	return &AlterMaterializedViewStatement{v: x.v, tail: func(a *sqlb.Args) string { return writeParameters(a, ps) }}
}

// ResetStorage puts the named storage parameters back to their defaults.
func (x *AlterMaterializedViewQuery) ResetStorage(first string, more ...string) *AlterMaterializedViewStatement {
	names := append([]string{first}, more...)
	return &AlterMaterializedViewStatement{v: x.v, tail: func(a *sqlb.Args) string { return writeParameterNames(a, names) }}
}

// SetAccessMethod rewrites the view's rows into another table access method.
func (x *AlterMaterializedViewQuery) SetAccessMethod(m AccessMethod) *AlterMaterializedViewStatement {
	return &AlterMaterializedViewStatement{v: x.v, tail: func(a *sqlb.Args) string {
		if m == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwSetAccessMethod + " " + a.Dialect().QuoteIdent(string(m))
	}}
}

// Build renders the statement.
func (x *AlterMaterializedViewStatement) Build() (ddl.Script, error) {
	if x.tail == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.v.Name, true, func(a *sqlb.Args) string {
		return kwAlterMatView + " " + qualified(a.Dialect(), x.v.Schema, x.v.Name) + " " + x.tail(a)
	})
}

// RenameViewColumnStatement is ALTER VIEW ... RENAME COLUMN.
type RenameViewColumnStatement struct {
	v        View
	from, to string
	ifExists bool
}

// RenameViewColumn renames one of a view's output columns, the migration a
// library writes when a name it exposed changes without its body changing.
// The names are strings because a view's columns are named by its select
// list; every query reading the view through this package names the column
// through its own Col value, which must change in the same release.
func RenameViewColumn(v View, from, to string) *RenameViewColumnStatement {
	return &RenameViewColumnStatement{v: v, from: from, to: to}
}

// IfExists makes renaming a column of a missing view a no-op.
func (x *RenameViewColumnStatement) IfExists() *RenameViewColumnStatement {
	c := *x
	c.ifExists = true
	return &c
}

// Build renders the statement.
func (x *RenameViewColumnStatement) Build() (ddl.Script, error) {
	return buildOne(x.v.Name, true, func(a *sqlb.Args) string {
		return kwAlterView + ifExists(x.ifExists) + " " + qualified(a.Dialect(), x.v.Schema, x.v.Name) + " " + renameColumn(a, x.from, x.to)
	})
}

// renameColumn writes RENAME COLUMN from TO to, failing on either name empty,
// which is the shape every relation's rename takes.
func renameColumn(a *sqlb.Args, from, to string) string {
	if from == "" || to == "" {
		a.Fail(ErrEmptyName)
		return ""
	}
	d := a.Dialect()
	return kwRenameColumn + " " + d.QuoteIdent(from) + " " + kwTo + " " + d.QuoteIdent(to)
}

// SetDefault gives the domain a default, written as a literal of its own Go
// type. A domain's default applies where a column of the domain has none of
// its own, so it is the answer for a value every column of the type should
// start at; a default that differs per column belongs on the column, which is
// why this was left out until the detector asked for it by name.
func (x *AlterDomainQuery[T]) SetDefault(v T) *AlterDomainStatement[T] {
	return &AlterDomainStatement[T]{d: x.d, tail: func(a *sqlb.Args) string {
		return kwSetDefault + " " + a.Literal(v)
	}}
}

// SetDefaultExpr gives the domain a default computed by e, for a default that
// is a call rather than a constant.
func (x *AlterDomainQuery[T]) SetDefaultExpr(e sqlb.Expr[T]) *AlterDomainStatement[T] {
	return &AlterDomainStatement[T]{d: x.d, tail: func(a *sqlb.Args) string {
		return kwSetDefault + " (" + a.Bare(func() string { return e.SQL(a) }) + ")"
	}}
}

// DropDefault removes the domain's default. Columns of the domain that have
// one of their own keep it.
func (x *AlterDomainQuery[T]) DropDefault() *AlterDomainStatement[T] {
	return &AlterDomainStatement[T]{d: x.d, tail: func(*sqlb.Args) string { return kwDropDefault }}
}

// ExtensionMember is a declared object an extension can own. The kind comes
// from the Go type, as it does for Commentable, and the set is closed to this
// package's object values and the one constructor for a table.
//
// An Index and a Statistics are deliberately not members, and this is the one
// thing in this file measured rather than read: Postgres refuses both with
// "cannot add an object of this type to an extension", on 17.10, however well
// formed the statement is. Both were members here for a few hours on
// 2026-09-24 because the member list in the grammar was skimmed rather than
// read, and the rendering test passed. An index that an extension's script
// creates is tied to it with DependsOnExtension instead, which is what that
// statement is for.
type ExtensionMember interface {
	// commentTarget is the object's kind and qualified name, which is the
	// same pair ALTER EXTENSION ADD writes.
	commentTarget(d sqlb.Dialect) (kind, qualified, bare string, err error)
	extensionMember()
}

func (Function) extensionMember()         {}
func (View) extensionMember()             {}
func (MaterializedView) extensionMember() {}
func (Sequence[T]) extensionMember()      {}
func (Enum[T]) extensionMember()          {}
func (Composite[T]) extensionMember()     {}
func (Domain[T]) extensionMember()        {}
func (RangeType[T]) extensionMember()     {}
func (BaseType[T]) extensionMember()      {}
func (Collation) extensionMember()        {}
func (Schema) extensionMember()           {}
func (tableTarget) extensionMember()      {}

// MemberTable names a table as an extension's member, the one member kind that
// is not a declared object value of this package: a table is a sqlb.Table.
func MemberTable(t sqlb.Table) ExtensionMember { return tableTarget{t} }

// ExtensionMemberStatement is ALTER EXTENSION ... ADD or DROP one object.
type ExtensionMemberStatement struct {
	e  Extension
	kw string
	o  ExtensionMember
}

// AddExtensionMember makes an existing object part of an extension, so that
// dropping the extension drops the object and a dump writes the extension
// rather than the object. It is the statement an extension's own upgrade
// script runs, and the reason it is here is that a library shipping its schema
// as an extension has no other way to say what the extension contains.
func AddExtensionMember(e Extension, o ExtensionMember) *ExtensionMemberStatement {
	return &ExtensionMemberStatement{e: e, kw: kwAddMember, o: o}
}

// DropExtensionMember takes an object back out of an extension, leaving the
// object in place; it is what an upgrade runs for an object the new version no
// longer ships.
func DropExtensionMember(e Extension, o ExtensionMember) *ExtensionMemberStatement {
	return &ExtensionMemberStatement{e: e, kw: kwDropMember, o: o}
}

// Build renders the statement.
func (x *ExtensionMemberStatement) Build() (ddl.Script, error) {
	if x.o == nil || x.kw == "" {
		return nil, ddl.ErrZeroValue
	}
	kind, name, _, err := x.o.commentTarget(Dialect{})
	if err != nil {
		return nil, err
	}
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		return kwAlterExtension + " " + a.Dialect().QuoteIdent(x.e.Name) + " " + x.kw + " " + kind + " " + name
	})
}

// SetExtensionSchemaStatement is ALTER EXTENSION ... SET SCHEMA.
type SetExtensionSchemaStatement struct {
	e  Extension
	to Schema
}

// SetExtensionSchema moves an extension's objects into another schema, the
// statement a library runs when it stops leaving an extension's types in the
// search path. Only an extension that declares itself relocatable can move,
// which Postgres checks, and every Table and Col naming one of its types must
// carry the new schema in the same release.
func SetExtensionSchema(e Extension, to Schema) *SetExtensionSchemaStatement {
	return &SetExtensionSchemaStatement{e: e, to: to}
}

// Build renders the statement.
func (x *SetExtensionSchemaStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterExtension + " " + d.QuoteIdent(x.e.Name) + " " + kwSetSchema + " " + d.QuoteIdent(x.to.Name)
	})
}
