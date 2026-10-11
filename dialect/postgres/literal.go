package postgres

import (
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb/render"
)

// QuoteLiteral writes v as a Postgres literal that reads back as exactly v,
// whatever the server's standard_conforming_strings setting.
//
// That setting is the whole difficulty. With it off, Postgres reads a backslash
// in a plain '...' string as an escape, so 'a\b' is four characters on one
// server and three on another, with only a WARNING to say so; measured on
// Postgres 17, a jsonpath's like_regex "^\\d$" matched with the setting on and
// did not with it off. So a string containing a backslash is written in the
// E'...' form with the backslash doubled, which reads the same under both. A
// string without one is written plainly, which also reads the same under both,
// and keeps every constant this package already renders byte-identical.
//
// The value set is closed: strings, bools, every integer and float kind,
// []byte, time.Time, nil, a pointer to any of those, and named types whose
// underlying type is one of them. A NUL in a string, a NaN or an infinity, and
// every other type are ErrNotInlinable.
func (Dialect) QuoteLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return kwNull, nil
	case string:
		return quoteString(x)
	case []byte:
		return quoteBytes(x), nil
	case time.Time:
		return quoteTime(x), nil
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
			return kwTrue, nil
		}
		return kwFalse, nil
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

// Keywords and fixed spellings the literal renderer writes.
const (
	kwNull  = "NULL"
	kwTrue  = "true"
	kwFalse = "false"

	// timeLayout is ISO 8601 in UTC with microseconds, the precision a
	// timestamptz stores; a finer fraction would be rounded by the server.
	timeLayout = "2006-01-02 15:04:05.999999Z07:00"
)

// quoteString writes s plainly when it holds no backslash and in the E'...'
// form when it does, and refuses a NUL, which a Postgres text value cannot
// hold at all.
func quoteString(s string) (string, error) {
	if strings.IndexByte(s, 0) >= 0 {
		return "", fmt.Errorf("%w: a string containing NUL", render.ErrNotInlinable)
	}
	quoted := strings.ReplaceAll(s, "'", "''")
	if !strings.Contains(s, `\`) {
		return "'" + quoted + "'", nil
	}
	return "E'" + strings.ReplaceAll(quoted, `\`, `\\`) + "'", nil
}

// quoteBytes writes a bytea in hex form. The hex form begins with a backslash,
// so it is always the E'...' spelling for the reason quoteString gives.
func quoteBytes(b []byte) string {
	return `E'\\x` + hex.EncodeToString(b) + `'::bytea`
}

// quoteTime writes an instant as a timestamptz in UTC, so the literal does
// not depend on the session's time zone.
func quoteTime(t time.Time) string {
	return "'" + t.UTC().Format(timeLayout) + "'::timestamptz"
}
