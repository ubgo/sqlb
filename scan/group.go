package scan

// GroupBy folds the flat rows a join returns into a nested shape.
//
// A join returns the parent repeated once per child, and what a caller wants
// is one parent holding its children. That reshaping is the job every ORM does
// with reflection over primary-key tags; here it is a function, so the key and
// the shape are both decided by the caller and both checked by the compiler.
//
//	rows, err := scan.All(ctx, db, sel, flatReader)
//	actors := scan.GroupBy(rows,
//		func(r flat) int64 { return r.Actor.ID },
//		func(first flat, group []flat) actorWithFilms {
//			films := make([]film, len(group))
//			for i, r := range group {
//				films[i] = r.Film
//			}
//			return actorWithFilms{first.Actor, films}
//		})
//
// combine receives the first row of the group, which carries the parent's own
// columns, and every row in it. Taking both rather than only the slice is what
// keeps the parent available without the caller indexing into group[0] and
// having to reason about whether it can be empty; it cannot.
//
// The result is in first-appearance order, not map order, so a query with an
// ORDER BY keeps it. That is worth stating because a grouping built on a map
// alone would return a different order on every run and the difference would
// only show up in a test somebody wrote later.
//
// Nesting deeper is this function applied again inside combine. Two levels is
// the common case and there is no limit.
func GroupBy[T any, K comparable, R any](
	rows []T,
	key func(T) K,
	combine func(first T, group []T) R,
) []R {
	if len(rows) == 0 {
		return nil
	}
	order := make([]K, 0, len(rows))
	groups := make(map[K][]T, len(rows))
	for _, r := range rows {
		k := key(r)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	out := make([]R, 0, len(order))
	for _, k := range order {
		g := groups[k]
		out = append(out, combine(g[0], g))
	}
	return out
}

// GroupByPtr is GroupBy for a LEFT JOIN, where a parent with no children still
// produces a row and every child column in it is NULL.
//
// Rows whose key is nil are dropped rather than grouped under a zero key, so a
// parent with no children yields an empty slice instead of a slice holding one
// zero-valued child. That is the difference between "Bob has no films" and
// "Bob has one film with no name", and getting it wrong is the classic bug of
// hand-written join mapping.
//
//	films := scan.GroupByPtr(rowsForThisActor,
//		func(r flat) *int64 { return r.Film.ID },
//		func(f flat, group []flat) filmWithCategories { ... })
//
// Use GroupBy when the join is inner and the key cannot be NULL; the compiler
// will tell you which you have, because the column's Go type is a pointer only
// when it is nullable.
func GroupByPtr[T any, K comparable, R any](
	rows []T,
	key func(T) *K,
	combine func(first T, group []T) R,
) []R {
	present := make([]T, 0, len(rows))
	for _, r := range rows {
		if key(r) != nil {
			present = append(present, r)
		}
	}
	return GroupBy(present, func(r T) K { return *key(r) }, combine)
}

// Collect maps each row of a group through f, which is the body most combine
// functions would otherwise write by hand.
//
//	func(first flat, g []flat) actorWithFilms {
//		return actorWithFilms{first.Actor, scan.Collect(g, func(r flat) film { return r.Film })}
//	}
func Collect[T, R any](rows []T, f func(T) R) []R {
	if len(rows) == 0 {
		return nil
	}
	out := make([]R, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}

// CollectPtr is Collect for a nullable child, dropping rows whose value is nil
// and dereferencing the rest.
func CollectPtr[T, R any](rows []T, f func(T) *R) []R {
	var out []R
	for _, r := range rows {
		if v := f(r); v != nil {
			out = append(out, *v)
		}
	}
	return out
}
