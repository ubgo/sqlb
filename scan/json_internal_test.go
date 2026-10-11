package scan

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// An internal test rather than an external one: it exercises the scanner that
// sits between a driver and json.Unmarshal, which is deliberately unexported.
// Exporting alloc to reach it from outside would widen the API for a test.

var internalCol = expr.Col[string](expr.Table{Name: "t", Alias: "t"}, "doc")

var _ = render.Quote

// TestJSON_AcceptsWhatDriversActuallyReturn pins the four shapes a driver can
// hand back for a JSON column, including the one no driver produces.
//
// database/sql does not normalise this: some drivers return []byte for a text
// or jsonb column and others return string, and a NULL arrives as nil. A
// reader that handled only one of those would work against the driver it was
// written with and fail against the next.
//
// The error branch is here because no driver reaches it, which is exactly why
// it would otherwise go untested: it is the path that fires when a future
// driver returns something unexpected, and it should say so rather than panic.
func TestJSON_AcceptsWhatDriversActuallyReturn(t *testing.T) {
	read := func(t *testing.T, src any) ([]string, error) {
		t.Helper()
		r := JSON[[]string](internalCol)
		dests, value := r.alloc()
		s, ok := dests[0].(sql.Scanner)
		if !ok {
			t.Fatalf("the destination is %T, which cannot receive a driver value", dests[0])
		}
		if err := s.Scan(src); err != nil {
			return nil, err
		}
		return value(), nil
	}

	t.Run("bytes", func(t *testing.T) {
		got, err := read(t, []byte(`["a","b"]`))
		if err != nil || fmt.Sprint(got) != "[a b]" {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("string", func(t *testing.T) {
		got, err := read(t, `["a","b"]`)
		if err != nil || fmt.Sprint(got) != "[a b]" {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("nil is the zero value, not an error", func(t *testing.T) {
		got, err := read(t, nil)
		if err != nil || got != nil {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("anything else is a named error", func(t *testing.T) {
		_, err := read(t, 42)
		if err == nil {
			t.Fatal("an int should not be readable as a JSON column")
		}
		if !strings.Contains(err.Error(), "int") {
			t.Errorf("the error should name the type it got: %v", err)
		}
	})
}

// TestJSON_CopiesTheDriverBuffer pins a subtlety that would corrupt every row
// but the last.
//
// A driver may reuse its scratch buffer for the next row, so a reader that
// kept the slice it was handed would find earlier rows rewritten under it. The
// bytes are copied for that reason, and this is what says so.
func TestJSON_CopiesTheDriverBuffer(t *testing.T) {
	r := JSON[[]string](internalCol)
	dests, value := r.alloc()
	s := dests[0].(sql.Scanner)

	buf := []byte(`["first"]`)
	if err := s.Scan(buf); err != nil {
		t.Fatal(err)
	}
	// The driver reuses the buffer.
	copy(buf, `["XXXXX"]`)

	if got := value(); fmt.Sprint(got) != "[first]" {
		t.Fatalf("got %v; the reader kept the driver's buffer instead of copying it", got)
	}
}
