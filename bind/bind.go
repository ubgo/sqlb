// Package bind is the result strategy that writes rows through pointers the
// caller supplied.
//
// It is one of two strategies and the shorter one to write: Bind(col, &dst)
// pairs a column with a destination, the column list and the scan targets are
// derived from the same slice in the same order, and nothing has to be kept in
// step by hand. That property is the reason the library exists in this shape.
//
// The cost is that a bound query holds pointers into caller memory. It is
// therefore NOT SAFE FOR CONCURRENT USE: two goroutines executing one query
// interleave each other's rows into the same variables, which is a DATA RACE
// and reads like inert data. One and Each refuse an overlapping execution with
// ErrQueryInUse, but that is detection, not prevention. Build the query where
// its destinations live.
//
// github.com/ubgo/sqlb/scan is the peer strategy without that hazard: it
// returns values rather than writing through pointers, so one Reader is safe to
// share across any number of concurrent executions. Neither is deprecated.
package bind

import (
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/stmt"
)

// binding pairs a SELECT expression with the pointer it scans into.
//
// It is unexported because it exists only to satisfy stmt.Binding. Keeping the
// fields private is what stops a caller from building one with a mismatched
// expression and target, which the type parameter on Bind otherwise prevents.
type binding struct {
	render func(a *render.Args) string
	target any
	name   string
}

func (b binding) SQL(a *render.Args) string { return b.render(a) }
func (b binding) Target() any               { return b.target }
func (b binding) ColumnName() string        { return b.name }

// Bind pairs e with dst. The compiler rejects a dst whose type is not *T.
//
// The pointer is captured, not the value, and it lives as long as the query
// does. Two consequences worth knowing before they bite: Each writes every
// row through the same pointer, so a callback keeping a row must copy it; and
// a query is not safe for concurrent use, because two goroutines would write
// through these pointers at once, which is a data race. Build the query where its
// destination lives. See One and ErrQueryInUse.
func Bind[T any](e expr.Expr[T], dst *T) stmt.Binding {
	return binding{render: e.SQL, target: dst, name: e.Name()}
}

// Bindings concatenates binding groups in order. A composite destination uses
// it to join the core's bindings with each plugin's.
func Bindings(groups ...[]stmt.Binding) []stmt.Binding {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	out := make([]stmt.Binding, 0, n)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// Targets extracts the scan pointers of bs in order, for rows.Scan.
func Targets(bs []stmt.Binding) []any { return stmt.Targets(bs) }
