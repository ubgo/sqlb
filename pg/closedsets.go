package pg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
)

// The named string types, and which of them are closed.
//
// A named string type with a list of constants beside it looks like a closed
// set and is not one: Go lets any caller write T("anything at all"), and where
// the value is concatenated into the statement rather than quoted, that string
// is the statement. The SQLite package found this on 2026-09-24 in its
// OrAction, which was interpolated verbatim at three call sites, two of them
// shipped long before the third was written. This file is the answer for the
// same shape here.
//
// Every named string type in pg is now in one of three groups, and each group
// has a different fix because the three are different problems.
//
//  1. A CLOSED set of keywords the grammar allows, which cannot be quoted
//     because a keyword in quotes is not a keyword. TableLockMode, TrimSide,
//     NormalForm, JSONKind, DateField, Weight and Bounds are these, and each
//     was checked against the server for the question that matters: can an
//     EXTENSION add a value? For these seven the answer is no, because each is
//     a word in Postgres's own grammar or a value its own C code enumerates.
//     SampleMethod looked like one of them and is not — tsm_system_rows brings
//     SYSTEM_ROWS — so it moved to group 3, which is the correction this
//     paragraph exists to record. They validate: valid() reports whether the value is one of the
//     package's own constants, and every render site writes it through
//     closedWord, which fails the build with ErrUnknownValue on anything
//     else. Validation is the only defence available, so it must be at every
//     site rather than at the one being written.
//
//  2. An OPEN set that is an identifier, because an extension adds members
//     this package has never heard of. AccessMethod, IndexMethod and Language
//     are these. They need no validation and must not have any: the dialect
//     writes them through QuoteIdent, so a hostile value is one identifier
//     that does not exist and the server says so. Adding a list here would
//     only break the extension that brings a real one.
//
//  3. An OPEN set that is neither a keyword nor an identifier, where the only
//     defence is a shape. ExcludeOp is an operator token, which Postgres
//     builds from a defined character set and which cannot be quoted at all,
//     and TypeCategory is a single character. Both check the shape the server
//     documents rather than a list of values.
//
// TSVector, TSQuery, BitString and Interval are none of the three: they are
// value types, they are never concatenated into a statement, and each reaches
// the server through a bound parameter or the dialect's literal quoting.

// ErrUnknownValue is returned for a value of a closed named string type that
// is not one of the package's own constants: pg.TrimSide("; DROP TABLE t")
// compiles, and this is where it stops.
var ErrUnknownValue = errors.New("sqlb/pg: value is not one of this type's constants")

// closedWord writes a value from a closed set as a bare keyword, failing the
// build on anything outside the set. It is keywordOf for the named string
// types: the same refusal, for a type whose values are its own constants
// rather than an int.
func closedWord[T ~string](a *sqlb.Args, v T, valid bool, what string) string {
	if !valid {
		a.Fail(fmt.Errorf("%w: %s %q", ErrUnknownValue, what, string(v)))
		return ""
	}
	return string(v)
}

// closedLiteral is closedWord for a value the statement writes as a literal
// rather than as a keyword. The quoting already contains a hostile value, so
// this adds nothing to safety and everything to the error: a range built with
// Bounds("nonsense") fails at Build naming the value, instead of reaching the
// server as a literal it rejects with a message about a range constructor.
func closedLiteral[T ~string](a *sqlb.Args, v T, valid bool, what string) string {
	if !valid {
		a.Fail(fmt.Errorf("%w: %s %q", ErrUnknownValue, what, string(v)))
		return ""
	}
	return a.Literal(string(v))
}

// closedLit is quoteLit for a value from a closed set: the SQLer it returns
// writes the literal, or fails the build naming the value. It exists because
// the sites that quote a closed value pass a SQLer to a function call rather
// than building a string, so the refusal has to travel with the value.
func closedLit[T ~string](v T, valid bool, what string) sqlb.SQLer {
	return sqlb.Raw(func(a *sqlb.Args) string { return closedLiteral(a, v, valid, what) })
}

// TableLockModeValues is declared in locktable.go, beside the type.

func (m TableLockMode) valid() bool { return slices.Contains(TableLockModeValues, m) }

// SampleMethodValues is the two sampling methods every Postgres has. It is
// NOT the set a value is validated against: an extension brings its own, so
// TableSample checks the shape of a keyword instead. The slice is here for a
// caller iterating what this package names.
var SampleMethodValues = []SampleMethod{SampleBernoulli, SampleSystem}

// TrimSideValues is every side trim accepts.
var TrimSideValues = []TrimSide{TrimBoth, TrimLeading, TrimTrailing}

func (s TrimSide) valid() bool { return slices.Contains(TrimSideValues, s) }

// NormalFormValues is every Unicode normalization form.
var NormalFormValues = []NormalForm{NFC, NFD, NFKC, NFKD}

func (f NormalForm) valid() bool { return slices.Contains(NormalFormValues, f) }

// JSONKindValues is every qualifier IS JSON accepts, the empty one included:
// JSONAny is a legitimate value and tests only that the text parses as JSON.
var JSONKindValues = []JSONKind{JSONAny, JSONAnyValue, JSONObj, JSONArr, JSONScalar}

func (k JSONKind) valid() bool { return slices.Contains(JSONKindValues, k) }

// DateFieldValues is every field extract and date_trunc accept, the four in
// more.go included. A field added to either list belongs here too, which
// TestClosedSets_EveryConstantIsInItsValues checks by reading the source.
var DateFieldValues = []DateField{
	FieldYear, FieldQuarter, FieldMonth, FieldWeek, FieldDay,
	FieldHour, FieldMinute, FieldSecond, FieldDow, FieldDoy, FieldEpoch,
	FieldIsoDow, FieldIsoYear, FieldTimezone, FieldMillennium,
	FieldMicroseconds, FieldMilliseconds, FieldDecade, FieldCentury,
	FieldTimezoneHour, FieldTimezoneMinute, FieldJulian,
}

func (f DateField) valid() bool { return slices.Contains(DateFieldValues, f) }

// WeightValues is the four tsvector labels.
var WeightValues = []Weight{WeightA, WeightB, WeightC, WeightD}

func (w Weight) valid() bool { return slices.Contains(WeightValues, w) }

// BoundsValues is the four inclusivity combinations.
var BoundsValues = []Bounds{BoundsIncExc, BoundsIncInc, BoundsExcInc, BoundsExcExc}

func (b Bounds) valid() bool { return slices.Contains(BoundsValues, b) }

// operatorChars is the set Postgres builds an operator name from, from its
// own lexical rules. An exclusion constraint writes its operator as a bare
// token — an operator in quotes is not an operator — so the shape is the only
// check available, and it is the server's shape rather than a guess.
const operatorChars = "+-*/<>=~!@#%^&|`?"

// valid reports whether the operator is a sequence of operator characters.
// It deliberately does not check that the operator exists: an extension brings
// its own, which is why ExcludeOp is an open set at all. What it stops is the
// sequence that is not an operator in any extension, "&& ) OR true --".
func (o ExcludeOp) valid() bool {
	if o == "" {
		return false
	}
	return !strings.ContainsFunc(string(o), func(r rune) bool {
		return !strings.ContainsRune(operatorChars, r)
	})
}

// CompareOp is the operator a comparison against ANY or ALL uses. It is a
// named type rather than a bare string because the value is written into the
// statement as a token and cannot be quoted: an operator in quotes is not an
// operator. The core's untyped operator constants, sqlb.OpEq and its
// siblings, are still what a caller passes.
//
// It is an open set, as ExcludeOp is, because an extension brings its own
// operators; what it is not is a free string.
type CompareOp string

// anyWordOperators is every word operator Postgres accepts before ANY and
// ALL, measured on 17.10 rather than read: LIKE, NOT LIKE, ILIKE and NOT
// ILIKE are accepted, and SIMILAR TO and IS DISTINCT FROM are syntax errors
// there, though both are operators elsewhere. A list rather than a rule,
// because the server's own grammar is a list.
var anyWordOperators = []CompareOp{"LIKE", "NOT LIKE", "ILIKE", "NOT ILIKE"}

// valid reports whether the operator is one Postgres takes before ANY or ALL:
// a token built from operator characters, or one of the four word operators
// the grammar names there.
func (o CompareOp) valid() bool {
	return ExcludeOp(o).valid() || slices.Contains(anyWordOperators, o)
}

// valid reports whether the category is one character, which is the whole of
// what Postgres requires: it reserves the upper-case letters for itself and
// leaves the rest to a user-defined type, so a list would be a decision about
// somebody else's type. The value is written as a literal, so this bounds the
// shape rather than containing an injection.
func (c TypeCategory) valid() bool { return len([]rune(string(c))) == 1 }
