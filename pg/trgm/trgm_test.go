package trgm

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	users = sqlb.Table{Name: "users", Alias: "u"}
	name  = sqlb.Col[string](users, "name")
)

func TestRender(t *testing.T) {
	needle := sqlb.Param("jon smith")
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
		Exprs(name, sqlb.As(Similarity(name, needle), "sim")).From(users).
		Where(Similar(name, needle), WordSimilar(name, needle), StrictWordSimilar(name, needle)).
		Order(sqlb.OrderBy(Distance(name, needle), sqlb.SortAsc)).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "u"."name", similarity("u"."name", $1) AS "sim" FROM "users" AS "u" WHERE (("u"."name" % $2) AND ("u"."name" <% $3) AND ("u"."name" <<% $4)) ORDER BY ("u"."name" <-> $5) ASC`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
}

func TestGuard(t *testing.T) {
	lite := func(items ...sqlb.SQLer) error {
		_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(items...).From(users).Build()
		return err
	}
	for k, e := range map[string]sqlb.SQLer{
		"Similar": sqlb.BoolExpr(Similar(name, name)), "Distance": Distance(name, name),
		"WordDistance": WordDistance(name, name), "Similarity": Similarity(name, name),
		"WordSimilarity": WordSimilarity(name, name), "StrictWordSimilarity": StrictWordSimilarity(name, name),
		"ShowTrgm": ShowTrgm(name),
	} {
		if err := lite(e); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
