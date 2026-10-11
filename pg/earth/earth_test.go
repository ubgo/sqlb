package earth

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	places = sqlb.Table{Name: "places", Alias: "p"}
	lat    = sqlb.Col[float64](places, "lat")
	lon    = sqlb.Col[float64](places, "lon")
)

func TestRender(t *testing.T) {
	here := LLToEarth(sqlb.Param(-33.8), sqlb.Param(151.2))
	there := LLToEarth(lat, lon)
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(Distance(here, there)).From(places).
		Where(BoxContains(Box(here, 5000), there)).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT earth_distance(ll_to_earth($1, $2), ll_to_earth("p"."lat", "p"."lon")) FROM "places" AS "p" WHERE ((earth_box(ll_to_earth($3, $4), $5) @> ll_to_earth("p"."lat", "p"."lon")))`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
}

func TestGuard(t *testing.T) {
	p := LLToEarth(lat, lon)
	for k, e := range map[string]sqlb.SQLer{
		"LLToEarth": p, "Distance": Distance(p, p), "Box": Box(p, 1), "BoxContains": sqlb.BoolExpr(BoxContains(p, p)),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(places).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: got %v", k, err)
		}
	}
}
