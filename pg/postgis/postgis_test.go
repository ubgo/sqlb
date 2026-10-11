package postgis

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	places   = sqlb.Table{Name: "places", Alias: "p"}
	location = sqlb.Col[Geometry](places, "location")
)

const srid = 4326

func nearby() *sqlb.Select[sqlb.PostgresDialect] {
	var d float64
	here := MakePoint(sqlb.Param(151.2), sqlb.Param(-33.8), srid)
	return sqlb.NewSelect(sqlb.PostgresDialect{}).
		Columns(sqlb.Bind(DistanceSphere(location, here), &d)).
		From(places).
		Where(DWithin(location, here, sqlb.Param(5000.0))).
		Where(Intersects(location, here)).
		Order(sqlb.OrderBy(KNN(location, here), sqlb.SortAsc))
}

func TestRender(t *testing.T) {
	q, err := nearby().Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT ST_DistanceSphere("p"."location", ST_SetSRID(ST_MakePoint($1, $2), $3)) FROM "places" AS "p" WHERE (ST_DWithin("p"."location", ST_SetSRID(ST_MakePoint($4, $5), $6), $7) AND ST_Intersects("p"."location", ST_SetSRID(ST_MakePoint($8, $9), $10))) ORDER BY ("p"."location" <-> ST_SetSRID(ST_MakePoint($11, $12), $13)) ASC`
	if q.SQL != want {
		t.Fatalf("got\n%s\nwant\n%s", q.SQL, want)
	}
	if len(q.Args) != 13 {
		t.Fatalf("args %d", len(q.Args))
	}
}

func TestGuard(t *testing.T) {
	var d float64
	here := MakePoint(sqlb.Param(1.0), sqlb.Param(2.0), srid)
	_, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).
		Columns(sqlb.Bind(Distance(location, here), &d)).From(places).Build()
	if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Fatalf("want ErrUnsupportedDialect, got %v", err)
	}
}

func TestGeometry_Scan(t *testing.T) {
	var g Geometry
	if err := g.Scan([]byte("0101000020E6100000")); err != nil || g != "0101000020E6100000" {
		t.Fatalf("scan %q %v", g, err)
	}
	if err := g.Scan(42); !errors.Is(err, ErrGeometryType) {
		t.Fatalf("want ErrGeometryType, got %v", err)
	}
}

// Contains and AsGeoJSON are the two calls the nearby-search example does not
// use, and Geometry.Value is the direction of the round trip that example
// never exercises: it reads geometry back, it never sends one as a parameter.
//
// Value matters more than it looks. A geometry read from one query must be a
// valid parameter to the next, and the only thing that guarantees it is that
// Value hands back exactly the text Scan received, unchanged.
func TestContainsAsGeoJSONAndValue(t *testing.T) {
	region := sqlb.Col[Geometry](places, "region")

	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
		Exprs(AsGeoJSON(location)).
		From(places).
		Where(Contains(region, location)).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// The WHERE group is parenthesised even with one member. That is the
	// core's documented behaviour: the parentheses cost nothing and their
	// absence costs a precedence bug the day a second predicate is added.
	want := `SELECT ST_AsGeoJSON("p"."location") FROM "places" AS "p" WHERE (ST_Contains("p"."region", "p"."location"))`
	if q.SQL != want {
		t.Fatalf("got  %s\nwant %s", q.SQL, want)
	}

	for name, e := range map[string]sqlb.SQLer{
		"Contains":  sqlb.BoolExpr(Contains(region, location)),
		"AsGeoJSON": AsGeoJSON(location),
	} {
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(e).From(places).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: want ErrUnsupportedDialect, got %v", name, err)
		}
	}

	const ewkb = "0101000020E6100000"
	var g Geometry
	if err := g.Scan(ewkb); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	v, err := g.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if v != ewkb {
		t.Fatalf("Value changed the text: got %v, want %s", v, ewkb)
	}
}

// Scan's nil arm. A NULL geometry column must clear the value, or a row
// without a location inherits the previous row's, which in a nearest-search
// result is a wrong answer rather than a visible failure.
func TestGeometry_ScanNull(t *testing.T) {
	g := Geometry("0101000020E6100000")
	if err := g.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if g != "" {
		t.Fatalf("a NULL geometry left a stale value: %q", g)
	}
}
