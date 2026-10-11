package mysql

import (
	"strings"

	"github.com/ubgo/sqlb"
)

const (
	kwIgnore            = "IGNORE"
	kwAs                = "AS"
	kwOnDuplicateUpdate = "ON DUPLICATE KEY UPDATE"
	kwOrderBy           = "ORDER BY"
	kwLimit             = "LIMIT"

	// newRow is the alias OnDuplicateKeyUpdate gives the row being inserted,
	// which New refers to. MySQL 8.0.19 replaced VALUES(col) with it, and
	// VALUES() in this position is deprecated.
	newRow = "new"
)

// InsertIgnore makes the INSERT skip a row that would violate a unique key
// instead of failing, turning the error into a warning. It also turns other
// errors into warnings, a value too long for its column among them, which is
// why OnDuplicateKeyUpdate is the better answer when a duplicate is the only
// expected conflict.
func InsertIgnore() InsertClause {
	return clause[sqlb.InsertKind](sqlb.SlotAfterInsert, constructWrite, func(*sqlb.Args) string { return kwIgnore })
}

// OnDuplicateKeyUpdate is MySQL's upsert: when the row would violate a unique
// key, the existing row is updated with the assignments instead. It names the
// inserted row with a row alias, AS new, which New refers to; it targets every
// unique key at once, where Postgres's ON CONFLICT targets one, which is why it
// is here and not beside it.
func OnDuplicateKeyUpdate(first sqlb.Assignment, more ...sqlb.Assignment) InsertClause {
	as := append([]sqlb.Assignment{first}, more...)
	return clause[sqlb.InsertKind](sqlb.SlotTail, constructWrite, func(a *sqlb.Args) string {
		// MySQL's INSERT takes no table alias, so an alias-qualified column is
		// unknown, and the row alias exposes new.col, so a bare one is
		// ambiguous: the existing row's columns are qualified by the table's
		// name. The target of each SET stays bare, which MySQL requires.
		parts := make([]string, len(as))
		for i, x := range as {
			parts[i] = a.TableNamed(func() string { return x.SQL(a) })
		}
		return kwAs + " " + a.Dialect().QuoteIdent(newRow) + " " + kwOnDuplicateUpdate + " " + strings.Join(parts, ", ")
	})
}

// New is the value the INSERT tried to write to col, inside an
// OnDuplicateKeyUpdate: SET hits = hits + new.hits.
func New[T any](col sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructWrite, func(a *sqlb.Args) string {
		d := a.Dialect()
		return d.QuoteIdent(newRow) + "." + d.QuoteIdent(col.Name())
	})
}

// orderLimit renders ORDER BY terms and a bound LIMIT for a single-table
// UPDATE or DELETE, which MySQL accepts and the other engines here do not.
func orderLimit(a *sqlb.Args, n int64, order []sqlb.OrderTerm) string {
	var b strings.Builder
	if len(order) > 0 {
		b.WriteString(kwOrderBy + " ")
		for i, o := range order {
			if i > 0 {
				b.WriteString(", ")
			}
			o.WriteSQL(&b, a)
		}
		b.WriteString(" ")
	}
	b.WriteString(kwLimit + " " + a.Next(n))
	return b.String()
}

// UpdateLimit caps an UPDATE at n rows, in the order given, which is how a
// large backfill is done in batches without a key range. A single-table
// UPDATE only.
func UpdateLimit(n int64, order ...sqlb.OrderTerm) UpdateClause {
	return clause[sqlb.UpdateKind](sqlb.SlotTail, constructWrite, func(a *sqlb.Args) string { return orderLimit(a, n, order) })
}

// DeleteLimit caps a DELETE at n rows, in the order given: the batched purge
// of an old-rows table. A single-table DELETE only.
func DeleteLimit(n int64, order ...sqlb.OrderTerm) DeleteClause {
	return clause[sqlb.DeleteKind](sqlb.SlotTail, constructWrite, func(a *sqlb.Args) string { return orderLimit(a, n, order) })
}
