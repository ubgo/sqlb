// Package constnames declares tables and columns whose names are constants
// rather than literals, which is how many libraries write them, for the test
// that Declarations resolves them.
package constnames

import (
	"github.com/ubgo/sqlb/contrib/vet/internal/probe/constnames/lookalike"
	"github.com/ubgo/sqlb/expr"
)

// The names, as a library keeps them.
const (
	schemaName = "auth"
	prefix     = "auth_"
	usersTable = prefix + "users"
	emailCol   = "email"
)

// tableKind is a named string type, so a typed constant is resolved too.
type tableKind string

const sessionsTable tableKind = "sessions"

var (
	// Users is named entirely by constants, one of them a concatenation.
	Users = expr.Table{Schema: schemaName, Name: usersTable, Alias: "u"}
	// Email names its column by a constant.
	Email = expr.Col[string](Users, emailCol)
	// Sessions is named by a typed constant.
	Sessions = expr.Table{Name: string(sessionsTable), Alias: "s"}
	// SessionID is a literal, for contrast.
	SessionID = expr.Col[int64](Sessions, "id")
	// Dynamic has its table built by a function, which no reader of source can
	// resolve, so Declarations reports it rather than guessing.
	Dynamic = expr.Col[int64](table(), "id")
)

func table() expr.Table { return expr.Table{Name: "made_at_run_time"} }

// Shapes Declarations must skip or report, never misread.
var (
	// columnName is a variable, so a column named by it is reported with no
	// name rather than read as whatever it holds at run time.
	columnName = "chosen_at_run_time"
	// FromVariable names its column by the variable.
	FromVariable = expr.Col[string](Users, columnName)
	// Built is a Table from a function call, not a literal.
	Built = table()
	// aliasVar makes one field of a literal non-constant.
	aliasVar = "v"
	// PartlyConstant has a constant Name and a variable Alias.
	PartlyConstant = expr.Table{Name: "partly", Alias: aliasVar}
	// NotAColumn calls a Col that is not sqlb's.
	NotAColumn = lookalike.Col[int64](Users, "x")
	// NotACol is a two-argument generic call by another name.
	NotACol = lookalike.Pair[int](1, 2)
	// Local is a two-argument generic call with no package at all.
	Local = pair[int](1, 2)
	// Plain is a two-argument call that is not generic.
	Plain = two(1, 2)
)

// noValueA and noValueB are declared with no literal to read.
var noValueA, noValueB expr.Table

// Declared lists the two, so the pair is used and stays a declaration the
// scanner must read past.
var Declared = []expr.Table{noValueA, noValueB}

func pair[T any](a, b T) [2]T { return [2]T{a, b} }

func two(a, b int) int { return a + b }

// holder has a field named Col holding functions, so holder.Col[0](a, b)
// looks like a generic Col call to a reader of syntax and is not one.
type holder struct {
	Col   []func(a, b int) int
	inner *holder
}

var h = holder{Col: []func(a, b int) int{two}, inner: &holder{Col: []func(a, b int) int{two}}}

var (
	// FieldCall indexes a field named Col.
	FieldCall = h.Col[0](1, 2)
	// NestedCall reaches a field named Col through a nested selector.
	NestedCall = h.inner.Col[0](1, 2)
)

// point is written positionally, a literal with no field names to read.
type point struct{ x, y int }

// Origin is a positional literal.
var Origin = point{0, 0}
