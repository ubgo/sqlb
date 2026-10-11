package vet

import (
	"encoding/json"
	"math/big"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/ubgo/sqlb/schema"
)

// A default as SQL text is compared by what it means, not by how it is
// spelled, because no two engines print one alike. Measured on Postgres 17,
// MySQL 8.4 and SQLite 3.50 with the same table:
//
//   - Postgres reprints a literal with a cast, and quotes a negative number:
//     DEFAULT -5 on a bigint reads back as '-5'::integer, 'active' as
//     'active'::text, and E'a\\b' as 'a\b'::text.
//   - MySQL returns a literal's value unquoted in COLUMN_DEFAULT, 1 for TRUE,
//     and pads a DECIMAL to its scale, so 2.5 reads back as 2.50; an
//     expression default is marked DEFAULT_GENERATED and may carry a charset
//     introducer, ('j') reading back as _latin1\'j\'.
//   - SQLite returns the text exactly as the DDL wrote it.
//
// So a literal is reduced to its value, and two literals are equal when their
// values are: numbers numerically, booleans as 1 and 0, times as instants and
// strings exactly. An expression is reduced to a normalised spelling, and two
// expressions that still differ are a warning rather than an error, since the
// engine's reprinting is not a documented format.

// defaultKind is what a default's SQL text turned out to be.
type defaultKind int

const (
	kindNone defaultKind = iota // no default, or DEFAULT NULL
	kindString
	kindNumber
	kindBool
	kindExpression
)

// parsedDefault is a default reduced to what it means.
type parsedDefault struct {
	kind  defaultKind
	value string // the literal's value, or the normalised expression
}

var (
	numberRe     = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)
	castRe       = regexp.MustCompile(`^"?[A-Za-z_][A-Za-z0-9_ ."]*"?(\(\d+(,\s*\d+)?\))?(\[\])*$`)
	introducerRe = regexp.MustCompile(`^_[A-Za-z0-9]+\s*`)
	exprCastRe   = regexp.MustCompile(`::[a-z_][a-z0-9_]*(\[\])*`)
)

// parseDefault reduces a default's SQL text. The empty string is no default.
func parseDefault(sql string) parsedDefault {
	s := strings.TrimSpace(sql)
	if s == "" {
		return parsedDefault{}
	}
	for {
		prev := s
		s = stripParens(s)
		s = stripCast(s)
		if s == prev {
			break
		}
	}
	// MySQL's expression form for a string: an introducer, then the string
	// with its quotes backslash-escaped.
	if loc := introducerRe.FindStringIndex(s); loc != nil && strings.HasPrefix(s[loc[1]:], `\'`) {
		s = strings.ReplaceAll(s[loc[1]:], `\'`, `'`)
	}
	switch {
	case strings.EqualFold(s, "null"):
		return parsedDefault{}
	case strings.EqualFold(s, "true"):
		return parsedDefault{kind: kindBool, value: "1"}
	case strings.EqualFold(s, "false"):
		return parsedDefault{kind: kindBool, value: "0"}
	case numberRe.MatchString(s):
		return parsedDefault{kind: kindNumber, value: s}
	}
	if v, ok := unquote(s); ok {
		return parsedDefault{kind: kindString, value: v}
	}
	return parsedDefault{kind: kindExpression, value: normaliseExpression(s)}
}

// stripParens removes one pair of parentheses that wraps the whole text.
func stripParens(s string) string {
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return s
	}
	depth, inQuote := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			inQuote = !inQuote
		case inQuote:
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return s // the first group closes before the end: (a) + (b)
			}
		}
	}
	return strings.TrimSpace(s[1 : len(s)-1])
}

// stripCast removes a trailing ::type that applies to the whole text, the
// form Postgres prints a literal default in.
func stripCast(s string) string {
	inQuote := false
	last := -1
	for i := 0; i+1 < len(s); i++ {
		switch {
		case s[i] == '\'':
			inQuote = !inQuote
		case !inQuote && s[i] == ':' && s[i+1] == ':':
			last = i
		}
	}
	if last < 0 || !castRe.MatchString(strings.TrimSpace(s[last+2:])) {
		return s
	}
	head := strings.TrimSpace(s[:last])
	if _, ok := unquote(head); ok || numberRe.MatchString(head) || strings.EqualFold(head, "null") ||
		strings.EqualFold(head, "true") || strings.EqualFold(head, "false") || isWrapped(head) {
		return head
	}
	return s
}

func isWrapped(s string) bool { return stripParens(s) != s }

// unquote reads a whole-text SQL string literal, with an embedded quote
// written twice, or Postgres's escape form E'...' with backslash escapes.
func unquote(s string) (string, bool) {
	escaped := false
	if len(s) >= 3 && (s[0] == 'E' || s[0] == 'e') && s[1] == '\'' {
		escaped, s = true, s[1:]
	}
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case escaped && c == '\\' && i+1 < len(body):
			i++
			b.WriteByte(body[i])
		case c == '\'':
			if i+1 >= len(body) || body[i+1] != '\'' {
				return "", false // a quote that ends the literal before the text does
			}
			i++
			b.WriteByte('\'')
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// normaliseExpression is the spelling two expressions are compared by: lower
// case, no whitespace, no parentheses, no ::casts and no charset introducers.
func normaliseExpression(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '(', ')':
			return -1
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, `\'`, `'`)
	return exprCastRe.ReplaceAllString(s, "")
}

// mysqlCatalogueDefault turns MySQL's COLUMN_DEFAULT and EXTRA into SQL text:
// a literal's value is unquoted there, so it is quoted here, and an
// expression is kept as MySQL printed it.
func mysqlCatalogueDefault(value *string, extra string) *string {
	if value == nil {
		return nil
	}
	if strings.Contains(strings.ToUpper(extra), "DEFAULT_GENERATED") {
		return value
	}
	quoted := "'" + strings.ReplaceAll(*value, "'", "''") + "'"
	return &quoted
}

// timeLayouts are the spellings a time literal comes back in: the dialects'
// own and the catalogues'.
var timeLayouts = []string{
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

func parseTime(s string) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// defaultVerdict is how two defaults compare.
type defaultVerdict int

const (
	defaultsEqual defaultVerdict = iota
	defaultsDiffer
	defaultsMayDiffer // at least one is an expression the engine reprinted
)

// compareDefaults compares the DDL's default with the catalogue's. The DDL's
// kind decides how a literal is read, because MySQL's catalogue gives a value
// with no type: a DDL number compares numerically with any catalogue value
// that parses as one, a DDL boolean with 1 or 0, a DDL string exactly, or as
// an instant when both sides are times.
func compareDefaults(ddlSQL string, cat *string) defaultVerdict {
	want := parseDefault(ddlSQL)
	var got parsedDefault
	if cat != nil {
		got = parseDefault(*cat)
	}
	if want.kind == kindNone || got.kind == kindNone {
		if want.kind == got.kind {
			return defaultsEqual
		}
		return defaultsDiffer
	}
	if want.kind == kindExpression || got.kind == kindExpression {
		if want.kind == got.kind && want.value == got.value {
			return defaultsEqual
		}
		return defaultsMayDiffer
	}
	switch want.kind {
	case kindNumber, kindBool:
		a, ok1 := new(big.Rat).SetString(want.value)
		b, ok2 := new(big.Rat).SetString(boolAsNumber(got.value))
		if ok1 && ok2 && a.Cmp(b) == 0 {
			return defaultsEqual
		}
		return defaultsDiffer
	default:
		if want.value == got.value {
			return defaultsEqual
		}
		if a, ok := parseTime(want.value); ok {
			if b, ok := parseTime(got.value); ok && a.Equal(b) {
				return defaultsEqual
			}
		}
		return defaultsDiffer
	}
}

// compareDefaultsAs is compareDefaults, reading two string literals as JSON
// when the column stores JSON: Postgres reprints a jsonb default normalised,
// so {"a":1} reads back as {"a": 1}, and that is the same default.
func compareDefaultsAs(ddlSQL string, cat *string, asJSON bool) defaultVerdict {
	v := compareDefaults(ddlSQL, cat)
	if v != defaultsDiffer || !asJSON || cat == nil {
		return v
	}
	want, got := parseDefault(ddlSQL), parseDefault(*cat)
	if want.kind == kindString && got.kind == kindString && jsonEqual(want.value, got.value) {
		return defaultsEqual
	}
	return v
}

// jsonEqual reports whether two texts are the same JSON value.
func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// isJSONColumn reports whether a described column stores JSON: the portable
// JSON type, or a raw spelling that names json or jsonb.
func isJSONColumn(c schema.Column) bool {
	if c.Type == schema.TypeJSON {
		return true
	}
	r := strings.ToLower(strings.TrimSpace(c.Raw))
	return r == "json" || r == "jsonb"
}

func boolAsNumber(s string) string {
	switch strings.ToLower(s) {
	case "true", "t":
		return "1"
	case "false", "f":
		return "0"
	}
	return s
}
