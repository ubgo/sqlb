// Package vet holds the analyzers behind `sqlb vet`: the checks the compiler
// cannot make, as standard golang.org/x/tools/go/analysis values.
//
// Two mistakes survive the type system and are caught here instead. An engine
// expression used in another engine's statement — pg.ArrayAgg inside a
// sqlite.Select — is universal by type and fails only at Build; the compiler
// cannot see it because an expression carries no dialect. And a builder
// method whose result is discarded — q.Where(x) with no assignment — does
// nothing, because every builder method returns a new statement, and the
// compiler cannot flag an ignored return value.
//
// The analyzers are ordinary analysis.Analyzer values so they run three ways
// with one implementation: from cmd/sqlb as `sqlb vet ./...`, from
// `go vet -vettool`, and from an application's own test through Check, which
// needs no binary installed. This is a contrib module because it depends on
// x/tools, and the core depends on nothing.
package vet

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Module is the import path of the core; the engine and entry packages hang
// off it. A path is compared rather than a package name, so an import alias
// cannot hide a package from the check.
const Module = "github.com/ubgo/sqlb"

// Engine packages, by the dialect name their constructs are guarded with.
// An extension sub-package such as pg/vector belongs to its parent's engine.
var enginePackages = map[string]string{
	Module + "/pg":     "postgres",
	Module + "/sqlite": "sqlite",
	Module + "/ch":     "clickhouse",
	Module + "/mysql":  "mysql",
}

// Entry packages and dialect types, by the engine they fix a statement to.
//
// The engine packages are also the entry packages: pg.Select starts a Postgres
// statement and pg.ArrayAgg is a Postgres expression, in one package. So a root
// call counts as a statement constructor only when its name is one of
// entryConstructors. Matching the package alone was correct while the entry
// packages were pgq and liteq, which held nothing else; after they merged into
// pg and sqlite it would have treated a chain rooted at any engine function as
// a statement of that engine.
var entryPackages = map[string]string{
	Module + "/pg":     "postgres",
	Module + "/sqlite": "sqlite",
	Module + "/mysql":  "mysql",
}

// entryConstructors are the functions in an entry package that start a
// statement. A new statement constructor added to an engine package is not
// recognised until it is named here, and TestEntryConstructors_MatchTheEnginePackages
// fails until it is.
var entryConstructors = map[string]bool{
	"Select": true, "Insert": true, "Update": true, "Delete": true,
	"CreateTable": true, "AlterTable": true, "DropTable": true,
	"CreateIndex": true, "RenameIndex": true, "DropIndex": true,
	"CreateSchema": true, "DropSchema": true, "RenameSchema": true, "CreateExtension": true, "DropExtension": true,
	"CreateEnum": true, "AlterEnum": true, "DropType": true,
	"CommentOn": true, "CommentOnTable": true, "CommentOnColumn": true, "CommentOnConstraint": true,
	"Guarded": true, "AddConstraintStatement": true, "CreateTableLike": true,
	"CreateSequence": true, "AlterSequence": true, "DropSequence": true,
	"CreateView": true, "RenameView": true, "DropView": true,
	"CreateMaterializedView": true, "RefreshMaterializedView": true, "DropMaterializedView": true,
	"CreateTriggerFunction": true, "DropFunction": true, "CreateTrigger": true, "DropTrigger": true, "RenameTrigger": true,
	"CreatePartition": true, "DetachPartition": true,
	"CreatePolicy": true, "AlterPolicy": true, "DropPolicy": true,
	"CreateDomain": true, "AlterDomain": true, "DropDomain": true,
	"CreateCollation": true, "DropCollation": true, "CreateStatistics": true, "DropStatistics": true,
	"UpdateExtension": true, "RenameMaterializedView": true,
	"SetUserVersion": true, "ForeignKeys": true, "CreateFTS5": true, "Rebuild": true, "CreateRTree": true, "SetApplicationID": true,
	"CreateVirtualTable": true, "CreateGeopoly": true,
	"DeferForeignKeys": true, "RecursiveTriggers": true, "LegacyAlterTable": true,
	"SetJournalMode": true, "SetSynchronous": true, "SetTempStore": true, "SetAutoVacuum": true, "SetSecureDelete": true,
	"SetBusyTimeout": true, "SetCacheSizePages": true, "SetCacheSizeKibibytes": true, "SetMmapSize": true, "SetPageSize": true,
	"IntegrityCheck": true, "QuickCheck": true, "ForeignKeyCheck": true,
	"IntegrityCheckOf": true, "QuickCheckOf": true, "ForeignKeyCheckOf": true,
	"SetViewColumnDefault": true, "DropViewColumnDefault": true, "CreateComposite": true, "AlterComposite": true, "DropComposite": true,
	"Truncate": true, "RenameTable": true,
	"SetObjectSchema": true, "AlterViewOptions": true,
	"AlterView":   true,
	"CreateEvent": true, "AlterEvent": true, "DropEvent": true,
	"CreateFunction": true, "CreateProcedure": true,
	"AlterFunction": true, "AlterProcedure": true, "DropProcedure": true,
	"CreateTableAs": true, "DropTemporaryTable": true,
	"SetOwner": true, "AlterIndex": true, "AlterMaterializedView": true, "RenameViewColumn": true,
	"AddExtensionMember": true, "DropExtensionMember": true, "SetExtensionSchema": true, "CreateCFunction": true,
	"CreateRangeType": true, "CreateShellType": true, "CreateBaseType": true,
	"AlterRangeType": true, "AlterBaseType": true, "CommentOnDomainConstraint": true,
	"CreateCollationFrom": true, "CreateStatisticsExpr": true,
	"DependsOnExtension": true, "NoDependsOnExtension": true,
}

var dialectTypes = map[string]string{
	Module + "/dialect/postgres.Dialect":   "postgres",
	Module + "/dialect/sqlite.Dialect":     "sqlite",
	Module + "/dialect/clickhouse.Dialect": "clickhouse",
	Module + "/dialect/mysql.Dialect":      "mysql",
	Module + ".PostgresDialect":            "postgres",
	Module + ".SQLiteDialect":              "sqlite",
	Module + ".ClickHouseDialect":          "clickhouse",
	Module + ".MySQLDialect":               "mysql",
}

// dialectEngine names the engine of a dialect type, or "" for any other type.
//
// It resolves aliases first. pg.Dialect is an alias for postgres.Dialect, and
// types.TypeString prints an alias by its own name, so looking the name up
// directly missed every statement written as pg.Select, whose type argument is
// the alias, and every sqlb.NewSelect(pg.Dialect{}). A user's own alias for a
// dialect would have hidden it the same way.
func dialectEngine(t types.Type) string {
	return dialectTypes[types.TypeString(types.Unalias(t), nil)]
}

// Statement types whose builder methods return a new value; a call whose
// result is dropped did nothing.
var statementTypes = map[string]bool{
	"Select": true, "Insert": true, "InsertReady": true,
	"Update": true, "FilteredUpdate": true, "Delete": true, "FilteredDelete": true,
}

// Analyzers returns every analyzer this package defines, for a runner.
func Analyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{Dialect, Discarded, Literal, StructLit, Keyword, Rows}
}

// Dialect reports an engine expression used inside a statement whose engine
// is statically known to be a different one.
//
// The statement's engine comes from its root call: an entry package's
// constructor (pg.Select), or NewSelect and its siblings given a dialect
// literal. A statement whose dialect is a runtime value has no engine here
// and is not checked; Build catches it, and that is documented. The engine
// of an expression comes from the import path of the package it is selected
// from, resolved through type information so an alias does not hide it.
var Dialect = &analysis.Analyzer{
	Name:     "sqlbdialect",
	Doc:      "reports an engine expression (pg., sqlite., ch.) used in a statement fixed to another engine",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runDialect,
}

// Discarded reports a builder method call whose result is not used.
//
// Every builder method returns a derived statement and leaves its receiver
// alone, so `q.Where(p)` on its own line builds the unfiltered query and the
// compiler cannot say so.
var Discarded = &analysis.Analyzer{
	Name:     "sqlbdiscarded",
	Doc:      "reports a statement builder call whose result is discarded",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runDiscarded,
}

func runDialect(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	// Only outermost chains: a call that is itself the receiver of another call
	// in the same chain is examined as part of that outer call.
	inner := map[*ast.CallExpr]bool{}
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if rc, ok := sel.X.(*ast.CallExpr); ok {
				inner[rc] = true
			}
		}
	})
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if inner[call] {
			return
		}
		engine := chainEngine(pass, call)
		if engine == "" {
			return
		}
		for _, arg := range chainArgs(call) {
			ast.Inspect(arg, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgEngine := selectorEngine(pass, sel)
				if pkgEngine != "" && pkgEngine != engine {
					pass.Reportf(sel.Pos(), "%s construct %s in a statement fixed to %s: this fails at Build, not at compile time",
						pkgEngine, sel.Sel.Name, engine)
					return false
				}
				return true
			})
		}
	})
	return nil, nil
}

// chainEngine walks a method chain down to its root call and names the engine
// it fixes the statement to, or "" when the engine is not statically known.
func chainEngine(pass *analysis.Pass, call *ast.CallExpr) string {
	root := call
	for {
		sel, ok := root.Fun.(*ast.SelectorExpr)
		if !ok {
			break
		}
		rc, ok := sel.X.(*ast.CallExpr)
		if !ok {
			break
		}
		root = rc
	}
	sel, ok := root.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg := selectorPackage(pass, sel)
	if pkg == "" {
		return ""
	}
	if engine, ok := entryPackages[pkg]; ok && entryConstructors[sel.Sel.Name] {
		return engine
	}
	// NewSelect(d) and siblings, from stmt or the root facade: the engine is
	// the argument's static type, when it is a dialect literal.
	if (pkg == Module || pkg == Module+"/stmt") && strings.HasPrefix(sel.Sel.Name, "New") && len(root.Args) == 1 {
		if t := pass.TypesInfo.TypeOf(root.Args[0]); t != nil {
			if engine := dialectEngine(t); engine != "" {
				return engine
			}
		}
	}
	return ""
}

// chainArgs collects the arguments of every call in a method chain.
func chainArgs(call *ast.CallExpr) []ast.Expr {
	var out []ast.Expr
	for c := call; c != nil; {
		out = append(out, c.Args...)
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			break
		}
		rc, ok := sel.X.(*ast.CallExpr)
		if !ok {
			break
		}
		c = rc
	}
	return out
}

// selectorPackage returns the import path a qualified selector names, or "".
func selectorPackage(pass *analysis.Pass, sel *ast.SelectorExpr) string {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	pn, ok := pass.TypesInfo.Uses[id].(*types.PkgName)
	if !ok {
		return ""
	}
	return pn.Imported().Path()
}

// selectorEngine names the engine of a qualified selector into an engine
// package or one of its extension sub-packages, or "".
func selectorEngine(pass *analysis.Pass, sel *ast.SelectorExpr) string {
	pkg := selectorPackage(pass, sel)
	if pkg == "" {
		return ""
	}
	// A type renders nothing, so it is not a construct. pg.Dialect{} is the
	// dialect of a pg statement embedded in another engine's statement, and
	// reporting it alongside the statement's constructor gave one mistake two
	// findings.
	if _, isType := pass.TypesInfo.Uses[sel.Sel].(*types.TypeName); isType {
		return ""
	}
	for path, engine := range enginePackages {
		if pkg == path || strings.HasPrefix(pkg, path+"/") {
			return engine
		}
	}
	return ""
}

func runDiscarded(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	ins.Preorder([]ast.Node{(*ast.ExprStmt)(nil)}, func(n ast.Node) {
		call, ok := n.(*ast.ExprStmt).X.(*ast.CallExpr)
		if !ok {
			return
		}
		if name := statementType(pass.TypesInfo.TypeOf(call)); name != "" {
			pass.Reportf(call.Pos(), "result of the %s builder call is discarded: every builder method returns a new statement, so this changed nothing", name)
		}
	})
	return nil, nil
}

// statementType names the stmt statement type a value is a pointer to, or "".
func statementType(t types.Type) string {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return ""
	}
	named, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	path := named.Obj().Pkg().Path()
	if path == Module+"/stmt" && statementTypes[named.Obj().Name()] {
		return named.Obj().Name()
	}
	if _, engine := enginePackages[path]; engine && hasBuild(p) {
		return named.Obj().Name()
	}
	// Every pointer type in ddl is a statement, the incomplete forms too:
	// dropping the result of CreateTable(t).Column(c) is the same mistake as
	// dropping a Where, and the incomplete form has no Build to recognise.
	if path == Module+"/ddl" {
		return named.Obj().Name()
	}
	return ""
}

// hasBuild reports whether t has a Build method, which is what makes a type in
// an engine package a statement builder: pg.Merge, pg.Truncate, pg.LockTable,
// and every DDL builder to come. Matching the method rather than a list of
// names is deliberate. The engine builders were missed by the core's
// receiver-write test for exactly the reason a name list would miss the next
// one here: nobody added it.
func hasBuild(t types.Type) bool {
	sel := types.NewMethodSet(t).Lookup(nil, "Build")
	return sel != nil
}
