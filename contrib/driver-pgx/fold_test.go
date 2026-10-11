package driverpgx_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/sqlbtest"
)

// portablePostgres is Postgres without the ILIKE capability: it forwards what
// the conformance suite needs and omits SupportsILike, so the fold predicates
// take the lower() LIKE lower() spelling every other engine gets.
//
// Running the suite under it proves the two spellings agree on the one engine
// that can run both. Without it, "ILIKE on Postgres, lower() elsewhere" would
// be two implementations each tested on different servers, and a difference
// between them would look like an engine difference.
type portablePostgres struct{}

func (portablePostgres) Placeholder(n int) string   { return postgres.Dialect{}.Placeholder(n) }
func (portablePostgres) QuoteIdent(s string) string { return postgres.Dialect{}.QuoteIdent(s) }
func (portablePostgres) Name() string               { return postgres.Dialect{}.Name() }
func (portablePostgres) SupportsLikeEscape() bool   { return true }
func (portablePostgres) TextParam(ph string) string { return postgres.Dialect{}.TextParam(ph) }
func (portablePostgres) QuoteLiteral(v any) (string, error) {
	return postgres.Dialect{}.QuoteLiteral(v)
}

// TestConformance_PortableFoldSpelling runs the whole suite on Postgres with
// the fold predicates spelled lower() LIKE lower(), and requires the same ids
// ILIKE produced in TestConformance.
func TestConformance_PortableFoldSpelling(t *testing.T) {
	db := openOrSkip(t)
	if _, ok := any(portablePostgres{}).(sqlb.CaseInsensitiveLike); ok {
		t.Fatal("portablePostgres declares ILIKE, so this test would run the ILIKE spelling twice")
	}
	sqlbtest.Run(t, sqlbtest.Config{DB: db, Dialect: portablePostgres{}, Setup: setupDDL, Teardown: teardownDDL})
}

// Beyond ASCII, a Postgres fold is the column's collation's, and these are the
// two collations every Postgres 17 has: "C", which folds ASCII only, and
// pg_c_utf8, the builtin provider's, which folds Unicode. Measured on
// 2026-09-28 on 17 and 18. The database default is whatever initdb chose, so a
// table that relies on non-ASCII folding names its collation.
var (
	foldTable = sqlb.Table{Name: "sqlb_fold_collation", Alias: "f"}
	foldID    = sqlb.Col[int64](foldTable, "id")
	foldC     = sqlb.Col[string](foldTable, "c_text")
	foldUTF8  = sqlb.Col[string](foldTable, "utf8_text")
)

func foldIDs(t *testing.T, db *sql.DB, d sqlb.Dialect, p sqlb.Predicate) []int64 {
	t.Helper()
	var id int64
	q, err := sqlb.NewSelect(d).Columns(sqlb.Bind(foldID, &id)).From(foldTable).Where(p).
		Order(sqlb.OrderBy(foldID, sqlb.SortAsc)).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out := []int64{}
	if err := sqlb.Each(context.Background(), db, q, func() error { out = append(out, id); return nil }); err != nil {
		t.Fatalf("%s: %v", q.SQL, err)
	}
	return out
}

func TestFold_NonASCIIIsTheColumnsCollation(t *testing.T) {
	db := openOrSkip(t)
	ctx := context.Background()
	for _, s := range []string{
		`DROP TABLE IF EXISTS sqlb_fold_collation`,
		`CREATE TABLE sqlb_fold_collation (id BIGINT PRIMARY KEY, c_text TEXT COLLATE "C", utf8_text TEXT COLLATE pg_c_utf8)`,
		`INSERT INTO sqlb_fold_collation VALUES (1, 'ÉCOLE', 'ÉCOLE'), (2, 'café', 'café')`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS sqlb_fold_collation`) })

	for _, d := range []sqlb.Dialect{sqlb.PostgresDialect{}, portablePostgres{}} {
		for _, tc := range []struct {
			name string
			p    sqlb.Predicate
			want []int64
		}{
			{"EqFold under C folds ASCII only", sqlb.EqFold(foldC, "école"), []int64{}},
			{"ContainsFold under C folds ASCII only", sqlb.ContainsFold(foldC, "éco"), []int64{}},
			{"EqFold under pg_c_utf8 folds Unicode", sqlb.EqFold(foldUTF8, "école"), []int64{1}},
			{"ContainsFold under pg_c_utf8 folds Unicode", sqlb.ContainsFold(foldUTF8, "éco"), []int64{1}},
			{"folding case is not folding accents", sqlb.EqFold(foldUTF8, "CAFE"), []int64{}},
		} {
			if got := foldIDs(t, db, d, tc.p); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%T %s: %v, want %v", d, tc.name, got, tc.want)
			}
		}
	}
}
