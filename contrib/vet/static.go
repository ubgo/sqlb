package vet

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"

	"github.com/ubgo/sqlb/render"
)

// The static checks beyond the dialect residue. Each names a mistake that
// compiles, renders, and is wrong: SQL text taken from a variable, a
// statement built without its constructor, a keyword the engine will reject,
// a row comparison whose sides do not line up. None of them needs a database.

// Literal reports expr.Literal receiving anything but a constant. A literal
// is SQL text the author wrote and is rendered verbatim, so a variable there
// is interpolation; a value belongs in Param, which binds it.
var Literal = &analysis.Analyzer{
	Name:     "sqlbliteral",
	Doc:      "reports expr.Literal called with a non-constant argument, which interpolates a value into SQL text",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runLiteral,
}

// StructLit reports a statement or Query built as a struct literal outside
// the stmt package. A literal skips the constructor, so it has no exec
// guard and no typestate: a literal Update renders without a WHERE and a
// literal Query lets two goroutines interleave rows. The core pins that the
// literal is possible; this is where a caller is told not to.
var StructLit = &analysis.Analyzer{
	Name:     "sqlbstructlit",
	Doc:      "reports a stmt statement or Query built as a struct literal, which has no exec guard and no typestate",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runStructLit,
}

// Keyword reports a JoinType or SetOp keyword the statement's engine does not
// accept, when the engine is statically known, and NATURAL in any join under
// any engine. JoinType and SetOp take a string on purpose, as the escape
// hatch for a keyword the constants omit; this is the check that the hatch
// was used for a keyword the engine has.
var Keyword = &analysis.Analyzer{
	Name:     "sqlbkeyword",
	Doc:      "reports a JoinType or SetOp keyword the statement's engine rejects, and NATURAL joins everywhere",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runKeyword,
}

// Rows reports a row comparison whose literal rows disagree: RowIn, RowEq
// and the rest compare Row(...) values positionally, and a row with the wrong
// arity or an element of another type is an SQL error the compiler cannot
// see because Row returns an untyped SQLer.
var Rows = &analysis.Analyzer{
	Name:     "sqlbrow",
	Doc:      "reports RowIn, RowEq and RowLess rows whose arity or element types differ",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runRows,
}

// Escapes lists every use of an escape hatch: Raw, Literal, New,
// PredicateOf, a JoinType or SetOp keyword, and a Col whose name is not a
// constant. It reports nothing wrong; it is the review list for the places
// where the compiler has been asked to step aside, which is why it is not in
// Analyzers and runs only when asked for.
var Escapes = &analysis.Analyzer{
	Name:     "sqlbescape",
	Doc:      "lists every escape hatch use for review: Raw, Literal, New, PredicateOf, JoinType, SetOp, non-constant Col",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runEscapes,
}

// illegalJoins is, per engine, the join keywords that engine rejects. The
// three listed are other engines' spellings: STRAIGHT_JOIN is MySQL's, CROSS
// APPLY and OUTER APPLY are T-SQL's, and Postgres and SQLite spell the latter
// LATERAL. RIGHT and FULL OUTER JOIN are legal on SQLite from 3.39, which is
// older than the driver contrib/driver-sqlite bundles.
var illegalJoins = map[string][]string{
	render.DialectPostgres:   {"STRAIGHT_JOIN", "CROSS APPLY", "OUTER APPLY"},
	render.DialectSQLite:     {"STRAIGHT_JOIN", "CROSS APPLY", "OUTER APPLY"},
	render.DialectClickHouse: {"STRAIGHT_JOIN", "CROSS APPLY", "OUTER APPLY"},
}

// illegalSetOps is, per engine, the set operations that engine rejects.
// SQLite has no ALL form of INTERSECT or EXCEPT. Postgres accepts all six.
// ClickHouse is absent because its support has changed across versions and
// an absent engine means no opinion, not legal.
var illegalSetOps = map[string][]string{
	render.DialectPostgres: {},
	render.DialectSQLite:   {"INTERSECT ALL", "EXCEPT ALL"},
}

func runLiteral(pass *analysis.Pass) (any, error) {
	// The definition and the facade's forwarder pass their own parameter
	// through; neither is a use.
	if pass.Pkg.Path() == Module || pass.Pkg.Path() == Module+"/expr" {
		return nil, nil
	}
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if !isCoreCall(pass, call, "Literal") || len(call.Args) != 1 {
			return
		}
		if constantString(pass, call.Args[0]) == nil {
			pass.Reportf(call.Args[0].Pos(), "Literal receives a non-constant: a literal is SQL text the author wrote and is rendered verbatim; bind a value with Param")
		}
	})
	return nil, nil
}

func runStructLit(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == Module+"/stmt" {
		return nil, nil
	}
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.CompositeLit)(nil)}, func(n ast.Node) {
		lit := n.(*ast.CompositeLit)
		named, ok := types.Unalias(pass.TypesInfo.TypeOf(lit)).(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != Module+"/stmt" {
			return
		}
		name := named.Obj().Name()
		if name == "Query" && len(lit.Elts) == 0 {
			return // the zero value beside an error: nothing to execute
		}
		if statementTypes[name] || name == "Query" {
			pass.Reportf(lit.Pos(), "%s built as a struct literal has no exec guard and no typestate; use its constructor", name)
		}
	})
	return nil, nil
}

func runKeyword(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		// A JoinType conversion anywhere: NATURAL is refused under every engine.
		if isJoinTypeConversion(pass, call) {
			if kw := constantString(pass, call.Args[0]); kw != nil && strings.Contains(strings.ToUpper(*kw), "NATURAL") {
				pass.Reportf(call.Pos(), "NATURAL join: a natural join changes which columns it matches when a column is added; name the columns with Using")
			}
			return
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		engine := receiverEngine(pass, sel.X)
		if engine == "" {
			return
		}
		switch sel.Sel.Name {
		case "SetOp":
			if len(call.Args) != 2 {
				return
			}
			kw := constantString(pass, call.Args[0])
			if kw == nil {
				return
			}
			illegal, known := illegalSetOps[engine]
			if known && contains(illegal, strings.ToUpper(*kw)) {
				pass.Reportf(call.Args[0].Pos(), "%s has no %s", engine, *kw)
			}
		case "Join":
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, el := range lit.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Type" {
						continue
					}
					kw := constantString(pass, kv.Value)
					if kw == nil {
						continue
					}
					if contains(illegalJoins[engine], strings.ToUpper(*kw)) {
						pass.Reportf(kv.Value.Pos(), "%s has no %s", engine, *kw)
					}
				}
			}
		}
	})
	return nil, nil
}

// rowFamily is the predicates that compare Row values positionally.
var rowFamily = map[string]bool{"RowIn": true, "RowNotIn": true, "RowEq": true, "RowLess": true, "RowGreater": true}

func runRows(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !rowFamily[sel.Sel.Name] || !isCorePackage(selectorPackage(pass, sel)) {
			return
		}
		var first []string
		for i, arg := range call.Args {
			elems, ok := rowElements(pass, arg)
			if !ok {
				continue
			}
			if first == nil {
				first = elems
				continue
			}
			if len(elems) != len(first) {
				pass.Reportf(arg.Pos(), "%s: row %d has %d elements, the first row has %d", sel.Sel.Name, i+1, len(elems), len(first))
				continue
			}
			for j := range elems {
				if elems[j] != "" && first[j] != "" && elems[j] != first[j] {
					pass.Reportf(arg.Pos(), "%s: row %d element %d is %s, the first row's is %s", sel.Sel.Name, i+1, j+1, elems[j], first[j])
				}
			}
		}
	})
	return nil, nil
}

func runEscapes(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if isJoinTypeConversion(pass, call) {
			pass.Reportf(call.Pos(), "escape hatch: JoinType keyword")
			return
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetOp" && receiverStatement(pass, sel.X) {
			pass.Reportf(call.Pos(), "escape hatch: SetOp keyword")
			return
		}
		for _, name := range []string{"Raw", "Literal", "New", "PredicateOf"} {
			if isCoreCall(pass, call, name) {
				pass.Reportf(call.Pos(), "escape hatch: %s renders text the compiler does not check", name)
				return
			}
		}
		if isCoreCall(pass, call, "Col") && len(call.Args) == 2 && constantString(pass, call.Args[1]) == nil {
			pass.Reportf(call.Pos(), "escape hatch: Col with a non-constant column name")
		}
	})
	return nil, nil
}

// isCorePackage says whether an import path is the root facade or one of the
// layer packages the facade forwards to.
func isCorePackage(path string) bool {
	switch path {
	case Module, Module + "/expr", Module + "/render", Module + "/stmt":
		return true
	}
	return false
}

// isCoreCall says whether call is pkg.Name(...) or pkg.Name[T](...) for a
// core package, resolved through type information.
func isCoreCall(pass *analysis.Pass, call *ast.CallExpr, name string) bool {
	fun := call.Fun
	if ix, ok := fun.(*ast.IndexExpr); ok {
		fun = ix.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	return isCorePackage(selectorPackage(pass, sel))
}

// isJoinTypeConversion says whether call is JoinType("...") from stmt or the
// facade: a call whose function is the type itself.
func isJoinTypeConversion(pass *analysis.Pass, call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	tv, ok := pass.TypesInfo.Types[call.Fun]
	if !ok || !tv.IsType() {
		return false
	}
	named, ok := types.Unalias(tv.Type).(*types.Named)
	return ok && named.Obj().Name() == "JoinType" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == Module+"/stmt"
}

// constantString is the constant string value of e, or nil when e is not a
// constant. A conversion of a constant, JoinType("X"), is still a constant.
func constantString(pass *analysis.Pass, e ast.Expr) *string {
	tv, ok := pass.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return nil
	}
	s := constant.StringVal(tv.Value)
	return &s
}

// receiverEngine names the engine of a *Select[D] receiver whose D is a
// dialect type, or "" when the receiver is not a statement or its dialect is
// a runtime value.
func receiverEngine(pass *analysis.Pass, recv ast.Expr) string {
	t := pass.TypesInfo.TypeOf(recv)
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return ""
	}
	named, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok || statementType(t) == "" || named.TypeArgs().Len() != 1 {
		return ""
	}
	return dialectEngine(named.TypeArgs().At(0))
}

// receiverStatement says whether recv is a pointer to a statement type.
func receiverStatement(pass *analysis.Pass, recv ast.Expr) bool {
	return statementType(pass.TypesInfo.TypeOf(recv)) != ""
}

// rowElements is the element types of a literal Row(...) call, "" for an
// element whose type is not Expr[T]; ok is false when arg is not such a call.
func rowElements(pass *analysis.Pass, arg ast.Expr) ([]string, bool) {
	call, ok := arg.(*ast.CallExpr)
	if !ok || !isCoreCall(pass, call, "Row") {
		return nil, false
	}
	out := make([]string, len(call.Args))
	for i, a := range call.Args {
		named, ok := types.Unalias(pass.TypesInfo.TypeOf(a)).(*types.Named)
		if !ok || named.Obj().Name() != "Expr" || named.TypeArgs().Len() != 1 {
			continue
		}
		out[i] = types.TypeString(named.TypeArgs().At(0), func(p *types.Package) string { return p.Name() })
	}
	return out, true
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}
