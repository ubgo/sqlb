// Package schema declares columns for the --dsn mode's test, some right and
// some wrong against the table the test creates.
package schema

import (
	"time"

	"github.com/ubgo/sqlb/expr"
)

// Accounts is the table the test creates; the columns below are declared
// against it, some right and some wrong.
var (
	Accounts = expr.Table{Name: "accounts", Alias: "a"}
	ID       = expr.Col[int64](Accounts, "id")       // right
	Email    = expr.Col[string](Accounts, "email")   // right
	Age      = expr.Col[string](Accounts, "age")     // wrong type: integer column
	Nick     = expr.Col[string](Accounts, "nick")    // wrong nullability: nullable column
	Gone     = expr.Col[bool](Accounts, "deleted")   // column does not exist
	Created  = expr.Col[time.Time](Accounts, "made") // right

	Ghost   = expr.Table{Name: "ghosts", Alias: "g"}
	GhostID = expr.Col[int64](Ghost, "id") // table does not exist
)
