package ltree

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	cats = sqlb.Table{Name: "categories", Alias: "c"}
	path = sqlb.Col[Path](cats, "path")
)

func TestRender(t *testing.T) {
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
		Exprs(path, NLevel(path), Subpath(path, 0, 2), Index(path, "phones"), LCA(path, Lit("a.b")), Concat(path, Lit("z"))).
		From(cats).
		Where(DescendantOf(path, Lit("electronics.phones")), MatchesLQuery(path, "electronics.*{1}"), MatchesTxtQuery(path, "phones & !android")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "c"."path", nlevel("c"."path"), subpath("c"."path", $1, $2), index("c"."path", $3::ltree), lca("c"."path", $4::ltree), ("c"."path" || $5::ltree) FROM "categories" AS "c" WHERE (("c"."path" <@ $6::ltree) AND ("c"."path" ~ $7::lquery) AND ("c"."path" @ $8::ltxtquery))`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
}

func TestGuard(t *testing.T) {
	for k, e := range map[string]sqlb.SQLer{
		"Lit": Lit("a"), "AncestorOf": sqlb.BoolExpr(AncestorOf(path, path)),
		"DescendantOf": sqlb.BoolExpr(DescendantOf(path, path)), "MatchesLQuery": sqlb.BoolExpr(MatchesLQuery(path, "*")),
		"MatchesTxtQuery": sqlb.BoolExpr(MatchesTxtQuery(path, "a")), "NLevel": NLevel(path),
		"Subpath": Subpath(path, 0, 1), "Subltree": Subltree(path, 0, 1), "Index": Index(path, "a"),
		"LCA": LCA(path), "Concat": Concat(path, path),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(cats).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
