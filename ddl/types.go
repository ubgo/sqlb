package ddl

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
)

// SQLType is a column's SQL type, typed to the Go type the column stores.
//
// The type parameter is the point. ColumnDef[T].As takes an SQLType[T], so a
// Postgres varchar can be given to a string column and not to an int64 one,
// and an enum declared over a Go named type can be given only to a column of
// that type. TypeOf is the marker that makes the parameter bite: an interface
// whose parameter appears in no method would be satisfied by every type for
// every T.
type SQLType[T any] interface {
	// Spelling is the type as d writes it.
	Spelling(d render.Dialect) (string, error)
	// TypeOf is never called; it types the SQLType to T.
	TypeOf(T)
}

// ErrNoDefaultType is returned for a column whose Go type has no unambiguous
// SQL type, such as json.RawMessage, which is a []byte underneath and would be
// stored as bytea if its kind were guessed. Give the type with As.
var ErrNoDefaultType = errors.New("sqlb/ddl: the column's Go type has no default SQL type; give one with As")

// ErrNoTypeTable is returned when a portable type is asked of a dialect that
// has no type table, which an outside dialect does not until it adds one.
var ErrNoTypeTable = errors.New("sqlb/ddl: the dialect has no spelling for this portable type; use RawType")

// Portable is one of schema's portable types, spelled by each dialect from the
// table contrib/migrate and contrib/vet already read, so a column the vet
// accepts is one this would create. It is how a string column becomes a uuid
// or a timestamp rather than text, and how a json.RawMessage becomes jsonb.
//
// The Go type is checked against the portable type when the statement is
// built: Portable[int64](schema.TypeText) fails with schema.ErrTypeMismatch.
func Portable[T any](t schema.Type) SQLType[T] { return portable[T]{t: t} }

type portable[T any] struct{ t schema.Type }

// TypeOf is never called, here or in raw below: it exists so T appears in a
// method, which is what makes SQLType[T] refuse a type for another Go type.
func (portable[T]) TypeOf(T) {}

func (p portable[T]) Spelling(d render.Dialect) (string, error) {
	if err := checkPortable[T](p.t); err != nil {
		return "", err
	}
	s, ok := schema.EngineType(render.DialectName(d), p.t)
	if !ok {
		return "", fmt.Errorf("%w: %s under %q", ErrNoTypeTable, p.t, render.DialectName(d))
	}
	return s, nil
}

// RawType is a column type written exactly as given, for a type no portable
// type covers: citext, an extension's type, a sized varchar. It is the escape
// hatch, and it is still typed to the column's Go type, so it cannot be handed
// to a column of another type. The spelling is written into the statement as
// is; it is the author's text, never a value from a request.
func RawType[T any](spelling string) SQLType[T] { return raw[T]{s: spelling} }

type raw[T any] struct{ s string }

func (raw[T]) TypeOf(T) {}

func (r raw[T]) Spelling(render.Dialect) (string, error) { return r.s, nil }

// timeType and bytesType are the two Go types inferred by identity rather than
// by kind: a named []byte such as json.RawMessage must not be taken for bytea,
// and time.Time is a struct.
var (
	timeType  = reflect.TypeFor[time.Time]()
	bytesType = reflect.TypeFor[[]byte]()
)

// goType is T with one pointer removed, and whether there was one. A pointer
// is how a Col declares that the column may be NULL.
func goType[T any]() (reflect.Type, bool) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		return rt.Elem(), true
	}
	return rt, false
}

// inferType is the portable type a Go type is stored as when the author gave
// none. It infers only where the answer is unambiguous; everything else is
// ErrNoDefaultType, because a guessed column type is wrong silently and for
// the life of the table.
func inferType[T any]() (schema.Type, error) {
	rt, _ := goType[T]()
	switch rt {
	case timeType:
		return schema.TypeTimestamp, nil
	case bytesType:
		return schema.TypeBytes, nil
	}
	switch rt.Kind() {
	case reflect.String:
		return schema.TypeText, nil
	case reflect.Bool:
		return schema.TypeBool, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16:
		return schema.TypeInt, nil
	case reflect.Int64, reflect.Uint32, reflect.Uint64:
		return schema.TypeBigInt, nil
	case reflect.Float32, reflect.Float64:
		return schema.TypeFloat, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoDefaultType, rt)
}

// checkPortable accepts T for t when T, or T's underlying basic type, is one of
// the Go types schema allows for t. A named string enum is a string; a
// json.RawMessage is a []uint8.
func checkPortable[T any](t schema.Type) error {
	allowed := schema.GoTypes(t)
	if len(allowed) == 0 {
		return nil // a type schema does not constrain
	}
	rt, _ := goType[T]()
	if slices.Contains(allowed, rt.String()) || slices.Contains(allowed, basicName(rt)) {
		return nil
	}
	return fmt.Errorf("%w: %s cannot be stored as %s", schema.ErrTypeMismatch, rt, t)
}

// basicName is the name of rt's underlying basic type as reflect prints it.
func basicName(rt reflect.Type) string {
	if rt.Kind() == reflect.Slice && rt.Elem().Kind() == reflect.Uint8 {
		return "[]uint8"
	}
	return rt.Kind().String()
}

// ErrInvalidJSON is returned for a JSON column's default that is not valid
// JSON, or a value that cannot be turned into JSON.
var ErrInvalidJSON = errors.New("sqlb/ddl: a JSON column's default is not valid JSON")

// JSONStorer is an SQLType that stores JSON, which changes how a default is
// written; see ColumnDef.Default. Portable(schema.TypeJSON) and RawJSONType
// are the two; a type of an engine package implements it to join them.
type JSONStorer interface {
	StoresJSON() bool
}

// StoresJSON reports whether the portable type is JSON.
func (p portable[T]) StoresJSON() bool { return p.t == schema.TypeJSON }

// RawJSONType is RawType for a column that stores JSON under a spelling of
// its own, jsonb or json, for a Go type the portable JSON type does not take,
// such as pg.JSON[T]. Its default is written as JSON, as Portable(TypeJSON)'s
// is.
func RawJSONType[T any](spelling string) SQLType[T] { return rawJSON[T]{raw: raw[T]{s: spelling}} }

type rawJSON[T any] struct{ raw[T] }

// StoresJSON reports true.
func (rawJSON[T]) StoresJSON() bool { return true }

func storesJSON[T any](t SQLType[T]) bool {
	js, ok := t.(JSONStorer)
	return ok && js.StoresJSON()
}

// jsonLiteral writes v, a JSON column's default, as ('<json text>').
func jsonLiteral(a *render.Args, v any) string {
	var text []byte
	switch x := v.(type) {
	case []byte:
		text = x
	case string:
		text = []byte(x)
	case driver.Valuer:
		dv, err := x.Value()
		if err != nil {
			a.Fail(fmt.Errorf("%w: %w", ErrInvalidJSON, err))
			return ""
		}
		switch y := dv.(type) {
		case []byte:
			text = y
		case string:
			text = []byte(y)
		default:
			a.Fail(fmt.Errorf("%w: Value returned %T", ErrInvalidJSON, dv))
			return ""
		}
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			text = rv.Bytes() // json.RawMessage and other named byte slices
			break
		}
		b, err := json.Marshal(v)
		if err != nil {
			a.Fail(fmt.Errorf("%w: %w", ErrInvalidJSON, err))
			return ""
		}
		text = b
	}
	if !json.Valid(text) {
		a.Fail(fmt.Errorf("%w: %q", ErrInvalidJSON, text))
		return ""
	}
	return "(" + a.Literal(string(text)) + ")"
}
