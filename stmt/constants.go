// The statement layer's vocabulary: join keywords, sort directions and the
// keywords a statement is assembled from.
//
// These are here rather than below because a clause keyword is grammar. The
// render layer must stay free of them so that a target which is not SQL can
// still use it.

package stmt

// JoinType is the SQL join keyword. It is a distinct string type rather than a
// bare string so a caller cannot pass an arbitrary fragment into the FROM
// clause, which would be an injection point in an otherwise parameterised
// builder.
type JoinType string

// The join types the core names.
//
// These four are what Postgres, MySQL 8, SQLite and ClickHouse all accept with
// the same meaning, which is the same rule every other construct here follows.
//
// FULL OUTER JOIN is absent for that reason rather than for want of a use
// case: MySQL has no FULL OUTER JOIN and emulates it with a UNION of a LEFT
// and a RIGHT join, so naming it here would mean shipping a constant that
// works on three engines and silently fails on the fourth.
//
// It is still reachable, because JoinType is an ordinary string type and Join
// takes whatever it is given:
//
//	Join(Join{Type: JoinType("FULL OUTER JOIN"), ...})
//
// That is the documented escape hatch and the SET stays open: no list here
// decides which joins exist, so an engine's own keyword is reachable without
// this module naming it. An engine package wanting a version checked against
// what it supports would wrap it and record Args.Unsupported, the way every
// other engine-specific construct does.
//
// The TEXT is checked, which is a different thing and was added on
// 2026-09-24. The keyword is written into the FROM clause verbatim and cannot
// be quoted — a join keyword in quotes is an identifier — so a JoinType
// holding "INNER JOIN t ON 1=1; DROP TABLE users; --" was a statement, not a
// join. It now has to have the shape of a keyword: letters and single spaces,
// in either case. That enforces the word "keyword" this decision already
// rested on and refuses nothing a caller could legitimately write;
// TestJoinAndSetOp_TheEscapeHatchStillWorks builds FULL OUTER JOIN to prove
// the hatch is intact.
//
// NATURAL joins are absent and will stay absent. A natural join changes which
// columns it matches on when somebody adds a column, so a query that was
// correct yesterday returns different rows today with nothing in the diff to
// show why.
const (
	JoinInner JoinType = "INNER JOIN"
	JoinLeft  JoinType = "LEFT JOIN"
	JoinRight JoinType = "RIGHT JOIN"
	JoinCross JoinType = "CROSS JOIN"
)

// JoinTypeValues is the canonical iteration order over join types.
var JoinTypeValues = []JoinType{JoinInner, JoinLeft, JoinRight, JoinCross}

// SQL keywords and separators the builder assembles statements from.
const (
	kwSelect      = "SELECT"
	kwFrom        = "FROM"
	kwWhere       = "WHERE"
	kwGroupBy     = "GROUP BY"
	kwHaving      = "HAVING"
	kwWindow      = "WINDOW"
	kwOrderBy     = "ORDER BY"
	kwUsing       = "USING"
	kwDistinct    = "DISTINCT"
	kwWith        = "WITH"
	kwRecursive   = "RECURSIVE"
	kwUnion       = "UNION"
	kwUnionAll    = "UNION ALL"
	kwIntersect   = "INTERSECT"
	kwExcept      = "EXCEPT"
	kwPartitionBy = "PARTITION BY"
	kwValues      = "VALUES"
	kwInsert      = "INSERT"
	kwInto        = "INTO"
	kwDefault     = "DEFAULT"
	kwUpdate      = "UPDATE"
	kwSet         = "SET"
	kwDelete      = "DELETE"
	kwReturning   = "RETURNING"

	// Function names used by the core. Standard SQL or present in every
	// dialect the core ships.
	fnConcat     = "concat"
	fnCount      = "count"
	fnSum        = "sum"
	fnAvg        = "avg"
	fnMin        = "min"
	fnMax        = "max"
	fnRowNumber  = "row_number"
	fnRank       = "rank"
	fnDenseRank  = "dense_rank"
	fnFirstValue = "first_value"
	fnLastValue  = "last_value"
	fnNthValue   = "nth_value"
	kwLimit      = "LIMIT"
	kwOffset     = "OFFSET"
	kwOn         = "ON"
	kwAs         = "AS"

	sepComma = ", "
	sepSpace = " "
)
