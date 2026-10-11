package pg

import (
	"encoding/json"
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructSQLJSON = "pg.sqljson"

	kwJSONObject     = "JSON_OBJECT"
	kwJSONArrayAgg   = "JSON_ARRAYAGG"
	kwJSONObjectAgg  = "JSON_OBJECTAGG"
	kwAbsentOnNull   = "ABSENT ON NULL"
	kwNullOnNull     = "NULL ON NULL"
	kwWithUniqueKeys = "WITH UNIQUE KEYS"
	kwReturningJSONB = "RETURNING jsonb"
	kwIsJSON         = "IS JSON"
	kwJSONValue      = "JSON_VALUE"
	kwJSONQuery      = "JSON_QUERY"
	kwJSONExists     = "JSON_EXISTS"
	kwWithWrapper    = "WITH WRAPPER"
	kwWithoutWrapper = "WITHOUT WRAPPER"
	kwWithCondWrap   = "WITH CONDITIONAL WRAPPER"
)

// JSONOption modifies a SQL/JSON constructor or aggregate (PG16).
type JSONOption func(*jsonSpec)

type jsonSpec struct {
	absentOnNull bool
	nullOnNull   bool
	uniqueKeys   bool
	returning    string
	order        []sqlb.OrderTerm
}

// AbsentOnNull drops null values from the object or array.
func AbsentOnNull() JSONOption { return func(s *jsonSpec) { s.absentOnNull = true } }

// NullOnNull keeps null values, the default, stated explicitly.
func NullOnNull() JSONOption { return func(s *jsonSpec) { s.nullOnNull = true } }

// WithUniqueKeys rejects duplicate keys.
func WithUniqueKeys() JSONOption { return func(s *jsonSpec) { s.uniqueKeys = true } }

// ReturningJSONB selects jsonb output; the default is json.
func ReturningJSONB() JSONOption { return func(s *jsonSpec) { s.returning = kwReturningJSONB } }

// JSONOrderBy orders the elements of JSON_ARRAYAGG.
func JSONOrderBy(ts ...sqlb.OrderTerm) JSONOption {
	return func(s *jsonSpec) { s.order = append(s.order, ts...) }
}

func (s jsonSpec) suffix(a *sqlb.Args) string {
	var parts []string
	if len(s.order) > 0 {
		terms := make([]string, len(s.order))
		for i, t := range s.order {
			terms[i] = orderTermSQL(t, a)
		}
		parts = append(parts, "ORDER BY "+strings.Join(terms, ", "))
	}
	if s.absentOnNull {
		parts = append(parts, kwAbsentOnNull)
	}
	if s.nullOnNull {
		parts = append(parts, kwNullOnNull)
	}
	if s.uniqueKeys {
		parts = append(parts, kwWithUniqueKeys)
	}
	if s.returning != "" {
		parts = append(parts, s.returning)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// JSONPair is one 'key': value of JSON_OBJECT. The key is a bound value.
type JSONPair struct {
	Key   string
	Value sqlb.SQLer
}

// Pair makes a JSONPair.
func Pair(key string, value sqlb.SQLer) JSONPair { return JSONPair{key, value} }

// JSONObject renders JSON_OBJECT('k': v, ... [options]) (PG16).
func JSONObject(pairs []JSONPair, opts ...JSONOption) sqlb.Expr[json.RawMessage] {
	var spec jsonSpec
	for _, o := range opts {
		o(&spec)
	}
	return expr[json.RawMessage](constructSQLJSON, func(a *sqlb.Args) string {
		parts := make([]string, len(pairs))
		for i, p := range pairs {
			if p.Value == nil {
				a.Fail(ErrIncompleteSpec)
				return ""
			}
			parts[i] = sqlb.Cast[string](sqlb.Param(p.Key), typeText).SQL(a) + ": " + p.Value.SQL(a)
		}
		return kwJSONObject + "(" + strings.Join(parts, ", ") + spec.suffix(a) + ")"
	})
}

// JSONArrayAgg renders JSON_ARRAYAGG(e [ORDER BY ...] [ABSENT ON NULL]).
func JSONArrayAgg(e sqlb.SQLer, opts ...JSONOption) sqlb.Expr[json.RawMessage] {
	var spec jsonSpec
	for _, o := range opts {
		o(&spec)
	}
	return expr[json.RawMessage](constructSQLJSON, func(a *sqlb.Args) string {
		return kwJSONArrayAgg + "(" + e.SQL(a) + spec.suffix(a) + ")"
	})
}

// JSONObjectAgg renders JSON_OBJECTAGG(k: v [ABSENT ON NULL]).
func JSONObjectAgg(key, value sqlb.SQLer, opts ...JSONOption) sqlb.Expr[json.RawMessage] {
	var spec jsonSpec
	for _, o := range opts {
		o(&spec)
	}
	return expr[json.RawMessage](constructSQLJSON, func(a *sqlb.Args) string {
		return kwJSONObjectAgg + "(" + key.SQL(a) + ": " + value.SQL(a) + spec.suffix(a) + ")"
	})
}

// JSONKind is the IS JSON qualifier.
type JSONKind string

// The qualifiers IS JSON accepts, measured against the server rather than
// read: JSONAny, the empty one, tests only that the text parses as JSON at
// all, and JSONAnyValue is the written-out spelling of the same test, which
// the grammar also takes. JSONValue, without the Any, is the JSON_VALUE
// function further down this file and a different thing entirely.
// The set is closed by Postgres's grammar — no extension adds a qualifier —
// which is why this one is a list and SampleMethod is not.
const (
	JSONAny      JSONKind = ""
	JSONAnyValue JSONKind = "VALUE"
	JSONObj      JSONKind = "OBJECT"
	JSONArr      JSONKind = "ARRAY"
	JSONScalar   JSONKind = "SCALAR"
)

// IsJSON renders e IS JSON [kind] [WITH UNIQUE KEYS] (PG16).
func IsJSON(e sqlb.SQLer, kind JSONKind, uniqueKeys bool) sqlb.Predicate {
	return pred(constructSQLJSON, func(a *sqlb.Args) string {
		s := e.SQL(a) + " " + kwIsJSON
		if kind != JSONAny {
			s += " " + closedWord(a, kind, kind.valid(), "IS JSON qualifier")
		}
		if uniqueKeys {
			s += " " + kwWithUniqueKeys
		}
		return s
	})
}

// JSONValueOption configures JSON_VALUE and JSON_QUERY (PG17).
type JSONValueOption func(*jsonValueSpec)

type jsonValueSpec struct {
	returning      string
	defaultOnEmpty string
	defaultOnError string
	wrapper        string
}

// Returning sets the RETURNING type of JSON_VALUE. The type name is written
// into the statement as is, because a type name is not one identifier and
// cannot be quoted; it is the author's text, never a value from a request.
func Returning(sqlType string) JSONValueOption {
	return func(s *jsonValueSpec) { s.returning = sqlType }
}

// DefaultOnEmpty and DefaultOnError set the fallbacks. The SQL is written
// into the statement as is, because the grammar wants a literal expression
// there and there is no placeholder for one: it is the author's text, never a
// value from a request. A fallback that is a value belongs in a Param on the
// other side of the call.
func DefaultOnEmpty(sql string) JSONValueOption {
	return func(s *jsonValueSpec) { s.defaultOnEmpty = sql }
}

// DefaultOnError supplies a value when evaluating the path raises, instead of
// failing the statement. The SQL is an author constant; the grammar requires
// a literal here, so it cannot be bound.
func DefaultOnError(sql string) JSONValueOption {
	return func(s *jsonValueSpec) { s.defaultOnError = sql }
}

// WithWrapper wraps the result in an array always.
//
// The three wrapper options decide what JSON_QUERY does when a path matches
// more or fewer than one value. Without a wrapper, multiple matches raise.
func WithWrapper() JSONValueOption { return func(s *jsonValueSpec) { s.wrapper = kwWithWrapper } }

// WithoutWrapper returns the value bare, and raises when the path matched
// more than one.
func WithoutWrapper() JSONValueOption { return func(s *jsonValueSpec) { s.wrapper = kwWithoutWrapper } }

// WithConditionalWrapper wraps only when the path matched more than one
// value. Usually the one you want: single matches stay scalar.
func WithConditionalWrapper() JSONValueOption {
	return func(s *jsonValueSpec) { s.wrapper = kwWithCondWrap }
}

func (s jsonValueSpec) suffix() string {
	var parts []string
	if s.returning != "" {
		parts = append(parts, kwReturning+" "+s.returning)
	}
	if s.wrapper != "" {
		parts = append(parts, s.wrapper)
	}
	if s.defaultOnEmpty != "" {
		parts = append(parts, kwDefault+" "+s.defaultOnEmpty+" "+kwOnEmpty)
	}
	if s.defaultOnError != "" {
		parts = append(parts, kwDefault+" "+s.defaultOnError+" "+kwOnError)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// JSONValue renders JSON_VALUE(doc, 'path' ...) typed R (PG17). The path is
// an author constant.
func JSONValue[R any](doc sqlb.SQLer, path string, opts ...JSONValueOption) sqlb.Expr[R] {
	var spec jsonValueSpec
	for _, o := range opts {
		o(&spec)
	}
	return expr[R](constructSQLJSON, func(a *sqlb.Args) string {
		return kwJSONValue + "(" + doc.SQL(a) + ", " + quoteLit(path).SQL(a) + spec.suffix() + ")"
	})
}

// JSONQuery renders JSON_QUERY(doc, 'path' ...) (PG17).
func JSONQuery(doc sqlb.SQLer, path string, opts ...JSONValueOption) sqlb.Expr[json.RawMessage] {
	var spec jsonValueSpec
	for _, o := range opts {
		o(&spec)
	}
	return expr[json.RawMessage](constructSQLJSON, func(a *sqlb.Args) string {
		return kwJSONQuery + "(" + doc.SQL(a) + ", " + quoteLit(path).SQL(a) + spec.suffix() + ")"
	})
}

// JSONExists renders JSON_EXISTS(doc, 'path') (PG17).
func JSONExists(doc sqlb.SQLer, path string) sqlb.Predicate {
	return pred(constructSQLJSON, func(a *sqlb.Args) string {
		return kwJSONExists + "(" + doc.SQL(a) + ", " + quoteLit(path).SQL(a) + ")"
	})
}

// JSONSubscript renders doc['a']['b'] (PG14), the subscript form of a path
// lookup. It reads more like the Go it came from than the -> chain does.
func JSONSubscript(doc sqlb.Expr[json.RawMessage], keys ...string) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string { return doc.SQL(a) + subscriptPath(a, keys) })
}

// SetSubscript is an UPDATE target: SET doc['customer']['tier'] = value. It
// updates one field in place without rewriting the document, and creates the
// intermediate objects when they are missing.
func SetSubscript(col sqlb.Expr[json.RawMessage], keys []string, value sqlb.SQLer) sqlb.Assignment {
	return sqlb.AssignTarget(col.Name(), func(a *sqlb.Args) string {
		if !guard(a, constructJSON) {
			return ""
		}
		return a.Dialect().QuoteIdent(col.Name()) + subscriptPath(a, keys)
	}, value)
}

func subscriptPath(a *sqlb.Args, keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("[")
		b.WriteString(a.Next(k))
		b.WriteString("]")
	}
	return b.String()
}

// PathQueryArray renders jsonb_path_query_array(doc, 'path').
func PathQueryArray(doc sqlb.SQLer, path string) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return "jsonb_path_query_array(" + doc.SQL(a) + ", " + a.Next(path) + "::jsonpath)"
	})
}

// JSONBSetLax renders jsonb_set_lax(col, path, value, create_missing, null_treatment).
// NullTreatment is one of the strings Postgres accepts: raise_exception,
// use_json_null, delete_key, return_target.
func JSONBSetLax(col sqlb.SQLer, path []string, value sqlb.SQLer, createMissing bool, nullTreatment string) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return "jsonb_set_lax(" + col.SQL(a) + ", " + a.Next(path) + ", " + value.SQL(a) + ", " + a.Next(createMissing) + ", " + a.Next(nullTreatment) + ")"
	})
}

// JSONNull is the JSON null literal, distinct from SQL NULL.
func JSONNull() sqlb.Expr[json.RawMessage] {
	return litExpr[json.RawMessage](constructJSON, "'null'::jsonb")
}

// EmptyJSONArray and EmptyJSONObject are the COALESCE fallbacks for an
// aggregate over zero rows.
func EmptyJSONArray() sqlb.Expr[json.RawMessage] {
	return litExpr[json.RawMessage](constructJSON, "'[]'::jsonb")
}

// EmptyJSONObject renders '{}'::jsonb, the COALESCE fallback for an object
// aggregate over zero rows.
func EmptyJSONObject() sqlb.Expr[json.RawMessage] {
	return litExpr[json.RawMessage](constructJSON, "'{}'::jsonb")
}

// RowRef references a whole row by its table alias, for to_jsonb(t) and
// count(t.*)-style uses.
//
// Deliberately unguarded, and the only construct here that is. It renders a
// bare quoted identifier, which parses on every engine; verified against
// SQLite, which accepts it. What makes the use Postgres-specific is the
// function wrapped around it, and that function guards.
func RowRef(t sqlb.Table) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string { return t.Qualifier(a.Dialect()) })
}
