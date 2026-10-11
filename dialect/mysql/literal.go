package mysql

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

// Keywords and fixed spellings the literal renderer writes.
const (
	kwNull  = "NULL"
	kwTrue  = "TRUE"
	kwFalse = "FALSE"

	// timeLayout is a DATETIME(6) literal in UTC with microseconds and no zone:
	// MySQL 8 accepts an offset in a literal but converts it to the session's
	// time zone, so the only spelling that means one instant everywhere is UTC
	// written bare, with the column read back in UTC.
	timeLayout = "2006-01-02 15:04:05.999999"
)

// QuoteLiteral writes v as a MySQL literal that reads back as exactly v,
// whatever the server's sql_mode.
//
// The mode is the difficulty, as standard_conforming_strings is on Postgres.
// With NO_BACKSLASH_ESCAPES off, the default, a backslash in a string literal
// is an escape; with it on, it is a character. A string without a backslash,
// quotes doubled, reads the same under both, measured on MySQL 8.4. A string
// with one has no spelling that does: the hex form _utf8mb4 X'...' reads
// correctly in a query, but MySQL stores an expression default as printed
// text and re-parses it under the session's mode, so a DEFAULT of that form
// holding the three characters a\b read back as four with NO_BACKSLASH_ESCAPES
// on. A string containing a backslash is therefore ErrNotInlinable, not a
// best guess; bind it where a parameter is allowed, which is everywhere but
// DDL.
//
// The value set is otherwise the Postgres one: strings, bools, every integer
// and float kind, []byte, time.Time (in UTC, for a DATETIME), nil, pointers
// and named types over those. A NUL, a NaN and an infinity are refused.
func (Dialect) QuoteLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return kwNull, nil
	case string:
		return quoteString(x)
	case []byte:
		return quoteBytes(x), nil
	case time.Time:
		return "'" + x.UTC().Format(timeLayout) + "'", nil
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

// quoteString doubles quotes, and refuses a backslash and a NUL for the
// reasons QuoteLiteral gives.
func quoteString(s string) (string, error) {
	if strings.IndexByte(s, 0) >= 0 {
		return "", fmt.Errorf("%w: a string containing NUL", render.ErrNotInlinable)
	}
	if strings.Contains(s, `\`) {
		return "", fmt.Errorf("%w: a string containing a backslash, which MySQL reads differently under NO_BACKSLASH_ESCAPES", render.ErrNotInlinable)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}

// quoteBytes writes a hex literal, which has no escape syntax and reads the
// same under every mode.
func quoteBytes(b []byte) string { return "X'" + hex.EncodeToString(b) + "'" }
