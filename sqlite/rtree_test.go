package sqlite_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/sqlite"
)

var (
	boxes = sqlb.Table{Name: "boxes", Alias: "b"}
	boxID = sqlb.Col[int64](boxes, "id")
	minX  = sqlb.Col[float64](boxes, "min_x")
	maxX  = sqlb.Col[float64](boxes, "max_x")
	minY  = sqlb.Col[float64](boxes, "min_y")
	maxY  = sqlb.Col[float64](boxes, "max_y")
)

// TestRTree_Renders pins the R*Tree statement in both coordinate kinds and
// application_id, and what each refuses.
func TestRTree_Renders(t *testing.T) {
	one := func(s ddl.Script, err error) string {
		t.Helper()
		if err != nil || len(s) != 1 {
			t.Fatalf("%v, %v", s, err)
		}
		return s[0].SQL
	}
	if got := one(sqlite.CreateRTree(boxes).IfNotExists().Columns(boxID, sqlite.Dimension(minX, maxX), sqlite.Dimension(minY, maxY)).Build()); got != `CREATE VIRTUAL TABLE IF NOT EXISTS "boxes" USING rtree("id", "min_x", "max_x", "min_y", "max_y")` {
		t.Errorf("got %s", got)
	}
	ix := sqlb.Col[int32](boxes, "lo")
	iy := sqlb.Col[int32](boxes, "hi")
	if got := one(sqlite.CreateRTree(boxes).Integer().Columns(boxID, sqlite.Dimension(ix, iy)).Build()); got != `CREATE VIRTUAL TABLE "boxes" USING rtree_i32("id", "lo", "hi")` {
		t.Errorf("got %s", got)
	}
	if got := one(sqlite.SetApplicationID(0x53514C42).Build()); got != `PRAGMA application_id = 1397836866` {
		t.Errorf("got %s", got)
	}
	d := sqlite.Dimension(minX, maxX)
	if _, err := sqlite.CreateRTree(boxes).Columns(boxID, d, d, d, d, d, d).Build(); !errors.Is(err, sqlite.ErrRTreeDims) {
		t.Errorf("six dimensions: %v", err)
	}
	other := sqlb.Col[float64](sqlb.Table{Name: "other"}, "x")
	if _, err := sqlite.CreateRTree(boxes).Columns(boxID, sqlite.Dimension(other, maxX)).Build(); !errors.Is(err, ddl.ErrForeignColumn) {
		t.Errorf("a column of another table: %v", err)
	}
}
