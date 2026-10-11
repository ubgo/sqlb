package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The Postgres forms built after the four tiers: a view column's default,
// row-level security's force and disable, and composite types with their
// attributes. Each was a raw recipe in docs/ddl/not-built.md before.

const (
	kwAlterColumnView  = "ALTER COLUMN"
	kwDropDefaultView  = "DROP DEFAULT"
	kwDisableRLS       = "DISABLE ROW LEVEL SECURITY"
	kwForceRLS         = "FORCE ROW LEVEL SECURITY"
	kwNoForceRLS       = "NO FORCE ROW LEVEL SECURITY"
	kwAsComposite      = "AS"
	kwAddAttribute     = "ADD ATTRIBUTE"
	kwDropAttribute    = "DROP ATTRIBUTE IF EXISTS"
	kwAlterAttribute   = "ALTER ATTRIBUTE"
	kwRenameAttribute  = "RENAME ATTRIBUTE"
	kwTypeKeyword      = "TYPE"
	kwDropTypeIfExists = "DROP TYPE"
)

// AlterViewStatement is ALTER VIEW ... ALTER COLUMN, the default of a view's
// column, which an INSERT through an updatable view uses.
type AlterViewStatement struct {
	v    View
	col  ddl.ColumnRef
	tail func(a *sqlb.Args) string
}

// SetViewColumnDefault gives a view's column a default, a literal of the
// column's own Go type. It is a function rather than a method because the
// value's type comes from the column.
func SetViewColumnDefault[T any](v View, col sqlb.Expr[T], value T) *AlterViewStatement {
	return &AlterViewStatement{v: v, col: col, tail: func(a *sqlb.Args) string { return kwSetDefault + " " + a.Literal(value) }}
}

// DropViewColumnDefault removes a view column's default.
func DropViewColumnDefault[T any](v View, col sqlb.Expr[T]) *AlterViewStatement {
	return &AlterViewStatement{v: v, col: col, tail: func(*sqlb.Args) string { return kwDropDefaultView }}
}

// Build renders the statement.
func (x *AlterViewStatement) Build() (ddl.Script, error) {
	if x.tail == nil {
		return nil, ddl.ErrZeroValue
	}
	if x.col == nil || x.col.Name() == "" {
		return nil, ddl.ErrNotAColumn
	}
	return buildOne(x.v.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterView + " " + qualified(d, x.v.Schema, x.v.Name) + " " + kwAlterColumnView + " " + d.QuoteIdent(x.col.Name()) + " " + x.tail(a)
	})
}

// DisableRowLevelSecurity turns row-level security off for the table, the
// inverse of EnableRowLevelSecurity.
func DisableRowLevelSecurity() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwDisableRLS }}
}

// ForceRowLevelSecurity applies the table's policies to its owner too.
// Postgres exempts the owner otherwise, so a library whose own role owns its
// tables needs this for its policies to bind it.
func ForceRowLevelSecurity() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwForceRLS }}
}

// NoForceRowLevelSecurity exempts the owner again, the inverse of
// ForceRowLevelSecurity.
func NoForceRowLevelSecurity() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwNoForceRLS }}
}

// Composite is a composite type, declared once: a named row of typed
// attributes, CREATE TYPE ... AS (...). It is a ddl.SQLType[T] for the Go type
// T a column of it scans into, a struct with a Scan method say, so
// ddl.Column(c).As(money) compiles only on a Col[T].
type Composite[T any] struct{ Schema, Name string }

// Spelling is the type's qualified name, as a column definition writes it.
func (c Composite[T]) Spelling(d sqlb.Dialect) (string, error) {
	if c.Name == "" {
		return "", ErrEmptyName
	}
	return qualified(d, c.Schema, c.Name), nil
}

// TypeOf types the composite to T for ddl.SQLType.
func (Composite[T]) TypeOf(T) {}

func (c Composite[T]) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjType, qualified(d, c.Schema, c.Name), c.Name, nil
}

// Attribute is one attribute of a composite type: a name and a typed SQL
// type, the type's Go parameter checked when the attribute is made and erased
// after, since a composite's attributes are of different types.
type Attribute struct {
	name  string
	spell func(d sqlb.Dialect) (string, error)
}

// Attr makes an attribute of type t.
func Attr[T any](name string, t ddl.SQLType[T]) Attribute {
	return Attribute{name: name, spell: t.Spelling}
}

// parts renders the attribute's quoted name and its type separately, so an
// action can put a keyword between them; a name containing a space stays one
// identifier, which splitting the joined text would break.
func (at Attribute) parts(a *sqlb.Args) (name, typ string, ok bool) {
	if at.name == "" || at.spell == nil {
		a.Fail(ddl.ErrZeroValue)
		return "", "", false
	}
	d := a.Dialect()
	sp, err := at.spell(d)
	if err != nil {
		a.Fail(err)
		return "", "", false
	}
	return d.QuoteIdent(at.name), sp, true
}

func (at Attribute) write(a *sqlb.Args) string {
	name, typ, ok := at.parts(a)
	if !ok {
		return ""
	}
	return name + " " + typ
}

// CreateCompositeStatement is CREATE TYPE ... AS (...). Postgres has no IF
// NOT EXISTS for it; wrap it in Guarded.
type CreateCompositeStatement[T any] struct {
	c     Composite[T]
	attrs []Attribute
}

// CreateComposite creates a composite type with at least one attribute.
func CreateComposite[T any](c Composite[T], first Attribute, more ...Attribute) *CreateCompositeStatement[T] {
	return &CreateCompositeStatement[T]{c: c, attrs: append([]Attribute{first}, more...)}
}

// Build renders the statement.
func (x *CreateCompositeStatement[T]) Build() (ddl.Script, error) {
	if len(x.attrs) == 0 {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.c.Name, true, func(a *sqlb.Args) string {
		parts := make([]string, len(x.attrs))
		for i, at := range x.attrs {
			parts[i] = at.write(a)
		}
		return kwCreateType + " " + qualified(a.Dialect(), x.c.Schema, x.c.Name) + " " + kwAsComposite + " (" + strings.Join(parts, ", ") + ")"
	})
}

// duplicateCondition is what a repeated CREATE TYPE raises.
func (*CreateCompositeStatement[T]) duplicateCondition() string { return condDuplicateObject }

// AlterCompositeQuery is ALTER TYPE on a composite with no action yet, which
// has no Build.
type AlterCompositeQuery[T any] struct{ c Composite[T] }

// AlterComposite starts an ALTER TYPE on a composite.
func AlterComposite[T any](c Composite[T]) *AlterCompositeQuery[T] {
	return &AlterCompositeQuery[T]{c: c}
}

// AlterCompositeStatement is a complete ALTER TYPE on a composite, one
// action.
type AlterCompositeStatement[T any] struct {
	c    Composite[T]
	tail func(a *sqlb.Args) string
	// cascade propagates the change to the tables and types that use this
	// one, which is the only way an attribute change reaches a typed table.
	cascade bool
}

// Cascade also changes everything that uses the type: a typed table made with
// AlterTable's OfType, and any composite type holding this one. Without it
// Postgres refuses the change while anything depends on it, which is the safe
// default and not always the one that can be acted on.
func (x *AlterCompositeStatement[T]) Cascade() *AlterCompositeStatement[T] {
	c := *x
	c.cascade = true
	return &c
}

// AddAttribute adds an attribute; existing values have it NULL.
func (x *AlterCompositeQuery[T]) AddAttribute(at Attribute) *AlterCompositeStatement[T] {
	return &AlterCompositeStatement[T]{c: x.c, tail: func(a *sqlb.Args) string { return kwAddAttribute + " " + at.write(a) }}
}

// DropAttribute drops an attribute if it exists, and its data in every value.
func (x *AlterCompositeQuery[T]) DropAttribute(name string) *AlterCompositeStatement[T] {
	return &AlterCompositeStatement[T]{c: x.c, tail: func(a *sqlb.Args) string {
		if name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwDropAttribute + " " + a.Dialect().QuoteIdent(name)
	}}
}

// AlterAttributeType changes an attribute's type to the one at gives; the
// attribute is named by at.
func (x *AlterCompositeQuery[T]) AlterAttributeType(at Attribute) *AlterCompositeStatement[T] {
	return &AlterCompositeStatement[T]{c: x.c, tail: func(a *sqlb.Args) string {
		name, typ, ok := at.parts(a)
		if !ok {
			return ""
		}
		return kwAlterAttribute + " " + name + " " + kwTypeKeyword + " " + typ
	}}
}

// RenameAttribute renames an attribute.
func (x *AlterCompositeQuery[T]) RenameAttribute(from, to string) *AlterCompositeStatement[T] {
	return &AlterCompositeStatement[T]{c: x.c, tail: func(a *sqlb.Args) string {
		if from == "" || to == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		d := a.Dialect()
		return kwRenameAttribute + " " + d.QuoteIdent(from) + " " + kwTo + " " + d.QuoteIdent(to)
	}}
}

// Build renders the statement.
func (x *AlterCompositeStatement[T]) Build() (ddl.Script, error) {
	if x.tail == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.c.Name, true, func(a *sqlb.Args) string {
		return kwAlterType + " " + qualified(a.Dialect(), x.c.Schema, x.c.Name) + " " + x.tail(a) + cascade(x.cascade)
	})
}

// DropComposite drops a composite type, which fails while a column uses it
// unless Cascade is given.
func DropComposite[T any](c Composite[T]) *DropObjectStatement {
	return &DropObjectStatement{kw: kwDropTypeIfExists, schema: c.Schema, name: c.Name}
}
