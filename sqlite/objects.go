package sqlite

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The SQLite statements around a table: indexes, views and triggers. Each has
// a Build that returns a ddl.Script and every method returns a derived
// statement. The PRAGMA statements are in pragma.go and the virtual tables in
// virtual.go, fts.go and rtree.go.

const (
	kwCreateIndex       = "CREATE INDEX"
	kwCreateUniqueIndex = "CREATE UNIQUE INDEX"
	kwDropIndex         = "DROP INDEX"
	kwCreate            = "CREATE"
	kwView              = "VIEW"
	kwTrigger           = "TRIGGER"
	kwDropView          = "DROP VIEW"
	kwDropTrigger       = "DROP TRIGGER"
	kwIfNotExists       = "IF NOT EXISTS"
	kwIfExists          = "IF EXISTS"
	kwOn                = "ON"
	kwAs                = "AS"
	kwWhere             = "WHERE"
	kwDesc              = "DESC"
	kwAsc               = "ASC"
	kwBefore            = "BEFORE"
	kwAfter             = "AFTER"
	kwInsteadOf         = "INSTEAD OF"
	kwForEachRow        = "FOR EACH ROW"
	kwWhen              = "WHEN"
	kwBegin             = "BEGIN"
	kwEnd               = "END"
	kwOf                = "OF"
	kwOld               = "OLD"
	kwNew               = "NEW"
	kwPragma            = "PRAGMA"
	kwRaise             = "RAISE"
	// schemaTemp is the one schema a temporary object may name, because
	// SQLite puts every one of them there.
	schemaTemp = "temp"
)

// ErrEmptyName is returned for an object value with no name.
var ErrEmptyName = errors.New("sqlb/sqlite: object has no name")

// ErrTemporaryQualified is a temporary object whose name is qualified by a
// schema other than temp.
//
// SQLite refuses it: "temporary table name must be unqualified", measured on
// 3.50.3. A temporary object always lives in the temp schema, so naming
// another one is a contradiction rather than a placement, and Build says so
// instead of letting the engine.
var ErrTemporaryQualified = errors.New("sqlb/sqlite: a temporary object cannot name a schema other than temp")

// qualified renders an object name, qualified by its schema when it declares
// one. SQLite's schemas are main, temp and whatever ATTACH DATABASE opened,
// so a name with no schema is the ordinary case and means main.
func qualified(a *sqlb.Args, schema, name string) string {
	d := a.Dialect()
	if schema != "" {
		return d.QuoteIdent(schema) + "." + d.QuoteIdent(name)
	}
	return d.QuoteIdent(name)
}

// temporaryPrefix writes TEMP between CREATE and the object keyword, and
// refuses a schema the engine would refuse with it.
func temporaryPrefix(a *sqlb.Args, temporary bool, schema string) string {
	if !temporary {
		return ""
	}
	if schema != "" && schema != schemaTemp {
		a.Fail(ErrTemporaryQualified)
	}
	return " " + kwTemporary
}

func ifNotExists(on bool) string {
	if on {
		return " " + kwIfNotExists
	}
	return ""
}

func ifExists(on bool) string {
	if on {
		return " " + kwIfExists
	}
	return ""
}

// buildOne renders one statement through inline Args, so every value in it is
// a literal, and fails closed on an empty name.
func buildOne(name string, render func(a *sqlb.Args) string) (ddl.Script, error) {
	if name == "" {
		return nil, ErrEmptyName
	}
	a := sqlb.NewInlineArgs(Dialect{})
	sql := render(a)
	if err := a.Err(); err != nil {
		return nil, err
	}
	return ddl.Script{{SQL: sql, Transactional: true}}, nil
}

// onTable reports whether a column belongs to t, by schema and name.
func onTable(r ddl.ColumnRef, t sqlb.Table) bool {
	return r.Table().Name == t.Name && r.Table().Schema == t.Schema
}

// Index is a SQLite index, declared once as a value.
//
// Schema names the database the index is created in, empty for main. It is
// the index that is qualified and never the table: SQLite requires an index
// and its table to be in the same schema, so CREATE INDEX writes the table
// unqualified. There is no TEMP form — CREATE TEMP INDEX is a syntax error,
// measured on 3.50.3 — and a temporary index is written by giving the index
// the temp schema instead.
type Index struct{ Name, Schema string }

// IndexElement is one key of an index: a column or an expression, with its
// sort order and collation.
type IndexElement interface {
	WriteIndexElement(b *strings.Builder, a *sqlb.Args)
	IndexColumns() []ddl.ColumnRef
}

// IndexKeyElem is a column or expression key.
type IndexKeyElem[T any] struct {
	e      sqlb.Expr[T]
	isExpr bool
	dir    string
	coll   Collation
}

// IndexKey indexes a column.
func IndexKey[T any](col sqlb.Expr[T]) IndexKeyElem[T] { return IndexKeyElem[T]{e: col} }

// IndexExpr indexes an expression; a query uses it only when it filters on the
// same expression.
func IndexExpr[T any](e sqlb.Expr[T]) IndexKeyElem[T] { return IndexKeyElem[T]{e: e, isExpr: true} }

// Desc sorts the key descending.
func (k IndexKeyElem[T]) Desc() IndexKeyElem[T] { k.dir = kwDesc; return k }

// Asc sorts the key ascending, the default, written out.
func (k IndexKeyElem[T]) Asc() IndexKeyElem[T] { k.dir = kwAsc; return k }

// Collate indexes the key under a collation, which a query must use too for
// the index to serve it.
func (k IndexKeyElem[T]) Collate(c Collation) IndexKeyElem[T] { k.coll = c; return k }

// WriteIndexElement renders the key, its columns bare.
func (k IndexKeyElem[T]) WriteIndexElement(b *strings.Builder, a *sqlb.Args) {
	sql := a.Bare(func() string { return k.e.SQL(a) })
	if k.isExpr {
		sql = "(" + sql + ")"
	} else if k.e.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	b.WriteString(sql)
	if k.coll != 0 {
		kw, ok := k.coll.keyword()
		if !ok {
			a.Fail(ErrUnknownKeyword)
			return
		}
		b.WriteString(" " + kwCollate + " " + kw)
	}
	if k.dir != "" {
		b.WriteString(" " + k.dir)
	}
}

// IndexColumns is the column a column key names.
func (k IndexKeyElem[T]) IndexColumns() []ddl.ColumnRef {
	if k.isExpr {
		return nil
	}
	return []ddl.ColumnRef{k.e}
}

type indexCore struct {
	ix                  Index
	t                   sqlb.Table
	unique, ifNotExists bool
	keys                []IndexElement
	where               sqlb.Predicate
}

func (c indexCore) derive() indexCore { c.keys = slices.Clone(c.keys); return c }

// CreateIndexQuery is a CREATE INDEX with no key yet, which has no Build.
type CreateIndexQuery struct{ core indexCore }

// CreateIndexReady is a CREATE INDEX with at least one key.
type CreateIndexReady struct{ core indexCore }

// CreateIndex starts an index ix on table t.
func CreateIndex(ix Index, t sqlb.Table) *CreateIndexQuery {
	return &CreateIndexQuery{core: indexCore{ix: ix, t: t}}
}

// Unique makes the index refuse duplicate keys.
func (x *CreateIndexQuery) Unique() *CreateIndexQuery {
	c := x.core.derive()
	c.unique = true
	return &CreateIndexQuery{core: c}
}

// IfNotExists makes creating an existing index a no-op; it checks the name.
func (x *CreateIndexQuery) IfNotExists() *CreateIndexQuery {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateIndexQuery{core: c}
}

// On gives the keys, at least one, and so completes the statement.
func (x *CreateIndexQuery) On(first IndexElement, more ...IndexElement) *CreateIndexReady {
	c := x.core.derive()
	c.keys = append(append(c.keys, first), more...)
	return &CreateIndexReady{core: c}
}

// Unique makes the index refuse duplicate keys.
func (x *CreateIndexReady) Unique() *CreateIndexReady {
	c := x.core.derive()
	c.unique = true
	return &CreateIndexReady{core: c}
}

// IfNotExists makes creating an existing index a no-op.
func (x *CreateIndexReady) IfNotExists() *CreateIndexReady {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateIndexReady{core: c}
}

// Where makes the index partial: only rows matching p are indexed.
func (x *CreateIndexReady) Where(p sqlb.Predicate) *CreateIndexReady {
	c := x.core.derive()
	c.where = p
	return &CreateIndexReady{core: c}
}

// Build renders the statement.
func (x *CreateIndexReady) Build() (ddl.Script, error) {
	c := x.core
	for _, k := range c.keys {
		for _, r := range k.IndexColumns() {
			if !onTable(r, c.t) {
				return nil, ddl.ErrForeignColumn
			}
		}
	}
	return buildOne(c.ix.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		kw := kwCreateIndex
		if c.unique {
			kw = kwCreateUniqueIndex
		}
		var b strings.Builder
		b.WriteString(kw + ifNotExists(c.ifNotExists) + " " + qualified(a, c.ix.Schema, c.ix.Name) + " " + kwOn + " " + d.QuoteIdent(c.t.Name) + " (")
		for i, k := range c.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			k.WriteIndexElement(&b, a)
		}
		b.WriteString(")")
		if c.where != nil {
			b.WriteString(" " + kwWhere + " " + writePred(a, c.where))
		}
		return b.String()
	})
}

// writePred renders a predicate with columns bare.
func writePred(a *sqlb.Args, p sqlb.Predicate) string {
	return a.Bare(func() string {
		var b strings.Builder
		p.WriteSQL(&b, a)
		return b.String()
	})
}

// DropStatement is DROP INDEX, DROP VIEW or DROP TRIGGER, with IF EXISTS.
// Each takes the object value, so the schema it was declared with is the
// schema it is dropped from and the two cannot drift apart.
type DropStatement struct {
	kw, name, schema string
	ifExists         bool
}

// DropIndex drops an index.
func DropIndex(ix Index) *DropStatement {
	return &DropStatement{kw: kwDropIndex, name: ix.Name, schema: ix.Schema}
}

// IfExists makes dropping a missing object a no-op.
func (x *DropStatement) IfExists() *DropStatement { c := *x; c.ifExists = true; return &c }

// Build renders the statement.
func (x *DropStatement) Build() (ddl.Script, error) {
	return buildOne(x.name, func(a *sqlb.Args) string {
		return x.kw + ifExists(x.ifExists) + " " + qualified(a, x.schema, x.name)
	})
}

// View is a SQLite view.
//
// Schema names the database the view is created in, empty for main; a
// temporary view is Temporary rather than Schema "temp", though SQLite
// accepts both.
type View struct{ Name, Schema string }

// CreateViewQuery is CREATE VIEW with no body yet, which has no Build. There is
// no OrReplace: CREATE OR REPLACE VIEW is a syntax error in SQLite, so
// replacing a view is DropView then CreateView.
type CreateViewQuery struct{ core viewCore }

type viewCore struct {
	v                      View
	ifNotExists, temporary bool
	cols                   []ddl.ColumnRef
	body                   *SelectQuery
}

func (c viewCore) derive() viewCore { c.cols = slices.Clone(c.cols); return c }

// CreateView starts a view.
func CreateView(v View) *CreateViewQuery { return &CreateViewQuery{core: viewCore{v: v}} }

// IfNotExists makes creating an existing view a no-op.
func (x *CreateViewQuery) IfNotExists() *CreateViewQuery {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateViewQuery{core: c}
}

// Temporary makes the view live in the temp schema and vanish when the
// connection closes. See CreateViewStatement.Temporary for the warning that
// comes with it.
func (x *CreateViewQuery) Temporary() *CreateViewQuery {
	c := x.core.derive()
	c.temporary = true
	return &CreateViewQuery{core: c}
}

// Columns names the view's columns, overriding the names the body's result
// would otherwise get.
//
// The columns are declared on a table of the view's own name, which is how a
// consumer reads the view afterwards, so the same Col values that name the
// columns here are the ones every query against the view uses: a view cannot
// be created under one column name and read under another. SQLite's own
// documentation recommends always naming them, because the rules by which it
// derives a name from a result expression are undefined and may change.
func (x *CreateViewQuery) Columns(first ddl.ColumnRef, more ...ddl.ColumnRef) *CreateViewQuery {
	c := x.core.derive()
	c.cols = append(append(c.cols, first), more...)
	return &CreateViewQuery{core: c}
}

// As gives the body, a SQLite SELECT from the query builder, rendered with its
// bound values as literals.
func (x *CreateViewQuery) As(body *SelectQuery) *CreateViewStatement {
	c := x.core.derive()
	c.body = body
	return &CreateViewStatement{core: c}
}

// CreateViewStatement is a complete CREATE VIEW.
type CreateViewStatement struct{ core viewCore }

// IfNotExists makes creating an existing view a no-op.
func (x *CreateViewStatement) IfNotExists() *CreateViewStatement {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateViewStatement{core: c}
}

// Temporary makes the view live in the temp schema and vanish when the
// connection closes.
//
// With database/sql that is a sharper edge than it looks: the pool hands out
// connections, so a temporary object created by one statement is invisible to
// the next unless both run on the same connection. Create it inside a
// transaction, or on a connection taken with Conn, or not at all. A temporary
// view whose View names a schema other than temp fails Build with
// ErrTemporaryQualified, which is what SQLite does.
func (x *CreateViewStatement) Temporary() *CreateViewStatement {
	c := x.core.derive()
	c.temporary = true
	return &CreateViewStatement{core: c}
}

// Columns names the view's columns; see CreateViewQuery.Columns.
func (x *CreateViewStatement) Columns(first ddl.ColumnRef, more ...ddl.ColumnRef) *CreateViewStatement {
	c := x.core.derive()
	c.cols = append(append(c.cols, first), more...)
	return &CreateViewStatement{core: c}
}

// Build renders the statement.
func (x *CreateViewStatement) Build() (ddl.Script, error) {
	c := x.core
	if c.body == nil {
		return nil, ddl.ErrZeroValue
	}
	// The view's columns are declared on a table of the view's own name, so a
	// column of some other table here is the same mistake as a key on a
	// column the table does not define.
	self := sqlb.Table{Name: c.v.Name, Schema: c.v.Schema}
	for _, r := range c.cols {
		if r == nil || r.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(r, self) {
			return nil, ddl.ErrForeignColumn
		}
	}
	return buildOne(c.v.Name, func(a *sqlb.Args) string {
		var b strings.Builder
		c.body.WriteSQL(&b, a)
		cols := ""
		if len(c.cols) > 0 {
			cols = " " + quoteCols(a, c.cols)
		}
		return kwCreate + temporaryPrefix(a, c.temporary, c.v.Schema) + " " + kwView +
			ifNotExists(c.ifNotExists) + " " + qualified(a, c.v.Schema, c.v.Name) + cols + " " + kwAs + " " + b.String()
	})
}

// DropView drops a view.
func DropView(v View) *DropStatement {
	return &DropStatement{kw: kwDropView, name: v.Name, schema: v.Schema}
}

// Trigger is a SQLite trigger; its name is unique per database.
//
// Schema names the database the trigger is created in, empty for main. The
// table the trigger fires on is written unqualified, because a non-temporary
// trigger must be in the same database as its table.
type Trigger struct{ Name, Schema string }

// TriggerEvent is the one event that fires a SQLite trigger; SQLite takes one
// per trigger, so there is no list.
type TriggerEvent struct {
	kw   string
	cols []ddl.ColumnRef
}

// OnInsert fires on INSERT.
func OnInsert() TriggerEvent { return TriggerEvent{kw: "INSERT"} }

// OnUpdate fires on any UPDATE.
func OnUpdate() TriggerEvent { return TriggerEvent{kw: "UPDATE"} }

// OnUpdateOf fires on an UPDATE whose SET names one of cols.
func OnUpdateOf(first ddl.ColumnRef, more ...ddl.ColumnRef) TriggerEvent {
	return TriggerEvent{kw: "UPDATE", cols: append([]ddl.ColumnRef{first}, more...)}
}

// OnDelete fires on DELETE.
func OnDelete() TriggerEvent { return TriggerEvent{kw: "DELETE"} }

// Old is a column's value before the change, in a trigger's WHEN or body.
func Old[T any](col sqlb.Expr[T]) sqlb.Expr[T] { return rowRef(kwOld, col) }

// New is a column's value after the change.
func New[T any](col sqlb.Expr[T]) sqlb.Expr[T] { return rowRef(kwNew, col) }

func rowRef[T any](which string, col sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructMisc, func(a *sqlb.Args) string {
		if col.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return ""
		}
		return which + "." + a.Dialect().QuoteIdent(col.Name())
	})
}

// TriggerStep is one statement in a trigger's body. It is closed to the four
// constructors below, each typed to a complete SQLite statement, so the
// typestate that stops an unfiltered UPDATE compiling stops one inside a
// trigger too, and a Postgres statement does not fit.
type TriggerStep struct {
	write func(b *strings.Builder, a *sqlb.Args)
}

// StepInsert is an INSERT in a trigger body.
func StepInsert(s *InsertReady) TriggerStep { return TriggerStep{write: s.WriteSQL} }

// StepUpdate is an UPDATE in a trigger body.
func StepUpdate(s *FilteredUpdate) TriggerStep { return TriggerStep{write: s.WriteSQL} }

// StepDelete is a DELETE in a trigger body.
func StepDelete(s *FilteredDelete) TriggerStep { return TriggerStep{write: s.WriteSQL} }

// StepSelect is a SELECT in a trigger body, which is how a trigger raises an
// error through a function such as RAISE.
func StepSelect(s *SelectQuery) TriggerStep { return TriggerStep{write: s.WriteSQL} }

type triggerCore struct {
	tr                     Trigger
	ifNotExists, temporary bool
	timing                 string
	event                  TriggerEvent
	table                  sqlb.Table
	when                   sqlb.Predicate
	steps                  []TriggerStep
}

func (c triggerCore) derive() triggerCore { c.steps = slices.Clone(c.steps); return c }

// CreateTriggerQuery is CREATE TRIGGER with no timing yet, which has no Build.
type CreateTriggerQuery struct{ core triggerCore }

// CreateTrigger starts a trigger.
func CreateTrigger(tr Trigger) *CreateTriggerQuery {
	return &CreateTriggerQuery{core: triggerCore{tr: tr}}
}

// IfNotExists makes creating an existing trigger a no-op.
func (x *CreateTriggerQuery) IfNotExists() *CreateTriggerQuery {
	c := x.core.derive()
	c.ifNotExists = true
	return &CreateTriggerQuery{core: c}
}

// Temporary makes the trigger live in the temp schema and vanish when the
// connection closes.
//
// A temporary trigger is the only kind that may fire on a table in another
// database, and the only kind that survives its table being dropped without
// being dropped with it. It carries the pooling warning
// CreateViewStatement.Temporary describes: with database/sql the next
// statement may run on a connection that has never seen it. A Trigger naming
// a schema other than temp fails Build with ErrTemporaryQualified.
func (x *CreateTriggerQuery) Temporary() *CreateTriggerQuery {
	c := x.core.derive()
	c.temporary = true
	return &CreateTriggerQuery{core: c}
}

// Before fires before the change on t.
func (x *CreateTriggerQuery) Before(e TriggerEvent, t sqlb.Table) *TimedTriggerQuery {
	return x.timed(kwBefore, e, t)
}

// After fires after the change on t.
func (x *CreateTriggerQuery) After(e TriggerEvent, t sqlb.Table) *TimedTriggerQuery {
	return x.timed(kwAfter, e, t)
}

// InsteadOf replaces the change on a view, which is how a view is made
// writable.
func (x *CreateTriggerQuery) InsteadOf(e TriggerEvent, v View) *TimedTriggerQuery {
	return x.timed(kwInsteadOf, e, sqlb.Table{Name: v.Name})
}

func (x *CreateTriggerQuery) timed(kw string, e TriggerEvent, t sqlb.Table) *TimedTriggerQuery {
	c := x.core.derive()
	c.timing, c.event, c.table = kw, e, t
	return &TimedTriggerQuery{core: c}
}

// TimedTriggerQuery is CREATE TRIGGER with its timing and no body yet.
type TimedTriggerQuery struct{ core triggerCore }

// When fires the trigger only for rows matching p, written with Old and New.
func (x *TimedTriggerQuery) When(p sqlb.Predicate) *TimedTriggerQuery {
	c := x.core.derive()
	c.when = p
	return &TimedTriggerQuery{core: c}
}

// Do gives the body, at least one statement, and so completes the trigger.
func (x *TimedTriggerQuery) Do(first TriggerStep, more ...TriggerStep) *CreateTriggerStatement {
	c := x.core.derive()
	c.steps = append(append(c.steps, first), more...)
	return &CreateTriggerStatement{core: c}
}

// CreateTriggerStatement is a complete CREATE TRIGGER. SQLite triggers are
// always row-level, so FOR EACH ROW is written and there is no statement form.
type CreateTriggerStatement struct{ core triggerCore }

// Build renders the statement. The body's statements render inline, so their
// bound values are literals.
func (x *CreateTriggerStatement) Build() (ddl.Script, error) {
	c := x.core
	for _, r := range c.event.cols {
		if r.Name() == "" {
			return nil, ddl.ErrNotAColumn
		}
		if !onTable(r, c.table) {
			return nil, ddl.ErrForeignColumn
		}
	}
	if c.table.Name == "" {
		return nil, ErrEmptyName
	}
	// A TriggerEvent{} or TriggerStep{} literal carries no keyword and no
	// statement; each is refused rather than rendered as "AFTER  ON" or called
	// as a nil function.
	if c.event.kw == "" {
		return nil, ddl.ErrZeroValue
	}
	for _, s := range c.steps {
		if s.write == nil {
			return nil, ddl.ErrZeroValue
		}
	}
	return buildOne(c.tr.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		ev := c.event.kw
		if len(c.event.cols) > 0 {
			names := make([]string, len(c.event.cols))
			for i, col := range c.event.cols {
				names[i] = d.QuoteIdent(col.Name())
			}
			ev += " " + kwOf + " " + strings.Join(names, ", ")
		}
		var b strings.Builder
		b.WriteString(kwCreate + temporaryPrefix(a, c.temporary, c.tr.Schema) + " " + kwTrigger +
			ifNotExists(c.ifNotExists) + " " + qualified(a, c.tr.Schema, c.tr.Name) + " " + c.timing + " " + ev +
			" " + kwOn + " " + d.QuoteIdent(c.table.Name) + " " + kwForEachRow)
		if c.when != nil {
			b.WriteString(" " + kwWhen + " " + writePred(a, c.when))
		}
		b.WriteString(" " + kwBegin + " ")
		for _, s := range c.steps {
			s.write(&b, a)
			b.WriteString("; ")
		}
		b.WriteString(kwEnd)
		return b.String()
	})
}

// RaiseAction is what RAISE does to the statement that fired the trigger.
type RaiseAction int

// The raise actions that carry a message. Rollback undoes the whole
// transaction; Abort undoes only the statement, which is SQLite's default for
// a constraint violation; Fail stops the statement where it is and keeps the
// changes it has already made, which is almost never what a validation wants.
const (
	RaiseRollback RaiseAction = iota + 1
	RaiseAbort
	RaiseFail
)

func (r RaiseAction) keyword() (string, bool) {
	switch r {
	case RaiseRollback:
		return "ROLLBACK", true
	case RaiseAbort:
		return "ABORT", true
	case RaiseFail:
		return "FAIL", true
	}
	return "", false
}

// Raise refuses the row from inside a trigger, with a message the caller
// receives as the error text.
//
// It is how a constraint that a CHECK cannot express is enforced: a BEFORE
// trigger with a WHEN that matches the bad row, whose body is
// StepSelect(Select(sqlite.Raise(...))). Measured on 3.50.3, the insert then
// fails with SQLITE_CONSTRAINT and the message.
//
// RAISE is legal only inside a trigger body — SQLite answers "RAISE() may only
// be used within a trigger-program" anywhere else, measured — so this is the
// one construct here whose position is checked by the engine rather than by
// the type. The message is author-written text rendered as a literal, never a
// value from a request. The result type is any because RAISE returns nothing:
// it ends the statement rather than producing a value, so there is nothing to
// scan and nothing to compare.
func Raise(action RaiseAction, message string) sqlb.Expr[any] {
	return expr[any](constructMisc, func(a *sqlb.Args) string {
		kw, ok := action.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: RaiseAction", ErrUnknownKeyword))
			return ""
		}
		return kwRaise + "(" + kw + ", " + quoteLit(message).SQL(a) + ")"
	})
}

// RaiseIgnore abandons the rest of the trigger and the statement that fired
// it, without an error and without undoing anything.
//
// It is the one RAISE form that takes no message, because nothing is reported:
// the row is skipped and the caller is told the statement succeeded. Measured
// on 3.50.3, an INSERT whose BEFORE trigger raises IGNORE returns no error and
// inserts no row — which is useful for a filter and dangerous for a
// validation, since a caller cannot tell the difference from a row that was
// written.
func RaiseIgnore() sqlb.Expr[any] {
	return expr[any](constructMisc, func(*sqlb.Args) string { return kwRaise + "(IGNORE)" })
}

// DropTrigger drops a trigger.
func DropTrigger(tr Trigger) *DropStatement {
	return &DropStatement{kw: kwDropTrigger, name: tr.Name, schema: tr.Schema}
}
