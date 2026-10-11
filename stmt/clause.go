// Package stmt assembles SQL statements: SELECT, INSERT, UPDATE and DELETE,
// the clause positions they can carry, and the Query a build produces.
//
// It knows the grammar and nothing about meaning. A Predicate is something that
// can render itself, and this package writes it after WHERE without knowing
// what it compares; a Clause is something that names a Slot, and this package
// writes it at that position without knowing what it says. That is what lets an
// engine package add a construct, and an outside package add a whole statement
// kind, with no change here.
//
// This is also the highest layer that is still purely about text. A Query is a
// string and a slice of values; how those values come back into Go is the
// concern of the layers above.
package stmt

import (
	"github.com/ubgo/sqlb/render"
	"strconv"
	"strings"
)

// Slot is a fixed position in a statement where an engine-specific clause may
// be written. The core defines the slots; engine packages define the clauses.
//
// The set is complete rather than minimal: there is a slot at every boundary
// the renderer emits, so every position in a statement has a name. That is a
// deliberate change from "no slot ships ahead of a clause". A slot is the one
// extension point a package outside this module cannot add for itself, since
// a position only means something if the renderer writes it, and completeness
// is what keeps an outside engine package first class. Several slots
// therefore have no clause in this module; the doc comment on each says which
// engine's clause it is for, or says plainly that none is known.
//
// A clause aimed at a slot a statement does not render fails at Build with
// ErrUnknownSlot rather than being dropped.
type Slot int

const (
	// SlotAfterSelect sits between SELECT and the column list: DISTINCT ON, TOP.
	SlotAfterSelect Slot = iota
	// SlotAfterColumns sits after the select list and before FROM. T-SQL puts
	// INTO there. No engine in this module uses it; it exists so the set has
	// no hole between the column list and FROM.
	SlotAfterColumns
	// SlotAfterFrom sits after the driving table and before joins: FINAL,
	// SAMPLE, TABLESAMPLE, ARRAY JOIN.
	SlotAfterFrom
	// SlotBeforeWhere sits after joins and before WHERE: PREWHERE.
	SlotBeforeWhere
	// SlotAfterWhere sits after WHERE and before GROUP BY, in a SELECT, an
	// UPDATE or a DELETE. No engine in this module uses it; it exists so the
	// set has no hole on either side of WHERE.
	SlotAfterWhere
	// SlotAfterGroupBy sits after the GROUP BY list: MySQL's WITH ROLLUP and
	// WITH CUBE, which are a suffix there rather than a wrapper as they are
	// in Postgres.
	SlotAfterGroupBy
	// SlotAfterHaving sits after HAVING and before WINDOW. No engine in this
	// module uses it; it exists so the set has no hole between the two.
	SlotAfterHaving
	// SlotBeforeOrder sits after HAVING and WINDOW and before ORDER BY:
	// QUALIFY, which DuckDB, Snowflake and BigQuery use to filter on a window
	// result without a subquery.
	SlotBeforeOrder
	// SlotAfterOrder sits after ORDER BY and before LIMIT: LIMIT n BY.
	SlotAfterOrder
	// SlotAfterOffset sits after OFFSET and before the tail: FETCH FIRST.
	SlotAfterOffset
	// SlotTail sits last: FOR UPDATE, SETTINGS, ON CONFLICT.
	SlotTail
	// SlotAfterInsert sits between the INSERT keyword and INTO: SQLite's
	// OR REPLACE and its siblings, which no other engine spells there.
	SlotAfterInsert
	// SlotAfterVerb sits between the UPDATE or DELETE keyword and its target,
	// where MySQL puts LOW_PRIORITY, QUICK and IGNORE. SlotAfterInsert is the
	// same position in an INSERT, and is separate because SQLite's OR REPLACE
	// family renders there and reads as part of the keyword.
	SlotAfterVerb
	// SlotBeforeValues is an INSERT slot between the column list and VALUES
	// or SELECT: OVERRIDING SYSTEM VALUE.
	SlotBeforeValues
	// SlotInsertBody replaces the VALUES part of an INSERT: DEFAULT VALUES.
	SlotInsertBody
	// SlotAfterSet is an UPDATE slot after the SET list: FROM sources.
	SlotAfterSet
	// SlotAfterTarget sits after the target table of an INSERT, UPDATE or
	// DELETE and before whatever follows it: DELETE's USING sources, and the
	// place T-SQL puts a table hint such as WITH (TABLOCK).
	SlotAfterTarget
)

// SlotValues lists every slot.
var SlotValues = []Slot{
	SlotAfterSelect, SlotAfterColumns, SlotAfterFrom, SlotBeforeWhere, SlotAfterWhere,
	SlotAfterGroupBy, SlotAfterHaving, SlotBeforeOrder, SlotAfterOrder, SlotAfterOffset,
	SlotTail,
	SlotAfterInsert, SlotAfterVerb, SlotAfterTarget, SlotBeforeValues, SlotInsertBody,
	SlotAfterSet,
}

// slotNames is the canonical spelling of each slot, for the error a statement
// returns when a clause asks for a slot that statement does not render.
var slotNames = map[Slot]string{
	SlotAfterSelect:  "SlotAfterSelect",
	SlotAfterColumns: "SlotAfterColumns",
	SlotAfterWhere:   "SlotAfterWhere",
	SlotAfterHaving:  "SlotAfterHaving",
	SlotAfterVerb:    "SlotAfterVerb",
	SlotAfterFrom:    "SlotAfterFrom",
	SlotBeforeWhere:  "SlotBeforeWhere",
	SlotAfterGroupBy: "SlotAfterGroupBy",
	SlotBeforeOrder:  "SlotBeforeOrder",
	SlotAfterOrder:   "SlotAfterOrder",
	SlotAfterOffset:  "SlotAfterOffset",
	SlotTail:         "SlotTail",
	SlotAfterInsert:  "SlotAfterInsert",
	SlotBeforeValues: "SlotBeforeValues",
	SlotInsertBody:   "SlotInsertBody",
	SlotAfterSet:     "SlotAfterSet",
	SlotAfterTarget:  "SlotAfterTarget",
}

// String names the slot, so an error can say which position was asked for.
func (s Slot) String() string {
	if n, ok := slotNames[s]; ok {
		return n
	}
	return "Slot(" + strconv.Itoa(int(s)) + ")"
}

// AnyClause is a statement part that is neither an expression nor a predicate,
// with no dialect or kind in its type: what a Join carries for a per-table
// hint, and what every typed Clause also satisfies.
// It is an open interface, like Predicate, so an engine package can add one
// without a method per clause on Select. A clause must call Args.Unsupported
// when rendered through a dialect it does not belong to.
type AnyClause interface {
	Slot() Slot
	WriteSQL(b *strings.Builder, args *Args)
}

// Clause is a clause typed to the dialect D it belongs to and the statement
// kind K it may appear in.
//
// Both parameters are phantom, and the marker method is what makes them bite:
// an interface whose type parameters appeared nowhere in its method set would
// be satisfied by every clause for every instantiation, and the check would be
// decoration. Because ClauseOf names D and K, a value built for Postgres and
// SELECT satisfies exactly Clause[postgres.Dialect, SelectKind], so a Select
// for SQLite refuses it at compile time and an Update for Postgres refuses it
// too. The method does nothing; its signature is the whole point.
//
// EngineClause is how one is made. A package outside this module builds
// clauses for its own dialect through the same factory, and a statement of
// its own declares its own kind beside it: the kind set is open like the
// dialect set, and nothing engine-specific enters this package's list.
type Clause[D render.Dialect, K any] interface {
	AnyClause
	ClauseOf(D, K)
}

// The statement kinds a clause may be typed to. They are empty structs rather
// than an enum because they are used only as type arguments.
type (
	// SelectKind marks a clause for SELECT.
	SelectKind struct{}
	// InsertKind marks a clause for INSERT.
	InsertKind struct{}
	// UpdateKind marks a clause for UPDATE.
	UpdateKind struct{}
	// DeleteKind marks a clause for DELETE.
	DeleteKind struct{}
)
