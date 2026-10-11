// The operator vocabulary of the expression layer.
//
// These are here and not in render because an operator is SQL, not rendering. A
// target that is not SQL has no "<>" and no "IS DISTINCT FROM", so putting this
// file below would make the render layer a SQL package wearing a generic name.

package expr

import "errors"

// Comparison and logical operators emitted into WHERE / ON clauses.
//
// Why they are constants rather than inline literals: a predicate constructor
// is one line of string concatenation, and a typo there produces SQL that is
// syntactically valid but semantically wrong (">=" typed as "=>" fails loudly,
// but "<>" typed as "<" does not). Naming them removes the class.
const (
	OpEq        = "="
	OpNotEq     = "<>"
	OpLess      = "<"
	OpLessEq    = "<="
	OpGreater   = ">"
	OpGreaterEq = ">="
	OpIn        = "IN"
	OpNotIn     = "NOT IN"
	OpIsNull    = "IS NULL"
	OpIsNotNull = "IS NOT NULL"

	OpAdd    = "+"
	OpSub    = "-"
	OpMul    = "*"
	OpDiv    = "/"
	OpMod    = "%"
	OpConcat = "||"

	OpIsDistinctFrom    = "IS DISTINCT FROM"
	OpIsNotDistinctFrom = "IS NOT DISTINCT FROM"
	OpIsTrue            = "IS TRUE"
	OpIsFalse           = "IS FALSE"
	OpIsUnknown         = "IS UNKNOWN"
	OpLike              = "LIKE"
	OpNotLike           = "NOT LIKE"
)

// OpValues is the canonical iteration order over comparison operators. It
// exists so a future validator or documentation generator reads the same list
// the builder writes from, instead of drifting into its own copy.
var OpValues = []string{
	OpEq, OpNotEq, OpLess, OpLessEq, OpGreater, OpGreaterEq,
	OpIn, OpNotIn, OpIsNull, OpIsNotNull,
}

// Logical connectives used to combine predicates.
const (
	LogicAnd = "AND"
	LogicOr  = "OR"
	LogicNot = "NOT"
)

// Separators. Duplicated rather than shared with the statement layer: two
// characters of formatting are not worth an import, and a shared constant here
// would be the only thing pulling the layers together.
const (
	sepComma = ", "
	sepSpace = " "
)

// DISTINCT inside an aggregate, which is an expression-level modifier.
const kwDistinct = "DISTINCT"

// SQL function names the expression layer emits. Standard SQL, present in
// every dialect this module ships.
const (
	fnConcat     = "concat"
	fnCount      = "count"
	fnSum        = "sum"
	fnAvg        = "avg"
	fnMin        = "min"
	fnMax        = "max"
	fnRowNumber  = "row_number"
	fnRank       = "rank"
	fnDenseRank  = "dense_rank"
	fnFirstValue = "first_value"
	fnLastValue  = "last_value"
	fnNthValue   = "nth_value"
)

// Keywords that appear inside an expression rather than between clauses.
const (
	kwPartitionBy = "PARTITION BY"
	kwOrderBy     = "ORDER BY"
	kwAs          = "AS"
	kwOn          = "ON"
)

// SortDirection is the ORDER BY direction.
type SortDirection string

// Supported sort directions.
const (
	SortAsc  SortDirection = "ASC"
	SortDesc SortDirection = "DESC"
)

// SortDirectionValues is the canonical iteration order over sort directions.
var SortDirectionValues = []SortDirection{SortAsc, SortDesc}

// valid reports whether d is one of the two directions SQL has.
//
// Every closed set in this module answers this, because a named string type
// with constants and no check is not a closed set: the constants are a
// suggestion and any string reaches the statement text. Ten of them were found
// that way on 2026-09-24, in three engine packages, and each one was an
// injection through a type that looked safe.
func (d SortDirection) valid() bool {
	for _, v := range SortDirectionValues {
		if d == v {
			return true
		}
	}
	return false
}

// NullsOrder is where NULLs sort relative to values, the NULLS FIRST or NULLS
// LAST of an ORDER BY term.
//
// A named type with constants rather than the bare string this was until
// 2026-09-24: the set is closed, it is written into the statement verbatim,
// and an OrderTerm is a struct literal a caller fills, so nothing stopped an
// arbitrary string reaching the text. The empty value is legitimate and means
// the engine's own default, which is why it is not an error.
type NullsOrder string

// The two placements every engine here spells the same way.
const (
	NullsFirst NullsOrder = "NULLS FIRST"
	NullsLast  NullsOrder = "NULLS LAST"
)

// NullsOrderValues is the canonical iteration order over NULL placements.
var NullsOrderValues = []NullsOrder{NullsFirst, NullsLast}

// valid reports whether n is empty, meaning the engine's default, or one of
// the two placements.
func (n NullsOrder) valid() bool {
	if n == "" {
		return true
	}
	for _, v := range NullsOrderValues {
		if n == v {
			return true
		}
	}
	return false
}

// ErrUnknownValue reports a closed-set value outside its constants, which is
// refused rather than written: the constants are the whole set the grammar
// allows, so a value outside them is either a typo or text from somewhere it
// should never have come from.
var ErrUnknownValue = errors.New("sqlb: value is not one of its type's constants")
