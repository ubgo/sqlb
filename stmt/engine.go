package stmt

import (
	"strings"

	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/render"
)

// Building blocks for an engine package.
//
// An engine package holds the SQL one engine has and the others do not, and
// every construct in it must fail at Build under another dialect rather than
// reach that engine's server. The guard that does this is six lines, and it
// was copied into pg, sqlite and ch, which is three places for one rule to
// drift.
//
// These functions are that rule, once. They are exported for a second reason
// that matters more: a community package in another repository can build a
// dialect and its constructs with the same guarantees as the packages in this
// one. Nothing here is privileged, and an engine name is a plain string
// rather than a member of a closed set, so no list in this module needs
// editing for a new engine to exist.
//
// The shape of an engine package, in full:
//
//	package duck
//
//	const engine = "duckdb"
//
//	type Dialect struct{}
//
//	func (Dialect) Placeholder(int) string         { return "?" }
//	func (Dialect) QuoteIdent(id string) string    { return sqlb.Quote(id, '"') }
//	func (Dialect) Name() string                   { return engine }
//
//	func expr[T any](c string, r func(*sqlb.Args) string) sqlb.Expr[T] {
//		return sqlb.EngineExpr[T](engine, c, r)
//	}
//
//	// ListValue renders list_value(e), which only this engine has.
//	func ListValue(e sqlb.Expr[string]) sqlb.Expr[string] {
//		return expr[string]("duck.ListValue", func(a *sqlb.Args) string {
//			return "list_value(" + e.SQL(a) + ")"
//		})
//	}

// EngineGuard reports whether a is rendering for engine, and records the
// construct as unsupported when it is not.
//
// It is the one place the guard rule lives. A constructor calls it first and
// returns an empty fragment on false; Build then fails with
// ErrUnsupportedDialect naming the construct and the dialect it met.
func EngineGuard(a *Args, engine, construct string) bool {
	if DialectName(a.Dialect()) == engine {
		return true
	}
	a.Unsupported(construct)
	return false
}

// EngineExpr makes a guarded expression typed T from a render function. It is
// the general case; the constructors below are the shapes that recur.
func EngineExpr[T any](engine, construct string, render func(a *Args) string) Expr[T] {
	return New[T](func(a *Args) string {
		if !EngineGuard(a, engine, construct) {
			return ""
		}
		return render(a)
	})
}

// EnginePred makes a guarded predicate from a render function.
func EnginePred(engine, construct string, render func(a *Args) string) Predicate {
	return PredicateOf(func(a *Args) string {
		if !EngineGuard(a, engine, construct) {
			return ""
		}
		return render(a)
	})
}

// EngineRaw makes a guarded fragment that is neither an expression nor a
// predicate, such as an argument decorator or a CTE suffix.
func EngineRaw(engine, construct string, render func(a *Args) string) SQLer {
	return Raw(func(a *Args) string {
		if !EngineGuard(a, engine, construct) {
			return ""
		}
		return render(a)
	})
}

// EngineBinOp renders "(left op right)" as a guarded expression typed R.
//
// The parentheses matter. Without them an engine operator nested inside a core
// one takes its meaning from the engine's precedence table rather than from the
// author's nesting: pg.BitAnd inside expr.Mul rendered "tag & $1 * $2", which
// Postgres reads as tag & (16 * 2) because * binds tighter than &. That is
// valid SQL returning the wrong rows, which is the worst shape a bug can take.
// Pinned by TestEngineBinOp_NestsCorrectly.
// op is checked for the shape of an operator rather than against a list, for
// the reason expr.Binary's is: the operators an engine has come from its
// extensions, and a list here would refuse a valid one. An extension author
// writing a helper is still the one choosing the operator; what the shape
// stops is a payload that closes the expression and appends a statement.
func EngineBinOp[R any](engine, construct, op string, l, r SQLer) Expr[R] {
	return EngineExpr[R](engine, construct, func(a *Args) string {
		return "(" + l.SQL(a) + " " + expr.CheckedOperator(a, op, "operator") + " " + r.SQL(a) + ")"
	})
}

// EngineBinPred renders "(left op right)" as a guarded predicate.
//
// Parenthesised for the same reason as EngineBinOp: a caller nests these
// inside And, Or and Not, and the author's nesting must decide the meaning
// rather than the engine's precedence table. The parentheses are never wrong
// where the bare form is valid, so this is the safe default rather than a
// judgement about which operators need them.
func EngineBinPred(engine, construct, op string, l, r SQLer) Predicate {
	return EnginePred(engine, construct, func(a *Args) string {
		return "(" + l.SQL(a) + " " + expr.CheckedOperator(a, op, "operator") + " " + r.SQL(a) + ")"
	})
}

// EngineFn renders a guarded function call typed R.
func EngineFn[R any](engine, construct, name string, args ...SQLer) Expr[R] {
	return EngineExpr[R](engine, construct, func(a *Args) string {
		out := make([]string, len(args))
		for i, arg := range args {
			out[i] = arg.SQL(a)
		}
		return expr.CheckedFunctionName(a, name, "function") + "(" + strings.Join(out, sepComma) + ")"
	})
}

// EngineLit makes a guarded expression from a constant fragment.
//
// Use it rather than Literal for anything only one engine accepts. Literal
// has no dialect to consult, so a constant such as '{}'::jsonb built on it
// renders under every dialect and is rejected by the database instead of by
// Build.
func EngineLit[T any](engine, construct, sql string) Expr[T] {
	return EngineExpr[T](engine, construct, func(*Args) string { return sql })
}

// EngineClause makes a guarded clause for a statement slot.
//
// A statement that does not render the given slot fails at Build with
// ErrUnknownSlot, so a clause aimed at a position a statement does not have
// is reported rather than dropped.
//
// D is the dialect the clause belongs to and K the statement kind it may
// appear in; both are type arguments written once, in the engine package,
// and never in user code. The engine name the runtime guard compares against
// is derived from D, so there is no string to misspell and no way to register
// a clause under one engine and use it from another. The guard stays as
// belt-and-braces: a statement typed to D can only hold a Clause[D, K], so it
// never fires for a clause that reached its statement through the types.
//
// D must be usable as a zero value — a plain struct, as every dialect here is.
// A dialect that is an interface type yields no name and the guard refuses at
// Build, which is right: an engine clause on a statement whose engine is not
// statically known has already been refused by the compiler.
func EngineClause[D render.Dialect, K any](construct string, slot Slot, render func(a *Args) string) Clause[D, K] {
	var zero D
	engine := ""
	if any(zero) != nil {
		engine = DialectName(zero)
	}
	return engineClause[D, K]{engine: engine, construct: construct, slot: slot, render: render}
}

type engineClause[D render.Dialect, K any] struct {
	engine    string
	construct string
	slot      Slot
	render    func(a *Args) string
}

func (c engineClause[D, K]) Slot() Slot { return c.slot }

// ClauseOf is the marker that types the clause; see Clause.
func (engineClause[D, K]) ClauseOf(D, K) {}

func (c engineClause[D, K]) WriteSQL(b *strings.Builder, args *Args) {
	if !EngineGuard(args, c.engine, c.construct) {
		return
	}
	b.WriteString(c.render(args))
}

// EngineSource makes a guarded FROM source, for a set-returning function or
// any other table expression only one engine has.
func EngineSource(engine, construct, alias string, render func(a *Args) string) Source {
	return SourceOf(alias, func(a *Args) string {
		if !EngineGuard(a, engine, construct) {
			return ""
		}
		return render(a)
	})
}

// EngineList renders SQLers comma-separated, for a constructor assembling its
// own argument list.
func EngineList(a *Args, items []SQLer) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.SQL(a)
	}
	return strings.Join(out, sepComma)
}

// EngineQuoteLit renders s as a string literal, written by the dialect's own
// quoter.
//
// For author-chosen constants only, such as a config name or a jsonpath the
// grammar will not take as a parameter. Request data goes through Param; this
// puts its argument straight into the statement text.
//
// It asks the dialect rather than doubling quotes itself, because that was not
// enough: on Postgres with standard_conforming_strings off, a backslash in a
// plain literal is an escape, and a jsonpath's like_regex "^\\d$" matched
// with the setting on and silently did not with it off. A dialect that does
// not implement LiteralQuoting cannot be given a literal, and Build fails with
// ErrNoLiteralQuoting naming it rather than writing one by a guessed rule.
func EngineQuoteLit(s string) SQLer {
	return Raw(func(a *Args) string { return a.Literal(s) })
}
