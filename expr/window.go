package expr

import (
	"github.com/ubgo/sqlb/render"
	"strconv"
	"strings"
)

// FrameMode selects ROWS or RANGE for a window frame. GROUPS is Postgres
// and SQLite only and lives in pg.
type FrameMode string

// The two frame modes every engine has. ROWS counts physical rows; RANGE
// counts peer groups, so tied rows share a frame.
const (
	FrameRows  FrameMode = "ROWS"
	FrameRange FrameMode = "RANGE"
)

// FrameExclude is the EXCLUDE part of a frame. The constants are in pg,
// because MySQL and ClickHouse have no EXCLUDE.
type FrameExclude string

// A FrameExclude is an engine's constant, not one of this package's: Postgres
// and SQLite have EXCLUDE and MySQL and ClickHouse do not, so by the
// universality rule the values belong to the engine packages and only the
// mechanism is here. It therefore reaches the statement text as the author
// wrote it, like JoinType and OrderTerm.Using, and like them it is an author's
// keyword rather than a value from a request.

// FrameModeValues is the canonical iteration order over the frame modes every
// engine here has.
//
// It is NOT the whole set a caller may write, which is why FrameMode is not
// validated against it. GROUPS is the case that proves it: Postgres and SQLite
// have it, MySQL 8 does not, so by the universality rule it is an engine's
// constant rather than one of this package's — and `examples/pgsurface` writes
// GROUPS today. FrameMode is the same shape as FrameExclude: the mechanism is
// here and the vocabulary belongs to the engine, so the value reaches the
// statement as the author wrote it. A closed check here was tried on
// 2026-09-24 and reverted within the hour, because it refused a frame the
// module's own example builds.
var FrameModeValues = []FrameMode{FrameRows, FrameRange}

// FrameBound is one end of a frame. Use the constructors below.
type FrameBound struct {
	kind   string
	offset int
	// expr, when set, replaces the integer offset: an interval literal for a
	// RANGE frame over time. An author constant, never request data.
	expr string
}

const (
	boundUnboundedPreceding = "UNBOUNDED PRECEDING"
	boundUnboundedFollowing = "UNBOUNDED FOLLOWING"
	boundCurrentRow         = "CURRENT ROW"
	boundPreceding          = "PRECEDING"
	boundFollowing          = "FOLLOWING"
)

// UnboundedPreceding starts a frame at the first row of the partition.
func UnboundedPreceding() FrameBound { return FrameBound{kind: boundUnboundedPreceding} }

// UnboundedFollowing ends a frame at the last row of the partition.
func UnboundedFollowing() FrameBound { return FrameBound{kind: boundUnboundedFollowing} }

// CurrentRow bounds a frame at the current row. Under RANGE that means the
// whole peer group, not one row, which is the default frame most people are
// surprised by.
func CurrentRow() FrameBound { return FrameBound{kind: boundCurrentRow} }

// Preceding bounds a frame n rows back under ROWS, or n units back under
// RANGE.
func Preceding(n int) FrameBound { return FrameBound{kind: boundPreceding, offset: n} }

// Following bounds a frame n rows forward.
func Following(n int) FrameBound { return FrameBound{kind: boundFollowing, offset: n} }

// PrecedingExpr and FollowingExpr take a constant SQL expression as the
// offset, for RANGE frames over a time column: PrecedingExpr("interval '7 days'").
func PrecedingExpr(sql string) FrameBound { return FrameBound{kind: boundPreceding, expr: sql} }

// FollowingExpr bounds a frame with an expression rather than a count, for a
// RANGE frame whose offset is an interval.
func FollowingExpr(sql string) FrameBound { return FrameBound{kind: boundFollowing, expr: sql} }

func (fb FrameBound) sql() string {
	switch fb.kind {
	case boundPreceding, boundFollowing:
		if fb.expr != "" {
			return fb.expr + sepSpace + fb.kind
		}
		// The offset is a literal, not a bound parameter: Postgres requires a
		// constant here and rejects a placeholder.
		return strconv.Itoa(fb.offset) + sepSpace + fb.kind
	default:
		return fb.kind
	}
}

// Window is a window specification: PARTITION BY, ORDER BY and a frame. It
// is a value; each method returns a modified copy, so a base window can be
// shared.
type Window struct {
	partition []render.SQLer
	order     []OrderTerm
	mode      FrameMode
	start     *FrameBound
	end       *FrameBound
	exclude   FrameExclude
}

// NewWindow starts an empty window specification.
func NewWindow() Window { return Window{} }

// PartitionBy adds partition expressions. Window is a value, so each method
// returns a copy and a shared base window cannot be mutated by a caller.
func (w Window) PartitionBy(es ...render.SQLer) Window {
	w.partition = append(append([]render.SQLer(nil), w.partition...), es...)
	return w
}

// OrderBy adds ordering terms. Under RANGE the ordering decides what counts
// as a peer, so it changes the frame and not just the sequence.
func (w Window) OrderBy(ts ...OrderTerm) Window {
	w.order = append(append([]OrderTerm(nil), w.order...), ts...)
	return w
}

// Frame sets the frame clause: mode BETWEEN start AND end.
func (w Window) Frame(mode FrameMode, start, end FrameBound) Window {
	w.mode, w.start, w.end = mode, &start, &end
	return w
}

// Exclude sets the EXCLUDE part of the frame. The mechanism is here; the
// values are engine constants.
func (w Window) Exclude(x FrameExclude) Window { w.exclude = x; return w }

// WriteSQL renders the window definition into b. Exported because a named
// WINDOW clause is written by the statement layer.
func (w Window) WriteSQL(b *strings.Builder, a *render.Args) { w.writeSQL(b, a) }

func (w Window) writeSQL(b *strings.Builder, a *render.Args) {
	first := true
	sep := func() {
		if !first {
			b.WriteString(sepSpace)
		}
		first = false
	}
	if len(w.partition) > 0 {
		sep()
		b.WriteString(kwPartitionBy)
		b.WriteString(sepSpace)
		for i, e := range w.partition {
			if i > 0 {
				b.WriteString(sepComma)
			}
			b.WriteString(e.SQL(a))
		}
	}
	if len(w.order) > 0 {
		sep()
		b.WriteString(kwOrderBy)
		b.WriteString(sepSpace)
		for i, t := range w.order {
			if i > 0 {
				b.WriteString(sepComma)
			}
			t.writeSQL(b, a)
		}
	}
	if w.start != nil {
		sep()
		b.WriteString(string(w.mode))
		b.WriteString(" BETWEEN ")
		b.WriteString(w.start.sql())
		b.WriteString(" AND ")
		b.WriteString(w.end.sql())
		if w.exclude != "" {
			b.WriteString(sepSpace)
			b.WriteString(string(w.exclude))
		}
	}
}

// WinFn is a window function or an aggregate that has not yet been given its
// OVER clause. It is not an Expr, so it cannot be selected without OVER; a
// window function without OVER is a SQL error, and the type makes it a
// compile error instead.
type WinFn[T any] struct {
	render func(a *render.Args) string
}

// NewWinFn makes a window function from a render function. Engine packages
// use it for functions the core does not name.
func NewWinFn[T any](render func(a *render.Args) string) WinFn[T] { return WinFn[T]{render} }

// Over attaches an inline window specification and yields the expression.
func (f WinFn[T]) Over(w Window) Expr[T] {
	return New[T](func(a *render.Args) string {
		var b strings.Builder
		b.WriteString(f.render(a))
		b.WriteString(" OVER (")
		w.writeSQL(&b, a)
		b.WriteString(")")
		return b.String()
	})
}

// OverNamed attaches a window declared with Select.Window.
func (f WinFn[T]) OverNamed(name string) Expr[T] {
	return New[T](func(a *render.Args) string {
		return f.render(a) + " OVER " + a.Dialect().QuoteIdent(name)
	})
}

// Over turns any expression, in practice an aggregate, into a window
// function call: sum(x) OVER (...).
func Over[T any](e Expr[T], w Window) Expr[T] { return WinFn[T]{e.SQL}.Over(w) }

// OverNamed is Over with a named window.
func OverNamed[T any](e Expr[T], name string) Expr[T] { return WinFn[T]{e.SQL}.OverNamed(name) }

func winFn0[T any](name string) WinFn[T] {
	return WinFn[T]{func(*render.Args) string { return name + "()" }}
}

// RowNumber renders row_number(), a distinct number per row.
//
// This and the two below are the ranking functions every engine has. All
// three are typed non-null, because a rank always exists for a row.
func RowNumber() WinFn[int64] { return winFn0[int64](fnRowNumber) }

// Rank renders rank(), which leaves gaps after a tie.
func Rank() WinFn[int64] { return winFn0[int64](fnRank) }

// DenseRank renders dense_rank(), which does not leave gaps.
func DenseRank() WinFn[int64] { return winFn0[int64](fnDenseRank) }

// FirstValue renders first_value(e).
//
// This and the two below are typed nullable on purpose: a frame can be empty,
// and the pointer is what makes the caller handle that instead of scanning a
// NULL into a value type at run time.
func FirstValue[T any](e Expr[T]) WinFn[*T] {
	return WinFn[*T]{func(a *render.Args) string { return fnFirstValue + "(" + e.SQL(a) + ")" }}
}

// LastValue renders last_value(e). With the default frame it returns the
// current row, not the partition's last, so it usually needs an explicit
// frame ending at UnboundedFollowing.
func LastValue[T any](e Expr[T]) WinFn[*T] {
	return WinFn[*T]{func(a *render.Args) string { return fnLastValue + "(" + e.SQL(a) + ")" }}
}

// NthValue renders nth_value(e, n), 1-based within the frame.
func NthValue[T any](e Expr[T], n int) WinFn[*T] {
	return WinFn[*T]{func(a *render.Args) string { return fnNthValue + "(" + e.SQL(a) + sepComma + a.Next(n) + ")" }}
}
