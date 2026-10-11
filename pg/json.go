package pg

import (
	"database/sql/driver"
	"encoding/json"

	"github.com/ubgo/sqlb"
)

// JSON is the typed form of a json or jsonb column: it scans into T and
// values T as JSON, so the Go struct is the schema.
//
// The alternative is json.RawMessage plus a marshal at every use, which puts
// the shape of the column in each caller instead of in one type.
type JSON[T any] struct{ V T }

// Scan unmarshals the column into V. A NULL clears V to its zero value
// rather than leaving the previous row's, which matters because Each reuses
// the same destination for every row.
func (j *JSON[T]) Scan(src any) error {
	switch b := src.(type) {
	case []byte:
		return json.Unmarshal(b, &j.V)
	case string:
		return json.Unmarshal([]byte(b), &j.V)
	case nil:
		var zero T
		j.V = zero
		return nil
	default:
		return errUnknownJSONSource
	}
}

// Value marshals V. It returns a string rather than bytes: driver.Value
// permits both, and every driver this package targets accepts the string
// form for a json or jsonb parameter.
func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

type jsonSourceError struct{}

func (jsonSourceError) Error() string {
	return "pg: json column scanned from an unsupported source type"
}

var errUnknownJSONSource = jsonSourceError{}

const (
	constructJSON = "pg.json"

	opJSONGet       = "->"
	opJSONGetText   = "->>"
	opJSONPath      = "#>"
	opJSONPathText  = "#>>"
	opJSONContains  = "@>"
	opJSONContained = "<@"
	opJSONHasKey    = "?"
	opJSONHasAny    = "?|"
	opJSONHasAll    = "?&"
	opJSONConcat    = "||"
	opJSONDelete    = "-"
	opJSONDeletePth = "#-"
	opJSONPathEx    = "@?"
	opJSONPathMatch = "@@"

	fnJSONBSet         = "jsonb_set"
	fnJSONBInsert      = "jsonb_insert"
	fnJSONBBuildObject = "jsonb_build_object"
	fnJSONBBuildArray  = "jsonb_build_array"
	fnToJSONB          = "to_jsonb"
	fnRowToJSON        = "row_to_json"
	fnJSONBTypeof      = "jsonb_typeof"
	fnJSONBArrayLength = "jsonb_array_length"
	fnJSONBStripNulls  = "jsonb_strip_nulls"
	fnJSONBPretty      = "jsonb_pretty"
	fnJSONBArrayElems  = "jsonb_array_elements"
	fnJSONBArrayElemsT = "jsonb_array_elements_text"
	fnJSONBEach        = "jsonb_each"
	fnJSONBEachText    = "jsonb_each_text"
	fnJSONBObjectKeys  = "jsonb_object_keys"
	fnJSONBPathQuery   = "jsonb_path_query"
	fnJSONBPathQueryF  = "jsonb_path_query_first"
	fnJSONBAgg         = "jsonb_agg"
	fnJSONBObjectAgg   = "jsonb_object_agg"
	fnJSONAgg          = "json_agg"
)

// Get renders col->key, a JSON value at a bound key. Always nullable: a
// missing key is NULL.
func Get(col sqlb.Expr[json.RawMessage], key string) sqlb.Expr[json.RawMessage] {
	return binop[json.RawMessage](constructJSON, opJSONGet, col, sqlb.Param(key))
}

// GetIndex renders col->$1::int for an array element. The cast decides the
// operator: -> with a text parameter is the key form, so without it Postgres
// would look up the key "0" rather than the first element.
func GetIndex(col sqlb.Expr[json.RawMessage], n int) sqlb.Expr[json.RawMessage] {
	return binop[json.RawMessage](constructJSON, opJSONGet, col, sqlb.Cast[int](sqlb.Param(n), typeInt))
}

// JSONText renders col->>key, the text at a bound key.
func JSONText(col sqlb.Expr[json.RawMessage], key string) sqlb.Expr[string] {
	return binop[string](constructJSON, opJSONGetText, col, sqlb.Param(key))
}

// PathGet renders col#>path with a bound text[] path.
func PathGet(col sqlb.Expr[json.RawMessage], path []string) sqlb.Expr[json.RawMessage] {
	return binop[json.RawMessage](constructJSON, opJSONPath, col, sqlb.Param(path))
}

// PathText renders col#>>path.
func PathText(col sqlb.Expr[json.RawMessage], path []string) sqlb.Expr[string] {
	return binop[string](constructJSON, opJSONPathText, col, sqlb.Param(path))
}

// Contains renders col @> $n: true when col contains the JSON document v.
// Usable with a GIN index.
func Contains(col sqlb.Expr[json.RawMessage], v json.RawMessage) sqlb.Predicate {
	return binpred(constructJSON, opJSONContains, col, sqlb.Param(v))
}

// ContainedBy renders col <@ $n.
func ContainedBy(col sqlb.Expr[json.RawMessage], v json.RawMessage) sqlb.Predicate {
	return binpred(constructJSON, opJSONContained, col, sqlb.Param(v))
}

// HasKey renders col ? key: the object has that top-level key.
//
// The ? in these three is a jsonb operator, not a placeholder. Drivers that
// use ? for parameters, which is most of them outside Postgres, need it
// escaped, and that is one more reason these live in the pg package.
func HasKey(col sqlb.Expr[json.RawMessage], key string) sqlb.Predicate {
	return binpred(constructJSON, opJSONHasKey, col, sqlb.Param(key))
}

// HasAnyKey renders col ?| keys: at least one of the keys is present.
func HasAnyKey(col sqlb.Expr[json.RawMessage], keys []string) sqlb.Predicate {
	return binpred(constructJSON, opJSONHasAny, col, sqlb.Param(keys))
}

// HasAllKeys renders col ?& keys: every key is present.
func HasAllKeys(col sqlb.Expr[json.RawMessage], keys []string) sqlb.Predicate {
	return binpred(constructJSON, opJSONHasAll, col, sqlb.Param(keys))
}

// JSONConcat renders l || r, a shallow merge.
func JSONConcat(l, r sqlb.Expr[json.RawMessage]) sqlb.Expr[json.RawMessage] {
	return binop[json.RawMessage](constructJSON, opJSONConcat, l, r)
}

// DeleteKey renders col - key, removing a top-level key.
func DeleteKey(col sqlb.Expr[json.RawMessage], key string) sqlb.Expr[json.RawMessage] {
	return binop[json.RawMessage](constructJSON, opJSONDelete, col, sqlb.Param(key))
}

// DeletePath renders col #- path, removing a nested key by path.
func DeletePath(col sqlb.Expr[json.RawMessage], path []string) sqlb.Expr[json.RawMessage] {
	return binop[json.RawMessage](constructJSON, opJSONDeletePth, col, sqlb.Param(path))
}

// PathExists renders col @? jsonpath: the path matches anything.
//
// The path is bound and cast rather than rendered, so a path built from
// request data cannot reach the statement text. PathMatch is the variant that
// evaluates the path as a predicate instead of testing for a match.
func PathExists(col sqlb.Expr[json.RawMessage], jsonpath string) sqlb.Predicate {
	return pred(constructJSON, func(a *sqlb.Args) string {
		return col.SQL(a) + " " + opJSONPathEx + " " + a.Next(jsonpath) + "::jsonpath"
	})
}

// PathMatch renders col @@ jsonpath, evaluating the path as a boolean
// expression. @? asks whether anything matched; @@ asks what it evaluated to.
func PathMatch(col sqlb.Expr[json.RawMessage], jsonpath string) sqlb.Predicate {
	return pred(constructJSON, func(a *sqlb.Args) string {
		return col.SQL(a) + " " + opJSONPathMatch + " " + a.Next(jsonpath) + "::jsonpath"
	})
}

// JSONBSet renders jsonb_set(col, path, value, create_missing).
func JSONBSet(col sqlb.Expr[json.RawMessage], path []string, value json.RawMessage, createMissing bool) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnJSONBSet, col, sqlb.Param(path), sqlb.Param(value), sqlb.Param(createMissing))
}

// JSONBInsert renders jsonb_insert(col, path, value).
func JSONBInsert(col sqlb.Expr[json.RawMessage], path []string, value json.RawMessage) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnJSONBInsert, col, sqlb.Param(path), sqlb.Param(value))
}

// BuildObject renders jsonb_build_object(k1, v1, ...). Keys are bound.
func BuildObject(pairs ...sqlb.SQLer) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnJSONBBuildObject, pairs...)
}

// Key is a bound object key for BuildObject, cast to text because the
// function is variadic "any" and gives a bare parameter no type.
func Key(k string) sqlb.SQLer { return sqlb.Cast[string](sqlb.Param(k), typeText) }

// BuildArray renders jsonb_build_array(...).
func BuildArray(items ...sqlb.SQLer) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnJSONBBuildArray, items...)
}

// ToJSONB renders to_jsonb(e). RowToJSON renders row_to_json(alias).
func ToJSONB(e sqlb.SQLer) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnToJSONB, e)
}

// RowToJSON renders row_to_json(alias), the whole row as a JSON object.
// ToJSONB is usually the better choice: jsonb is the indexable type.
func RowToJSON(t sqlb.Table) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return fnRowToJSON + "(" + t.Qualifier(a.Dialect()) + ")"
	})
}

// JSONBTypeof renders jsonb_typeof(e): "object", "array", "string",
// "number", "boolean" or "null".
func JSONBTypeof(e sqlb.Expr[json.RawMessage]) sqlb.Expr[string] {
	return fn[string](constructJSON, fnJSONBTypeof, e)
}

// JSONBArrayLength renders jsonb_array_length(e). It raises on a value that
// is not an array, so guard it with JSONBTypeof over mixed data.
func JSONBArrayLength(e sqlb.Expr[json.RawMessage]) sqlb.Expr[int32] {
	return fn[int32](constructJSON, fnJSONBArrayLength, e)
}

// StripNulls renders jsonb_strip_nulls(e), dropping object keys whose value
// is null. It does not touch nulls inside arrays, where removing an element
// would shift the indexes of the rest.
func StripNulls(e sqlb.Expr[json.RawMessage]) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnJSONBStripNulls, e)
}

// Pretty renders jsonb_pretty(e), indented text. For human output; it is
// larger than the compact form and not what you store.
func Pretty(e sqlb.Expr[json.RawMessage]) sqlb.Expr[string] {
	return fn[string](constructJSON, fnJSONBPretty, e)
}

// Set-returning JSON functions as FROM sources. Each returns the source and
// the typed column expressions to reference it by.

// ArrayElements renders jsonb_array_elements(col) AS alias (value).
func ArrayElements(col sqlb.Expr[json.RawMessage], alias string) (sqlb.Source, sqlb.Expr[json.RawMessage]) {
	src := FuncSource(fn[json.RawMessage](constructJSON, fnJSONBArrayElems, col), alias, "value")
	return src, sqlb.Col[json.RawMessage](sqlb.Ref(src), "value")
}

// ArrayElementsText renders jsonb_array_elements_text(col) AS alias (value).
func ArrayElementsText(col sqlb.Expr[json.RawMessage], alias string) (sqlb.Source, sqlb.Expr[string]) {
	src := FuncSource(fn[string](constructJSON, fnJSONBArrayElemsT, col), alias, "value")
	return src, sqlb.Col[string](sqlb.Ref(src), "value")
}

// Each renders jsonb_each(col) AS alias (key, value).
func Each(col sqlb.Expr[json.RawMessage], alias string) (sqlb.Source, sqlb.Expr[string], sqlb.Expr[json.RawMessage]) {
	src := FuncSource(fn[json.RawMessage](constructJSON, fnJSONBEach, col), alias, "key", "value")
	return src, sqlb.Col[string](sqlb.Ref(src), "key"), sqlb.Col[json.RawMessage](sqlb.Ref(src), "value")
}

// ObjectKeys renders jsonb_object_keys(col) AS alias (key).
func ObjectKeys(col sqlb.Expr[json.RawMessage], alias string) (sqlb.Source, sqlb.Expr[string]) {
	src := FuncSource(fn[string](constructJSON, fnJSONBObjectKeys, col), alias, "key")
	return src, sqlb.Col[string](sqlb.Ref(src), "key")
}

// PathQuery renders jsonb_path_query(col, path) AS alias (value).
func PathQuery(col sqlb.Expr[json.RawMessage], jsonpath string, alias string) (sqlb.Source, sqlb.Expr[json.RawMessage]) {
	call := expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return fnJSONBPathQuery + "(" + col.SQL(a) + ", " + a.Next(jsonpath) + "::jsonpath)"
	})
	src := FuncSource(call, alias, "value")
	return src, sqlb.Col[json.RawMessage](sqlb.Ref(src), "value")
}

// PathQueryFirst renders jsonb_path_query_first(col, path) as a scalar.
func PathQueryFirst(col sqlb.Expr[json.RawMessage], jsonpath string) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return fnJSONBPathQueryF + "(" + col.SQL(a) + ", " + a.Next(jsonpath) + "::jsonpath)"
	})
}

// JSONBAgg renders jsonb_agg(e), the group as a JSON array.
//
// All four aggregates here return NULL over zero rows, not an empty array or
// object. Wrap them in COALESCE with EmptyJSONArray or EmptyJSONObject when
// the caller expects a container.
func JSONBAgg(e sqlb.SQLer, opts ...sqlb.AggOption) sqlb.Expr[json.RawMessage] {
	return sqlb.Aggregate[json.RawMessage](fnJSONBAgg, []sqlb.SQLer{e}, opts...)
}

// JSONAgg renders json_agg(e), the json rather than jsonb form. It preserves
// key order and duplicate keys, which jsonb does not.
func JSONAgg(e sqlb.SQLer, opts ...sqlb.AggOption) sqlb.Expr[json.RawMessage] {
	return sqlb.Aggregate[json.RawMessage](fnJSONAgg, []sqlb.SQLer{e}, opts...)
}

// JSONBObjectAgg renders jsonb_object_agg(key, value), the group as a JSON
// object. A NULL key raises rather than being skipped.
func JSONBObjectAgg(key, value sqlb.SQLer, opts ...sqlb.AggOption) sqlb.Expr[json.RawMessage] {
	return sqlb.Aggregate[json.RawMessage](fnJSONBObjectAgg, []sqlb.SQLer{key, value}, opts...)
}
