package scan

import (
	"encoding/json"
	"fmt"

	"github.com/ubgo/sqlb/render"
)

// JSON reads a column holding JSON and unmarshals it into T.
//
// It is the other half of the database-side answer to nesting. GroupBy folds a
// join's repeated rows in Go; an aggregate such as json_agg or SQLite's
// json_group_array builds the array in the database instead, returning one row
// per parent with its children already collected. This is what turns that
// column into Go values.
//
//	reader := scan.Map2(
//		scan.Field(authorName),
//		scan.JSON[[]string](pg.JSONAgg(bookTitle)),
//		func(name string, titles []string) author { return author{name, titles} },
//	)
//
// Which of the two to use is a real decision rather than a preference.
//
// Grouping in Go sends the parent's columns once per child, so a wide parent
// with many children is mostly duplicated bytes on the wire; it keeps every
// value SQL-typed, and it works on every engine.
//
// Aggregating in the database sends each parent once, which is dramatically
// less for a wide parent, and it is the only way to get a correct LIMIT on the
// parents: a join's LIMIT counts rows, not parents, so it truncates a parent's
// children. The cost is that the values arrive JSON-typed, so a timestamp
// becomes a string and a large integer can lose precision, and the aggregate
// is engine-specific.
// JSON returns a Reader that unmarshals a JSON column into T.
//
// The column is taken as a SQLer rather than a typed expression on purpose.
// JSON arrives as a different Go type per engine: Postgres hands back
// json.RawMessage for jsonb, SQLite hands back a string because
// json_group_array returns TEXT. Insisting on one of those would make the
// helper unusable on the other engine, and the type parameter that matters is
// T, the shape you get back, which is still checked.
//
// Unmarshalling happens per row into a fresh T, so a JSON reader composes with
// Map2 and stays safe to share exactly like every other Reader.
//
// An SQL NULL becomes the zero T rather than an error: a LEFT JOIN with no
// match produces NULL, which means "no children", and failing there would make
// the common case the error case.
func JSON[T any](e render.SQLer) Reader[T] {
	return Reader[T]{
		cols: []render.SQLer{e},
		alloc: func() ([]any, func() T) {
			var raw []byte
			var out T
			return []any{&rawScanner{&raw}}, func() T {
				if len(raw) == 0 {
					var zero T
					return zero
				}
				if err := json.Unmarshal(raw, &out); err != nil {
					// The error is carried in the value rather than returned,
					// because alloc has no error path and inventing one would
					// change every Reader's shape for this case alone. A
					// malformed aggregate is a schema bug, not a row bug.
					panic(fmt.Errorf("sqlb/scan: JSON column: %w", err))
				}
				return out
			}
		},
	}
}

// rawScanner accepts what a driver hands back for a JSON column, which is
// []byte for some and string for others, and never a typed value.
type rawScanner struct{ dst *[]byte }

func (r *rawScanner) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r.dst = nil
	case []byte:
		// Copied: the driver may reuse its buffer for the next row.
		*r.dst = append([]byte(nil), v...)
	case string:
		*r.dst = []byte(v)
	default:
		return fmt.Errorf("sqlb/scan: cannot read %T as a JSON column", src)
	}
	return nil
}
