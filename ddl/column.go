package ddl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// ColumnSpec is a column definition whatever its Go type, which is what a
// statement's Column and AddColumn take. ColumnDef[T] is the one
// implementation; the interface exists because a statement holds columns of
// many types in one list.
type ColumnSpec interface {
	// ColumnName is the column's bare name.
	ColumnName() string
	// ColumnTable is the table the column was declared on, which a statement
	// checks against its own.
	ColumnTable() expr.Table
	// WriteColumn renders the definition: name, type, nullability, default,
	// with what the statement's column clauses add to it.
	WriteColumn(b *strings.Builder, a *render.Args, x ColumnExtras)
}

// DefaultKind is what a column definition's default is, which a dialect with
// a restricted ADD COLUMN must know to refuse the right ones.
type DefaultKind int

// The kinds of default.
const (
	DefaultNone DefaultKind = iota
	DefaultLiteral
	DefaultExpression
)

// ColumnFacts is what a definition says about NULL and its default, resolved
// against its Go type. A ColumnSpec that implements FactsReporter gets its
// ADD COLUMN checked on a restricted dialect; one that does not is added as
// it renders.
type ColumnFacts struct {
	NotNull bool
	Default DefaultKind
}

// FactsReporter is implemented by a ColumnSpec that can say its ColumnFacts.
type FactsReporter interface {
	ColumnFacts() ColumnFacts
}

// ErrNotAColumn is returned for a ColumnDef built from an expression that is
// not a column made by Col, which has no name to define.
var ErrNotAColumn = errors.New("sqlb/ddl: a column definition needs a column made by Col")

// nullability is what a definition says about NULL: inferred from the Go type,
// or overridden.
type nullability int8

const (
	nullInferred nullability = iota
	nullAllowed
	nullRefused
)

// ColumnDef defines a column from the query column it will be read through, so
// the name and the Go type are written once, in the Col declaration.
//
// Nullability follows the Go type: a Col[*time.Time] is NULL, a Col[time.Time]
// is NOT NULL. Nullable and NotNull override it for the one legitimate
// disagreement, adding a column nullable, backfilling it, then tightening it.
// The SQL type is inferred where the Go type makes it unambiguous and given
// with As everywhere else.
//
// Methods take and return a value, so a definition shared between two
// statements cannot be changed through either.
type ColumnDef[T any] struct {
	col  expr.Expr[T]
	typ  SQLType[T]
	null nullability
	// def renders the default. It takes the column's type as the statement
	// renders it, not as it was when Default was called, because As may come
	// after Default and a JSON column writes its default differently.
	def       func(a *render.Args, typ SQLType[T]) string
	defIsExpr bool
}

// Column starts a definition of c.
func Column[T any](c expr.Expr[T]) ColumnDef[T] { return ColumnDef[T]{col: c} }

// As gives the column's SQL type. It is typed to the column's Go type, so a
// type for another Go type does not compile.
func (c ColumnDef[T]) As(t SQLType[T]) ColumnDef[T] { c.typ = t; return c }

// Default gives the column a default of its own Go type, written as a literal
// by the dialect.
//
// On a column whose type stores JSON, Portable(schema.TypeJSON) or
// RawJSONType, the value is JSON text: a json.RawMessage or []byte as is, a
// string as is, a driver.Valuer such as pg.JSON[T] through Value, anything else
// through json.Marshal. It is checked to be valid JSON, so invalid JSON fails
// Build with ErrInvalidJSON on every engine rather than only where the server
// happens to check, and written as ('<text>'): MySQL refuses a JSON column's
// plain literal default and accepts only that expression form, and Postgres
// and SQLite accept it too, measured on all three. Until 2026-09-22 a
// json.RawMessage default was written as a byte-string literal, because it is
// a []byte, which a jsonb column refuses.
func (c ColumnDef[T]) Default(v T) ColumnDef[T] {
	c.defIsExpr = false
	c.def = func(a *render.Args, typ SQLType[T]) string {
		if storesJSON(typ) {
			return jsonLiteral(a, v)
		}
		return a.Literal(v)
	}
	return c
}

// DefaultExpr gives the column a default computed by an expression of its own
// type, now() or gen_random_uuid() or nextval(...). It is written in
// parentheses, which SQLite requires of an expression default and Postgres
// accepts.
func (c ColumnDef[T]) DefaultExpr(e expr.Expr[T]) ColumnDef[T] {
	c.defIsExpr = true
	c.def = func(a *render.Args, _ SQLType[T]) string { return "(" + e.SQL(a) + ")" }
	return c
}

// Nullable allows NULL whatever the Go type says.
func (c ColumnDef[T]) Nullable() ColumnDef[T] { c.null = nullAllowed; return c }

// NotNull refuses NULL whatever the Go type says.
func (c ColumnDef[T]) NotNull() ColumnDef[T] { c.null = nullRefused; return c }

// ColumnFacts reports the definition's nullability and default.
func (c ColumnDef[T]) ColumnFacts() ColumnFacts {
	f := ColumnFacts{NotNull: !c.nullable()}
	switch {
	case c.def != nil && c.defIsExpr:
		f.Default = DefaultExpression
	case c.def != nil:
		f.Default = DefaultLiteral
	}
	return f
}

// ColumnName is the column's bare name.
func (c ColumnDef[T]) ColumnName() string { return c.col.Name() }

// ColumnTable is the table the column was declared on.
func (c ColumnDef[T]) ColumnTable() expr.Table { return c.col.Table() }

// WriteColumn renders the definition. A failure, a type that cannot be spelled
// or a default that cannot be written, is recorded on a and fails Build.
func (c ColumnDef[T]) WriteColumn(b *strings.Builder, a *render.Args, x ColumnExtras) {
	name := c.col.Name()
	if name == "" {
		a.Fail(ErrNotAColumn)
		return
	}
	d := a.Dialect()
	typ := c.typ
	if typ == nil {
		t, err := inferType[T]()
		if err != nil {
			a.Fail(err)
			return
		}
		typ = Portable[T](t)
	}
	spelling, err := typ.Spelling(d)
	if err != nil {
		a.Fail(err)
		return
	}
	b.WriteString(d.QuoteIdent(name))
	b.WriteString(sepSpace)
	b.WriteString(spelling)
	for _, s := range x.AfterType {
		b.WriteString(sepSpace)
		b.WriteString(s)
	}
	if !x.Computed && !c.nullable() {
		b.WriteString(sepSpace)
		b.WriteString(kwNotNull)
		for _, s := range x.AfterNotNull {
			b.WriteString(sepSpace)
			b.WriteString(s)
		}
	} else if len(x.AfterNotNull) > 0 {
		a.Fail(fmt.Errorf("%w: %s", ErrNoNotNull, name))
		return
	}
	if x.RequiresDefault && c.def == nil {
		a.Fail(fmt.Errorf("%w: %s", ErrNoDefault, name))
		return
	}
	if c.def != nil {
		if x.Computed || x.ValueSource {
			a.Fail(fmt.Errorf("%w: %s", ErrDefaultOnGenerated, name))
			return
		}
		b.WriteString(sepSpace)
		b.WriteString(kwDefault)
		b.WriteString(sepSpace)
		b.WriteString(a.Bare(func() string { return c.def(a, c.typ) }))
	}
	for _, s := range x.Constraints {
		b.WriteString(sepSpace)
		b.WriteString(s)
	}
}

// nullable resolves the definition's nullability against its Go type.
func (c ColumnDef[T]) nullable() bool {
	switch c.null {
	case nullAllowed:
		return true
	case nullRefused:
		return false
	}
	_, ptr := goType[T]()
	return ptr
}
