package pg

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// Named Postgres objects are values, declared once like a sqlb.Table, and every
// statement that creates, alters, drops or comments on one takes the value, so
// no object name is typed twice. The statements below each have a Build that
// returns a ddl.Script, and every method returns a derived statement.

const (
	kwCreateSchema    = "CREATE SCHEMA"
	kwDropSchema      = "DROP SCHEMA"
	kwAlterSchema     = "ALTER SCHEMA"
	kwCreateExtension = "CREATE EXTENSION"
	kwDropExtension   = "DROP EXTENSION"
	kwCreateType      = "CREATE TYPE"
	kwAlterType       = "ALTER TYPE"
	kwDropType        = "DROP TYPE"
	kwAsEnum          = "AS ENUM"
	kwAddValue        = "ADD VALUE"
	kwRenameValue     = "RENAME VALUE"
	kwBefore          = "BEFORE"
	kwAfter           = "AFTER"
	kwIfNotExists     = "IF NOT EXISTS"
)

// ErrEmptyName is returned for an object value with no name.
var ErrEmptyName = errors.New("sqlb/pg: object has no name")

// Schema is a Postgres schema.
type Schema struct{ Name string }

// Extension is a Postgres extension. Each extension sub-package exports the one
// it needs, vector.Extension and trgm.Extension, so a library installs what it
// uses without spelling the name.
type Extension struct{ Name string }

// Enum is a Postgres enum type whose labels are values of the Go type T, so a
// typo in a label is a compile error and a column using the type must be a
// Col[T]. It is also the column's SQL type: ddl.Column(c).As(StatusType).
type Enum[T ~string] struct{ Schema, Name string }

// Spelling is the type's qualified name, as a column definition writes it.
func (e Enum[T]) Spelling(d sqlb.Dialect) (string, error) {
	if e.Name == "" {
		return "", ErrEmptyName
	}
	return qualified(d, e.Schema, e.Name), nil
}

// TypeOf types the enum to T for ddl.SQLType.
func (Enum[T]) TypeOf(T) {}

// qualified writes schema.name, or name alone when the schema is empty.
func qualified(d sqlb.Dialect, schema, name string) string {
	if schema == "" {
		return d.QuoteIdent(name)
	}
	return d.QuoteIdent(schema) + "." + d.QuoteIdent(name)
}

// buildOne renders one DDL statement through inline Args, so every value in
// it is a literal, and fails closed on an empty name.
func buildOne(name string, transactional bool, render func(a *sqlb.Args) string) (ddl.Script, error) {
	if name == "" {
		return nil, ErrEmptyName
	}
	a := sqlb.NewInlineArgs(Dialect{})
	sql := render(a)
	if err := a.Err(); err != nil {
		return nil, err
	}
	return ddl.Script{{SQL: sql, Transactional: transactional}}, nil
}

// CreateSchemaStatement is CREATE SCHEMA.
type CreateSchemaStatement struct {
	s           Schema
	owner       RoleRef
	ifNotExists bool
}

// CreateSchema creates a schema.
func CreateSchema(s Schema) *CreateSchemaStatement { return &CreateSchemaStatement{s: s} }

// IfNotExists makes creating an existing schema a no-op.
func (x *CreateSchemaStatement) IfNotExists() *CreateSchemaStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreateSchemaStatement) Build() (ddl.Script, error) {
	return buildOne(x.s.Name, true, func(a *sqlb.Args) string {
		s := kwCreateSchema + ifNotExists(x.ifNotExists) + " " + a.Dialect().QuoteIdent(x.s.Name)
		if x.owner != nil {
			s += " " + kwAuthorization + " " + x.owner.roleSQL(a)
		}
		return s
	})
}

// DropSchemaStatement is DROP SCHEMA.
type DropSchemaStatement struct {
	s                 Schema
	ifExists, cascade bool
}

// DropSchema drops a schema, which fails while it holds anything unless
// Cascade is given.
func DropSchema(s Schema) *DropSchemaStatement { return &DropSchemaStatement{s: s} }

// IfExists makes dropping a missing schema a no-op.
func (x *DropSchemaStatement) IfExists() *DropSchemaStatement { c := *x; c.ifExists = true; return &c }

// Cascade also drops everything in the schema.
func (x *DropSchemaStatement) Cascade() *DropSchemaStatement { c := *x; c.cascade = true; return &c }

// Build renders the statement.
func (x *DropSchemaStatement) Build() (ddl.Script, error) {
	return buildOne(x.s.Name, true, func(a *sqlb.Args) string {
		return kwDropSchema + ifExists(x.ifExists) + " " + a.Dialect().QuoteIdent(x.s.Name) + cascade(x.cascade)
	})
}

// CreateExtensionStatement is CREATE EXTENSION.
type CreateExtensionStatement struct {
	e           Extension
	schema      Schema
	version     string
	ifNotExists bool
	cascade     bool
}

// CreateExtension installs an extension into the database. It needs a role
// allowed to, which a library's own role often is not; the operator may run
// it instead, which is why it is a statement of its own.
func CreateExtension(e Extension) *CreateExtensionStatement { return &CreateExtensionStatement{e: e} }

// IfNotExists makes installing an installed extension a no-op.
func (x *CreateExtensionStatement) IfNotExists() *CreateExtensionStatement {
	c := *x
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *CreateExtensionStatement) Build() (ddl.Script, error) {
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		s := kwCreateExtension + ifNotExists(x.ifNotExists) + " " + d.QuoteIdent(x.e.Name)
		if x.schema.Name != "" {
			s += " " + kwSchemaOption + " " + d.QuoteIdent(x.schema.Name)
		}
		if x.version != "" {
			s += " " + kwWithVersion + " " + a.Literal(x.version)
		}
		return s + cascade(x.cascade)
	})
}

// DropExtensionStatement is DROP EXTENSION.
type DropExtensionStatement struct {
	e                 Extension
	ifExists, cascade bool
}

// DropExtension removes an extension, which fails while a column uses one of
// its types unless Cascade is given.
func DropExtension(e Extension) *DropExtensionStatement { return &DropExtensionStatement{e: e} }

// IfExists makes removing a missing extension a no-op.
func (x *DropExtensionStatement) IfExists() *DropExtensionStatement {
	c := *x
	c.ifExists = true
	return &c
}

// Cascade also drops what uses the extension, columns included.
func (x *DropExtensionStatement) Cascade() *DropExtensionStatement {
	c := *x
	c.cascade = true
	return &c
}

// Build renders the statement.
func (x *DropExtensionStatement) Build() (ddl.Script, error) {
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		return kwDropExtension + ifExists(x.ifExists) + " " + a.Dialect().QuoteIdent(x.e.Name) + cascade(x.cascade)
	})
}

// CreateEnumStatement is CREATE TYPE ... AS ENUM.
type CreateEnumStatement[T ~string] struct {
	e      Enum[T]
	labels []T
}

// CreateEnum creates an enum with its labels in order. Postgres has no IF NOT
// EXISTS for it, so there is no such method; wrap it in Guarded to make it
// safe to run twice.
func CreateEnum[T ~string](e Enum[T], first T, more ...T) *CreateEnumStatement[T] {
	return &CreateEnumStatement[T]{e: e, labels: append([]T{first}, more...)}
}

// Build renders the statement.
func (x *CreateEnumStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		lits := make([]string, len(x.labels))
		for i, l := range x.labels {
			lits[i] = a.Literal(string(l))
		}
		return kwCreateType + " " + qualified(a.Dialect(), x.e.Schema, x.e.Name) + " " + kwAsEnum + " (" + strings.Join(lits, ", ") + ")"
	})
}

// duplicateCondition is the condition CREATE TYPE raises on a second run.
func (*CreateEnumStatement[T]) duplicateCondition() string { return condDuplicateObject }

// AlterEnumQuery is ALTER TYPE on an enum with no action yet, which has no
// Build.
type AlterEnumQuery[T ~string] struct{ e Enum[T] }

// AlterEnum starts an ALTER TYPE on an enum.
func AlterEnum[T ~string](e Enum[T]) *AlterEnumQuery[T] { return &AlterEnumQuery[T]{e: e} }

// AddValueStatement is ALTER TYPE ... ADD VALUE.
type AddValueStatement[T ~string] struct {
	e           Enum[T]
	v           T
	pos         string
	neighbour   T
	ifNotExists bool
}

// AddValue adds a label, at the end unless Before or After says otherwise. It
// runs inside a transaction since Postgres 12, but the new label cannot be
// used until that transaction commits.
func (x *AlterEnumQuery[T]) AddValue(v T) *AddValueStatement[T] {
	return &AddValueStatement[T]{e: x.e, v: v}
}

// Before places the label before another.
func (x *AddValueStatement[T]) Before(v T) *AddValueStatement[T] {
	c := *x
	c.pos, c.neighbour = kwBefore, v
	return &c
}

// After places the label after another.
func (x *AddValueStatement[T]) After(v T) *AddValueStatement[T] {
	c := *x
	c.pos, c.neighbour = kwAfter, v
	return &c
}

// IfNotExists makes adding an existing label a no-op.
func (x *AddValueStatement[T]) IfNotExists() *AddValueStatement[T] {
	c := *x
	c.ifNotExists = true
	return &c
}

// Build renders the statement.
func (x *AddValueStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		s := kwAlterType + " " + qualified(a.Dialect(), x.e.Schema, x.e.Name) + " " + kwAddValue + ifNotExists(x.ifNotExists) + " " + a.Literal(string(x.v))
		if x.pos != "" {
			s += " " + x.pos + " " + a.Literal(string(x.neighbour))
		}
		return s
	})
}

// AlterEnumStatement is an ALTER TYPE that renames a label or the type.
type AlterEnumStatement[T ~string] struct {
	e    Enum[T]
	tail func(a *sqlb.Args) string
}

// RenameValue renames a label. Both are values of T: the old constant is what
// the code still uses and the new one what it will.
func (x *AlterEnumQuery[T]) RenameValue(from, to T) *AlterEnumStatement[T] {
	return &AlterEnumStatement[T]{e: x.e, tail: func(a *sqlb.Args) string {
		return kwRenameValue + " " + a.Literal(string(from)) + " " + kwTo + " " + a.Literal(string(to))
	}}
}

// RenameTo renames the type to to's name; its schema is unchanged.
func (x *AlterEnumQuery[T]) RenameTo(to Enum[T]) *AlterEnumStatement[T] {
	return &AlterEnumStatement[T]{e: x.e, tail: func(a *sqlb.Args) string {
		return kwRenameTo + " " + a.Dialect().QuoteIdent(to.Name)
	}}
}

// Build renders the statement.
func (x *AlterEnumStatement[T]) Build() (ddl.Script, error) {
	return buildOne(x.e.Name, true, func(a *sqlb.Args) string {
		return kwAlterType + " " + qualified(a.Dialect(), x.e.Schema, x.e.Name) + " " + x.tail(a)
	})
}

// DropTypeStatement is DROP TYPE.
type DropTypeStatement struct {
	schema, name      string
	ifExists, cascade bool
}

// DropType drops an enum, which fails while a column uses it unless Cascade is
// given.
func DropType[T ~string](e Enum[T]) *DropTypeStatement {
	return &DropTypeStatement{schema: e.Schema, name: e.Name}
}

// IfExists makes dropping a missing type a no-op.
func (x *DropTypeStatement) IfExists() *DropTypeStatement { c := *x; c.ifExists = true; return &c }

// Cascade also drops the columns that use the type.
func (x *DropTypeStatement) Cascade() *DropTypeStatement { c := *x; c.cascade = true; return &c }

// Build renders the statement.
func (x *DropTypeStatement) Build() (ddl.Script, error) {
	return buildOne(x.name, true, func(a *sqlb.Args) string {
		return kwDropType + ifExists(x.ifExists) + " " + qualified(a.Dialect(), x.schema, x.name) + cascade(x.cascade)
	})
}

func ifNotExists(on bool) string {
	if on {
		return " " + kwIfNotExists
	}
	return ""
}

func ifExists(on bool) string {
	if on {
		return " " + kwIfExists
	}
	return ""
}

func cascade(on bool) string {
	if on {
		return " " + kwCascade
	}
	return ""
}

// restrict writes the default a DROP has anyway, for a statement that says so
// rather than leaving a reader to wonder whether anyone considered it.
func restrict(on bool) string {
	if on {
		return " " + kwRestrict
	}
	return ""
}

// The conditions a repeated create raises, measured on Postgres 17: a type or
// a foreign key or check constraint raises duplicate_object; a unique or
// primary key or an exclusion constraint raises duplicate_table, because the
// index behind it is a relation and the relation's name is what collides.
const (
	condDuplicateObject = "duplicate_object"
	condDuplicateTable  = "duplicate_table"
)

// Guardable is a statement Postgres has no IF NOT EXISTS for, which knows the
// one condition it raises when repeated. The set is closed, so Guarded cannot
// be handed a statement whose failure it would silently swallow, and each
// statement names its own condition, so Guarded swallows that and no other.
type Guardable interface {
	Build() (ddl.Script, error)
	duplicateCondition() string
}

// GuardedStatement is a statement wrapped so that running it twice is a
// no-op.
type GuardedStatement struct{ inner Guardable }

// Guarded makes a create that Postgres cannot write IF NOT EXISTS for safe to
// repeat, by catching the one condition that statement raises on a second run
// and nothing else:
//
//	DO $sqlb$ BEGIN CREATE TYPE ...; EXCEPTION WHEN duplicate_object THEN NULL; END $sqlb$
//
// Like IF NOT EXISTS it checks the name only: an object of that name with
// another definition is left as it is. The dollar-quote tag is chosen so the
// statement cannot contain it.
func Guarded(s Guardable) *GuardedStatement { return &GuardedStatement{inner: s} }

// Build renders the guarded statement.
func (x *GuardedStatement) Build() (ddl.Script, error) {
	if x.inner == nil {
		return nil, ddl.ErrZeroValue
	}
	s, err := x.inner.Build()
	if err != nil {
		return nil, err
	}
	if len(s) != 1 {
		return nil, fmt.Errorf("sqlb/pg: Guarded wraps one statement, got %d", len(s))
	}
	body := s[0].SQL
	tag := dollarTag(body)
	sql := "DO " + tag + " BEGIN " + body + "; EXCEPTION WHEN " + x.inner.duplicateCondition() + " THEN NULL; END " + tag
	return ddl.Script{{SQL: sql, Transactional: s[0].Transactional}}, nil
}

// dollarTag is a dollar-quote tag the body does not contain, so the body
// cannot end the quote early.
func dollarTag(body string) string {
	for i := 0; ; i++ {
		tag := "$sqlb$"
		if i > 0 {
			tag = "$sqlb" + strconv.Itoa(i) + "$"
		}
		if !strings.Contains(body, tag) {
			return tag
		}
	}
}

// ConstraintStatement is ALTER TABLE ... ADD CONSTRAINT as a statement on its
// own, which is the form Guarded accepts: Postgres raises duplicate_object
// when a constraint of that name exists, and has no IF NOT EXISTS for it.
type ConstraintStatement struct {
	t sqlb.Table
	c Constraint
}

// AddConstraintStatement is ALTER TABLE ADD c as its own statement, for
// Guarded, on the table the constraint's TableConstraint names. On
// pg.AlterTable, AddConstraint is the same action among others.
func AddConstraintStatement(c Constraint) *ConstraintStatement {
	var t sqlb.Table
	if tn, ok := c.(ddl.TableNamer); ok {
		t = tn.ClauseTable()
	}
	return &ConstraintStatement{t: t, c: c}
}

// ErrUnnamedConstraint is returned for an unnamed constraint where a name is
// what makes repeating the statement safe: under Guarded, an unnamed foreign
// key raises no duplicate_object and is simply added a second time.
var ErrUnnamedConstraint = errors.New("sqlb/pg: a guarded constraint needs a name")

// Build renders the statement.
func (x *ConstraintStatement) Build() (ddl.Script, error) {
	if x.c == nil {
		return nil, ddl.ErrZeroValue
	}
	if x.c.constraintName() == "" {
		return nil, ErrUnnamedConstraint
	}
	return AlterTable(x.t).Clause(AddConstraint(x.c)).Build()
}

// duplicateCondition is what a repeated ADD CONSTRAINT raises, which depends
// on whether the constraint owns an index.
func (x *ConstraintStatement) duplicateCondition() string {
	switch x.c.(type) {
	case PrimaryKeyClause, UniqueClause, ExcludeClause:
		return condDuplicateTable
	}
	return condDuplicateObject
}

// RenameSchemaStatement is ALTER SCHEMA ... RENAME TO.
type RenameSchemaStatement struct{ from, to Schema }

// RenameSchema renames a schema. Every object in it moves with it, and every
// Table value naming the old schema must change with it.
func RenameSchema(from, to Schema) *RenameSchemaStatement {
	return &RenameSchemaStatement{from: from, to: to}
}

// Build renders the statement.
func (x *RenameSchemaStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	return buildOne(x.from.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterSchema + " " + d.QuoteIdent(x.from.Name) + " " + kwRenameTo + " " + d.QuoteIdent(x.to.Name)
	})
}
