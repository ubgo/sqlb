package pg

import (
	"encoding/json"
	"time"

	"github.com/ubgo/sqlb"
)

// The SQL types a bound Go value is cast to where Postgres cannot infer one.
// A bare parameter has no type: in ARRAY[$1], jsonb_build_object($1, …) or
// generate_series($1, $2) the server either guesses text or refuses, and the
// driver then fails to encode an integer as text. Every construct that puts
// a parameter where the grammar gives it no type casts it with one of these.
const (
	typeBigint  = "bigint"
	typeInt     = "int"
	typeText    = "text"
	typeBool    = "bool"
	typeFloat8  = "float8"
	typeFloat4  = "float4"
	typeTstz    = "timestamptz"
	typeBytea   = "bytea"
	typeJSONB   = "jsonb"
	typeSmall   = "smallint"
	typeUnknown = ""
)

// sqlTypeOf is the Postgres type a Go type binds as, or "" for a type this
// package does not know, in which case the parameter is left bare and the
// caller's context must type it.
func sqlTypeOf[T any]() string {
	var zero T
	switch any(zero).(type) {
	case int, int64, uint64, uint:
		return typeBigint
	case int32, uint32:
		return typeInt
	case int16, int8, uint16, uint8:
		return typeSmall
	case string:
		return typeText
	case bool:
		return typeBool
	case float64:
		return typeFloat8
	case float32:
		return typeFloat4
	case time.Time:
		return typeTstz
	case []byte:
		return typeBytea
	case json.RawMessage:
		return typeJSONB
	}
	return typeUnknown
}

// typedParam binds v and casts it to the type T binds as, when known.
func typedParam[T any](v T) sqlb.SQLer {
	p := sqlb.Param(v)
	if t := sqlTypeOf[T](); t != typeUnknown {
		return sqlb.Cast[T](p, t)
	}
	return p
}
