package stmt

// Option extends a statement that a library built, from code that the library
// does not know. It carries an extra join, predicate, binding or ordering
// term. The library applies options before Build and keeps ownership of
// Build, so its mandatory predicates cannot be removed by an option: options
// only add.
//
// An Option edits the universal core of a statement and nothing else, which
// is why it has no dialect type parameter. Go infers a type parameter from a
// function's arguments, never from where its result is used, so an Option[D]
// returned by WithWhere(p) could not have inferred D and every caller would
// have written it out — on the one seam that exists for callers. Editing the
// core sidesteps that: a clause carries a dialect and an option does not, so
// clauses are added with Clause on the statement, where the compiler checks
// them, and there is no WithClause.
type Option func(*selectCore)

// WithJoin adds joins to the statement.
func WithJoin(js ...Join) Option {
	return func(c *selectCore) { c.joins = append(c.joins, js...) }
}

// WithWhere adds predicates. They are ANDed with the library's own.
func WithWhere(ps ...Predicate) Option {
	return func(c *selectCore) { c.where = append(c.where, ps...) }
}

// WithColumns adds bindings after the library's own, so the library's scan
// targets keep their positions.
func WithColumns(bs ...Binding) Option {
	return func(c *selectCore) {
		for _, b := range bs {
			c.items = append(c.items, selectItem{expr: b.SQL, target: b.Target()})
		}
	}
}

// WithOrder adds order terms after the library's own.
func WithOrder(ts ...OrderTerm) Option {
	return func(c *selectCore) { c.order = append(c.order, ts...) }
}

// Apply runs each option against a derived copy of the statement, in order,
// and returns it. The receiver is not modified.
func (s *Select[D]) Apply(opts ...Option) *Select[D] {
	c := s.derive()
	for _, o := range opts {
		o(&c.selectCore)
	}
	return c
}
