package sqlite

import (
	"encoding/json"
	"errors"

	"github.com/ubgo/sqlb"
)

// The JSON1 extension, compiled into SQLite by default since 3.38.
//
// SQLite stores JSON as text, not as a parsed type, so there is no jsonb here
// and no index that understands a document. What makes a JSON query fast in
// SQLite is an expression index over the extracted value, which is DDL and so
// not this package's job; every helper here is what such an index is built on.

// JSONExtract renders json_extract(e, '$.path'). It returns the value with its
// SQL type, so a JSON number comes back as a number rather than as text.
//
// The path is an author constant rendered into the statement, because SQLite
// will not take a bound parameter where a path is expected in an expression
// index, and an index that does not match the query is worse than no index.
// A path built from request data does not belong here.
func JSONExtract[R any](e sqlb.Expr[string], path string) sqlb.Expr[R] {
	return fn[R](constructJSON, "json_extract", e, quoteLit(path))
}

// JSONArrow renders e -> '$.path' (3.38), which always yields JSON text. Extract
// is usually what you want, because it preserves the value's type.
func JSONArrow(e sqlb.Expr[string], path string) sqlb.Expr[string] {
	return binop[string](constructJSON, "->", e, quoteLit(path))
}

// JSONArrowText renders e ->> '$.path' (3.38), the extracted value as SQL text.
// It is the same spelling Postgres uses, and it means the same thing, which
// is rare enough between these two engines to be worth saying.
func JSONArrowText(e sqlb.Expr[string], path string) sqlb.Expr[string] {
	return binop[string](constructJSON, "->>", e, quoteLit(path))
}

// JSONValid renders json_valid(e). SQLite does not validate on write, so a text
// column holding JSON can hold anything; this is the check a CHECK constraint
// or a filter uses.
func JSONValid(e sqlb.Expr[string]) sqlb.Expr[bool] { return fn[bool](constructJSON, "json_valid", e) }

// JSONType renders json_type(e): "object", "array", "text", "integer", "real",
// "true", "false" or "null". Note that true and false are separate types, not
// one boolean, which differs from Postgres.
func JSONType(e sqlb.Expr[string]) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_type", e)
}

// JSONTypeAt renders json_type(e, '$.path'), the type at a path rather than of
// the whole document. NULL when the path matches nothing.
func JSONTypeAt(e sqlb.Expr[string], path string) sqlb.Expr[*string] {
	return fn[*string](constructJSON, "json_type", e, quoteLit(path))
}

// JSONArrayLength renders json_array_length(e).
func JSONArrayLength(e sqlb.Expr[string]) sqlb.Expr[int32] {
	return fn[int32](constructJSON, "json_array_length", e)
}

// JSONArrayLengthAt renders json_array_length(e, '$.path').
func JSONArrayLengthAt(e sqlb.Expr[string], path string) sqlb.Expr[int32] {
	return fn[int32](constructJSON, "json_array_length", e, quoteLit(path))
}

// JSONQuote renders json_quote(v), a Go value as a JSON literal. The value binds.
func JSONQuote[T any](v T) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_quote", sqlb.Param(v))
}

// JSONArray renders json_array(items...).
func JSONArray(items ...sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_array", items...)
}

// ErrIncompleteSpec is returned for a caller-written Pair with no Value: the
// type has exported fields and is written as a literal, so Pair{} is the easy
// mistake, and it rendered through a nil SQLer inside Build until 2026-09-22.
var ErrIncompleteSpec = errors.New("sqlb/sqlite: a JSON pair has no value")

// JSONObject renders json_object(k1, v1, k2, v2, ...) from alternating pairs.
// Keys are author constants; values are expressions and may bind.
func JSONObject(pairs ...Pair) sqlb.Expr[string] {
	args := make([]sqlb.SQLer, 0, len(pairs)*2)
	for _, p := range pairs {
		v := p.Value
		if v == nil {
			// A Pair{} literal has no value; it is refused at Build rather
			// than rendered through a nil SQLer.
			v = sqlb.Raw(func(a *sqlb.Args) string { a.Fail(ErrIncompleteSpec); return "" })
		}
		args = append(args, quoteLit(p.Key), v)
	}
	return fn[string](constructJSON, "json_object", args...)
}

// Pair is one key and value for Object. The key is an author constant, which
// is what keeps request data out of the JSON structure rather than only out
// of its values.
type Pair struct {
	Key   string
	Value sqlb.SQLer
}

// KV builds a Pair.
func KV(key string, value sqlb.SQLer) Pair { return Pair{Key: key, Value: value} }

// JSONSet renders json_set(e, '$.path', value): replaces the value at the path,
// and creates it when it is missing.
func JSONSet(e sqlb.Expr[string], path string, value sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_set", e, quoteLit(path), value)
}

// JSONInsert renders json_insert(e, '$.path', value): creates the value only when
// the path is missing, and leaves an existing one alone.
func JSONInsert(e sqlb.Expr[string], path string, value sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_insert", e, quoteLit(path), value)
}

// JSONReplace renders json_replace(e, '$.path', value): the mirror of Insert,
// replacing only what is already there.
func JSONReplace(e sqlb.Expr[string], path string, value sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_replace", e, quoteLit(path), value)
}

// JSONRemove renders json_remove(e, '$.path', ...), dropping one or more paths.
func JSONRemove(e sqlb.Expr[string], paths ...string) sqlb.Expr[string] {
	args := make([]sqlb.SQLer, 0, len(paths)+1)
	args = append(args, e)
	for _, p := range paths {
		args = append(args, quoteLit(p))
	}
	return fn[string](constructJSON, "json_remove", args...)
}

// JSONPatch renders json_patch(target, patch), an RFC 7386 merge patch: a null in
// the patch deletes the key rather than setting it to null.
func JSONPatch(target, patch sqlb.Expr[string]) sqlb.Expr[string] {
	return fn[string](constructJSON, "json_patch", target, patch)
}

// JSONGroupArray renders json_group_array(e), the group as a JSON array. It is
// SQLite's json_agg, and it returns "[]" over zero rows rather than NULL,
// which is the opposite of what Postgres does.
func JSONGroupArray(e sqlb.SQLer, opts ...sqlb.AggOption) sqlb.Expr[string] {
	return expr[string](constructJSON, sqlb.Aggregate[string]("json_group_array", []sqlb.SQLer{e}, opts...).SQL)
}

// JSONGroupObject renders json_group_object(key, value).
func JSONGroupObject(key, value sqlb.SQLer, opts ...sqlb.AggOption) sqlb.Expr[string] {
	return expr[string](constructJSON, sqlb.Aggregate[string]("json_group_object", []sqlb.SQLer{key, value}, opts...).SQL)
}

// JSONEach renders json_each(e) as a FROM source, one row per element of an array
// or member of an object. The returned Source carries the alias; its columns
// are key, value, type, atom, id, parent, fullkey and path.
func JSONEach(e sqlb.SQLer, alias string) sqlb.Source {
	return sqlb.SourceOf(alias, func(a *sqlb.Args) string {
		if !guard(a, constructJSON) {
			return ""
		}
		return "json_each(" + e.SQL(a) + ") AS " + a.Dialect().QuoteIdent(alias)
	})
}

// JSONTree renders json_tree(e) as a FROM source, which walks the whole document
// rather than one level. Same columns as Each.
func JSONTree(e sqlb.SQLer, alias string) sqlb.Source {
	return sqlb.SourceOf(alias, func(a *sqlb.Args) string {
		if !guard(a, constructJSON) {
			return ""
		}
		return "json_tree(" + e.SQL(a) + ") AS " + a.Dialect().QuoteIdent(alias)
	})
}

// RawJSON is json.RawMessage, re-exported so a caller declaring a JSON column
// does not need a second import to say what the column holds.
type RawJSON = json.RawMessage
