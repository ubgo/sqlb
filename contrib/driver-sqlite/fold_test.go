package driversqlite_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/ubgo/sqlb"
	driversqlite "github.com/ubgo/sqlb/contrib/driver-sqlite"
)

// SQLite's built-in lower() and its LIKE fold ASCII only, and its LIKE does
// so by default: 'Ada' LIKE 'ada%' is true, where Postgres answers false. Both
// measured through this driver on 2026-09-28 and pinned here, because they are
// the two things a library moving between SQLite and another engine meets.
// The first is why a fold of 'ÉCOLE' against 'école' misses on SQLite and hits
// on Postgres (under a Unicode collation) and MySQL. The second is why the
// case-sensitive Contains is not portable while ContainsFold is: Contains
// ignores ASCII case here and respects it on Postgres.
func TestFold_SQLiteFoldsASCIIOnly(t *testing.T) {
	db, err := driversqlite.Memory()
	if err != nil {
		t.Fatalf("Memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE fold_probe (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`,
		`INSERT INTO fold_probe VALUES (1, 'Ada@X'), (2, 'ÉCOLE'), (3, 'café')`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	tb := sqlb.Table{Name: "fold_probe", Alias: "f"}
	id, body := sqlb.Col[int64](tb, "id"), sqlb.Col[string](tb, "body")
	for _, tc := range []struct {
		name string
		p    sqlb.Predicate
		want []int64
	}{
		{"EqFold folds ASCII", sqlb.EqFold(body, "ada@x"), []int64{1}},
		{"EqFold does not fold É", sqlb.EqFold(body, "école"), []int64{}},
		{"ContainsFold does not fold É", sqlb.ContainsFold(body, "éco"), []int64{}},
		{"the exact value still matches itself", sqlb.EqFold(body, "ÉCOLE"), []int64{2}},
		{"accents are kept", sqlb.EqFold(body, "cafe"), []int64{}},
		{"the case-sensitive Contains ignores ASCII case here", sqlb.Contains(body, "ADA"), []int64{1}},
		{"Eq is exact", sqlb.Eq(body, "ada@x"), []int64{}},
	} {
		var got int64
		q, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Columns(sqlb.Bind(id, &got)).From(tb).Where(tc.p).
			Order(sqlb.OrderBy(id, sqlb.SortAsc)).Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		ids := []int64{}
		if err := sqlb.Each(ctx, db, q, func() error { ids = append(ids, got); return nil }); err != nil {
			t.Fatalf("%s: %v", q.SQL, err)
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, ids, tc.want)
		}
	}
}
