// Package mysql is the MySQL dialect: how MySQL 8 writes a bound parameter and
// quotes an identifier, and which optional capabilities it has.
//
// Like every dialect here it imports the render layer and nothing else, which
// is exactly what a dialect from another repository imports. The constructs
// only MySQL has live in github.com/ubgo/sqlb/mysql.
//
// Until 2026-09-22 MySQL was a reference engine only: the universality rule
// named it and nothing here could render for it, so that quarter of the rule
// was checked by reading its documentation. This package and
// contrib/driver-mysql, which runs the conformance suite on a real MySQL 8.4,
// replace the reading with a measurement.
package mysql

import (
	"reflect"

	"github.com/ubgo/sqlb/render"
)

// Name is the engine name this dialect reports.
const Name = render.DialectMySQL

// Dialect renders ? placeholders and backquoted identifiers.
type Dialect struct{}

// Placeholder renders ?. MySQL's placeholders are positional and unnumbered,
// so a value used twice is bound twice.
func (Dialect) Placeholder(int) string { return "?" }

// QuoteIdent backquotes an identifier, doubling any embedded backquote. It is
// not the double quote the other engines use: a double-quoted token is a
// string in MySQL unless the ANSI_QUOTES mode is on, and a builder cannot
// depend on a server's mode.
func (Dialect) QuoteIdent(ident string) string { return render.Quote(ident, '`') }

// Name reports "mysql".
func (Dialect) Name() string { return Name }

// SupportsLikeEscape reports true: MySQL accepts an ESCAPE clause on LIKE.
func (Dialect) SupportsLikeEscape() bool { return true }

// SupportsCombinedAlter reports true: MySQL applies several ALTER TABLE
// actions in one statement.
func (Dialect) SupportsCombinedAlter() bool { return true }

// SupportsUnrestrictedAddColumn reports true: MySQL's ADD COLUMN takes any
// column CREATE TABLE would, filling existing rows with the column's implicit
// default when it is NOT NULL.
func (Dialect) SupportsUnrestrictedAddColumn() bool { return true }

// NumericParam casts an integer parameter to SIGNED, or an unsigned one to
// UNSIGNED, so a DECIMAL beside it in COALESCE or arithmetic does not widen the
// result to DECIMAL(65,30). See render.NumericParameters for the measurement.
//
// It reads the kind rather than switching on types, so a named integer type
// (type Age int64) is cast like int64. A nil pointer and every non-integer
// value are left alone: a NULL has no type to give, and a float or a string
// was measured not to widen.
func (Dialect) NumericParam(placeholder string, v any) string {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return placeholder
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return castAs(placeholder, castSigned)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return castAs(placeholder, castUnsigned)
	default:
		return placeholder
	}
}

// The two CAST targets NumericParam writes. SIGNED and UNSIGNED are MySQL's
// 64-bit integer cast types; a narrower Go integer still casts to the 64-bit
// one, since MySQL has no CAST to a smaller integer and the value fits.
const (
	castSigned   = "SIGNED"
	castUnsigned = "UNSIGNED"
)

// castAs renders CAST(sql AS target).
func castAs(sql, target string) string { return "CAST(" + sql + " AS " + target + ")" }
