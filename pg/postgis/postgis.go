// Package postgis is the template for adding a Postgres extension without
// touching pg. It covers the PostGIS calls a location feature needs: make a
// point, measure, filter by radius, order by nearest. Needs CREATE EXTENSION
// postgis. Everything here is one line over pg's exported constructors, so
// a new extension package is a copy of this file with the names changed.
package postgis

import (
	"database/sql/driver"
	"errors"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

const (
	construct = "postgis"

	opKNN = "<->"
)

// Geometry is a PostGIS geometry as the server sends it: EWKB hex text, or
// GeoJSON when selected through AsGeoJSON. The wire form stays opaque here
// because decoding it is a job for a geometry library, not a query builder.
type Geometry string

// ErrGeometryType is returned by Scan for a value that is not text.
var ErrGeometryType = errors.New("postgis: geometry must scan from text")

// Scan accepts the text the server sends, EWKB hex or GeoJSON, and keeps it
// unchanged. It never parses, because decoding geometry is a job for a
// geometry library.
func (g *Geometry) Scan(src any) error {
	switch t := src.(type) {
	case []byte:
		*g = Geometry(t)
	case string:
		*g = Geometry(t)
	case nil:
		*g = ""
	default:
		return ErrGeometryType
	}
	return nil
}

// Value sends the text back unchanged, so a value read from one query is a
// valid parameter to the next.
func (g Geometry) Value() (driver.Value, error) { return string(g), nil }

// MakePoint renders ST_SetSRID(ST_MakePoint(lon, lat), srid). lon comes
// first because PostGIS points are x, y.
func MakePoint(lon, lat sqlb.Expr[float64], srid int) sqlb.Expr[Geometry] {
	return pg.Func[Geometry](construct+".MakePoint", "ST_SetSRID",
		pg.Func[Geometry](construct+".MakePoint", "ST_MakePoint", lon, lat), sqlb.Param(srid))
}

// Distance renders ST_Distance(a, b) in the SRID's units.
func Distance(a, b sqlb.Expr[Geometry]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".Distance", "ST_Distance", a, b)
}

// DistanceSphere renders ST_DistanceSphere(a, b) in metres.
func DistanceSphere(a, b sqlb.Expr[Geometry]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".DistanceSphere", "ST_DistanceSphere", a, b)
}

// DWithin renders ST_DWithin(a, b, d), the index-served radius predicate.
func DWithin(a, b sqlb.Expr[Geometry], d sqlb.Expr[float64]) sqlb.Predicate {
	return pg.Pred(construct+".DWithin", func(x *sqlb.Args) string {
		return "ST_DWithin(" + pg.List(x, []sqlb.SQLer{a, b, d}) + ")"
	})
}

// Intersects renders ST_Intersects(a, b): the geometries share any point.
func Intersects(a, b sqlb.Expr[Geometry]) sqlb.Predicate {
	return pg.Pred(construct+".Intersects", func(x *sqlb.Args) string {
		return "ST_Intersects(" + pg.List(x, []sqlb.SQLer{a, b}) + ")"
	})
}

// Contains renders ST_Contains(a, b): b lies entirely inside a. The argument
// order is the reverse of what reads naturally, so name the arguments at the
// call site.
func Contains(a, b sqlb.Expr[Geometry]) sqlb.Predicate {
	return pg.Pred(construct+".Contains", func(x *sqlb.Args) string {
		return "ST_Contains(" + pg.List(x, []sqlb.SQLer{a, b}) + ")"
	})
}

// KNN renders a <-> b for ORDER BY, the nearest-neighbour index scan.
func KNN(a, b sqlb.Expr[Geometry]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".KNN", opKNN, a, b)
}

// AsGeoJSON renders ST_AsGeoJSON(g).
func AsGeoJSON(g sqlb.Expr[Geometry]) sqlb.Expr[string] {
	return pg.Func[string](construct+".AsGeoJSON", "ST_AsGeoJSON", g)
}
