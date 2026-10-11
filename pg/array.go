package pg

import (
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	constructArray = "pg.array"

	opArrContains  = "@>"
	opArrContained = "<@"
	opArrOverlap   = "&&"
	opArrConcat    = "||"

	fnArrayDims     = "array_dims"
	fnArrayNDims    = "array_ndims"
	fnArrayLower    = "array_lower"
	fnArrayUpper    = "array_upper"
	fnArrayReverse  = "array_reverse"
	fnArraySort     = "array_sort"
	fnArrayShuffle  = "array_shuffle"
	fnArraySample   = "array_sample"
	fnTrimArray     = "trim_array"
	fnCardinality   = "cardinality"
	fnArrayLength   = "array_length"
	fnArrayAppend   = "array_append"
	fnArrayPrepend  = "array_prepend"
	fnArrayRemove   = "array_remove"
	fnArrayPosition = "array_position"
	fnArrayToString = "array_to_string"
	fnStringToArray = "string_to_array"
	fnUnnest        = "unnest"
	fnGenSeries     = "generate_series"
	fnGenSubscripts = "generate_subscripts"
	fnArrayCat      = "array_cat"
	fnArrayReplace  = "array_replace"
)

// Array renders ARRAY[$1::type, $2::type, ...] with every element bound and
// cast to the type T binds as. The cast is not decoration: ARRAY[$1, $2]
// gives Postgres nothing to type the parameters by, so it resolves them to
// text and the driver refuses to encode an integer there. Found by running
// this against a real server; every rendering test had passed.
func Array[T any](vals ...T) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string {
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = typedParam(v).SQL(a)
		}
		return "ARRAY[" + strings.Join(out, ", ") + "]"
	})
}

// ArrayContains renders col @> $n; ArrayContainedBy col <@ $n; Overlaps col && $n.
func ArrayContains[T any](col sqlb.Expr[[]T], v []T) sqlb.Predicate {
	return binpred(constructArray, opArrContains, col, sqlb.Param(v))
}

// ArrayContainedBy renders col <@ $n: every element of col appears in v.
// It is the mirror of ArrayContains, and getting the direction wrong is easy
// enough that the Go names say which side is which.
func ArrayContainedBy[T any](col sqlb.Expr[[]T], v []T) sqlb.Predicate {
	return binpred(constructArray, opArrContained, col, sqlb.Param(v))
}

// Overlaps renders col && $n: the two arrays share at least one element.
func Overlaps[T any](col sqlb.Expr[[]T], v []T) sqlb.Predicate {
	return binpred(constructArray, opArrOverlap, col, sqlb.Param(v))
}

// ArrayConcat renders l || r.
func ArrayConcat[T any](l, r sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return binop[[]T](constructArray, opArrConcat, l, r)
}

// Any renders $n = ANY(col): the array holds v. AnyOp renders $n op ANY(col).
func Any[T any](col sqlb.Expr[[]T], v T) sqlb.Predicate { return AnyOp(col, sqlb.OpEq, v) }

// AnyOp renders $n op ANY(col) for an operator other than equality, such as
// a comparison or a pattern match against every element.
func AnyOp[T any](col sqlb.Expr[[]T], op string, v T) sqlb.Predicate {
	return pred(constructArray, func(a *sqlb.Args) string {
		return a.Next(v) + " " + op + " ANY(" + col.SQL(a) + ")"
	})
}

// AllOp renders $n op ALL(col).
func AllOp[T any](col sqlb.Expr[[]T], op string, v T) sqlb.Predicate {
	return pred(constructArray, func(a *sqlb.Args) string {
		return a.Next(v) + " " + op + " ALL(" + col.SQL(a) + ")"
	})
}

// Subscript renders col[n]. Nullable: an index out of range yields NULL,
// not an error. The index is a literal because Postgres needs it constant
// inside an index expression; it is an author's choice, never request data.
func Subscript[T any](col sqlb.Expr[[]T], n int) sqlb.Expr[*T] {
	return expr[*T](constructArray, func(a *sqlb.Args) string {
		return col.SQL(a) + "[" + a.Next(n) + "]"
	})
}

// Slice renders col[lo:hi].
func Slice[T any](col sqlb.Expr[[]T], lo, hi int) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string {
		return col.SQL(a) + "[" + a.Next(lo) + ":" + a.Next(hi) + "]"
	})
}

// Cardinality renders cardinality(col): the total element count across every
// dimension. ArrayLength counts one dimension; on a one-dimensional array
// the two agree, and on anything else they do not.
func Cardinality[T any](col sqlb.Expr[[]T]) sqlb.Expr[int32] {
	return fn[int32](constructArray, fnCardinality, col)
}

// ArrayLength renders array_length(col, dim), 1-based. It is nullable on
// purpose: Postgres returns NULL for an empty array rather than 0, and a
// caller that treats the two alike writes a bug the type system can catch.
func ArrayLength[T any](col sqlb.Expr[[]T], dim int) sqlb.Expr[*int32] {
	return fn[*int32](constructArray, fnArrayLength, col, sqlb.Param(dim))
}

// ArrayAppend renders array_append(col, v), adding one element at the end.
func ArrayAppend[T any](col sqlb.Expr[[]T], v T) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayAppend, col, sqlb.Param(v))
}

// ArrayPrepend renders array_prepend(v, col). The value comes first, matching
// Postgres and reversing the argument order of ArrayAppend.
func ArrayPrepend[T any](v T, col sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayPrepend, sqlb.Param(v), col)
}

// ArrayRemove renders array_remove(col, v), dropping every element equal to v
// rather than the first.
func ArrayRemove[T any](col sqlb.Expr[[]T], v T) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayRemove, col, sqlb.Param(v))
}

// ArrayReplace renders array_replace(col, from, to), replacing every match.
func ArrayReplace[T any](col sqlb.Expr[[]T], from, to T) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayReplace, col, sqlb.Param(from), sqlb.Param(to))
}

// ArrayCat renders array_cat(l, r), the two arrays end to end.
func ArrayCat[T any](l, r sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayCat, l, r)
}

// ArrayPosition renders array_position(col, v): the 1-based index of the
// first match. Nullable, because a value that is absent yields NULL and not
// a sentinel index.
func ArrayPosition[T any](col sqlb.Expr[[]T], v T) sqlb.Expr[*int32] {
	return fn[*int32](constructArray, fnArrayPosition, col, sqlb.Param(v))
}

// ArrayToString renders array_to_string(col, sep). NULL elements are dropped
// rather than rendered, which is why it is not the inverse of StringToArray.
func ArrayToString[T any](col sqlb.Expr[[]T], sep string) sqlb.Expr[string] {
	return fn[string](constructArray, fnArrayToString, col, sqlb.Param(sep))
}

// StringToArray renders string_to_array(e, sep). An empty input yields an
// array with one empty element, not an empty array.
func StringToArray(e sqlb.Expr[string], sep string) sqlb.Expr[[]string] {
	return fn[[]string](constructArray, fnStringToArray, e, sqlb.Param(sep))
}

// Unnest renders unnest(col) AS alias (elem) and returns the typed element.
func Unnest[T any](col sqlb.Expr[[]T], alias string) (sqlb.Source, sqlb.Expr[T]) {
	src := FuncSource(fn[T](constructArray, fnUnnest, col), alias, "elem")
	return src, sqlb.Col[T](sqlb.Ref(src), "elem")
}

// UnnestOrdinal is Unnest WITH ORDINALITY: (elem, n).
func UnnestOrdinal[T any](col sqlb.Expr[[]T], alias string) (sqlb.Source, sqlb.Expr[T], sqlb.Expr[int64]) {
	src := WithOrdinality(FuncSource(fn[T](constructArray, fnUnnest, col), alias, "elem", "n"))
	return src, sqlb.Col[T](sqlb.Ref(src), "elem"), sqlb.Col[int64](sqlb.Ref(src), "n")
}

// GenerateSeries renders generate_series(CAST($1 AS bigint), ...) AS alias (n)
// for integers. Step 0 means no step argument.
//
// The casts are load-bearing. generate_series has integer, bigint, numeric
// and timestamp overloads, and a bare bound parameter carries no type, so
// Postgres reports "function generate_series(unknown, unknown, unknown) is
// not unique". The text rendered fine on every reading of it; running it
// against a server is what found this, which is the argument for the driver
// module in one construct.
func GenerateSeries(from, to, step int64, alias string) (sqlb.Source, sqlb.Expr[int64]) {
	args := []sqlb.SQLer{sqlb.Cast[int64](sqlb.Param(from), typeBigint), sqlb.Cast[int64](sqlb.Param(to), typeBigint)}
	if step != 0 {
		args = append(args, sqlb.Cast[int64](sqlb.Param(step), typeBigint))
	}
	src := FuncSource(fn[int64](constructArray, fnGenSeries, args...), alias, "n")
	return src, sqlb.Col[int64](sqlb.Ref(src), "n")
}

// GenerateSubscripts renders generate_subscripts(col, dim) AS alias (i).
func GenerateSubscripts[T any](col sqlb.Expr[[]T], dim int, alias string) (sqlb.Source, sqlb.Expr[int32]) {
	src := FuncSource(fn[int32](constructArray, fnGenSubscripts, col, sqlb.Param(dim)), alias, "i")
	return src, sqlb.Col[int32](sqlb.Ref(src), "i")
}

// InArray renders col = ANY($1) with one bound array. The statement text is
// then stable for any list length, so the prepared-statement cache hits and
// there is no parameter-count limit; prefer it over In for user-sized lists.
func InArray[T any](col sqlb.Expr[T], vals []T) sqlb.Predicate {
	return pred(constructArray, func(a *sqlb.Args) string {
		return col.SQL(a) + " = ANY(" + a.Next(vals) + ")"
	})
}

// The dimension and shape functions. Postgres arrays may be multidimensional,
// which almost nobody uses and everybody eventually meets, so these report the
// shape rather than assuming one dimension.

// ArrayDims renders array_dims(col), the shape as a text literal such as
// "[1:3]". Nullable: an empty array has no dimensions at all.
func ArrayDims[T any](col sqlb.Expr[[]T]) sqlb.Expr[*string] {
	return fn[*string](constructArray, fnArrayDims, col)
}

// ArrayNDims renders array_ndims(col), the number of dimensions. Nullable for
// the same reason as ArrayDims.
func ArrayNDims[T any](col sqlb.Expr[[]T]) sqlb.Expr[*int32] {
	return fn[*int32](constructArray, fnArrayNDims, col)
}

// ArrayLower renders array_lower(col, dim), the first subscript of a dimension.
//
// Postgres arrays are not necessarily 1-based, which is the trap this exists
// for: a slice built by array_remove or by a slice expression can start at any
// index, so code that assumes 1 is wrong on arrays it did not construct.
func ArrayLower[T any](col sqlb.Expr[[]T], dim int) sqlb.Expr[*int32] {
	return fn[*int32](constructArray, fnArrayLower, col, sqlb.Param(dim))
}

// ArrayUpper renders array_upper(col, dim), the last subscript of a dimension.
// With ArrayLower it is how a caller iterates an array whose bounds it does not
// control; ArrayLength is the count and is not the same thing.
func ArrayUpper[T any](col sqlb.Expr[[]T], dim int) sqlb.Expr[*int32] {
	return fn[*int32](constructArray, fnArrayUpper, col, sqlb.Param(dim))
}

// The reshaping functions, all Postgres 16 or later.

// ArrayReverse renders array_reverse(col). Postgres 18.
func ArrayReverse[T any](col sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayReverse, col)
}

// ArraySort renders array_sort(col). Postgres 18.
func ArraySort[T any](col sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArraySort, col)
}

// ArraySortDesc renders array_sort(col, descending), with the direction bound.
//
// A separate constructor rather than an option so the version requirement is
// visible at the call: array_sort in both forms is Postgres 18, and a server
// one release behind reports "function does not exist" at run time, which
// is how the number in this comment was corrected from 17.
func ArraySortDesc[T any](col sqlb.Expr[[]T], descending bool) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArraySort, col, sqlb.Param(descending))
}

// ArrayShuffle renders array_shuffle(col). Postgres 16.
func ArrayShuffle[T any](col sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayShuffle, col)
}

// ArraySample renders array_sample(col, n), n elements chosen at random.
// Postgres 16.
func ArraySample[T any](col sqlb.Expr[[]T], n int) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArraySample, col, sqlb.Param(n))
}

// TrimArray renders trim_array(col, n), the array without its last n elements.
// Postgres 14.
func TrimArray[T any](col sqlb.Expr[[]T], n int) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnTrimArray, col, sqlb.Param(n))
}
