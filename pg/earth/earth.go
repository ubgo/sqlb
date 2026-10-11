// Package earth is the earthdistance extension (with cube): great-circle
// distance and bounding boxes on the sphere. Needs CREATE EXTENSION cube
// and CREATE EXTENSION earthdistance. For anything beyond a radius search,
// use pg/postgis.
package earth

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const (
	construct = "earth"

	opContains = "@>"
)

// Point is an earth value (a cube). Only ever produced by the server; scan
// it as text if you need to read it back.
type Point string

// LLToEarth renders ll_to_earth(lat, lon).
func LLToEarth(lat, lon sqlb.Expr[float64]) sqlb.Expr[Point] {
	return pg.Func[Point](construct+".LLToEarth", "ll_to_earth", lat, lon)
}

// Distance renders earth_distance(a, b) in metres.
func Distance(a, b sqlb.Expr[Point]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".Distance", "earth_distance", a, b)
}

// Box renders earth_box(point, radiusMetres), the index-served pre-filter.
func Box(p sqlb.Expr[Point], radiusMetres float64) sqlb.Expr[Point] {
	return pg.Func[Point](construct+".Box", "earth_box", p, sqlb.Param(radiusMetres))
}

// BoxContains renders box @> point. Pair it with Distance for the exact cut,
// because the box is square and the radius is round.
func BoxContains(box, p sqlb.Expr[Point]) sqlb.Predicate {
	return pg.BinPred(construct+".BoxContains", opContains, box, p)
}
