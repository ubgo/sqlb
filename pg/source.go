package pg

import (
	"strconv"

	"github.com/ubgo/sqlb"
)

const (
	constructTableSample = "pg.TableSample"
	constructDistinctOn  = "pg.DistinctOn"
	constructFetchFirst  = "pg.FetchFirst"
	constructOverriding  = "pg.Overriding"

	kwTableSample   = "TABLESAMPLE"
	kwRepeatable    = "REPEATABLE"
	kwDistinctOn    = "DISTINCT ON"
	kwFetchFirst    = "FETCH FIRST"
	kwRowsOnly      = "ROWS ONLY"
	kwRowsWithTies  = "ROWS WITH TIES"
	kwOverridingSys = "OVERRIDING SYSTEM VALUE"
	kwOverridingUsr = "OVERRIDING USER VALUE"
)

// SampleMethod is the sampling method TABLESAMPLE uses.
//
// The set is the SERVER'S, not this module's, and that is why the two
// constants below are a starting point rather than a list to validate
// against: TABLESAMPLE takes whatever sampling method is installed, and
// tsm_system_rows and tsm_system_time each bring one. SYSTEM_ROWS is the
// method that takes a row count instead of a percentage, and this
// repository's own snippet catalogue builds it.
//
// It is checked in shape rather than membership for exactly the reason
// AccessMethod and IndexMethod are not checked at all: a list would refuse the
// extension that brings a real one. A list here was an over-closure that
// shipped for a few hours on 2026-09-24 and was caught by the examples, the
// only gate that had a SYSTEM_ROWS in it.
//
// Unlike AccessMethod it is NOT quoted, and that is measured rather than
// assumed: the method name folds to lower case unquoted, and the catalogue
// stores it lower case, so TABLESAMPLE "SYSTEM" (50) fails with "tablesample
// method SYSTEM does not exist" where TABLESAMPLE SYSTEM (50) works. Quoting
// this would have broken both constants below. The shape is therefore the
// only defence available, and the shape is an identifier's: the same check a
// function name gets, since a sampling method is a name the server looks up
// rather than a keyword its grammar knows.
type SampleMethod string

// The two sampling methods every Postgres has. An extension's method is
// written out: SampleMethod("SYSTEM_ROWS").
const (
	SampleBernoulli SampleMethod = "BERNOULLI"
	SampleSystem    SampleMethod = "SYSTEM"
)

// TableSample renders TABLESAMPLE method (percent) [REPEATABLE (seed)] after
// a table reference. Postgres requires the percentage as a literal
// expression; it is an author constant, never request data. seed < 0 omits
// REPEATABLE.
//
// TABLESAMPLE is per table reference, not per statement, so where the clause
// goes decides which table it samples. On the driving table it goes in the
// statement; on a joined table it goes in that join:
//
//	sel.From(t).Clause(pg.TableSample(pg.SampleSystem, 10, -1))
//	sel.Join(sqlb.Join{Type: sqlb.JoinInner, Table: other, On: ...,
//		Clauses: []sqlb.AnyClause{pg.TableSample(pg.SampleSystem, 10, -1)}})
//
// Putting a joined table's sample in the statement samples the driving table
// instead, which is a different query that still runs.
func TableSample(method SampleMethod, percent float64, seed int) SelectClause {
	return selectClause(sqlb.SlotAfterFrom, constructTableSample, func(a *sqlb.Args) string {
		s := kwTableSample + " " + sqlb.CheckedFunctionName(a, string(method), "sampling method") + " (" + strconv.FormatFloat(percent, 'g', -1, 64) + ")"
		if seed >= 0 {
			s += " " + kwRepeatable + " (" + strconv.Itoa(seed) + ")"
		}
		return s
	})
}

// DistinctOn renders DISTINCT ON (exprs) after SELECT. The expressions may be
// of mixed Go types, which is why it takes SQLer rather than Expr[T].
func DistinctOn(exprs ...sqlb.SQLer) SelectClause {
	return selectClause(sqlb.SlotAfterSelect, constructDistinctOn, func(a *sqlb.Args) string {
		return kwDistinctOn + " (" + list(a, exprs) + ")"
	})
}

// FetchFirst renders FETCH FIRST $n ROWS ONLY, or ROWS WITH TIES, after
// OFFSET. WITH TIES needs an ORDER BY; the database enforces that.
func FetchFirst(n int, withTies bool) SelectClause {
	return selectClause(sqlb.SlotAfterOffset, constructFetchFirst, func(a *sqlb.Args) string {
		tail := kwRowsOnly
		if withTies {
			tail = kwRowsWithTies
		}
		return kwFetchFirst + " " + a.Next(n) + " " + tail
	})
}

// OverridingSystemValue and OverridingUserValue are INSERT clauses for
// identity columns.
func OverridingSystemValue() InsertClause {
	return insertClause(sqlb.SlotBeforeValues, constructOverriding, func(*sqlb.Args) string { return kwOverridingSys })
}

// OverridingUserValue renders OVERRIDING USER VALUE, which makes the column
// take the generated value and ignore the one supplied. The clause a bulk
// load wants on a GENERATED ALWAYS identity column.
func OverridingUserValue() InsertClause {
	return insertClause(sqlb.SlotBeforeValues, constructOverriding, func(*sqlb.Args) string { return kwOverridingUsr })
}
