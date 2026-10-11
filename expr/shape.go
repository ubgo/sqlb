// The shapes of the three things a caller can put into statement text that
// cannot be quoted: an operator, a function name and a keyword.
//
// Everything else a caller supplies is bound as a parameter or written by the
// dialect — a value through Args.Next or Args.Literal, an identifier through
// QuoteIdent. These three are the residue, and they are residue for a reason
// rather than by omission:
//
//   - An operator in quotes is not an operator. "a" "=" "b" is a syntax error.
//   - A function name may be schema-qualified, so quoting it as one identifier
//     turns pg_catalog.date_trunc into a function of that literal name.
//   - A keyword in quotes is an identifier, which is the opposite of a keyword.
//
// So the defence is a shape rather than quoting or a list. A list is the wrong
// answer for all three: the operators an engine has come from its extensions,
// a function name is whatever the schema defines, and CLAUDE.md records that
// JoinType and SetOp are open on purpose so that FULL OUTER JOIN and INTERSECT
// ALL are reachable without this module naming them.
//
// The shapes below were measured before they were written, not guessed: every
// operator constant in expr, stmt and the four engine packages, 96 of them,
// and every function-name constant, 174, was extracted from the source and the
// rules admit all of them. What they refuse is the payload that closes the
// construct and appends a statement, which is the only thing a shape can
// usefully refuse here.
//
// The reason this is not paranoia about a caller who would have to try:
// examples/pgsurface/periodcompare/filters.go maps a request's comparison
// enum to a SQL operator and hands it to Compare. That code is correct — it is
// a lookup and its comment says so — but the correctness is the caller's
// discipline, and the operator does reach statement text from a request in
// real code. The shape check is what makes the discipline not the only thing
// standing there.

package expr

import (
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/render"
)

// operatorChars is the set Postgres builds an operator name from, and the
// superset of what MySQL, SQLite and ClickHouse use. Every symbolic operator
// constant in this module is made of these: @>, ->>, #-, <<->, !~*, -|-.
const operatorChars = "+-*/<>=~!@#%^&|`?"

// validOperator reports whether op has the shape of a SQL operator: a token
// built from operator characters, or a keyword phrase such as IS NOT DISTINCT
// FROM.
//
// Both forms are needed and neither alone is enough. The engine packages use
// symbolic operators for almost everything, and the core's own vocabulary
// includes IN, NOT IN, IS NULL, LIKE and IS DISTINCT FROM, which are operators
// in the grammar's sense and words in every other sense. A character-set rule
// alone would refuse half the core's constants — which is the mistake this
// nearly made, and the same one that was caught before applying a
// characters-only rule to pg.BinOp.
func validOperator(op string) bool {
	if op == "" {
		return false
	}
	symbolic := !strings.ContainsFunc(op, func(r rune) bool {
		return !strings.ContainsRune(operatorChars, r)
	})
	return symbolic || validKeyword(op)
}

// validKeyword reports whether s has the shape of a SQL keyword or keyword
// phrase: letters separated by single spaces, and nothing else.
//
// It is what makes JoinType and SetOp checkable without closing them. The
// decision CLAUDE.md records is that the SET of keywords is open — FULL OUTER
// JOIN and INTERSECT ALL are reachable precisely because this module does not
// name them — not that the TEXT may be anything. A shape check enforces the
// word "keyword" that the decision already rests on, and leaves every keyword
// a caller might write reachable, in either case.
func validKeyword(s string) bool {
	if s == "" {
		return false
	}
	for _, word := range strings.Split(s, " ") {
		if word == "" {
			return false
		}
		for _, r := range word {
			if r < 'A' || (r > 'Z' && r < 'a') || r > 'z' {
				return false
			}
		}
	}
	return true
}

// validFunctionName reports whether name has the shape of a function name: an
// identifier, or a schema-qualified pair of them.
//
// A function name cannot be written through QuoteIdent, because quoting
// pg_catalog.date_trunc as one identifier names a function that does not
// exist, and quoting each half would be a second guess about where the dot
// belongs. The shape is the answer: every one of this module's 174 function
// names is an identifier or a qualified pair, and a name holding a space, a
// parenthesis or a quote is not a function name in any engine here.
func validFunctionName(name string) bool {
	if name == "" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !validIdentifier(p) {
			return false
		}
	}
	return true
}

// validIdentifier reports whether s is an unquoted SQL identifier: a letter or
// underscore, then letters, digits, underscores or dollars. It is deliberately
// the unquoted shape only; a function whose name needs quoting is reachable
// through Raw, which is what Raw is for.
func validIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case i > 0 && (r == '$' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}
	return true
}

// CheckedOperator writes op, or fails the render naming it. The failure is
// ErrUnknownValue, the same error every closed set in this module reports,
// because a caller reading it wants the same thing in both cases: the value
// that was refused and which argument it came from.
func CheckedOperator(a *render.Args, op, what string) string {
	if !validOperator(op) {
		a.Fail(fmt.Errorf("%w: %s %q is not an operator", ErrUnknownValue, what, op))
		return ""
	}
	return op
}

// CheckedKeyword writes s, or fails the render naming it.
func CheckedKeyword(a *render.Args, s, what string) string {
	if !validKeyword(s) {
		a.Fail(fmt.Errorf("%w: %s %q is not a keyword", ErrUnknownValue, what, s))
		return ""
	}
	return s
}

// CheckedFunctionName writes name, or fails the render naming it.
func CheckedFunctionName(a *render.Args, name, what string) string {
	if !validFunctionName(name) {
		a.Fail(fmt.Errorf("%w: %s %q is not a function name", ErrUnknownValue, what, name))
		return ""
	}
	return name
}
