package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

var (
	shapeTbl = sqlb.Table{Name: "docs", Alias: "d"}
	tags     = sqlb.Col[[]string](shapeTbl, "tags")
)

func render1(t *testing.T, e sqlb.SQLer) (string, []any) {
	t.Helper()
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(e).From(shapeTbl).Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL, q.Args
}

// TestArrayShapeFunctions covers the dimension and reshaping helpers together,
// because each is one line and what matters is that every one is guarded and
// binds its arguments rather than inlining them.
func TestArrayShapeFunctions(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr sqlb.SQLer
		want string
		args int
	}{
		{"ArrayDims", pg.ArrayDims(tags), `array_dims("d"."tags")`, 0},
		{"ArrayNDims", pg.ArrayNDims(tags), `array_ndims("d"."tags")`, 0},
		{"ArrayLower", pg.ArrayLower(tags, 1), `array_lower("d"."tags", $1)`, 1},
		{"ArrayUpper", pg.ArrayUpper(tags, 1), `array_upper("d"."tags", $1)`, 1},
		{"ArrayReverse", pg.ArrayReverse(tags), `array_reverse("d"."tags")`, 0},
		{"ArraySort", pg.ArraySort(tags), `array_sort("d"."tags")`, 0},
		{"ArraySortDesc", pg.ArraySortDesc(tags, true), `array_sort("d"."tags", $1)`, 1},
		{"ArrayShuffle", pg.ArrayShuffle(tags), `array_shuffle("d"."tags")`, 0},
		{"ArraySample", pg.ArraySample(tags, 3), `array_sample("d"."tags", $1)`, 1},
		{"TrimArray", pg.TrimArray(tags, 2), `trim_array("d"."tags", $1)`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := render1(t, tc.expr)
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("got  %s\nwant it to contain %s", sql, tc.want)
			}
			if len(args) != tc.args {
				t.Fatalf("bound %d args, want %d: %v", len(args), tc.args, args)
			}
		})
	}
}

// TestArrayShapeFunctions_FailClosed pins that every one of them is guarded,
// which is the property a batch of one-line helpers most easily loses: the
// helper renders fine, nobody tries it under another dialect, and it ships
// producing SQL the engine rejects.
func TestArrayShapeFunctions_FailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr sqlb.SQLer
	}{
		{"ArrayDims", pg.ArrayDims(tags)},
		{"ArrayNDims", pg.ArrayNDims(tags)},
		{"ArrayLower", pg.ArrayLower(tags, 1)},
		{"ArrayUpper", pg.ArrayUpper(tags, 1)},
		{"ArrayReverse", pg.ArrayReverse(tags)},
		{"ArraySort", pg.ArraySort(tags)},
		{"ArraySortDesc", pg.ArraySortDesc(tags, false)},
		{"ArrayShuffle", pg.ArrayShuffle(tags)},
		{"ArraySample", pg.ArraySample(tags, 1)},
		{"TrimArray", pg.TrimArray(tags, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(tc.expr).From(shapeTbl).Build()
			if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
				t.Fatalf("rendered under SQLite; want ErrUnsupportedDialect, got %v", err)
			}
		})
	}
}

// TestArrayBounds_AreNullable pins the type choice that carries the trap.
//
// Postgres arrays are not necessarily 1-based, and every one of these returns
// NULL for an empty array rather than 0 or 1. Typing them as a pointer is what
// forces a caller to handle the empty case instead of reading a zero that
// means something else, and the consequence a caller actually meets is that
// the scan destination has to be a pointer too.
func TestArrayBounds_AreNullable(t *testing.T) {
	var lower, upper, ndims *int32
	var dims *string

	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(
			sqlb.Bind(pg.ArrayLower(tags, 1), &lower),
			sqlb.Bind(pg.ArrayUpper(tags, 1), &upper),
			sqlb.Bind(pg.ArrayNDims(tags), &ndims),
			sqlb.Bind(pg.ArrayDims(tags), &dims),
		).
		From(shapeTbl).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	// Binding any of these to a non-pointer is a compile error, which is the
	// whole point and is why this test binds rather than asserting a type.
	if len(q.Targets) != 4 {
		t.Fatalf("got %d targets, want 4", len(q.Targets))
	}
}
