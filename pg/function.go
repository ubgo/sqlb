package pg

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// Functions other than trigger functions.
//
// Until 2026-09-24 this package built one shape of CREATE FUNCTION, the
// trigger function, and the reason recorded for the rest was that a typed
// signature would be a second type system. That reason was wrong in one
// direction and right in another, and both matter to the shape here. It is
// wrong about the signature: a parameter is a name and a ddl.SQLType[T], the
// same pair a column and a composite attribute already are, so the signature
// costs no new vocabulary and is checked by the compiler exactly as a column
// is. It is right about the body: a function body is a program in another
// language, PL/pgSQL or SQL or an internal symbol, and this package will not
// parse, check or build one.
//
// So the body is a string the library author writes, as a trigger function's
// already is, and nothing here looks inside it: it is never assembled from
// input, it is dollar-quoted with a tag it does not contain, and a body that
// does not compile is reported by the server rather than by Build. Everything
// around the body is typed — the parameters, what the function returns, the
// language, and the volatility, null, security, parallel and planner
// characteristics, each a closed set whose zero value fails the build.
//
// The one form not built is the SQL-standard body, BEGIN ATOMIC ... END, which
// is a list of statements rather than a string; the same function written with
// a body string under LANGUAGE sql is what this builds instead.

const (
	kwLanguage      = "LANGUAGE"
	kwReturns       = "RETURNS"
	kwSetOf         = "SETOF"
	kwReturnsTable  = "TABLE"
	kwLeakproof     = "LEAKPROOF"
	kwNotLeakproof  = "NOT LEAKPROOF"
	kwCost          = "COST"
	kwRows          = "ROWS"
	kwSupport       = "SUPPORT"
	kwWindow        = "WINDOW"
	kwTransformFor  = "TRANSFORM FOR TYPE"
	kwFromCurrent   = "FROM CURRENT"
	kwArgIn         = "IN"
	kwArgOut        = "OUT"
	kwArgInOut      = "INOUT"
	kwArgVariadic   = "VARIADIC"
	kwSecurityLabel = "SECURITY"
	kwParallel      = "PARALLEL"
)

// ErrNoResult is returned for a function that neither says what it returns nor
// carries an output parameter, which Postgres refuses: there is nothing to
// call the function for and nothing to type the call site with.
var ErrNoResult = errors.New("sqlb/pg: a function needs a result type or an output parameter")

// ErrTriggerSignature is returned by CreateTriggerFunction for a Function that
// declares parameters. A trigger function takes none: Postgres passes it the
// rows through PL/pgSQL's OLD and NEW, and a declared parameter would be
// silently dropped from the statement it renders.
var ErrTriggerSignature = errors.New("sqlb/pg: a trigger function declares no parameters")

// Language is the language a function's body is written in. It is an open set
// on purpose: a deployment installs a procedural language this package has
// never heard of, and the name is written as an identifier, so it is one name
// whatever it holds.
type Language string

// The languages a stock Postgres has. LangC and LangInternal name a symbol in
// a shared object or in the server itself rather than source text, and both
// need a superuser.
const (
	LangPLpgSQL  Language = "plpgsql"
	LangSQL      Language = "sql"
	LangC        Language = "c"
	LangInternal Language = "internal"
)

// Volatility is what the planner may assume about a function's result: whether
// two calls with the same arguments must agree, and for how long.
type Volatility int

// The volatility categories. Immutable is the strongest promise and the only
// one that may be used in an index expression; Volatile, the default, promises
// nothing and is what a function reading tables must declare.
const (
	Immutable Volatility = iota + 1
	Stable
	Volatile
)

func (v Volatility) keyword() (string, bool) {
	switch v {
	case Immutable:
		return "IMMUTABLE", true
	case Stable:
		return "STABLE", true
	case Volatile:
		return "VOLATILE", true
	}
	return "", false
}

// NullInput is what the server does with a call that has a NULL argument.
type NullInput int

// The two behaviours. Strict returns NULL without running the body, which is
// both faster and the only correct declaration for a body that would fail on
// NULL; CalledOnNullInput, the default, runs it.
const (
	Strict NullInput = iota + 1
	CalledOnNullInput
)

func (n NullInput) keyword() (string, bool) {
	switch n {
	case Strict:
		return "STRICT", true
	case CalledOnNullInput:
		return "CALLED ON NULL INPUT", true
	}
	return "", false
}

// Security is whose privileges a function's body runs with.
type Security int

// The two modes. Invoker, the default, runs the body as the caller, so the
// caller's own privileges and row-level security policies apply; Definer runs
// it as the function's owner, which is how a library exposes one controlled
// operation on a table the caller may not touch directly, and is why a
// definer function's search_path must be pinned with Set.
const (
	Invoker Security = iota + 1
	Definer
)

func (s Security) keyword() (string, bool) {
	switch s {
	case Invoker:
		return "INVOKER", true
	case Definer:
		return "DEFINER", true
	}
	return "", false
}

// Parallel is whether a function may run in a parallel query plan.
type Parallel int

// The three levels. ParallelUnsafe, the default, stops the whole query being
// parallelised; ParallelRestricted allows the plan but keeps the function in
// the leader; ParallelSafe allows it anywhere.
const (
	ParallelSafe Parallel = iota + 1
	ParallelRestricted
	ParallelUnsafe
)

func (p Parallel) keyword() (string, bool) {
	switch p {
	case ParallelSafe:
		return "SAFE", true
	case ParallelRestricted:
		return "RESTRICTED", true
	case ParallelUnsafe:
		return "UNSAFE", true
	}
	return "", false
}

// FunctionArg is one parameter of a function: a mode, a name and a typed SQL
// type. The type's Go parameter is checked when the argument is made and
// erased after, since a function's parameters are of different types, which is
// what Attribute does for a composite type's fields.
type FunctionArg struct {
	mode  string
	name  string
	spell func(d sqlb.Dialect) (string, error)
}

// Arg is an input parameter of type t. The name is written out, so the body
// may use it, and it is part of the signature every other statement naming the
// function renders.
func Arg[T any](name string, t ddl.SQLType[T]) FunctionArg {
	return FunctionArg{mode: kwArgIn, name: name, spell: t.Spelling}
}

// OutArg is an output parameter, one of the values the function returns. A
// function with output parameters and no result type returns a record of them,
// which is why it needs no Returns.
func OutArg[T any](name string, t ddl.SQLType[T]) FunctionArg {
	return FunctionArg{mode: kwArgOut, name: name, spell: t.Spelling}
}

// InOutArg is a parameter that is both read and returned.
func InOutArg[T any](name string, t ddl.SQLType[T]) FunctionArg {
	return FunctionArg{mode: kwArgInOut, name: name, spell: t.Spelling}
}

// VariadicArg is the last parameter, an array type whose elements the caller
// passes one by one.
func VariadicArg[T any](name string, t ddl.SQLType[T]) FunctionArg {
	return FunctionArg{mode: kwArgVariadic, name: name, spell: t.Spelling}
}

// write renders the parameter, mode first, as both CREATE FUNCTION and every
// statement that identifies the function by its signature write it.
func (p FunctionArg) write(d sqlb.Dialect) (string, error) {
	if p.mode == "" || p.name == "" || p.spell == nil {
		return "", ddl.ErrZeroValue
	}
	sp, err := p.spell(d)
	if err != nil {
		return "", err
	}
	return p.mode + " " + d.QuoteIdent(p.name) + " " + sp, nil
}

// returnsItself reports whether the parameter is one of the function's
// results, which is what lets a function declare no result type.
func (p FunctionArg) returnsItself() bool { return p.mode == kwArgOut || p.mode == kwArgInOut }

// FunctionResult is what a function returns: one value of a type, a set of
// them, or a table of named columns. The Go type is checked by the constructor
// and erased after, as a FunctionArg's is.
type FunctionResult struct {
	spell func(d sqlb.Dialect) (string, error)
}

// Returns one value of type t.
func Returns[T any](t ddl.SQLType[T]) FunctionResult {
	return FunctionResult{spell: t.Spelling}
}

// ReturnsSetOf returns a set of values of type t, which a query reads as rows.
func ReturnsSetOf[T any](t ddl.SQLType[T]) FunctionResult {
	return FunctionResult{spell: func(d sqlb.Dialect) (string, error) {
		sp, err := t.Spelling(d)
		if err != nil {
			return "", err
		}
		return kwSetOf + " " + sp, nil
	}}
}

// ReturnsTable returns a set of rows of the named columns, the form a query
// reads as a table without a column definition list at the call site.
func ReturnsTable(first Attribute, more ...Attribute) FunctionResult {
	cols := append([]Attribute{first}, more...)
	return FunctionResult{spell: func(d sqlb.Dialect) (string, error) {
		parts := make([]string, len(cols))
		for i, at := range cols {
			if at.name == "" || at.spell == nil {
				return "", ddl.ErrZeroValue
			}
			sp, err := at.spell(d)
			if err != nil {
				return "", err
			}
			parts[i] = d.QuoteIdent(at.name) + " " + sp
		}
		return kwReturnsTable + " (" + strings.Join(parts, ", ") + ")", nil
	}}
}

// TypeRef is a declared SQL type named in a position where its Go type says
// nothing about the statement, a base type's element or a transform's type.
// The Go parameter is checked by NamedType and erased after.
type TypeRef struct {
	spell func(d sqlb.Dialect) (string, error)
}

// NamedType refers to t where only its name is written.
func NamedType[T any](t ddl.SQLType[T]) TypeRef { return TypeRef{spell: t.Spelling} }

// spelling renders the type, failing the build on a zero value.
func (r TypeRef) spelling(a *sqlb.Args) string {
	if r.spell == nil {
		a.Fail(ddl.ErrZeroValue)
		return ""
	}
	sp, err := r.spell(a.Dialect())
	if err != nil {
		a.Fail(err)
		return ""
	}
	return sp
}

// CreateRoutineStatement is CREATE FUNCTION for a function that is not a
// trigger function: a signature, what it returns, a language and a body.
//
// It is a separate type from CreateFunctionStatement, which is the trigger
// form, because the two have almost nothing in common: a trigger function's
// result, language and signature are all fixed, and every option here would be
// a method that could not be written on it.
type CreateRoutineStatement struct {
	f          Function
	lang       Language
	body       string
	objFile    string
	linkSymbol string
	orReplace  bool
	result     *FunctionResult
	volatility Volatility
	nullInput  NullInput
	security   Security
	parallel   Parallel
	leakproof  *bool
	cost       *float64
	rows       *float64
	window     bool
	support    *Function
	transforms []TypeRef
	settings   []func(a *sqlb.Args) string
}

// CreateFunction creates a function whose body is written in lang. The body is
// not checked, parsed or built here: it is another language, the library author
// writes it, and the server is what reports a body that does not compile.
//
// Postgres has no CREATE FUNCTION IF NOT EXISTS, so OrReplace is the form that
// is safe to run on every boot; unlike a guarded create it also updates the
// body, which is what a function's migration usually is.
func CreateFunction(f Function, lang Language, body string) *CreateRoutineStatement {
	return &CreateRoutineStatement{f: f, lang: lang, body: body}
}

// CreateCFunction creates a function implemented by linkSymbol in the shared
// object objFile, the two-string form of CREATE FUNCTION. It is how an
// extension's own C functions are declared, and it needs a superuser.
func CreateCFunction(f Function, objFile, linkSymbol string) *CreateRoutineStatement {
	return &CreateRoutineStatement{f: f, lang: LangC, objFile: objFile, linkSymbol: linkSymbol}
}

// derive returns a copy whose slices are its own, so a base statement several
// functions are derived from is never written to.
func (x *CreateRoutineStatement) derive() *CreateRoutineStatement {
	c := *x
	c.transforms = slices.Clone(x.transforms)
	c.settings = slices.Clone(x.settings)
	return &c
}

// OrReplace replaces an existing function of the same name and argument types,
// keeping its privileges; it cannot change the name, the argument types or the
// result type, which Postgres refuses.
func (x *CreateRoutineStatement) OrReplace() *CreateRoutineStatement {
	c := x.derive()
	c.orReplace = true
	return c
}

// Returns says what the function returns. A function with an output parameter
// needs no result type and may not have both a table result and one.
func (x *CreateRoutineStatement) Returns(r FunctionResult) *CreateRoutineStatement {
	c := x.derive()
	c.result = &r
	return c
}

// Volatility declares what the planner may assume about the function, which is
// what decides whether it can be used in an index expression or folded away.
// Without it Postgres assumes the weakest, Volatile.
func (x *CreateRoutineStatement) Volatility(v Volatility) *CreateRoutineStatement {
	c := x.derive()
	c.volatility = v
	return c
}

// NullInput declares what a NULL argument does.
func (x *CreateRoutineStatement) NullInput(n NullInput) *CreateRoutineStatement {
	c := x.derive()
	c.nullInput = n
	return c
}

// Security declares whose privileges the body runs with.
func (x *CreateRoutineStatement) Security(s Security) *CreateRoutineStatement {
	c := x.derive()
	c.security = s
	return c
}

// Parallel declares whether the function may run in a parallel plan.
func (x *CreateRoutineStatement) Parallel(p Parallel) *CreateRoutineStatement {
	c := x.derive()
	c.parallel = p
	return c
}

// Leakproof promises the function reveals nothing about its arguments beyond
// its result, not through an error message and not through timing, which is
// what lets the planner push it below a security barrier or an RLS policy. A
// wrong promise here is an information leak, so it needs a superuser.
func (x *CreateRoutineStatement) Leakproof(on bool) *CreateRoutineStatement {
	c := x.derive()
	c.leakproof = &on
	return c
}

// Cost is the planner's estimated execution cost in cpu_operator_cost units,
// which decides whether the function is called before or after a cheaper
// filter.
func (x *CreateRoutineStatement) Cost(n float64) *CreateRoutineStatement {
	c := x.derive()
	c.cost = &n
	return c
}

// Rows is the planner's estimate of how many rows a set-returning function
// returns. It is meaningless on a function that returns one value, which
// Postgres refuses.
func (x *CreateRoutineStatement) Rows(n float64) *CreateRoutineStatement {
	c := x.derive()
	c.rows = &n
	return c
}

// Window declares the function a window function, which only a C function can
// be: the implementation reads its frame through the server's window API and
// there is no procedural-language equivalent.
func (x *CreateRoutineStatement) Window() *CreateRoutineStatement {
	c := x.derive()
	c.window = true
	return c
}

// Support names a planner support function, a C function the planner calls to
// estimate this one's selectivity or to rewrite a call into something cheaper.
func (x *CreateRoutineStatement) Support(f Function) *CreateRoutineStatement {
	c := x.derive()
	c.support = &f
	return c
}

// Transform names a type whose transform this function uses, which is how a
// procedural language sees a Postgres type as one of its own rather than as
// text.
func (x *CreateRoutineStatement) Transform(first TypeRef, more ...TypeRef) *CreateRoutineStatement {
	c := x.derive()
	c.transforms = append(c.transforms, append([]TypeRef{first}, more...)...)
	return c
}

// Set pins a configuration parameter for the duration of a call, the setting a
// Definer function must carry: without a pinned search_path a caller can put a
// table of their own ahead of the library's and have the body find it.
func (x *CreateRoutineStatement) Set(name, value string) *CreateRoutineStatement {
	c := x.derive()
	c.settings = append(c.settings, func(a *sqlb.Args) string {
		return kwSet + " " + a.Dialect().QuoteIdent(name) + " " + kwTo + " " + a.Literal(value)
	})
	return c
}

// SetFromCurrent pins a configuration parameter to the value it has where the
// statement runs, which is the form a migration uses when the right value is
// the deployment's rather than the library's.
func (x *CreateRoutineStatement) SetFromCurrent(name string) *CreateRoutineStatement {
	c := x.derive()
	c.settings = append(c.settings, func(a *sqlb.Args) string {
		return kwSet + " " + a.Dialect().QuoteIdent(name) + " " + kwFromCurrent
	})
	return c
}

// Build renders the statement.
func (x *CreateRoutineStatement) Build() (ddl.Script, error) {
	if x.lang == "" {
		return nil, ddl.ErrZeroValue
	}
	if x.objFile == "" && strings.TrimSpace(x.body) == "" {
		return nil, ErrEmptyBody
	}
	if x.objFile != "" && x.linkSymbol == "" {
		return nil, ErrEmptyName
	}
	if x.result == nil && !slices.ContainsFunc(x.f.Params, FunctionArg.returnsItself) {
		return nil, ErrNoResult
	}
	sig, err := x.f.signature(Dialect{})
	if err != nil {
		return nil, err
	}
	return buildOne(x.f.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		kw := kwCreateFunction
		if x.orReplace {
			kw = kwCreateOrReplaceFunction
		}
		s := kw + " " + qualified(d, x.f.Schema, x.f.Name) + sig
		if x.result != nil {
			if x.result.spell == nil {
				a.Fail(ddl.ErrZeroValue)
				return ""
			}
			sp, err := x.result.spell(d)
			if err != nil {
				a.Fail(err)
				return ""
			}
			s += " " + kwReturns + " " + sp
		}
		s += " " + kwLanguage + " " + d.QuoteIdent(string(x.lang))
		for _, r := range x.transforms {
			s += " " + kwTransformFor + " " + r.spelling(a)
		}
		if x.window {
			s += " " + kwWindow
		}
		if x.volatility != 0 {
			kw, ok := x.volatility.keyword()
			s += " " + keywordOf(a, kw, ok, "Volatility")
		}
		if x.leakproof != nil {
			if *x.leakproof {
				s += " " + kwLeakproof
			} else {
				s += " " + kwNotLeakproof
			}
		}
		if x.nullInput != 0 {
			kw, ok := x.nullInput.keyword()
			s += " " + keywordOf(a, kw, ok, "NullInput")
		}
		if x.security != 0 {
			kw, ok := x.security.keyword()
			s += " " + kwSecurityLabel + " " + keywordOf(a, kw, ok, "Security")
		}
		if x.parallel != 0 {
			kw, ok := x.parallel.keyword()
			s += " " + kwParallel + " " + keywordOf(a, kw, ok, "Parallel")
		}
		if x.cost != nil {
			s += " " + kwCost + " " + formatFloat(*x.cost)
		}
		if x.rows != nil {
			s += " " + kwRows + " " + formatFloat(*x.rows)
		}
		if x.support != nil {
			if x.support.Name == "" {
				a.Fail(ErrEmptyName)
				return ""
			}
			s += " " + kwSupport + " " + qualified(d, x.support.Schema, x.support.Name)
		}
		for _, set := range x.settings {
			s += " " + set(a)
		}
		if x.objFile != "" {
			return s + " " + kwAs + " " + a.Literal(x.objFile) + ", " + a.Literal(x.linkSymbol)
		}
		tag := dollarTag(x.body)
		return s + " " + kwAs + " " + tag + x.body + tag
	})
}

// formatFloat writes a planner estimate, which is a number in the statement
// rather than a value: no engine takes a placeholder in CREATE FUNCTION, and
// the shortest round-tripping form is what the server parses back.
func formatFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
