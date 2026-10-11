// Package schema describes what tables exist, so that two packages which do not
// know about each other can still be migrated together.
//
// The query layers already solve half of this. A library exports typed columns
// and an application joins across the boundary, because both sides speak one
// vocabulary: expr.Table and expr.Col. Without a shared vocabulary for the
// schema itself, every generator and migration tool built on sqlb invents its
// own description, and the moment a library declares its tables in one format
// and the application uses another, a migration runner holds two incompatible
// accounts of the same database and can order neither.
//
// So this package is not a migration engine and never renders DDL. It is the
// noun those tools agree on. A migration engine, a code generator and an
// introspector all live above it, in contrib or in somebody else's module, and
// none of them has to agree with the others about anything but this.
//
// It imports expr, so a declared table can hand back the expr.Table and typed
// columns the query builder needs, and a caller never maintains the same table
// in two places.
package schema

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/expr"
)

// Type is a logical column type, not a SQL one.
//
// Deliberately logical: "the smallest integer that holds a user id" is a
// portable idea, whereas INTEGER, INT4 and Int32 are three engines disagreeing
// about how to spell it. Mapping a Type to an engine's spelling is the job of
// whatever renders DDL, which is a layer above this one and knows which engine
// it is targeting. Keeping the mapping out of here is what lets one declaration
// serve Postgres and SQLite without a conditional in the declaration.
type Type string

// The portable column types. This is a closed set on purpose: every entry has
// to mean the same thing on Postgres, MySQL, SQLite and ClickHouse, which is
// the same universality rule the core builder follows. An engine-specific type
// belongs in a Raw column, where it is visibly engine-specific.
const (
	TypeText      Type = "text"
	TypeInt       Type = "int"
	TypeBigInt    Type = "bigint"
	TypeBool      Type = "bool"
	TypeFloat     Type = "float"
	TypeBytes     Type = "bytes"
	TypeTimestamp Type = "timestamp"
	TypeDate      Type = "date"
	TypeUUID      Type = "uuid"
	TypeJSON      Type = "json"
	TypeDecimal   Type = "decimal"
)

// TypeValues is the canonical iteration order over the portable types, so a
// DDL renderer walks the same list this package defines rather than drifting
// into its own copy and silently omitting one.
var TypeValues = []Type{
	TypeText, TypeInt, TypeBigInt, TypeBool, TypeFloat, TypeBytes,
	TypeTimestamp, TypeDate, TypeUUID, TypeJSON, TypeDecimal,
}

// Action is what a foreign key does when the referenced row goes away.
type Action string

// The referential actions every engine here implements with the same meaning.
const (
	NoAction   Action = "NO ACTION"
	Restrict   Action = "RESTRICT"
	Cascade    Action = "CASCADE"
	SetNull    Action = "SET NULL"
	SetDefault Action = "SET DEFAULT"
)

// Column is one column's declaration.
//
// Raw is the single documented escape hatch, and it is per column rather than
// per table so that reaching for an engine-specific type costs one field and is
// visible in a diff. When Raw is set, Type is ignored by anything rendering
// DDL; the pair is checked by Validate so the contradiction cannot ship.
type Column struct {
	Name     string
	Type     Type
	Nullable bool
	// Default is rendered verbatim by a DDL renderer. It is a string rather
	// than a value because a default is frequently an expression, and quoting
	// rules belong to the engine, not here.
	Default string
	// Raw overrides Type with an engine's own spelling, for the case this
	// package's portable set cannot express.
	Raw string
	// Comment is carried through to DDL by renderers that support it.
	Comment string
}

// Index is a secondary index declaration. The primary key is on Table, because
// every engine treats it specially.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
}

// ForeignKey is a reference from this table to another.
//
// Table is named by string rather than by a Table value on purpose: the
// referenced table is frequently owned by a different package, and taking a
// value here would force that package to be imported, which is exactly the
// coupling this package exists to avoid. A Set resolves the names.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   Action
	OnUpdate   Action
}

// Table is one table's declaration.
//
// Alias is here so that the schema and the query builder cannot disagree. A
// caller declares the table once and calls Ref to get the expr.Table the
// builder wants, rather than writing the name and alias out a second time where
// a typo is a runtime surprise rather than a compile error.
type Table struct {
	Name        string
	Schema      string
	Alias       string
	Columns     []Column
	PrimaryKey  []string
	Indexes     []Index
	ForeignKeys []ForeignKey
	Comment     string
}

// Ref returns the expr.Table for this declaration, so a schema and the queries
// against it are never written out twice.
func (t Table) Ref() expr.Table {
	return expr.Table{Name: t.Name, Schema: t.Schema, Alias: t.Alias}
}

// Column finds a column by name.
func (t Table) Column(name string) (Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// ColumnNames returns the declared column names in order.
func (t Table) ColumnNames() []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// Col returns the typed expression for one of this table's columns.
//
// It is a function rather than a method because Go methods cannot take type
// parameters. The type argument is the caller's claim about the column's Go
// type, and it is checked against the declaration: asking for Col[int] on a
// text column is the mistake this package can actually catch, and catching it
// at declaration time beats a scan error in production.
func Col[T any](t Table, name string) (expr.Expr[T], error) {
	c, ok := t.Column(name)
	if !ok {
		return expr.Expr[T]{}, fmt.Errorf("schema: table %q has no column %q", t.Name, name)
	}
	if err := checkGoType[T](c); err != nil {
		return expr.Expr[T]{}, fmt.Errorf("schema: %s.%s: %w", t.Name, name, err)
	}
	return expr.Col[T](t.Ref(), name), nil
}

// MustCol is Col for a package-level declaration, panicking instead of
// returning an error.
//
// A library declares its columns in a var block, where there is no error to
// return: package initialisation has no error path, so every consumer would
// otherwise write the same three-line must helper around Col. Panicking is
// right here and nowhere else in this module, for the reason regexp.MustCompile
// panics — the argument is a literal the author wrote, so a failure is a
// program that is wrong rather than an input that is bad, it happens at package
// load, and it therefore cannot survive the first run of any test.
//
// Use Col wherever the table or the column name comes from outside the source,
// because there the failure is data and an error is the honest answer.
func MustCol[T any](t Table, name string) expr.Expr[T] {
	c, err := Col[T](t, name)
	if err != nil {
		panic(err)
	}
	return c
}

// ErrTypeMismatch reports that a Go type does not match a column's declared
// type.
var ErrTypeMismatch = errors.New("schema: Go type does not match the declared column type")

// checkGoType compares a requested Go type against the declaration. A Raw
// column is not checked, because this package does not know what the engine's
// own spelling means; that is the cost of the escape hatch and it is documented
// on Raw.
func checkGoType[T any](c Column) error {
	if c.Raw != "" {
		return nil
	}
	var zero T
	got := fmt.Sprintf("%T", zero)
	if got == "<nil>" {
		return nil // an interface or any: the caller has opted out
	}
	want, known := goTypes[c.Type]
	if !known {
		return nil
	}
	base := strings.TrimPrefix(got, "*")
	if base != got && !c.Nullable {
		return fmt.Errorf("%w: column is NOT NULL but was asked for %s", ErrTypeMismatch, got)
	}
	for _, w := range want {
		if base == w {
			return nil
		}
	}
	return fmt.Errorf("%w: column is %s, which is %s, but was asked for %s",
		ErrTypeMismatch, c.Type, strings.Join(want, " or "), got)
}

// goTypes is the Go types each portable type accepts. More than one is allowed
// where the choice is genuinely the caller's, such as whether a timestamp
// arrives as time.Time or as a string.
var goTypes = map[Type][]string{
	TypeText:      {"string"},
	TypeInt:       {"int", "int32", "int64"},
	TypeBigInt:    {"int64", "uint64"},
	TypeBool:      {"bool"},
	TypeFloat:     {"float32", "float64"},
	TypeBytes:     {"[]uint8"},
	TypeTimestamp: {"time.Time", "string"},
	TypeDate:      {"time.Time", "string"},
	TypeUUID:      {"string", "[]uint8"},
	TypeJSON:      {"string", "[]uint8"},
	TypeDecimal:   {"string", "float64"},
}

// Validate reports every problem with a declaration, rather than the first.
//
// Returning all of them matters because a schema is usually written in one
// sitting: fixing one typo only to be told about the next is the slowest
// possible way to find four mistakes.
func (t Table) Validate() error {
	var errs []error
	if t.Name == "" {
		errs = append(errs, errors.New("table has no name"))
	}
	if len(t.Columns) == 0 {
		errs = append(errs, fmt.Errorf("table %q declares no columns", t.Name))
	}
	seen := map[string]bool{}
	for _, c := range t.Columns {
		switch {
		case c.Name == "":
			errs = append(errs, fmt.Errorf("table %q has a column with no name", t.Name))
		case seen[c.Name]:
			errs = append(errs, fmt.Errorf("table %q declares column %q twice", t.Name, c.Name))
		}
		seen[c.Name] = true
		if c.Raw == "" && c.Type == "" {
			errs = append(errs, fmt.Errorf("column %s.%s has neither Type nor Raw", t.Name, c.Name))
		}
		if c.Raw != "" && c.Type != "" {
			errs = append(errs, fmt.Errorf("column %s.%s sets both Type and Raw; Raw would win silently", t.Name, c.Name))
		}
	}
	errs = append(errs, t.checkRefs(seen)...)
	return errors.Join(errs...)
}

// checkRefs validates every place a declaration names one of its own columns.
func (t Table) checkRefs(cols map[string]bool) []error {
	var errs []error
	for _, pk := range t.PrimaryKey {
		if !cols[pk] {
			errs = append(errs, fmt.Errorf("table %q has primary key column %q that it does not declare", t.Name, pk))
		}
	}
	for _, ix := range t.Indexes {
		if len(ix.Columns) == 0 {
			errs = append(errs, fmt.Errorf("index %q on %q names no columns", ix.Name, t.Name))
		}
		for _, c := range ix.Columns {
			if !cols[c] {
				errs = append(errs, fmt.Errorf("index %q on %q names column %q that the table does not declare", ix.Name, t.Name, c))
			}
		}
	}
	for _, fk := range t.ForeignKeys {
		if fk.RefTable == "" {
			errs = append(errs, fmt.Errorf("foreign key %q on %q names no referenced table", fk.Name, t.Name))
		}
		if len(fk.Columns) != len(fk.RefColumns) {
			errs = append(errs, fmt.Errorf("foreign key %q on %q has %d columns but %d referenced columns",
				fk.Name, t.Name, len(fk.Columns), len(fk.RefColumns)))
		}
		for _, c := range fk.Columns {
			if !cols[c] {
				errs = append(errs, fmt.Errorf("foreign key %q on %q names column %q that the table does not declare", fk.Name, t.Name, c))
			}
		}
	}
	return errs
}
