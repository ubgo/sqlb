package sqlite

import (
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb/render"
)

// QuoteLiteral writes v as a SQLite literal that reads back as exactly v.
//
// SQLite has no escape character in string literals at all, so doubling the
// single quote is the whole rule and a backslash is an ordinary character.
//
// The value set is closed: strings, bools, every integer and float kind,
// []byte, nil, a pointer to any of those, and named types whose underlying
// type is one of them. time.Time is deliberately absent. SQLite has no time
// type, and the modernc driver stores a bound time.Time as Go's String form,
// "2026-09-01 12:30:45.123456789 +0000 UTC", which it then cannot scan back
// into a time.Time from a TEXT column; a literal in any other spelling would
// not compare equal to a bound value, and there is no spelling that is right.
// A NUL in a string, a NaN or an infinity, and every other type are
// ErrNotInlinable.
func (Dialect) QuoteLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return kwNull, nil
	case string:
		return quoteString(x)
	case []byte:
		return quoteBytes(x), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return kwNull, nil
		}
		return Dialect{}.QuoteLiteral(rv.Elem().Interface())
	case reflect.String:
		return quoteString(rv.String())
	case reflect.Bool:
		if rv.Bool() {
			return litTrue, nil
		}
		return litFalse, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("%w: %v has no portable literal", render.ErrNotInlinable, f)
		}
		return strconv.FormatFloat(f, 'g', -1, rv.Type().Bits()), nil
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return quoteBytes(rv.Bytes()), nil
		}
	}
	return "", fmt.Errorf("%w: %T", render.ErrNotInlinable, v)
}

// The fixed spellings the literal renderer writes. SQLite has no boolean
// type; true and false are the integers 1 and 0.
const (
	kwNull   = "NULL"
	litTrue  = "1"
	litFalse = "0"
)

// quoteString doubles the single quote and refuses a NUL, which would end the
// string early in SQLite's C API.
func quoteString(s string) (string, error) {
	if strings.IndexByte(s, 0) >= 0 {
		return "", fmt.Errorf("%w: a string containing NUL", render.ErrNotInlinable)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}

// quoteBytes writes a blob literal.
func quoteBytes(b []byte) string {
	return "X'" + hex.EncodeToString(b) + "'"
}
