// Package structbind derives columns, bindings and assignments from a struct's
// tags, so a table's column list is written once instead of once per statement.
//
// It exists for two reasons, and the second is the more interesting.
//
// The first is convenience. Declaring a table's columns and then repeating them
// in a select list, a scan list and an insert is three lists that have to stay
// in step, and other builders remove that with reflection. This does the same.
//
// The second is that it is a test of the library's extension points. Nothing
// here is privileged: it implements stmt.Binding, which is an interface with no
// unexported methods, and builds columns through expr and render like any other
// package. If a reflection-driven binder could not be written outside the core,
// the claim that a community package can replace the result strategy would be
// false, and this package is what says otherwise.
//
// The cost is stated plainly because it is the whole trade. Column names come
// from struct tags, so a typo in a tag is a runtime error where a typo in an
// expr.Col declaration is a compile error, and the Go type of a field is never
// checked against the column's. Code generation over github.com/ubgo/sqlb/schema
// gives the same convenience without giving that up, and is the better answer
// where a schema exists. This is for the case where one does not.
package structbind

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/scan"
	"github.com/ubgo/sqlb/stmt"
)

// TagKey is the struct tag this package reads. It matches the convention most
// Go database packages use, so a struct already annotated for one of them works
// here unchanged.
const TagKey = "db"

// ErrNotAStruct is returned when the type parameter is not a struct.
var ErrNotAStruct = errors.New("structbind: type parameter must be a struct")

// ErrNoColumns is returned when a struct declares no usable fields, which is
// always a mistake: it would produce a statement selecting nothing.
var ErrNoColumns = errors.New("structbind: struct has no exported fields with a db tag")

// ErrDuplicateColumn is returned when two fields resolve to one column name.
//
// It is an error rather than a last-one-wins because the failure is otherwise
// invisible. Two fields mapping to "id" render SELECT "id", "id", which every
// engine accepts; both scan targets then receive the same value and one field
// silently holds the wrong data, with no error at any layer. A struct with an
// ID and an Id field does it, and so does a db tag that happens to match a name
// derived from another field.
//
// The error names both Go fields, because the column name alone does not say
// which two collided in a struct with embedded types.
var ErrDuplicateColumn = errors.New("structbind: two fields resolve to the same column")

// ErrEmptyColumn is returned when a Namer produces an empty column name.
//
// Only a custom Namer can cause this, since an empty tag falls through to the
// Namer and every Namer here returns a non-empty name for a non-empty field.
// It is caught because the alternative renders "u"."" , which is a valid
// identifier that no table has, so the failure would surface as a confusing
// error from the engine rather than as the bug it is.
var ErrEmptyColumn = errors.New("structbind: namer produced an empty column name")

// Mapping is the column list derived from a struct, bound to one table.
//
// It is built once and shared. Nothing in it is mutable and the reflection all
// happens in New, so a Mapping is safe to keep in a package-level variable and
// use from any number of goroutines, which is the point of separating it from
// the per-statement calls below.
type Mapping[T any] struct {
	table  expr.Table
	fields []field
}

// field is one column and the struct offset it maps to.
type field struct {
	column string
	index  []int // reflect index path, so embedded structs work
}

// New derives a Mapping for T against table t.
//
// Fields are included when they carry a db tag, or when they are exported and
// the tag is absent, in which case the column comes from the Namer. A tag of
// "-" excludes a field, which is how a struct carries something that is not a
// column.
//
// The optional Namer sets how an untagged field name becomes a column name.
// The default is Snake, so CreatedAt is created_at; pass Camel for a schema
// whose columns are createdAt, Exact for one whose columns are the Go
// spelling, or any func for anything else. A tag always wins over the Namer.
//
// Only the first Namer is used. It is variadic so that omitting it is the
// ordinary call rather than a nil argument, and because a second one could
// only mean that two parts of a program disagree about the schema.
func New[T any](t expr.Table, namer ...Namer) (*Mapping[T], error) {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil || rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: got %T", ErrNotAStruct, zero)
	}

	name := Namer(Snake)
	if len(namer) > 0 && namer[0] != nil {
		name = namer[0]
	}

	m := &Mapping[T]{table: t}
	collect(rt, nil, name, &m.fields)
	if len(m.fields) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoColumns, rt.Name())
	}
	for _, f := range m.fields {
		if f.column == "" {
			return nil, fmt.Errorf("%w: field %s", ErrEmptyColumn, fieldPath(rt, f.index))
		}
	}
	if err := checkDuplicates(rt, m.fields); err != nil {
		return nil, err
	}
	return m, nil
}

// checkDuplicates fails a Mapping whose fields do not map to distinct columns.
//
// Done here rather than during collection so the error can name both fields:
// the first is only known to be a collision once the second is seen.
func checkDuplicates(rt reflect.Type, fields []field) error {
	seen := make(map[string]field, len(fields))
	for _, f := range fields {
		if first, clash := seen[f.column]; clash {
			return fmt.Errorf("%w: %s and %s both map to %q",
				ErrDuplicateColumn,
				fieldPath(rt, first.index), fieldPath(rt, f.index), f.column)
		}
		seen[f.column] = f
	}
	return nil
}

// fieldPath spells a reflect index path as Go source would, so an error about
// an embedded field says Base.ID rather than an index nobody can read.
func fieldPath(rt reflect.Type, index []int) string {
	var parts []string
	for _, i := range index {
		f := rt.Field(i)
		parts = append(parts, f.Name)
		rt = f.Type
	}
	return strings.Join(parts, ".")
}

// MustNew is New for a package-level variable, where an error can only be a
// programming mistake and there is nobody to return it to.
func MustNew[T any](t expr.Table, namer ...Namer) *Mapping[T] {
	m, err := New[T](t, namer...)
	if err != nil {
		panic(err)
	}
	return m
}

// collect walks a struct type, following embedded structs so a shared base
// struct contributes its columns.
func collect(rt reflect.Type, prefix []int, name Namer, out *[]field) {
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		idx := append(append([]int{}, prefix...), i)

		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get(TagKey) == "" {
			collect(f.Type, idx, name, out)
			continue
		}
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get(TagKey)
		if tag == "-" {
			continue
		}
		col, _, _ := strings.Cut(tag, ",")
		if col == "" {
			col = name(f.Name)
		}
		*out = append(*out, field{column: col, index: idx})
	}
}

// Table returns the table this Mapping was built against.
func (m *Mapping[T]) Table() expr.Table { return m.table }

// Columns returns the column names in struct order.
func (m *Mapping[T]) Columns() []string {
	out := make([]string, len(m.fields))
	for i, f := range m.fields {
		out[i] = f.column
	}
	return out
}

// Exprs returns the columns as select items, for a statement that scans on its
// own or does not scan at all.
func (m *Mapping[T]) Exprs() []render.SQLer {
	out := make([]render.SQLer, len(m.fields))
	for i, f := range m.fields {
		out[i] = column{m.table, f.column}
	}
	return out
}

// Bindings returns one binding per column, pointing into dst.
//
// The pointers are into the struct the caller passed, so everything the binding
// API documents about concurrency applies: the query holds this memory, and two
// goroutines executing it write through the same fields.
func (m *Mapping[T]) Bindings(dst *T) []stmt.Binding {
	v := reflect.ValueOf(dst).Elem()
	out := make([]stmt.Binding, len(m.fields))
	for i, f := range m.fields {
		out[i] = binding{
			column: column{m.table, f.column},
			target: v.FieldByIndex(f.index).Addr().Interface(),
		}
	}
	return out
}

// Reader returns a scan.Reader that produces a fresh T per row.
//
// This is the counterpart to Bindings and the difference is who owns the
// memory. Bindings point into a struct the caller keeps, so a query holding
// them is not safe to share and reading many rows means copying the struct
// inside the loop. A Reader allocates per row and returns values, so
// scan.All gives a []T directly and one Mapping may be read by any number of
// goroutines at once.
//
// Prefer this for reads unless you specifically want the caller-owned struct,
// which is the shape exec.One and the binding API are built around.
func (m *Mapping[T]) Reader() scan.Reader[T] {
	fields := m.fields
	table := m.table
	cols := make([]render.SQLer, len(fields))
	for i, f := range fields {
		cols[i] = column{table, f.column}
	}
	return scan.New(cols, func() ([]any, func() T) {
		var row T
		v := reflect.ValueOf(&row).Elem()
		dests := make([]any, len(fields))
		for i, f := range fields {
			dests[i] = v.FieldByIndex(f.index).Addr().Interface()
		}
		// row is captured, and a new one is allocated on the next call, so
		// nothing is shared between rows or between executions.
		return dests, func() T { return row }
	})
}

// Assignments returns one assignment per column, reading from src, for an
// INSERT row or an UPDATE SET list.
func (m *Mapping[T]) Assignments(src *T) []stmt.Assignment {
	v := reflect.ValueOf(src).Elem()
	out := make([]stmt.Assignment, len(m.fields))
	for i, f := range m.fields {
		val := v.FieldByIndex(f.index).Interface()
		out[i] = stmt.AssignTarget(f.column,
			column{m.table, f.column}.SQL,
			render.Raw(func(a *render.Args) string { return a.Next(val) }))
	}
	return out
}

// column renders a qualified column without a Go type, which is what this
// package gives up in exchange for not being told one.
type column struct {
	table expr.Table
	name  string
}

func (c column) SQL(a *render.Args) string {
	d := a.Dialect()
	if c.table.Alias != "" {
		return d.QuoteIdent(c.table.Alias) + "." + d.QuoteIdent(c.name)
	}
	return d.QuoteIdent(c.name)
}

// binding implements stmt.Binding.
//
// This is the part worth reading. stmt.Binding is an interface with three
// methods and nothing unexported, so a package outside the module can be a
// binding. Without that, a reflection-driven binder would be impossible here:
// bind.Bind takes an Expr[T] and a *T, and reflection has no static T to give
// it.
type binding struct {
	column column
	target any
}

func (b binding) SQL(a *render.Args) string { return b.column.SQL(a) }
func (b binding) Target() any               { return b.target }
func (b binding) ColumnName() string        { return b.column.name }
