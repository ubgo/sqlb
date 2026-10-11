package stmt

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb/dialect/postgres"
)

// TestTypestate_LiteralHoleIsRejectedAtBuild pins the one way past the
// typestate: a struct literal of the complete type, which has Build but was
// never completed. It is reachable only from inside this package or by a
// caller who reaches for a zero value on purpose, and Build still refuses it
// with the errors the builders used to return. The hole is documented rather
// than closed, exactly as the Query literal's missing guard is.
func TestTypestate_LiteralHoleIsRejectedAtBuild(t *testing.T) {
	tbl := Table{Name: "t", Alias: "t"}

	u := FilteredUpdate[postgres.Dialect]{dialect: postgres.Dialect{}, table: tbl,
		sets: []Assignment{{col: "a", value: Raw(func(*Args) string { return "1" })}}}
	if _, err := u.Build(); !errors.Is(err, ErrNoWhere) {
		t.Errorf("update literal: got %v, want ErrNoWhere", err)
	}

	x := FilteredDelete[postgres.Dialect]{dialect: postgres.Dialect{}, table: tbl}
	if _, err := x.Build(); !errors.Is(err, ErrNoWhere) {
		t.Errorf("delete literal: got %v, want ErrNoWhere", err)
	}

	i := InsertReady[postgres.Dialect]{dialect: postgres.Dialect{}, table: tbl}
	if _, err := i.Build(); !errors.Is(err, ErrNoAssignments) {
		t.Errorf("insert literal: got %v, want ErrNoAssignments", err)
	}
}
