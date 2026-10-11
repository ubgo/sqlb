package stmt

// derive returns a copy to build on, the way every builder method here does.
//
// It is Clone with one difference: the execution guard is inherited rather
// than replaced. A method-derived statement is the same statement with one
// more clause, so a hoisted builder executed from two goroutines must still be
// refused with ErrQueryInUse; giving each derivation a fresh guard would have
// quietly dropped that, since the guard is what makes a hoisted builder as
// safe as a hoisted query. Clone is the deliberate act of starting a separate
// statement, and only Clone resets it.
func (s *Select[D]) derive() *Select[D] {
	c := s.Clone()
	c.guard = s.guard
	return c
}

// Clone returns a copy that shares nothing with the original, including its
// execution guard.
//
// Deriving a query no longer needs it: every builder method copies, so
// base.Where(x) leaves base alone. What Clone means now is narrower and still
// necessary — a derived statement inherits the guard, because it holds the
// same bindings, which are pointers into the caller's variables, so two
// queries derived from one base must not execute at once. A clone is a
// separate statement and is not refused because the one it came from is
// running.
//
// Why this is worth its own file rather than a one-line struct copy: a shallow
// copy shares the backing arrays, so appending to the copy can write into the
// original's spare capacity and change it. Every slice is therefore copied,
// and the clause map is rebuilt.
//
// What is deliberately not deep: the expressions, predicates and clauses
// themselves. Those are values that render and do not mutate, so sharing them
// is safe and copying them would be pointless work.
func (s *Select[D]) Clone() *Select[D] {
	if s == nil {
		return nil
	}
	c := *s
	c.guard = &execGuard{}
	c.ctes = cloneSlice(s.ctes)
	c.items = cloneSlice(s.items)
	c.joins = cloneSlice(s.joins)
	c.where = cloneSlice(s.where)
	c.groupBy = cloneSlice(s.groupBy)
	c.having = cloneSlice(s.having)
	c.windows = cloneSlice(s.windows)
	c.setOps = cloneSlice(s.setOps)
	c.order = cloneSlice(s.order)
	c.clauses = cloneClauses(s.clauses)
	return &c
}

// Clone returns an independent copy of the INSERT with its own guard.
func (i *InsertReady[D]) Clone() *InsertReady[D] {
	if i == nil {
		return nil
	}
	c := *i
	c.guard = &execGuard{}
	c.ctes = cloneSlice(i.ctes)
	c.rows = cloneRows(i.rows)
	c.returning = cloneSlice(i.returning)
	c.clauses = cloneClauses(i.clauses)
	return &c
}

// Clone returns an independent copy of the UPDATE with its own guard.
func (u *FilteredUpdate[D]) Clone() *FilteredUpdate[D] {
	if u == nil {
		return nil
	}
	c := *u
	c.guard = &execGuard{}
	c.ctes = cloneSlice(u.ctes)
	c.sets = cloneSlice(u.sets)
	c.where = cloneSlice(u.where)
	c.returning = cloneSlice(u.returning)
	c.clauses = cloneClauses(u.clauses)
	return &c
}

// Clone returns an independent copy of the DELETE with its own guard.
func (x *FilteredDelete[D]) Clone() *FilteredDelete[D] {
	if x == nil {
		return nil
	}
	c := *x
	c.guard = &execGuard{}
	c.ctes = cloneSlice(x.ctes)
	c.where = cloneSlice(x.where)
	c.returning = cloneSlice(x.returning)
	c.clauses = cloneClauses(x.clauses)
	return &c
}

// cloneSlice copies a slice so the copy cannot write into the original's spare
// capacity. A nil slice stays nil, which several renderers check.
func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

// cloneRows copies the row list of an INSERT, which is a slice of slices and
// so needs both levels.
func cloneRows(in [][]SQLer) [][]SQLer {
	if in == nil {
		return nil
	}
	out := make([][]SQLer, len(in))
	for i, r := range in {
		out[i] = cloneSlice(r)
	}
	return out
}

// cloneClauses rebuilds the slot map, since a shallow copy would share it.
func cloneClauses[C AnyClause](in map[Slot][]C) map[Slot][]C {
	if in == nil {
		return nil
	}
	out := make(map[Slot][]C, len(in))
	for k, v := range in {
		out[k] = cloneSlice(v)
	}
	return out
}

// derive returns a copy to build on, for the same reason Select.derive does:
// the guard is inherited, because a derived write still carries the same
// RETURNING bindings, and only Clone starts a statement with its own.
func (i *InsertReady[D]) derive() *InsertReady[D] { c := i.Clone(); c.guard = i.guard; return c }

// derive returns a copy to build on. See Select.derive.
func (u *FilteredUpdate[D]) derive() *FilteredUpdate[D] { c := u.Clone(); c.guard = u.guard; return c }

// derive returns a copy to build on. See Select.derive.
func (x *FilteredDelete[D]) derive() *FilteredDelete[D] { c := x.Clone(); c.guard = x.guard; return c }
