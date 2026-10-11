package stmt

// Binding is a select or RETURNING item that carries a scan destination.
//
// It is an interface, and that is the whole point of this file. The statement
// layer has to put something in Query.Targets, but it must not know that the
// something is a pointer into a caller's variable: the moment it does, the
// hazard belongs to this layer, and a second result strategy has to fight a
// statement that already took an opinion.
//
// So the statement layer knows only that an item renders, may carry a target,
// and may know its own bare column name. Whether the target is a pointer, a
// channel, a slot in an arena, or nil is decided above, by whichever package
// constructs the item. github.com/ubgo/sqlb/bind is the one that makes it a
// pointer, and it is where the concurrency warning lives because it is the
// layer that captured the memory.
//
// Being an interface rather than a struct is also what kept the split from
// breaking callers. Every existing use was a slice of Binding values produced
// by Bind, never a struct literal, so the same code compiles against the
// interface unchanged.
type Binding interface {
	// SQL renders the item.
	SQL(a *Args) string
	// Target is the scan destination, or nil when the item has none.
	Target() any
	// ColumnName is the bare column name when the item came from a column, and
	// "" otherwise. RETURNING on an unaliased INSERT renders it.
	ColumnName() string
}

// Targets returns the scan destinations of a binding list, in order.
//
// A nil result when the list is empty is deliberate: Query.Targets being nil is
// what One and Each check to report ErrNoTargets, and an empty non-nil slice
// would pass that check while scanning nothing.
func Targets(bs []Binding) []any {
	if len(bs) == 0 {
		return nil
	}
	out := make([]any, len(bs))
	for i, b := range bs {
		out[i] = b.Target()
	}
	return out
}
