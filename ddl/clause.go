package ddl

import (
	"strings"

	"github.com/ubgo/sqlb/render"
)

// Position is where in a CREATE TABLE an engine clause renders.
//
// DDL has its own positions rather than stmt's slots. A slot is an int from
// stmt's own iota, and every one of them is walked by stmt's completeness
// tests; a DDL position minted from the same space could collide with one
// silently. A DDL statement renders each of these, and refuses a clause for
// any other, so a clause cannot vanish from the statement.
type Position int

// The positions a CREATE TABLE renders.
const (
	// PositionModifier is between CREATE and TABLE: UNLOGGED, TEMP.
	PositionModifier Position = iota + 1
	// PositionElement is inside the parentheses, after the columns and the
	// universal constraints: a primary key, a unique or foreign key, EXCLUDE.
	PositionElement
	// PositionOption is after the closing parenthesis: PARTITION BY, STRICT,
	// WITHOUT ROWID.
	PositionOption
	// PositionColumn is inside one column's definition. Only a ColumnClause,
	// which names its column, may use it.
	PositionColumn
)

// PositionValues is every position a CREATE TABLE renders, in order, so a
// test can check each is written somewhere.
var PositionValues = []Position{PositionModifier, PositionElement, PositionOption, PositionColumn}

// TableClause is an engine's clause for CREATE TABLE, typed to its dialect:
// a Postgres clause on a SQLite CREATE TABLE does not compile, exactly as a
// Postgres clause on a SQLite SELECT does not. TableClauseOf is the marker
// that makes the dialect parameter bite.
type TableClause[D render.Dialect] interface {
	Position() Position
	WriteSQL(b *strings.Builder, a *render.Args)
	TableClauseOf(D)
}

// AlterClause is an engine's ALTER TABLE action, typed to its dialect: a
// Postgres SET NOT NULL on a SQLite ALTER TABLE does not compile.
type AlterClause[D render.Dialect] interface {
	WriteSQL(b *strings.Builder, a *render.Args)
	// Standalone reports whether the action must be a statement of its own,
	// as RENAME TO, SET SCHEMA and ATTACH PARTITION must on Postgres.
	Standalone() bool
	AlterClauseOf(D)
}

// DropClause is an engine's option after DROP TABLE's table name, typed to its
// dialect: CASCADE is Postgres's, and SQLite has none.
type DropClause[D render.Dialect] interface {
	WriteSQL(b *strings.Builder, a *render.Args)
	DropClauseOf(D)
}

// NewTableClause makes a TableClause for an engine package. The render
// function writes no leading space; the statement supplies separators.
//
// A clause made here at PositionOption carries no OptionRank, so it renders
// exactly where the caller placed it. An engine package makes its options
// with NewOptionClause instead, which is what lets the statement put them in
// the order the engine's grammar requires.
func NewTableClause[D render.Dialect](pos Position, render func(a *render.Args) string) TableClause[D] {
	return tableClause[D]{pos: pos, render: render}
}

// OptionRank is where an engine's grammar places one clause among the clauses
// sharing its position: an option after the closing parenthesis of CREATE
// TABLE, or a column clause within one part of a column's definition. Lower
// renders first. Zero means unranked.
//
// It covers column clauses as well because the same bug existed one level
// down, found by the test written for the table options: passing Postgres's
// STORAGE, COMPRESSION and COLLATE in reverse rendered them in reverse, and
// Postgres answers every order but that one with a syntax error. One
// mechanism for both, rather than a second, because the rule is the same —
// the engine knows the order, so the statement writes it.
//
// # Why this exists
//
// Every clause at PositionOption used to render in the order the caller
// passed it, and two engines fix that order in their grammar. Measured on
// 2026-09-24: Postgres 17 takes INHERITS, PARTITION BY, USING, WITH, ON
// COMMIT, TABLESPACE in exactly that order and answers `syntax error at or
// near "USING"` to `WITH (fillfactor=70) USING heap`; MySQL 8.4 takes its
// table options in any order but PARTITION BY only last, answering
// ER_PARSE_ERROR to `PARTITION BY HASH(id) PARTITIONS 2 ENGINE=InnoDB`. So a
// caller who passed two correct clauses in the wrong order got a clean Build
// and a server error about SQL they never wrote — the failure this module
// exists to prevent. SQLite's STRICT and WITHOUT ROWID are accepted either
// way round, so SQLite ranks nothing.
//
// # Why sort, rather than refuse or leave it
//
// Three answers were considered. Leaving it — documented, and pinned by a
// test that passes the options in the right order — is honest but keeps the
// wrong order writable, and a test in the right order proves nothing about a
// caller in the wrong one. Refusing at Build would name the mistake, but it
// makes the caller reproduce an order the library already knows: there is
// exactly one legal order, so there is no choice to get wrong, only busywork
// to demand. Refusing is right when the caller holds information the library
// does not; here the library holds all of it. So the statement sorts.
//
// # Why the rank is the engine's and the sort is ddl's
//
// Which option comes first is a fact about one engine's grammar, so the
// ranks live in pg and mysql, each in one table beside its option
// constructors. ddl knows only that options may carry a rank and that ranked
// options are written in rank order. That keeps ddl free of any engine's
// grammar, which is the rule every other layer here follows.
//
// # What happens to an unranked option
//
// It stays exactly where the caller put it, and only the ranked options move,
// each into a position a ranked option already occupied. The statement
// reorders only what it understands: an option from outside this module,
// made with NewTableClause, may be anything, and no default place for it is
// right in every case — first breaks a raw TABLESPACE on Postgres, last
// breaks a raw table option before MySQL's partitioning. Keeping the caller's
// placement is the one choice that never makes a correct statement wrong.
// Every option the engine packages ship is ranked, and a test in each fails on
// one that is not, so the unranked case is only ever a caller's own clause.
type OptionRank int

// OptionRanker is implemented by an option that knows where its engine's
// grammar places it. A clause made by NewOptionClause or NewOptionClauseOn
// implements it; a clause returning zero is treated as unranked.
type OptionRanker interface {
	OptionRank() OptionRank
}

// NewOptionClause makes a TableClause at PositionOption that renders at rank
// in the order its engine's grammar requires, whatever order the caller
// passed it in. See OptionRank for why the statement sorts rather than
// refusing an order it could fix.
func NewOptionClause[D render.Dialect](rank OptionRank, render func(a *render.Args) string) TableClause[D] {
	return tableClause[D]{pos: PositionOption, rank: rank, render: render}
}

// NewAlterClause makes an AlterClause for an engine package, for an action
// that may share a statement with others.
func NewAlterClause[D render.Dialect](render func(a *render.Args) string) AlterClause[D] {
	return alterClause[D]{render: render}
}

// NewStandaloneAlterClause makes an AlterClause for an action that must be a
// statement of its own: in Postgres's grammar RENAME TO, SET SCHEMA and
// ATTACH or DETACH PARTITION are top-level forms, not joinable actions.
func NewStandaloneAlterClause[D render.Dialect](render func(a *render.Args) string) AlterClause[D] {
	return alterClause[D]{render: render, standalone: true}
}

// NewDropClause makes a DropClause for an engine package.
func NewDropClause[D render.Dialect](render func(a *render.Args) string) DropClause[D] {
	return dropClause[D]{render: render}
}

type tableClause[D render.Dialect] struct {
	pos    Position
	rank   OptionRank
	render func(a *render.Args) string
}

func (c tableClause[D]) Position() Position                          { return c.pos }
func (c tableClause[D]) OptionRank() OptionRank                      { return c.rank }
func (c tableClause[D]) WriteSQL(b *strings.Builder, a *render.Args) { b.WriteString(c.render(a)) }

// TableClauseOf, and the AlterClauseOf and DropClauseOf below, are never
// called and never can be: each exists so the dialect parameter appears in a
// method, which is what makes the interface refuse another engine's clause at
// compile time. An empty body with no statements is the whole implementation.
func (tableClause[D]) TableClauseOf(D) {}

type alterClause[D render.Dialect] struct {
	render     func(a *render.Args) string
	standalone bool
}

func (c alterClause[D]) WriteSQL(b *strings.Builder, a *render.Args) { b.WriteString(c.render(a)) }
func (c alterClause[D]) Standalone() bool                            { return c.standalone }
func (alterClause[D]) AlterClauseOf(D)                               {}

type dropClause[D render.Dialect] struct{ render func(a *render.Args) string }

func (c dropClause[D]) WriteSQL(b *strings.Builder, a *render.Args) { b.WriteString(c.render(a)) }
func (dropClause[D]) DropClauseOf(D)                                {}
