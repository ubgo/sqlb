package sqlb

import (
	"github.com/ubgo/sqlb/bind"
	"github.com/ubgo/sqlb/stmt"
)

// Binding is a select or RETURNING item that carries a scan destination.
//
// It is an interface defined by the statement layer, which knows only that an
// item renders and may carry a target. That the target is a pointer into a
// caller variable is decided by the bind package, which is also where the
// concurrency warning lives, because it is the layer that captured the memory.
type Binding = stmt.Binding

// Bind pairs e with dst. The compiler rejects a dst whose type is not *T.
//
// The pointer is captured, not the value, and it lives as long as the query
// does. Two consequences worth knowing before they bite: Each writes every row
// through the same pointer, so a callback keeping a row must copy it; and a
// query built this way is NOT SAFE FOR CONCURRENT USE, because two goroutines
// would write through these pointers at once, which is a DATA RACE. Build the
// query where its destination lives. See One and ErrQueryInUse, and see
// github.com/ubgo/sqlb/scan for the strategy that returns values instead.
func Bind[T any](e Expr[T], dst *T) Binding { return bind.Bind[T](e, dst) }

// Bindings concatenates binding groups, which is how one query scans into
// structs owned by several packages that do not import each other.
func Bindings(groups ...[]Binding) []Binding { return bind.Bindings(groups...) }

// Targets returns the scan destinations of a binding list, in order.
func Targets(bs []Binding) []any { return bind.Targets(bs) }
