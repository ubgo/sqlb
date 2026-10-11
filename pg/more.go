package pg

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
)

// Helpers the snippet catalogue needed beyond the first pass. Each is a
// Postgres construct with its own syntax; none is universal.

const (
	constructMore = "pg.more"

	kwRowsFrom   = "ROWS FROM"
	kwVariadic   = "VARIADIC"
	kwNamedArrow = "=>"
	kwBoth       = "BOTH"
	kwLeading    = "LEADING"
	kwTrailing   = "TRAILING"
	kwFor        = "FOR"

	// The type names and keywords the literal constructors below write. Each
	// is a constant because it is grammar: the value beside it is the
	// caller's and goes through the dialect's quoting, and these do not.
	kwTimestampLit = "timestamp"
	kwTimestamptz  = "timestamptz"
	kwDateLit      = "date"
	kwRegclass     = "regclass"
)

// RowsFrom renders ROWS FROM (f1(), f2(), ...) [WITH ORDINALITY] AS alias (cols),
// which zips several set-returning functions and pads the shorter with NULL.
func RowsFrom(alias string, calls []sqlb.SQLer, ordinality bool, columns ...string) sqlb.Source {
	return sqlb.SourceOf(alias, func(a *sqlb.Args) string {
		if !guard(a, constructMore) {
			return ""
		}
		d := a.Dialect()
		s := kwRowsFrom + " (" + list(a, calls) + ")"
		if ordinality {
			s += " " + kwWithOrdinality
		}
		s += " AS " + d.QuoteIdent(alias)
		if len(columns) > 0 {
			s += " (" + sqlb.QuoteList(d, columns) + ")"
		}
		return s
	})
}

// UnnestMulti renders unnest(a1, a2, ...) [WITH ORDINALITY] AS alias (cols):
// several arrays zipped, the shorter padded with NULL.
func UnnestMulti(alias string, arrays []sqlb.SQLer, ordinality bool, columns ...string) sqlb.Source {
	src := FuncSource(sqlb.Fn[[]byte](fnUnnest, arrays...), alias, columns...)
	if ordinality {
		return WithOrdinality(src)
	}
	return src
}

// Variadic marks an array argument as VARIADIC in a function call.
func Variadic(arr sqlb.SQLer) sqlb.SQLer {
	return litSQLer(constructText, func(a *sqlb.Args) string { return kwVariadic + " " + arr.SQL(a) })
}

// Named renders name => value, the named-argument notation.
func Named(name string, value sqlb.SQLer) sqlb.SQLer {
	return litSQLer(constructText, func(a *sqlb.Args) string {
		return a.Dialect().QuoteIdent(name) + " " + kwNamedArrow + " " + value.SQL(a)
	})
}

// SubstringRegex renders substring(e FROM pattern): the first capture
// group, or the whole match with no group. Nullable: no match is NULL.
func SubstringRegex(e sqlb.Expr[string], pattern string) sqlb.Expr[*string] {
	return expr[*string](constructText, func(a *sqlb.Args) string {
		return fnSubstring + "(" + e.SQL(a) + " " + kwFrom + " " + a.Next(pattern) + ")"
	})
}

// TrimSide is BOTH, LEADING or TRAILING.
type TrimSide string

// The three sides Postgres accepts in trim(side chars FROM e).
const (
	TrimBoth     TrimSide = kwBoth
	TrimLeading  TrimSide = kwLeading
	TrimTrailing TrimSide = kwTrailing
)

// TrimChars renders trim(side chars FROM e).
func TrimChars(side TrimSide, chars string, e sqlb.Expr[string]) sqlb.Expr[string] {
	return expr[string](constructText, func(a *sqlb.Args) string {
		return "trim(" + closedWord(a, side, side.valid(), "trim side") + " " + a.Next(chars) + " " + kwFrom + " " + e.SQL(a) + ")"
	})
}

// NormalForm is a Unicode normalization form for Normalize.
type NormalForm string

// The four Unicode normalization forms. NFC and NFD are canonical; NFKC and
// NFKD are compatibility forms, which fold characters such as the ligature ﬁ
// and so change the text rather than only its encoding.
const (
	NFC  NormalForm = "NFC"
	NFD  NormalForm = "NFD"
	NFKC NormalForm = "NFKC"
	NFKD NormalForm = "NFKD"
)

// Normalize renders normalize(e, FORM) (PG13). The form is a keyword.
func Normalize(e sqlb.Expr[string], form NormalForm) sqlb.Expr[string] {
	return expr[string](constructText, func(a *sqlb.Args) string {
		return "normalize(" + e.SQL(a) + ", " + closedWord(a, form, form.valid(), "normalization form") + ")"
	})
}

// RegexpInstr renders regexp_instr(e, pattern): the 1-based position of the
// first match, or 0 when there is none.
func RegexpInstr(e sqlb.Expr[string], pattern string) sqlb.Expr[int32] {
	return fn[int32](constructText, "regexp_instr", e, sqlb.Param(pattern))
}

// RegexpSubstr renders regexp_substr(e, pattern). Nullable: no match is
// NULL, not the empty string, and the two mean different things.
func RegexpSubstr(e sqlb.Expr[string], pattern string) sqlb.Expr[*string] {
	return fn[*string](constructText, "regexp_substr", e, sqlb.Param(pattern))
}

// OctetLength renders octet_length(e), the byte count. It differs from
// length() for any text that is not pure ASCII, which is the point of it.
func OctetLength(e sqlb.Expr[string]) sqlb.Expr[int32] {
	return fn[int32](constructText, "octet_length", e)
}

// QuoteIdent renders quote_ident(e), quoting a value for use as an
// identifier. For building dynamic SQL inside a server-side function; the
// builder quotes its own identifiers through the dialect.
func QuoteIdent(e sqlb.Expr[string]) sqlb.Expr[string] {
	return fn[string](constructText, "quote_ident", e)
}

// QuoteLiteral renders quote_literal(e), quoting a value for use as a
// literal. Same caveat as QuoteIdent: request data belongs in a Param.
func QuoteLiteral(e sqlb.Expr[string]) sqlb.Expr[string] {
	return fn[string](constructText, "quote_literal", e)
}

// StringToTable renders string_to_table(e, sep) AS alias (part) (PG14).
func StringToTable(e sqlb.Expr[string], sep string, alias string) (sqlb.Source, sqlb.Expr[string]) {
	src := FuncSource(fn[string](constructText, "string_to_table", e, sqlb.Param(sep)), alias, "part")
	return src, sqlb.Col[string](sqlb.Ref(src), "part")
}

// StringToArrayNull renders string_to_array(e, sep, nullStr), the three-argument form.
func StringToArrayNull(e sqlb.Expr[string], sep, nullStr string) sqlb.Expr[[]*string] {
	return fn[[]*string](constructText, fnStringToArray, e, sqlb.Param(sep), sqlb.Param(nullStr))
}

// SliceFrom renders col[lo:], a slice with an open upper end.
func SliceFrom[T any](col sqlb.Expr[[]T], lo int) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string { return "(" + col.SQL(a) + ")[" + a.Next(lo) + ":]" })
}

// SliceTo renders col[:hi], a slice with an open lower end.
func SliceTo[T any](col sqlb.Expr[[]T], hi int) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string { return "(" + col.SQL(a) + ")[:" + a.Next(hi) + "]" })
}

// SubscriptExpr renders col[index] with an expression index.
func SubscriptExpr[T any](col sqlb.Expr[[]T], index sqlb.SQLer) sqlb.Expr[*T] {
	return expr[*T](constructArray, func(a *sqlb.Args) string { return "(" + col.SQL(a) + ")[" + index.SQL(a) + "]" })
}

// ArrayPositions renders array_positions(col, v).
func ArrayPositions[T any](col sqlb.Expr[[]T], v T) sqlb.Expr[[]int32] {
	return fn[[]int32](constructArray, "array_positions", col, sqlb.Param(v))
}

// ArrayRemoveNull renders array_remove(col, NULL).
func ArrayRemoveNull[T any](col sqlb.Expr[[]T]) sqlb.Expr[[]T] {
	return fn[[]T](constructArray, fnArrayRemove, col, lit("NULL"))
}

// EmptyArray renders '{}'::type[], the typed empty array. An ARRAY[] of
// nothing needs the cast.
//
// sqlType is written into the statement as is, because no engine takes a
// placeholder where a type name goes and a type name is not one identifier:
// "character varying(10)" and "int[]" are both legal and neither survives
// quoting. It is the author's text, never a value from a request.
func EmptyArray[T any](sqlType string) sqlb.Expr[[]T] {
	return litExpr[[]T](constructArray, "'{}'::"+sqlType+"[]")
}

// ArrayLit renders an array literal in text form, for the odd shapes a
// Param cannot express such as a non-default lower bound.
//
// text is written by the dialect's literal quoting, so it may hold anything;
// sqlType is written as is and is the author's text, for the reason EmptyArray
// gives.
func ArrayLit[T any](text, sqlType string) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string {
		return a.Literal(text) + "::" + sqlType + "[]"
	})
}

// WidthBucketThresholds renders width_bucket(e, thresholds[]), the array
// form, for weighted picks.
func WidthBucketThresholds[T sqlb.Number](e sqlb.Expr[T], thresholds []T) sqlb.Expr[int32] {
	return fn[int32](constructMath, fnWidthBucket, e, sqlb.Param(thresholds))
}

// ExtractFrom renders extract(field FROM e) for any operand, such as an
// interval or an age() result. Extract is the timestamp form.
func ExtractFrom(field DateField, e sqlb.SQLer) sqlb.Expr[float64] {
	return expr[float64](constructTime, func(a *sqlb.Args) string {
		return fnExtract + "(" + closedWord(a, field, field.valid(), "date field") + " FROM " + e.SQL(a) + ")"
	})
}

// More date fields.
//
// The seven after the first four were missing until 2026-09-24, when a closed
// check made the absence matter: extract takes twenty-two units on Postgres
// 17, measured one at a time, and this package named fifteen. A list that
// validates has to be complete, which is the cost of choosing a list over a
// shape; TestTask_EveryDateFieldIsAcceptedByTheServer is what keeps it so.
const (
	FieldIsoDow         DateField = "isodow"
	FieldIsoYear        DateField = "isoyear"
	FieldTimezone       DateField = "timezone"
	FieldMillennium     DateField = "millennium"
	FieldMicroseconds   DateField = "microseconds"
	FieldMilliseconds   DateField = "milliseconds"
	FieldDecade         DateField = "decade"
	FieldCentury        DateField = "century"
	FieldTimezoneHour   DateField = "timezone_hour"
	FieldTimezoneMinute DateField = "timezone_minute"
	FieldJulian         DateField = "julian"
)

// DateTruncZone renders date_trunc('field', e, 'zone') (PG12).
func DateTruncZone(field DateField, e sqlb.Expr[time.Time], zone string) sqlb.Expr[time.Time] {
	return fn[time.Time](constructTime, fnDateTrunc, closedLit(field, field.valid(), "date field"), e, sqlb.Param(zone))
}

// TimestampLit renders timestamp '...', a typed literal for an author
// constant. Never for request data, which belongs in a Param.
func TimestampLit(s string) sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(a *sqlb.Args) string { return kwTimestampLit + " " + a.Literal(s) })
}

// TimestamptzLit renders '...'::timestamptz, the zone-aware form.
func TimestamptzLit(s string) sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(a *sqlb.Args) string { return a.Literal(s) + "::" + kwTimestamptz })
}

// DateLit renders date '...'.
func DateLit(s string) sqlb.Expr[time.Time] {
	return expr[time.Time](constructTime, func(a *sqlb.Args) string { return kwDateLit + " " + a.Literal(s) })
}

// Infinity renders 'infinity'::timestamptz for open-ended validity.
func Infinity() sqlb.Expr[time.Time] {
	return litExpr[time.Time](constructTime, "'infinity'::timestamptz")
}

// OverlapsInterval renders (start1, interval1) OVERLAPS (start2, interval2).
func OverlapsInterval(s1 sqlb.Expr[time.Time], i1 sqlb.Expr[Interval], s2 sqlb.Expr[time.Time], i2 sqlb.Expr[Interval]) sqlb.Predicate {
	return pred(constructTime, func(a *sqlb.Args) string {
		return "(" + s1.SQL(a) + ", " + i1.SQL(a) + ") OVERLAPS (" + s2.SQL(a) + ", " + i2.SQL(a) + ")"
	})
}

// IntervalMul renders (iv * n), for random() * interval '365 days'.
func IntervalMul(iv sqlb.Expr[Interval], n sqlb.SQLer) sqlb.Expr[Interval] {
	return expr[Interval](constructTime, func(a *sqlb.Args) string { return "(" + n.SQL(a) + " * " + iv.SQL(a) + ")" })
}

// JustifyInterval renders justify_interval(iv).
func JustifyInterval(iv sqlb.Expr[Interval]) sqlb.Expr[Interval] {
	return fn[Interval](constructTime, fnJustifyInterval, iv)
}

// RangeExpr builds a range from two expressions, so a bound may be NULL or
// a column rather than a value. constructor is the Postgres range function
// name, such as daterange or numrange.
//
// constructor is written into the statement as is: a function name may be
// schema-qualified, so quoting it would make one identifier of two. It is the
// author's text, never a value from a request; bounds is checked, because it
// is a closed set.
func RangeExpr[T any](constructor string, lo, hi sqlb.SQLer, bounds Bounds) sqlb.Expr[Range[T]] {
	return fn[Range[T]](constructRange, constructor, lo, hi, closedLit(bounds, bounds.valid(), "range bounds"))
}

// NullBound is the unbounded side of a range, rendered as NULL.
func NullBound() sqlb.SQLer { return lit("NULL") }

// DateRange renders daterange(lo, hi, bounds).
func DateRange(lo, hi sqlb.SQLer, bounds Bounds) sqlb.Expr[Range[time.Time]] {
	return RangeExpr[time.Time]("daterange", lo, hi, bounds)
}

// NumRange renders numrange(lo, hi, bounds).
func NumRange(lo, hi sqlb.SQLer, bounds Bounds) sqlb.Expr[Range[float64]] {
	return RangeExpr[float64]("numrange", lo, hi, bounds)
}

// TstzRangeExpr renders tstzrange(lo, hi, bounds) from expressions.
func TstzRangeExpr(lo, hi sqlb.SQLer, bounds Bounds) sqlb.Expr[Range[time.Time]] {
	return RangeExpr[time.Time]("tstzrange", lo, hi, bounds)
}

// Int4Range renders int4range(lo, hi, bounds) from two bound values.
func Int4Range(lo, hi int32, bounds Bounds) sqlb.Expr[Range[int32]] {
	return RangeExpr[int32]("int4range", sqlb.Param(lo), sqlb.Param(hi), bounds)
}

// UpperInf and LowerInf test for an unbounded side.
func UpperInf[T any](r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return pred(constructRange, func(a *sqlb.Args) string { return "upper_inf(" + r.SQL(a) + ")" })
}

// LowerInf renders lower_inf(r): the range has no lower bound.
func LowerInf[T any](r sqlb.Expr[Range[T]]) sqlb.Predicate {
	return pred(constructRange, func(a *sqlb.Args) string { return "lower_inf(" + r.SQL(a) + ")" })
}

// Multirange is a Postgres multirange in text form, typed by element.
type Multirange[T any] string

// TstzMultirange renders tstzmultirange(r).
func TstzMultirange(r sqlb.Expr[Range[time.Time]]) sqlb.Expr[Multirange[time.Time]] {
	return fn[Multirange[time.Time]](constructRange, "tstzmultirange", r)
}

// MultirangeDiff renders (a - b) for multiranges.
func MultirangeDiff[T any](a, b sqlb.Expr[Multirange[T]]) sqlb.Expr[Multirange[T]] {
	return binop[Multirange[T]](constructRange, opRangeDiff, a, b)
}

// RangeAggTyped renders range_agg(r) typed as a multirange, and
// RangeIntersectAgg renders range_intersect_agg(r).
func RangeAggTyped[T any](r sqlb.Expr[Range[T]], opts ...sqlb.AggOption) sqlb.Expr[Multirange[T]] {
	return expr[Multirange[T]](constructRange, sqlb.Aggregate[Multirange[T]](fnRangeAgg, []sqlb.SQLer{r}, opts...).SQL)
}

// RangeIntersectAgg renders range_intersect_agg(r), the range common to
// every row in the group. Empty when any two rows do not overlap.
func RangeIntersectAgg[T any](r sqlb.Expr[Range[T]], opts ...sqlb.AggOption) sqlb.Expr[Range[T]] {
	return expr[Range[T]](constructRange, sqlb.Aggregate[Range[T]]("range_intersect_agg", []sqlb.SQLer{r}, opts...).SQL)
}

// UnnestMultirange renders unnest(m) as a scalar set-returning expression.
func UnnestMultirange[T any](m sqlb.Expr[Multirange[T]]) sqlb.Expr[Range[T]] {
	return fn[Range[T]](constructRange, fnUnnest, m)
}

// Regclass renders 'name'::regclass, an author constant naming a relation.
func Regclass(name string) sqlb.Expr[int64] {
	return expr[int64](constructMisc, func(a *sqlb.Args) string { return a.Literal(name) + "::" + kwRegclass })
}

// BitAnd renders l & r.
//
// This and the five below are Postgres helpers even though SQLite and MySQL
// share the spelling, because ClickHouse spells them as bitAnd() functions.
// Three of four engines agreeing is not the rule the core lives by.
func BitAnd[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[T] { return binop[T](constructMath, "&", l, r) }

// BitOr renders l | r.
func BitOr[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[T] { return binop[T](constructMath, "|", l, r) }

// BitXor renders l # r. Postgres spells bitwise XOR with #, not ^, which is
// exponentiation there.
func BitXor[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[T] { return binop[T](constructMath, "#", l, r) }

// BitNot renders ~e.
func BitNot[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructMath, func(a *sqlb.Args) string { return "(~" + e.SQL(a) + ")" })
}

// ShiftLeft renders e << n.
func ShiftLeft[T sqlb.Number](e sqlb.Expr[T], n int) sqlb.Expr[T] {
	return binop[T](constructMath, "<<", e, sqlb.Param(n))
}

// ShiftRight renders e >> n.
func ShiftRight[T sqlb.Number](e sqlb.Expr[T], n int) sqlb.Expr[T] {
	return binop[T](constructMath, ">>", e, sqlb.Param(n))
}

// BitString is a bit(n) or varbit value in text form.
type BitString string

// ErrNotBits is returned by BitLit for text that is not a bit string. A bit
// literal cannot be written through the dialect's quoting — B'1011' is its own
// syntax and the quoting would make it an ordinary string — so the only
// defence is the shape, and Postgres's shape for it is exactly the digits 0
// and 1.
var ErrNotBits = errors.New("sqlb/pg: a bit literal holds only the digits 0 and 1")

// BitLit renders B'1011'.
func BitLit(bits string) sqlb.Expr[BitString] {
	return expr[BitString](constructMath, func(a *sqlb.Args) string {
		if bits == "" || strings.ContainsFunc(bits, func(r rune) bool { return r != '0' && r != '1' }) {
			a.Fail(fmt.Errorf("%w: %q", ErrNotBits, bits))
			return ""
		}
		return "B'" + bits + "'"
	})
}

// BitStringOr renders l | r for two bit strings, which is a different
// operand type from BitOr even though the operator is the same.
func BitStringOr(l, r sqlb.Expr[BitString]) sqlb.Expr[BitString] {
	return binop[BitString](constructMath, "|", l, r)
}

// GetBit renders get_bit(e, n), the 0-based bit at n.
func GetBit(e sqlb.Expr[BitString], n int) sqlb.Expr[int32] {
	return fn[int32](constructMath, "get_bit", e, sqlb.Param(n))
}

// BitCount renders bit_count(e), the number of set bits.
func BitCount(e sqlb.Expr[BitString]) sqlb.Expr[int64] {
	return fn[int64](constructMath, "bit_count", e)
}

// PowerOp renders (base ^ exp), the operator form of exponentiation. In
// Postgres ^ is power, not XOR; BitXor is the XOR helper.
func PowerOp[T sqlb.Number](base sqlb.Expr[T], exp sqlb.SQLer) sqlb.Expr[float64] {
	return binop[float64](constructMath, "^", base, exp)
}

// Radians renders radians(e), converting degrees to radians. The
// trigonometric functions below take radians, so a latitude or longitude has
// to pass through here first.
func Radians[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, "radians", e)
}

// Sin renders sin(e), the operand in radians.
func Sin[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, "sin", e)
}

// Cos renders cos(e), the operand in radians.
func Cos[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, "cos", e)
}

// Asin renders asin(e), returning radians.
func Asin[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[float64] {
	return fn[float64](constructMath, "asin", e)
}

// Div renders div(l, r), integer division truncated toward zero. The /
// operator on integers already truncates; this is the explicit spelling and
// the one that works on numeric.
func Div[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, "div", l, r) }

// ModFn renders mod(l, r). Named for the function rather than Mod because
// the operator form is %, which a caller may also want.
func ModFn[T sqlb.Number](l, r sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, "mod", l, r) }

// RoundHalf renders round(e), to the nearest integer with halves away from
// zero. Round takes a digit count; this one does not.
func RoundHalf[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnRound, e) }

// TruncZero renders trunc(e), toward zero, with no digit count.
func TruncZero[T sqlb.Number](e sqlb.Expr[T]) sqlb.Expr[T] { return fn[T](constructMath, fnTrunc, e) }

// RandomNormal renders random_normal(mean, stddev) (PG16), a normally
// distributed value rather than the uniform one random() gives.
func RandomNormal(mean, stddev float64) sqlb.Expr[float64] {
	return fn[float64](constructMath, "random_normal", sqlb.Param(mean), sqlb.Param(stddev))
}

// Setseed renders setseed(seed), making the following random() calls
// reproducible in the same session. For tests and for a sampled query that
// must return the same sample twice.
func Setseed(seed float64) sqlb.Expr[[]byte] {
	return fn[[]byte](constructMath, "setseed", sqlb.Param(seed))
}

// AdvisoryXactLock renders pg_advisory_xact_lock(key), a transaction-scoped
// advisory lock.
//
// Transaction-scoped is the safe half of the advisory-lock family: the lock
// is released when the transaction ends, so a crashed process cannot hold it
// forever. The session-scoped variants are deliberately absent.
func AdvisoryXactLock(key sqlb.SQLer) sqlb.Expr[[]byte] {
	return fn[[]byte](constructMath, "pg_advisory_xact_lock", key)
}

// AdvisoryXactLock2 renders pg_advisory_xact_lock(namespace, key), the
// two-argument form. The namespace keeps one application's keys from
// colliding with another's in the single global lock space.
func AdvisoryXactLock2(namespace int32, key sqlb.SQLer) sqlb.Expr[[]byte] {
	return fn[[]byte](constructMath, "pg_advisory_xact_lock", sqlb.Param(namespace), key)
}

// TryAdvisoryXactLock renders pg_try_advisory_xact_lock(key), which returns
// false rather than waiting. The non-blocking form a queue worker wants.
func TryAdvisoryXactLock(key sqlb.SQLer) sqlb.Expr[bool] {
	return fn[bool](constructMath, "pg_try_advisory_xact_lock", key)
}

// Hashtext renders hashtext(e), the hash Postgres uses internally. It is the
// usual way to turn a string key into the bigint an advisory lock needs.
// Not stable across major versions, so never persist it.
func Hashtext(e sqlb.Expr[string]) sqlb.Expr[int32] { return fn[int32](constructMath, "hashtext", e) }

// SizePretty renders pg_size_pretty(bytes), a human-readable byte count.
//
// This and the helpers below read the catalogue rather than application
// tables. They belong in an admin query or a health check, not in a request
// path: several of them take a lock or scan the whole relation.
func SizePretty(bytes sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructMath, "pg_size_pretty", bytes)
}

// TotalRelationSize renders pg_total_relation_size(oid): the table, its
// indexes and its TOAST data together.
func TotalRelationSize(oid sqlb.SQLer) sqlb.Expr[int64] {
	return fn[int64](constructMath, "pg_total_relation_size", oid)
}

// RelationSize renders pg_relation_size(oid): the table alone, without
// indexes. The difference from TotalRelationSize is what the indexes cost.
func RelationSize(oid sqlb.SQLer) sqlb.Expr[int64] {
	return fn[int64](constructMath, "pg_relation_size", oid)
}

// IndexesSize renders pg_indexes_size(oid), the indexes alone.
func IndexesSize(oid sqlb.SQLer) sqlb.Expr[int64] {
	return fn[int64](constructMath, "pg_indexes_size", oid)
}

// ColumnSize renders pg_column_size(e), the on-disk bytes of one value
// including compression. Useful for deciding whether a jsonb column is worth
// splitting out.
func ColumnSize(e sqlb.SQLer) sqlb.Expr[int32] { return fn[int32](constructMath, "pg_column_size", e) }

// XidAge renders age(xid), transactions elapsed since xid. The number a
// wraparound-vacuum alert watches.
func XidAge(xid sqlb.SQLer) sqlb.Expr[int64] { return fn[int64](constructMath, "age", xid) }

// FormatType renders format_type(typid, typmod), the SQL name of a type as
// it would be written in DDL.
func FormatType(typid, typmod sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructMath, "format_type", typid, typmod)
}

// CancelBackend renders pg_cancel_backend(pid), cancelling the current query
// on that connection but leaving it open.
func CancelBackend(pid sqlb.SQLer) sqlb.Expr[bool] {
	return fn[bool](constructMath, "pg_cancel_backend", pid)
}

// TerminateBackend renders pg_terminate_backend(pid), closing the connection
// outright. CancelBackend is the one to try first.
func TerminateBackend(pid sqlb.SQLer) sqlb.Expr[bool] {
	return fn[bool](constructMath, "pg_terminate_backend", pid)
}

// BlockingPids renders pg_blocking_pids(pid), the backends holding the locks
// that pid is waiting for. The first call in any lock investigation.
func BlockingPids(pid sqlb.SQLer) sqlb.Expr[[]int32] {
	return fn[[]int32](constructMath, "pg_blocking_pids", pid)
}

// InputIsValid renders pg_input_is_valid(value, type) (PG16): whether the
// text parses as that type. It answers without raising, which is what makes
// it usable in a validation query over existing rows.
func InputIsValid(value, sqlType string) sqlb.Expr[bool] {
	return fn[bool](constructMath, "pg_input_is_valid", sqlb.Param(value), sqlb.Param(sqlType))
}

// GenerateSeriesScalar renders generate_series(from, to) in the select list
// rather than in FROM.
//
// This and the helpers below are the expression forms of functions whose
// other helpers take Go values. They exist so an argument can be a column or
// another expression instead of a constant.
func GenerateSeriesScalar(from, to sqlb.SQLer) sqlb.Expr[int64] {
	return fn[int64](constructArray, fnGenSeries, castBigint(from), castBigint(to))
}

// castBigint renders CAST(e AS bigint), which types a bare parameter and is
// a no-op on a column; generate_series is overloaded and refuses an untyped
// pair.
func castBigint(e sqlb.SQLer) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string { return "CAST(" + e.SQL(a) + " AS " + typeBigint + ")" })
}

// UnnestScalar renders unnest(arr) in the select list. A set-returning
// function there multiplies the rows, which is legal in Postgres and a
// surprise to everyone the first time.
func UnnestScalar[T any](arr sqlb.Expr[[]T]) sqlb.Expr[T] {
	return fn[T](constructArray, fnUnnest, arr)
}

// RepeatExpr renders repeat(e, n) with an expression count.
func RepeatExpr(e sqlb.Expr[string], n sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructText, fnRepeat, e, n)
}

// PowerExpr renders power(base, exp) with an expression exponent.
func PowerExpr[T sqlb.Number](base sqlb.Expr[T], exp sqlb.SQLer) sqlb.Expr[float64] {
	return fn[float64](constructMath, fnPower, base, exp)
}

// SubstrExpr renders substr(e, from, count) with expression bounds.
func SubstrExpr(e sqlb.Expr[string], from, count sqlb.SQLer) sqlb.Expr[string] {
	return fn[string](constructText, "substr", e, from, count)
}

// ArrayElementsScalar renders jsonb_array_elements(doc) in the select list.
func ArrayElementsScalar(doc sqlb.SQLer) sqlb.Expr[json.RawMessage] {
	return fn[json.RawMessage](constructJSON, fnJSONBArrayElems, doc)
}

// PathQueryScalar renders jsonb_path_query(doc, path) in the select list.
func PathQueryScalar(doc sqlb.SQLer, path string) sqlb.Expr[json.RawMessage] {
	return expr[json.RawMessage](constructJSON, func(a *sqlb.Args) string {
		return fnJSONBPathQuery + "(" + doc.SQL(a) + ", " + a.Next(path) + "::jsonpath)"
	})
}

// CurrentDatabase renders current_database().
//
// This and the helpers below report session, transaction and replication
// state. They are for health checks, diagnostics and replication lag
// monitoring, not for application logic.
func CurrentDatabase() sqlb.Expr[string] { return fn[string](constructMath, "current_database") }

// HasRole renders pg_has_role(user, role, privilege), where privilege is
// MEMBER or USAGE.
func HasRole(user sqlb.SQLer, role, privilege string) sqlb.Expr[bool] {
	return fn[bool](constructMath, "pg_has_role", user, sqlb.Param(role), sqlb.Param(privilege))
}

// CurrentXactID renders pg_current_xact_id(), assigning one if this
// transaction has not written yet. Use CurrentXactIDIfAssigned to observe
// without forcing an assignment.
func CurrentXactID() sqlb.Expr[int64] { return fn[int64](constructMath, "pg_current_xact_id") }

// CurrentXactIDIfAssigned renders pg_current_xact_id_if_assigned(), NULL
// when the transaction has not written. It does not consume an id.
func CurrentXactIDIfAssigned() sqlb.Expr[*int64] {
	return fn[*int64](constructMath, "pg_current_xact_id_if_assigned")
}

// TxidCurrent renders txid_current(), the pre-PG13 spelling of
// CurrentXactID. Kept for servers older than 13.
func TxidCurrent() sqlb.Expr[int64] { return fn[int64](constructMath, "txid_current") }

// ExportSnapshot renders pg_export_snapshot(), so another session can see
// exactly this transaction's view. The mechanism behind a consistent
// parallel dump.
func ExportSnapshot() sqlb.Expr[string] { return fn[string](constructMath, "pg_export_snapshot") }

// CurrentWalLsn renders pg_current_wal_lsn(), the primary's write position.
func CurrentWalLsn() sqlb.Expr[string] { return fn[string](constructMath, "pg_current_wal_lsn") }

// WalLsnDiff renders pg_wal_lsn_diff(a, b), the byte distance between two
// WAL positions. Replication lag in bytes.
func WalLsnDiff(a, b sqlb.SQLer) sqlb.Expr[int64] {
	return fn[int64](constructMath, "pg_wal_lsn_diff", a, b)
}

// LastXactReplayTimestamp renders pg_last_xact_replay_timestamp(), the
// commit time of the last replayed transaction. Replication lag in time,
// which is the number an alert should use.
func LastXactReplayTimestamp() sqlb.Expr[time.Time] {
	return fn[time.Time](constructMath, "pg_last_xact_replay_timestamp")
}

// GetSerialSequence renders pg_get_serial_sequence(table, column), the
// sequence name behind a serial or identity column. The argument to Setval
// after a bulk load that wrote explicit ids.
func GetSerialSequence(table, column string) sqlb.Expr[string] {
	return fn[string](constructMath, "pg_get_serial_sequence", sqlb.Param(table), sqlb.Param(column))
}

// Setval renders setval(sequence, value), moving a sequence forward. The
// repair after a load that inserted ids the sequence does not know about.
func Setval(sequence sqlb.SQLer, value sqlb.SQLer) sqlb.Expr[int64] {
	return fn[int64](constructMath, "setval", sequence, value)
}

// Encode renders encode(bytes, format), core Postgres: format is 'hex',
// 'base64' or 'escape'.
func Encode(e sqlb.Expr[[]byte], format string) sqlb.Expr[string] {
	return fn[string](constructText, "encode", e, sqlb.Param(format))
}

// DaysBetween renders l - r for two dates, which Postgres types as an
// integer day count rather than an interval. That typing is Postgres
// specific, which is why these three are here and not in the core.
func DaysBetween(l, r sqlb.Expr[time.Time]) sqlb.Expr[int32] {
	return binop[int32](constructTime, sqlb.OpSub, l, r)
}

// DatePlusDays renders d + days, where an integer added to a date means
// days.
func DatePlusDays(d sqlb.Expr[time.Time], days sqlb.SQLer) sqlb.Expr[time.Time] {
	return binop[time.Time](constructTime, sqlb.OpAdd, d, days)
}

// DateMinusDays renders d - days.
func DateMinusDays(d sqlb.Expr[time.Time], days sqlb.SQLer) sqlb.Expr[time.Time] {
	return binop[time.Time](constructTime, sqlb.OpSub, d, days)
}

// OverlapsExpr renders l && r for two array expressions.
//
// These three are the expression forms of Overlaps, ArrayContains and
// ArrayContainedBy, which compare against a Go slice. Both sides here are
// expressions, so a column can be compared with another column.
func OverlapsExpr[T any](l, r sqlb.Expr[[]T]) sqlb.Predicate {
	return binpred(constructArray, opArrOverlap, l, r)
}

// ArrayContainsExpr renders l @> r for two array expressions.
func ArrayContainsExpr[T any](l, r sqlb.Expr[[]T]) sqlb.Predicate {
	return binpred(constructArray, opArrContains, l, r)
}

// ArrayContainedByExpr renders l <@ r for two array expressions.
func ArrayContainedByExpr[T any](l, r sqlb.Expr[[]T]) sqlb.Predicate {
	return binpred(constructArray, opArrContained, l, r)
}

// UUIDv7Timestamp extracts the millisecond timestamp from a UUID v7 on any
// Postgres version: the first 48 bits, read as hex, as epoch milliseconds.
// Needs no extension; it is text and bit-string arithmetic.
func UUIDv7Timestamp(u sqlb.Expr[string]) sqlb.Expr[time.Time] {
	return expr[time.Time](constructMisc, func(a *sqlb.Args) string {
		return "to_timestamp(('x' || substr(replace(" + u.SQL(a) + "::text, '-', ''), 1, 12))::bit(48)::bigint / 1000.0)"
	})
}

// SliceExprs renders (col)[lo:hi] with expression bounds.
func SliceExprs[T any](col sqlb.Expr[[]T], lo, hi sqlb.SQLer) sqlb.Expr[[]T] {
	return expr[[]T](constructArray, func(a *sqlb.Args) string {
		return "(" + col.SQL(a) + ")[" + lo.SQL(a) + ":" + hi.SQL(a) + "]"
	})
}

// TSRankCDNormalized renders ts_rank_cd(vector, query, normalization).
func TSRankCDNormalized(v sqlb.Expr[TSVector], q sqlb.Expr[TSQuery], normalization int) sqlb.Expr[float32] {
	return fn[float32](constructFTS, fnTSRankCD, v, q, sqlb.Param(normalization))
}

// SetConfig renders set_config(name, value, is_local), which is how a session
// setting takes a bound value.
//
// SET accepts no bind parameter; set_config does, and with is_local true it
// lasts for the transaction exactly like SET LOCAL. The name is an author
// constant and is rendered into the statement; only the value binds.
func SetConfig(name, value string, local bool) sqlb.Expr[string] {
	return expr[string](constructMisc, func(a *sqlb.Args) string {
		return "set_config(" + quoteLit(name).SQL(a) + ", " + a.Next(value) + ", " + strconv.FormatBool(local) + ")"
	})
}

// SetLocal binds a setting for the current transaction.
func SetLocal(name, value string) sqlb.Expr[string] { return SetConfig(name, value, true) }

// CurrentSetting renders current_setting('name', true): NULL when unset.
func CurrentSetting(name string) sqlb.Expr[*string] {
	return expr[*string](constructMisc, func(a *sqlb.Args) string {
		return "current_setting(" + quoteLit(name).SQL(a) + ", true)"
	})
}
