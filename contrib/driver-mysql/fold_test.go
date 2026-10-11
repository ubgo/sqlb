package drivermysql_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/ubgo/sqlb"
	drivermysql "github.com/ubgo/sqlb/contrib/driver-mysql"
)

// MySQL decides case and accent sensitivity by the column's collation, and the
// default one decides both the permissive way. Measured on 8.4.11 on
// 2026-09-28, and pinned here because it is the difference a library meets
// first when it moves to MySQL: under utf8mb4_0900_ai_ci a plain Eq ignores
// case and accents, so a lookup that was exact on Postgres and SQLite
// silently is not, and 'café' = 'cafe'. A column declared utf8mb4_bin compares
// by its bytes, as the other two engines do. The fold predicates give the same
// answer under both for ASCII, which sqlbtest's case_fold case proves in
// TestConformance and TestConformance_DefaultCollation.
var (
	collTable = sqlb.Table{Name: "sqlb_fold_collation", Alias: "f"}
	collID    = sqlb.Col[int64](collTable, "id")
	collAI    = sqlb.Col[string](collTable, "ai_text")
	collBin   = sqlb.Col[string](collTable, "bin_text")
)

func collationTable(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, s := range []string{
		"DROP TABLE IF EXISTS sqlb_fold_collation",
		"CREATE TABLE sqlb_fold_collation (id BIGINT PRIMARY KEY, ai_text VARCHAR(50), bin_text VARCHAR(50) COLLATE utf8mb4_bin)",
		"INSERT INTO sqlb_fold_collation VALUES (1, 'Ada@X', 'Ada@X'), (2, 'café', 'café'), (3, 'ÉCOLE', 'ÉCOLE')",
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS sqlb_fold_collation") })
}

func collIDs(t *testing.T, db *sql.DB, p sqlb.Predicate) []int64 {
	t.Helper()
	var id int64
	q, err := sqlb.NewSelect(sqlb.MySQLDialect{}).Columns(sqlb.Bind(collID, &id)).From(collTable).Where(p).
		Order(sqlb.OrderBy(collID, sqlb.SortAsc)).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out := []int64{}
	if err := sqlb.Each(context.Background(), db, q, func() error { out = append(out, id); return nil }); err != nil {
		t.Fatalf("%s: %v", q.SQL, err)
	}
	return out
}

func TestDefaultCollation_EqIgnoresCaseAndAccents(t *testing.T) {
	db := openOrSkip(t)
	collationTable(t, db)
	for _, tc := range []struct {
		name string
		p    sqlb.Predicate
		want []int64
	}{
		{"default collation: Eq ignores case", sqlb.Eq(collAI, "ada@x"), []int64{1}},
		{"default collation: Eq ignores accents", sqlb.Eq(collAI, "cafe"), []int64{2}},
		{"default collation: Contains ignores case", sqlb.Contains(collAI, "ADA"), []int64{1}},
		{"utf8mb4_bin: Eq is exact", sqlb.Eq(collBin, "ada@x"), []int64{}},
		{"utf8mb4_bin: Eq keeps accents", sqlb.Eq(collBin, "cafe"), []int64{}},
		{"utf8mb4_bin: Contains is exact", sqlb.Contains(collBin, "ADA"), []int64{}},
	} {
		if got := collIDs(t, db, tc.p); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestFold_NonASCIIIsTheCollations pins what the fold predicates do beyond
// ASCII on MySQL: lower() folds Unicode under both collations, so 'ÉCOLE'
// matches 'école' on each, and the accent follows the collation, so 'cafe'
// matches 'café' only under the default one.
func TestFold_NonASCIIIsTheCollations(t *testing.T) {
	db := openOrSkip(t)
	collationTable(t, db)
	for _, tc := range []struct {
		name string
		p    sqlb.Predicate
		want []int64
	}{
		{"default collation: EqFold folds Unicode", sqlb.EqFold(collAI, "école"), []int64{3}},
		{"utf8mb4_bin: EqFold folds Unicode", sqlb.EqFold(collBin, "école"), []int64{3}},
		{"utf8mb4_bin: ContainsFold folds Unicode", sqlb.ContainsFold(collBin, "éCo"), []int64{3}},
		{"default collation: EqFold ignores accents", sqlb.EqFold(collAI, "CAFE"), []int64{2}},
		{"utf8mb4_bin: EqFold keeps accents", sqlb.EqFold(collBin, "CAFE"), []int64{}},
	} {
		if got := collIDs(t, db, tc.p); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestOpen_ReportsMatchedRows pins the clientFoundRows default and its escape
// hatch: an UPDATE setting a row to its current value reports 1 affected, as
// on Postgres and SQLite, and 0 only when WithChangedRows asks for MySQL's own
// count. crud_transcript checks the default on every engine; this checks that
// the option still reaches the server.
func TestOpen_ReportsMatchedRows(t *testing.T) {
	dsn, ok := drivermysql.DSNFromEnv()
	if !ok {
		t.Skipf("%s not set: the MySQL proof did not run", drivermysql.EnvDSN)
	}
	for _, tc := range []struct {
		name string
		opts []drivermysql.Option
		want int64
	}{{"default", nil, 1}, {"WithChangedRows", []drivermysql.Option{drivermysql.WithChangedRows()}, 0}} {
		db, err := drivermysql.Open(dsn, tc.opts...)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		ctx := context.Background()
		for _, s := range []string{"DROP TABLE IF EXISTS sqlb_found_rows", "CREATE TABLE sqlb_found_rows (id BIGINT PRIMARY KEY, v BIGINT NOT NULL)", "INSERT INTO sqlb_found_rows VALUES (1, 5)"} {
			if _, err := db.ExecContext(ctx, s); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
		}
		res, err := db.ExecContext(ctx, "UPDATE sqlb_found_rows SET v = 5 WHERE id = 1")
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n, _ := res.RowsAffected(); n != tc.want {
			t.Errorf("%s: %d affected, want %d", tc.name, n, tc.want)
		}
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS sqlb_found_rows")
		_ = db.Close()
	}
}
