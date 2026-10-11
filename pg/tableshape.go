package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The forms of CREATE TABLE, CREATE INDEX, CREATE VIEW, CREATE MATERIALIZED
// VIEW and CREATE TRIGGER that the detector's "built" hid.
//
// One rule runs through all of them and is worth stating once, because it is
// the thing that makes an option here different from an option in a query:
// Postgres's grammar fixes the ORDER of these, and the statement renders them
// in the order the caller gave. WITH ( ... ) USING heap is a syntax error
// where USING heap WITH ( ... ) is not, measured on 17.10, and the same holds
// for a column's STORAGE before its COLLATE. Each clause below says where it
// goes, and TestTableShape_OptionsRenderInGrammarOrder pins the whole order in
// one statement so the documented sequence is a test rather than a claim.
//
// A table's options are the one place this package writes a value it cannot
// check: a storage parameter's name comes from the access method or from an
// extension, so it is an identifier written by the dialect and nothing here
// knows whether the server has one of that name.

const (
	kwTableObject   = "TABLE"
	kwTruncateEvent = "TRUNCATE"
	kwInherits      = "INHERITS"
	kwUsingMethod   = "USING"
	kwOnCommit      = "ON COMMIT"
	kwStorage       = "STORAGE"
	kwCompression   = "COMPRESSION"
	kwMatch         = "MATCH"
	kwIncludeCols   = "INCLUDE"
	kwRecursive     = "RECURSIVE"
	kwTempView      = "TEMPORARY"
	kwConstraintTr  = "CONSTRAINT"
	kwInsteadOf     = "INSTEAD OF"
	kwFromTable     = "FROM"
	kwReferencing   = "REFERENCING"
	kwOldTable      = "OLD TABLE AS"
	kwNewTable      = "NEW TABLE AS"
)

// ErrRecursiveColumns is returned for a recursive view with no column list,
// which Postgres refuses: the recursive reference in the body needs names
// before the body has produced any.
var ErrRecursiveColumns = errors.New("sqlb/pg: a recursive view needs a column list")

// OnCommitAction is what happens to a temporary table's rows when the
// transaction that created it commits.
type OnCommitAction int

// The actions. PreserveRows is the default and keeps the table for the
// session; DeleteRows empties it at every commit, which is what makes a
// temporary table usable as a scratch table across a session; DropAtCommit
// removes it entirely, which is what makes a table that exists for one
// transaction possible at all.
const (
	PreserveRows OnCommitAction = iota + 1
	DeleteRows
	DropAtCommit
)

func (o OnCommitAction) keyword() (string, bool) {
	switch o {
	case PreserveRows:
		return "PRESERVE ROWS", true
	case DeleteRows:
		return "DELETE ROWS", true
	case DropAtCommit:
		return "DROP", true
	}
	return "", false
}

// Temporary makes the table live in the session's temporary schema and vanish
// when the session ends. Postgres also accepts GLOBAL and LOCAL before it,
// which it documents as accepted for compatibility and ignores, so neither is
// offered: a keyword that changes nothing is a keyword that misleads.
//
// It goes between CREATE and TABLE, beside Unlogged, and the two are mutually
// exclusive.
func Temporary() TableClause {
	return ddl.NewTableClause[Dialect](ddl.PositionModifier, func(*sqlb.Args) string { return kwTemporary })
}

// Postgres's CREATE TABLE options, in the one order its grammar accepts.
//
// This is the only place the order is written. Each option constructor takes
// its rank from here, and ddl sorts a statement's ranked options by it, so a
// caller may pass them in any order and get the order below. Measured on
// Postgres 17 on 2026-09-24: every pair out of this order is a syntax error —
// `WITH (fillfactor=70) USING heap` answers `syntax error at or near
// "USING"`, `ON COMMIT DELETE ROWS WITH (...)` at "WITH", and `USING heap
// INHERITS (p)` at "INHERITS". See ddl.OptionRank for why the statement sorts
// rather than refusing an order it knows how to fix.
//
// TABLESPACE, which comes after ON COMMIT, is not built: a tablespace names a
// directory on a machine this code has never seen. A raw one made with
// ddl.NewTableClause is unranked, so it stays where the caller writes it,
// which should be last.
// The order Postgres's grammar takes a column's clauses in, straight after its
// type: STORAGE, then COMPRESSION, then COLLATE. Measured on 17 on
// 2026-09-24 — every other order is a syntax error, `COLLATE "C" STORAGE
// external` answering `syntax error at or near "STORAGE"`. It lives here with
// the table options' ranks because it is the same fact about the same
// grammar, sorted by the same mechanism in ddl. It was found by the test
// written for the table options: passing those in reverse also passed the
// column clauses in reverse, and they came out reversed.
const (
	colRankStorage ddl.OptionRank = iota + 1
	colRankCompression
	colRankCollate
)

const (
	rankInherits ddl.OptionRank = iota + 1
	rankPartitionBy
	rankAccessMethod
	rankStorage
	rankOnCommit
)

// OnCommit says what happens to a temporary table's rows at commit. Postgres
// refuses it on an ordinary table with "ON COMMIT can only be used on
// temporary tables", so it belongs beside Temporary. It renders last of the
// table's options wherever the caller places it; see the rank table above.
func OnCommit(o OnCommitAction) TableClause {
	return ddl.NewOptionClause[Dialect](rankOnCommit, func(a *sqlb.Args) string {
		kw, ok := o.keyword()
		return kwOnCommit + " " + keywordOf(a, kw, ok, "OnCommit")
	})
}

// Inherits makes the new table a child of one or more parents, so a query on a
// parent also reads this table's rows. It is the CREATE TABLE half of Inherit,
// and the same advice applies: partitioning replaces it for a new schema. It
// renders first of the table's options, before PARTITION BY.
func Inherits(first sqlb.Table, more ...sqlb.Table) TableClause {
	parents := append([]sqlb.Table{first}, more...)
	return ddl.NewOptionClause[Dialect](rankInherits, func(a *sqlb.Args) string {
		names := make([]string, len(parents))
		for i, p := range parents {
			if p.Name == "" {
				a.Fail(ErrEmptyName)
				return ""
			}
			names[i] = p.Qualified(a.Dialect())
		}
		return kwInherits + " (" + strings.Join(names, ", ") + ")"
	})
}

// TableAccessMethod stores the table's rows in an access method other than the
// server's default. It renders after PARTITION BY and before the storage
// parameters.
func TableAccessMethod(m AccessMethod) TableClause {
	return ddl.NewOptionClause[Dialect](rankAccessMethod, func(a *sqlb.Args) string {
		if m == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwUsingMethod + " " + a.Dialect().QuoteIdent(string(m))
	})
}

// TableStorage sets the table's storage parameters at creation, the same
// open set AlterIndex and AlterMaterializedView take. It renders after the
// access method and before ON COMMIT.
func TableStorage(first StorageParameter, more ...StorageParameter) TableClause {
	ps := append([]StorageParameter{first}, more...)
	return ddl.NewOptionClause[Dialect](rankStorage, func(a *sqlb.Args) string {
		return writeWithParameters(a, ps)
	})
}

// ColumnStorage says what the server may do with one column's value when it is
// too wide for its row: the column-level counterpart of the type's own
// strategy. It renders immediately after the type and BEFORE a collation,
// which Postgres's grammar requires and which is a syntax error the other way
// round.
//
// On a column inherited through Inherits the parent's strategy wins and this
// one is dropped in silence, measured on 17.10: the same column reads extended
// under INHERITS and external without it. Set it on the parent.
func ColumnStorage[T any](col sqlb.Expr[T], s TypeStorage) ddl.ColumnClause[Dialect] {
	return ddl.NewRankedColumnClause[Dialect](colRankStorage, col, ddl.PartAfterType, func(a *sqlb.Args) string {
		kw, ok := s.keyword()
		return kwStorage + " " + keywordOf(a, kw, ok, "ColumnStorage")
	})
}

// ColumnCompression names the algorithm a variable-length column's values are
// compressed with, pglz or lz4 where the server was built with it. It renders
// after the storage strategy and before a collation.
func ColumnCompression[T any](col sqlb.Expr[T], method string) ddl.ColumnClause[Dialect] {
	return ddl.NewRankedColumnClause[Dialect](colRankCompression, col, ddl.PartAfterType, func(a *sqlb.Args) string {
		if method == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwCompression + " " + a.Dialect().QuoteIdent(method)
	})
}

// MatchType is how a composite foreign key treats a row with some columns NULL
// and some not.
type MatchType int

// The match types. MatchSimple, the default, allows the reference when any
// column is NULL; MatchFull allows it only when all are NULL or none is, which
// is what a key that must be wholly present or wholly absent needs. Postgres
// parses MatchPartial and refuses to implement it, which is why it is not
// offered here: a constructor for a form the server rejects is a compile-time
// promise the server breaks.
const (
	MatchFull MatchType = iota + 1
	MatchSimple
)

func (m MatchType) keyword() (string, bool) {
	switch m {
	case MatchFull:
		return "FULL", true
	case MatchSimple:
		return "SIMPLE", true
	}
	return "", false
}

// Match says how the key treats a partly NULL row. It renders after the
// referenced columns and before ON DELETE.
func (f ForeignKeyClause) Match(m MatchType) ForeignKeyClause { f.match = m; return f }

// Include adds columns to the key's index without making them part of the key,
// so an index-only scan can read them. They are not compared for uniqueness,
// which is the whole point: a unique key on one column that also carries the
// two columns every read of it wants.
func (k PrimaryKeyClause) Include(first ddl.ColumnRef, more ...ddl.ColumnRef) PrimaryKeyClause {
	k.include = append(slices.Clone(k.include), append([]ddl.ColumnRef{first}, more...)...)
	return k
}

// With sets the storage parameters of the index behind the key.
func (k PrimaryKeyClause) With(first StorageParameter, more ...StorageParameter) PrimaryKeyClause {
	k.with = append(slices.Clone(k.with), append([]StorageParameter{first}, more...)...)
	return k
}

// Include adds covering columns to the constraint's index.
func (u UniqueClause) Include(first ddl.ColumnRef, more ...ddl.ColumnRef) UniqueClause {
	u.include = append(slices.Clone(u.include), append([]ddl.ColumnRef{first}, more...)...)
	return u
}

// With sets the storage parameters of the index behind the constraint.
func (u UniqueClause) With(first StorageParameter, more ...StorageParameter) UniqueClause {
	u.with = append(slices.Clone(u.with), append([]StorageParameter{first}, more...)...)
	return u
}

// Include adds covering columns to the exclusion constraint's index.
func (x ExcludeClause) Include(first ddl.ColumnRef, more ...ddl.ColumnRef) ExcludeClause {
	x.elems = slices.Clone(x.elems)
	x.include = append(slices.Clone(x.include), append([]ddl.ColumnRef{first}, more...)...)
	return x
}

// With sets the storage parameters of the index behind the constraint.
func (x ExcludeClause) With(first StorageParameter, more ...StorageParameter) ExcludeClause {
	x.elems = slices.Clone(x.elems)
	x.with = append(slices.Clone(x.with), append([]StorageParameter{first}, more...)...)
	return x
}

// NoInherit keeps the check on this table alone, so a child table created by
// Inherits does not get it. It means nothing on a table nothing inherits from,
// and it is the one column-level difference inheritance makes to a constraint.
func (c CheckClause) NoInherit() CheckClause { c.noInherit = true; return c }

// writeIndexParameters renders the INCLUDE and WITH a key constraint's index
// takes, in the order Postgres's grammar fixes.
func writeIndexParameters(a *sqlb.Args, include []ddl.ColumnRef, with []StorageParameter) string {
	out := ""
	if len(include) > 0 {
		out += " " + kwIncludeCols + " " + quoteCols(a, include)
	}
	if len(with) > 0 {
		out += " " + writeWithParameters(a, with)
	}
	return out
}

// parameterList renders storage parameters as assignments, for a position that
// writes its own WITH.
func parameterList(a *sqlb.Args, ps []StorageParameter) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.write(a)
	}
	return out
}

// OnOnly creates the index on the named table alone rather than on its
// partitions, which is the first half of building a partitioned index without
// a long lock: the parent index is created invalid, each partition's index is
// built separately, and AlterIndex's AttachPartition makes the parent valid as
// the last one is attached.
func (x *CreateIndexReady) OnOnly() *CreateIndexReady {
	c := x.core.derive()
	c.onlyTable = true
	return &CreateIndexReady{core: c}
}

// OpclassWith gives the key an operator class and the parameters that class
// takes, which is how an extension's index is tuned: pgvector's HNSW takes its
// graph parameters here, and nothing else can pass them.
func (k IndexKeyElem[T]) OpclassWith(o OpClass[T], first StorageParameter, more ...StorageParameter) IndexKeyElem[T] {
	k.opclass, k.opclassSet = o.name, true
	k.opclassParams = append([]StorageParameter{first}, more...)
	return k
}

// Columns names the view's output columns, rather than taking the names its
// body's select list happens to produce. It is what makes a view's interface a
// declaration: the body can then be rewritten with OrReplace without the
// column names moving under a caller.
func (x *CreateViewStatement) Columns(first string, more ...string) *CreateViewStatement {
	c := *x
	c.columns = append(slices.Clone(x.columns), append([]string{first}, more...)...)
	return &c
}

// Temporary makes the view live in the session's temporary schema.
func (x *CreateViewStatement) Temporary() *CreateViewStatement {
	c := *x
	c.columns = slices.Clone(x.columns)
	c.temporary = true
	return &c
}

// Recursive makes the view refer to itself, the form whose body is a UNION of
// a seed and a step that selects from the view. Postgres requires the column
// list, which is why Build refuses a recursive view without one: the recursive
// reference needs names before the body has produced any.
func (x *CreateViewStatement) Recursive() *CreateViewStatement {
	c := *x
	c.columns = slices.Clone(x.columns)
	c.recursive = true
	return &c
}

// WithOptions sets the view's options at creation, which is the difference
// between a view that is security-invoker from its first moment and one that
// is created and then altered. A library shipping a view over its own
// row-level-secured tables wants SecurityInvoker on, and the window between
// the two statements is a window in which it is off.
func (x *CreateViewStatement) WithOptions(first ViewSetting, more ...ViewSetting) *CreateViewStatement {
	c := *x
	c.columns = slices.Clone(x.columns)
	c.options = append(slices.Clone(x.options), append([]ViewSetting{first}, more...)...)
	return &c
}

// Columns names the materialized view's output columns.
func (x *CreateMaterializedViewStatement) Columns(first string, more ...string) *CreateMaterializedViewStatement {
	c := *x
	c.columns = append(slices.Clone(x.columns), append([]string{first}, more...)...)
	return &c
}

// Using stores the view's rows in an access method other than the default.
func (x *CreateMaterializedViewStatement) Using(m AccessMethod) *CreateMaterializedViewStatement {
	c := *x
	c.columns = slices.Clone(x.columns)
	c.method = m
	return &c
}

// WithStorage sets the storage parameters of the table behind the view, which
// a view refreshed in full wants set differently from a table written a row at
// a time.
func (x *CreateMaterializedViewStatement) WithStorage(first StorageParameter, more ...StorageParameter) *CreateMaterializedViewStatement {
	c := *x
	c.columns = slices.Clone(x.columns)
	c.with = append(slices.Clone(x.with), append([]StorageParameter{first}, more...)...)
	return &c
}

// writeColumnList renders a parenthesised list of output column names, empty
// when there are none.
func writeColumnList(a *sqlb.Args, names []string) string {
	if len(names) == 0 {
		return ""
	}
	d := a.Dialect()
	out := make([]string, len(names))
	for i, n := range names {
		if n == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		out[i] = d.QuoteIdent(n)
	}
	return " (" + strings.Join(out, ", ") + ")"
}

// OnTruncate fires on TRUNCATE. It is a statement trigger only — there are no
// rows to see, which is why Postgres refuses it FOR EACH ROW — and it is the
// one event a row trigger cannot have.
func OnTruncate() TriggerEvent { return TriggerEvent{kw: kwTruncateEvent} }

// Constraint makes the trigger a constraint trigger, which can be deferred to
// the end of the transaction with Deferrable. It is how a rule that spans
// several rows is enforced without refusing each intermediate state: a check
// that runs at commit sees the finished work rather than the half of it the
// first statement wrote.
func (x *CreateTriggerQuery) Constraint() *CreateTriggerQuery {
	c := x.core.derive()
	c.constraint = true
	return &CreateTriggerQuery{core: c}
}

// InsteadOf replaces the statement on a view with the trigger's own work,
// which is how a view that Postgres cannot update automatically is made
// writable. It is a row trigger on a view and nothing else, which the server
// checks.
func (x *CreateTriggerQuery) InsteadOf(first TriggerEvent, more ...TriggerEvent) *TimedTriggerQuery {
	return x.timed(kwInsteadOf, first, more)
}

// From names the table a constraint trigger's other side is on, which is what
// a foreign-key-shaped constraint trigger points at. It has meaning only on a
// constraint trigger, which the server enforces.
func (x *TimedTriggerQuery) From(t sqlb.Table) *TimedTriggerQuery {
	c := x.core.derive()
	c.from = t
	return &TimedTriggerQuery{core: c}
}

// Deferrable makes a constraint trigger deferrable, checked when d says.
func (x *TimedTriggerQuery) Deferrable(d Deferral) *TimedTriggerQuery {
	c := x.core.derive()
	c.deferral = d
	return &TimedTriggerQuery{core: c}
}

// Referencing gives a statement trigger the rows the statement changed, as two
// transition tables its function can query. Without them a statement trigger
// knows only that something happened, which is why a per-statement audit had
// to be a row trigger and pay for a call per row.
//
// Both names are optional: an INSERT has no old rows and a DELETE no new ones,
// and Postgres refuses the table that does not apply to the events declared.
func (x *TimedTriggerQuery) Referencing(oldTable, newTable string) *TimedTriggerQuery {
	c := x.core.derive()
	c.oldTable, c.newTable = oldTable, newTable
	return &TimedTriggerQuery{core: c}
}

// ExecuteWith names the trigger function and the constant arguments Postgres
// passes it, which every trigger function reads out of TG_ARGV rather than as
// parameters — which is why the Function still declares none. The arguments
// are written as literals by the dialect, and they are the author's text, not
// a value from a request.
func (x *TimedTriggerQuery) ExecuteWith(f Function, args ...string) *CreateTriggerStatement {
	s := x.Execute(f)
	s.args = slices.Clone(args)
	return s
}

// CreateTableAsStatement is CREATE TABLE ... AS, which makes a table and fills
// it from a query in one statement.
type CreateTableAsStatement struct {
	t                   sqlb.Table
	body                *SelectQuery
	columns             []string
	method              AccessMethod
	with                []StorageParameter
	onCommit            OnCommitAction
	ifNotExists, noData bool
	temporary, unlogged bool
}

// CreateTableAs makes a table whose columns come from a query's select list
// and fills it with that query's rows. It is a statement of its own rather
// than a form of CreateTable because the two declare their columns in
// different places: CreateTable takes Col values the queries also use, and
// this takes none at all, so nothing here can be checked against a
// declaration. That is the trade, and the reason to reach for it is the case
// where there is nothing to declare — a summary table rebuilt from a query
// that is already written.
//
// It was classified as the operator's until 2026-09-24, on the reading that a
// table without a declaration is not a library's. SQLite and MySQL both build
// it, which made the reading a difference between engines rather than a
// decision, and this is the statement a library rebuilding its own derived
// table writes.
func CreateTableAs(t sqlb.Table, body *SelectQuery) *CreateTableAsStatement {
	return &CreateTableAsStatement{t: t, body: body}
}

// derive returns a copy whose slices are its own.
func (x *CreateTableAsStatement) derive() *CreateTableAsStatement {
	c := *x
	c.columns = slices.Clone(x.columns)
	c.with = slices.Clone(x.with)
	return &c
}

// IfNotExists makes creating an existing table a no-op, and on this form that
// means the query does not run either: the table keeps the rows it had and
// nothing is appended.
func (x *CreateTableAsStatement) IfNotExists() *CreateTableAsStatement {
	c := x.derive()
	c.ifNotExists = true
	return c
}

// Columns names the new table's columns rather than taking the names the
// query's select list produces.
func (x *CreateTableAsStatement) Columns(first string, more ...string) *CreateTableAsStatement {
	c := x.derive()
	c.columns = append(c.columns, append([]string{first}, more...)...)
	return c
}

// Using stores the new table's rows in another access method.
func (x *CreateTableAsStatement) Using(m AccessMethod) *CreateTableAsStatement {
	c := x.derive()
	c.method = m
	return c
}

// WithStorage sets the new table's storage parameters.
func (x *CreateTableAsStatement) WithStorage(first StorageParameter, more ...StorageParameter) *CreateTableAsStatement {
	c := x.derive()
	c.with = append(c.with, append([]StorageParameter{first}, more...)...)
	return c
}

// Temporary makes the table live in the session's temporary schema.
func (x *CreateTableAsStatement) Temporary() *CreateTableAsStatement {
	c := x.derive()
	c.temporary, c.unlogged = true, false
	return c
}

// Unlogged keeps the table out of the write-ahead log, which is faster and
// loses the table's contents after a crash. It is what a table rebuilt from a
// query on every boot should be.
func (x *CreateTableAsStatement) Unlogged() *CreateTableAsStatement {
	c := x.derive()
	c.unlogged, c.temporary = true, false
	return c
}

// OnCommit says what happens to a temporary table's rows at commit.
func (x *CreateTableAsStatement) OnCommit(o OnCommitAction) *CreateTableAsStatement {
	c := x.derive()
	c.onCommit = o
	return c
}

// WithNoData creates the table with the query's column types and none of its
// rows, which is how the shape is made now and filled later.
func (x *CreateTableAsStatement) WithNoData() *CreateTableAsStatement {
	c := x.derive()
	c.noData = true
	return c
}

// Build renders the statement.
func (x *CreateTableAsStatement) Build() (ddl.Script, error) {
	if x.body == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.t.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		head := kwCreate
		if x.temporary {
			head += " " + kwTemporary
		}
		if x.unlogged {
			head += " " + kwUnloggedSequence
		}
		s := head + " " + kwTableObject + ifNotExists(x.ifNotExists) + " " + x.t.Qualified(d) + writeColumnList(a, x.columns)
		if x.method != "" {
			s += " " + kwUsingMethod + " " + d.QuoteIdent(string(x.method))
		}
		if len(x.with) > 0 {
			s += " " + writeWithParameters(a, x.with)
		}
		if x.onCommit != 0 {
			kw, ok := x.onCommit.keyword()
			s += " " + kwOnCommit + " " + keywordOf(a, kw, ok, "OnCommit")
		}
		s += " " + kwAs + " " + renderBody(a, x.body)
		if x.noData {
			s += " " + kwWithNoData
		}
		return s
	})
}

// SetColumnStorage changes an existing column's storage strategy, the ALTER
// half of ColumnStorage. It was the operator's until the second pass noticed
// the asymmetry: a strategy that can be declared at CREATE and not changed
// afterwards is a declaration a migration cannot follow. It rewrites nothing,
// so it applies to values written from now on and leaves the ones already
// stored as they are.
func SetColumnStorage[T any](col sqlb.Expr[T], s TypeStorage) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string {
		kw, ok := s.keyword()
		return kwSet + " " + kwStorage + " " + keywordOf(a, kw, ok, "SetColumnStorage")
	})
}

// SetColumnCompression changes an existing column's compression algorithm, the
// ALTER half of ColumnCompression, and like it applies to values written from
// now on.
func SetColumnCompression[T any](col sqlb.Expr[T], method string) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string {
		if method == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwSet + " " + kwCompression + " " + a.Dialect().QuoteIdent(method)
	})
}
