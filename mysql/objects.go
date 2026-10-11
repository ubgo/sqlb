package mysql

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The MySQL statements around a table: indexes, views, triggers, TRUNCATE and
// RENAME TABLE. Each has a Build returning a ddl.Script, and every method
// returns a derived statement.
//
// What MySQL lacks has no method: CREATE INDEX and DROP INDEX take no IF
// [NOT] EXISTS, and a trigger takes no WHEN, so none of those exist here.

const (
	kwCreateIndex     = "CREATE INDEX"
	kwCreateUnique    = "CREATE UNIQUE INDEX"
	kwCreateFulltext  = "CREATE FULLTEXT INDEX"
	kwOn              = "ON"
	kwUsing           = "USING"
	kwDesc            = "DESC"
	kwCreateView      = "CREATE"
	kwCreateOrReplace = "CREATE OR REPLACE"
	kwDropView        = "DROP VIEW"
	kwWithLocal       = "WITH LOCAL CHECK OPTION"
	kwWithCascaded    = "WITH CASCADED CHECK OPTION"
	kwCreateTrigger   = "CREATE TRIGGER"
	kwDropTrigger     = "DROP TRIGGER"
	kwForEachRow      = "FOR EACH ROW"
	kwBegin           = "BEGIN"
	kwEnd             = "END"
	kwIfNotExists     = "IF NOT EXISTS"
	kwIfExists        = "IF EXISTS"
	kwNewRow          = "NEW"
	kwOldRow          = "OLD"
	kwTruncate        = "TRUNCATE TABLE"
	kwRenameTable     = "RENAME TABLE"
)

// ErrEmptyName is returned for an object value with no name.
var ErrEmptyName = errors.New("sqlb/mysql: object has no name")

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

// buildOne renders one statement through inline Args, every value a literal.
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

func onTable(r ddl.ColumnRef, t sqlb.Table) bool {
	return r.Table().Name == t.Name && r.Table().Schema == t.Schema
}

// Index is a MySQL index, declared once. Its name is unique per table.
type Index struct{ Name string }

// IndexMethod is BTREE or HASH; InnoDB builds BTREE whatever is asked.
//
// The set is closed. It is a named string type, so IndexMethod("anything")
// compiles, and until 2026-09-24 both places that rendered one wrote it into
// the statement without asking — which made it an injection path, not merely
// a way to reach a wrong keyword. writeUsing is now the only renderer and it
// fails the build on a value outside the constants.
type IndexMethod string

// The methods.
const (
	BTree IndexMethod = "BTREE"
	Hash  IndexMethod = "HASH"
)

func (m IndexMethod) valid() bool { return m == BTree || m == Hash }

// writeUsing renders USING <method>, or nothing for the zero value, and is
// the single render site for an IndexMethod. Both CREATE INDEX and an inline
// key call it; the defect it replaced was that each wrote its own
// string(method) and neither validated, so a fix applied to one would have
// left the other open. Keep it that way: an IndexMethod must reach statement
// text through here and nowhere else.
func writeUsing(a *sqlb.Args, m IndexMethod) string {
	if m == "" {
		return ""
	}
	if !m.valid() {
		a.Fail(fmt.Errorf("%w: index method %q", ErrUnknownKeyword, string(m)))
		return ""
	}
	return " " + kwUsing + " " + string(m)
}

// IndexElement is one key part of an index.
type IndexElement interface {
	WriteIndexElement(b *strings.Builder, a *sqlb.Args)
	IndexColumns() []ddl.ColumnRef
}

// IndexKeyElem is a column, prefix or expression key part.
type IndexKeyElem[T any] struct {
	e      sqlb.Expr[T]
	isExpr bool
	prefix int
	desc   bool
}

// IndexKey indexes a column.
func IndexKey[T any](col sqlb.Expr[T]) IndexKeyElem[T] { return IndexKeyElem[T]{e: col} }

// IndexPrefix indexes the first n characters of a text column, which is the
// only way a TEXT column is indexed at all.
func IndexPrefix[T ~string | *string](col sqlb.Expr[T], n int) IndexKeyElem[T] {
	return IndexKeyElem[T]{e: col, prefix: n}
}

// IndexExpr indexes an expression, a functional key part, which MySQL writes
// in a second pair of parentheses.
func IndexExpr[T any](e sqlb.Expr[T]) IndexKeyElem[T] { return IndexKeyElem[T]{e: e, isExpr: true} }

// Desc sorts the key part descending.
func (k IndexKeyElem[T]) Desc() IndexKeyElem[T] { k.desc = true; return k }

// WriteIndexElement renders the key part, its columns bare.
func (k IndexKeyElem[T]) WriteIndexElement(b *strings.Builder, a *sqlb.Args) {
	sql := a.Bare(func() string { return k.e.SQL(a) })
	switch {
	case k.isExpr:
		sql = "(" + sql + ")"
	case k.e.Name() == "":
		a.Fail(ddl.ErrNotAColumn)
		return
	case k.prefix > 0:
		sql += "(" + strconv.Itoa(k.prefix) + ")"
	}
	b.WriteString(sql)
	if k.desc {
		b.WriteString(" " + kwDesc)
	}
}

// IndexColumns is the column a column key part names.
func (k IndexKeyElem[T]) IndexColumns() []ddl.ColumnRef {
	if k.isExpr {
		return nil
	}
	return []ddl.ColumnRef{k.e}
}

type indexCore struct {
	ix          Index
	t           sqlb.Table
	kind        string
	method      IndexMethod
	keys        []IndexElement
	options     []IndexOption
	algorithm   AlterAlgorithm
	lock        AlterLock
	ifNotExists bool
}

// indexExists is the catalogue query for the index, the SkipIf of a CREATE
// INDEX that may already have run. A table with no schema is looked up in the
// connection's database.
func indexExists(a *sqlb.Args, t sqlb.Table, ix Index) string {
	db := "DATABASE()"
	if t.Schema != "" {
		db = a.Literal(t.Schema)
	}
	return "SELECT 1 FROM information_schema.statistics WHERE table_schema = " + db +
		" AND table_name = " + a.Literal(t.Name) + " AND index_name = " + a.Literal(ix.Name)
}

func (c indexCore) derive() indexCore {
	c.keys = slices.Clone(c.keys)
	c.options = slices.Clone(c.options)
	return c
}

// CreateIndexQuery is CREATE INDEX with no key yet, which has no Build.
type CreateIndexQuery struct{ core indexCore }

// CreateIndexReady is a CREATE INDEX with at least one key.
type CreateIndexReady struct{ core indexCore }

// CreateIndex starts an index on t. MySQL has no IF NOT EXISTS for it, so a
// repeatable install checks information_schema.statistics first.
func CreateIndex(ix Index, t sqlb.Table) *CreateIndexQuery {
	return &CreateIndexQuery{core: indexCore{ix: ix, t: t, kind: kwCreateIndex}}
}

// Unique makes the index refuse duplicate keys.
func (x *CreateIndexQuery) Unique() *CreateIndexQuery {
	c := x.core.derive()
	c.kind = kwCreateUnique
	return &CreateIndexQuery{core: c}
}

// Fulltext makes a FULLTEXT index, which Match searches.
func (x *CreateIndexQuery) Fulltext() *CreateIndexQuery {
	c := x.core.derive()
	c.kind = kwCreateFulltext
	return &CreateIndexQuery{core: c}
}

// Using picks the index method.
func (x *CreateIndexQuery) Using(m IndexMethod) *CreateIndexQuery {
	c := x.core.derive()
	c.method = m
	return &CreateIndexQuery{core: c}
}

// On gives the key parts, at least one, and so completes the statement.
func (x *CreateIndexQuery) On(first IndexElement, more ...IndexElement) *CreateIndexReady {
	c := x.core.derive()
	c.keys = append(append(c.keys, first), more...)
	return &CreateIndexReady{core: c}
}

// IfNotExists makes the index repeatable. MySQL has no IF NOT EXISTS for
// CREATE INDEX, so the statement carries a SkipIf query for the index in
// information_schema.statistics, which contrib/migrate's Execute runs first
// and a runner of your own must honour too; see ddl.Statement.SkipIf.
func (x *CreateIndexReady) IfNotExists() *CreateIndexReady {
	c := x.core.derive()
	c.ifNotExists = true
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
	s, err := buildOne(c.ix.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		var b strings.Builder
		b.WriteString(c.kind + " " + d.QuoteIdent(c.ix.Name))
		b.WriteString(writeUsing(a, c.method))
		b.WriteString(" " + kwOn + " " + c.t.Qualified(d) + " (")
		for i, k := range c.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			k.WriteIndexElement(&b, a)
		}
		b.WriteString(")")
		b.WriteString(writeIndexOptions(a, c.options))
		b.WriteString(writeHow(a, c.algorithm, c.lock))
		return b.String()
	})
	if !c.ifNotExists {
		return s, err
	}
	return withSkipIf(s, err, func(a *sqlb.Args) string { return indexExists(a, c.t, c.ix) })
}

// withSkipIf renders a guard query into a one-statement script's SkipIf.
func withSkipIf(s ddl.Script, err error, guard func(a *sqlb.Args) string) (ddl.Script, error) {
	if err != nil {
		return nil, err
	}
	a := sqlb.NewInlineArgs(Dialect{})
	q := guard(a)
	if err := a.Err(); err != nil {
		return nil, err
	}
	s[0].SkipIf = q
	return s, nil
}

// DropIndexStatement is DROP INDEX ... ON.
type DropIndexStatement struct {
	ix        Index
	t         sqlb.Table
	algorithm AlterAlgorithm
	lock      AlterLock
	ifExists  bool
}

// IfExists makes dropping a missing index a no-op, through a SkipIf query as
// CreateIndexReady.IfNotExists does, since MySQL has no DROP INDEX IF EXISTS.
func (x *DropIndexStatement) IfExists() *DropIndexStatement { c := *x; c.ifExists = true; return &c }

// DropIndex drops an index from its table. MySQL has no IF EXISTS for it.
func DropIndex(ix Index, t sqlb.Table) *DropIndexStatement { return &DropIndexStatement{ix: ix, t: t} }

// Build renders the statement.
func (x *DropIndexStatement) Build() (ddl.Script, error) {
	s, err := buildOne(x.ix.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwDropIndex + " " + d.QuoteIdent(x.ix.Name) + " " + kwOn + " " + x.t.Qualified(d) +
			writeHow(a, x.algorithm, x.lock)
	})
	if !x.ifExists {
		return s, err
	}
	return withSkipIf(s, err, func(a *sqlb.Args) string {
		return "SELECT 1 FROM DUAL WHERE NOT EXISTS (" + indexExists(a, x.t, x.ix) + ")"
	})
}

// View is a MySQL view.
type View struct{ Name string }

// CheckOption is what WITH CHECK OPTION checks.
type CheckOption int

// The check options.
const (
	Local CheckOption = iota + 1
	Cascaded
)

// CreateViewQuery is CREATE VIEW with no body yet, which has no Build.
type CreateViewQuery struct {
	v         View
	orReplace bool
	algorithm ViewAlgorithmKind
	security  ViewSecurity
	columns   []string
}

// CreateView starts a view.
func CreateView(v View) *CreateViewQuery { return &CreateViewQuery{v: v} }

// OrReplace replaces an existing view, the idempotent form MySQL has.
func (x *CreateViewQuery) OrReplace() *CreateViewQuery { c := *x; c.orReplace = true; return &c }

// As gives the body, a MySQL SELECT from the query builder.
func (x *CreateViewQuery) As(body *SelectQuery) *CreateViewStatement {
	return &CreateViewStatement{v: x.v, orReplace: x.orReplace, body: body,
		algorithm: x.algorithm, security: x.security, columns: slices.Clone(x.columns)}
}

// CreateViewStatement is a complete CREATE VIEW.
type CreateViewStatement struct {
	v         View
	orReplace bool
	body      *SelectQuery
	check     CheckOption
	algorithm ViewAlgorithmKind
	security  ViewSecurity
	columns   []string
}

// OrReplace replaces an existing view.
func (x *CreateViewStatement) OrReplace() *CreateViewStatement {
	c := *x
	c.orReplace = true
	return &c
}

// WithCheckOption refuses a write through the view that it would not show.
func (x *CreateViewStatement) WithCheckOption(o CheckOption) *CreateViewStatement {
	c := *x
	c.check = o
	return &c
}

// Build renders the statement.
func (x *CreateViewStatement) Build() (ddl.Script, error) {
	return buildOne(x.v.Name, func(a *sqlb.Args) string {
		kw := kwCreateView
		if x.orReplace {
			kw = kwCreateOrReplace
		}
		var b strings.Builder
		x.body.WriteSQL(&b, a)
		s := kw + writeViewHead(a, x.algorithm, x.security) + " " + kwView + " " + a.Dialect().QuoteIdent(x.v.Name) +
			writeViewColumns(a, x.columns) + " " + kwAs + " " + b.String()
		switch x.check {
		case 0:
		case Local:
			s += " " + kwWithLocal
		case Cascaded:
			s += " " + kwWithCascaded
		default:
			a.Fail(ErrUnknownKeyword)
		}
		return s
	})
}

// DropStatement is DROP VIEW or DROP TRIGGER, with IF EXISTS.
type DropStatement struct {
	kw, name string
	ifExists bool
}

// DropView drops a view.
func DropView(v View) *DropStatement { return &DropStatement{kw: kwDropView, name: v.Name} }

// IfExists makes dropping a missing object a no-op.
func (x *DropStatement) IfExists() *DropStatement { c := *x; c.ifExists = true; return &c }

// Build renders the statement.
func (x *DropStatement) Build() (ddl.Script, error) {
	return buildOne(x.name, func(a *sqlb.Args) string {
		return x.kw + ifExists(x.ifExists) + " " + a.Dialect().QuoteIdent(x.name)
	})
}

// Trigger is a MySQL trigger; its name is unique per schema.
type Trigger struct{ Name string }

// TriggerEvent is the one event that fires a trigger.
type TriggerEvent string

// The events.
const (
	OnInsert TriggerEvent = "INSERT"
	OnUpdate TriggerEvent = "UPDATE"
	OnDelete TriggerEvent = "DELETE"
)

// OldRow is a column's value before the change, in a trigger's body. It is
// not New, which is the upsert's row alias; a trigger's NEW is a keyword.
func OldRow[T any](col sqlb.Expr[T]) sqlb.Expr[T] { return rowRef(kwOldRow, col) }

// NewRow is a column's value after the change, in a trigger's body.
func NewRow[T any](col sqlb.Expr[T]) sqlb.Expr[T] { return rowRef(kwNewRow, col) }

func rowRef[T any](which string, col sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructWrite, func(a *sqlb.Args) string {
		if col.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return ""
		}
		return which + "." + a.Dialect().QuoteIdent(col.Name())
	})
}

// TriggerStep is a trigger's body statement, closed to the constructors
// below, each typed to a complete MySQL statement.
type TriggerStep struct {
	write func(b *strings.Builder, a *sqlb.Args)
}

// StepInsert is an INSERT as a trigger's body.
func StepInsert(s *InsertReady) TriggerStep { return TriggerStep{write: s.WriteSQL} }

// StepUpdate is an UPDATE as a trigger's body.
func StepUpdate(s *FilteredUpdate) TriggerStep { return TriggerStep{write: s.WriteSQL} }

// StepDelete is a DELETE as a trigger's body.
func StepDelete(s *FilteredDelete) TriggerStep { return TriggerStep{write: s.WriteSQL} }

// CreateTriggerQuery is CREATE TRIGGER with no timing yet.
type CreateTriggerQuery struct {
	tr          Trigger
	ifNotExists bool
}

// CreateTrigger starts a trigger.
func CreateTrigger(tr Trigger) *CreateTriggerQuery { return &CreateTriggerQuery{tr: tr} }

// IfNotExists makes creating an existing trigger a no-op; MySQL 8.0.29 or
// later.
func (x *CreateTriggerQuery) IfNotExists() *CreateTriggerQuery {
	c := *x
	c.ifNotExists = true
	return &c
}

// Before fires before the change on t, where the body may set NEW columns.
func (x *CreateTriggerQuery) Before(e TriggerEvent, t sqlb.Table) *TimedTriggerQuery {
	return &TimedTriggerQuery{q: *x, timing: "BEFORE", event: e, table: t}
}

// After fires after the change on t.
func (x *CreateTriggerQuery) After(e TriggerEvent, t sqlb.Table) *TimedTriggerQuery {
	return &TimedTriggerQuery{q: *x, timing: "AFTER", event: e, table: t}
}

// TimedTriggerQuery is CREATE TRIGGER with its timing and no body yet. MySQL
// has no WHEN clause, so a condition belongs in the body statement's WHERE.
type TimedTriggerQuery struct {
	q       CreateTriggerQuery
	timing  string
	event   TriggerEvent
	table   sqlb.Table
	order   Trigger
	orderKw string
}

// Do gives the body and completes the trigger. One step is written as the
// body; several are written BEGIN s1; s2; END, run in order for each row.
// No DELIMITER is involved: that is a command of the mysql client, and
// database/sql sends the statement whole, measured on MySQL 8.4.
func (x *TimedTriggerQuery) Do(first TriggerStep, more ...TriggerStep) *CreateTriggerStatement {
	return &CreateTriggerStatement{t: *x, steps: append([]TriggerStep{first}, more...)}
}

// CreateTriggerStatement is a complete CREATE TRIGGER.
type CreateTriggerStatement struct {
	t     TimedTriggerQuery
	steps []TriggerStep
}

// Build renders the statement.
func (x *CreateTriggerStatement) Build() (ddl.Script, error) {
	if x.t.table.Name == "" || len(x.steps) == 0 {
		return nil, ErrEmptyName
	}
	for _, st := range x.steps {
		if st.write == nil {
			return nil, ddl.ErrZeroValue
		}
	}
	switch x.t.event {
	case OnInsert, OnUpdate, OnDelete:
	default:
		return nil, ErrUnknownKeyword
	}
	return buildOne(x.t.q.tr.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		var b strings.Builder
		b.WriteString(kwCreateTrigger + ifNotExists(x.t.q.ifNotExists) + " " + d.QuoteIdent(x.t.q.tr.Name) + " " + x.t.timing + " " +
			string(x.t.event) + " " + kwOn + " " + x.t.table.Qualified(d) + " " + kwForEachRow)
		if x.t.orderKw != "" {
			if x.t.order.Name == "" {
				a.Fail(ErrEmptyName)
				return ""
			}
			b.WriteString(" " + x.t.orderKw + " " + d.QuoteIdent(x.t.order.Name))
		}
		b.WriteString(" ")
		writeSteps(&b, a, x.steps)
		return b.String()
	})
}

// writeSteps renders a body of trigger or event steps: one step bare, several
// wrapped in BEGIN ... END. It is shared because a MySQL event's DO takes the
// same body as a trigger's FOR EACH ROW, and two copies of the wrapping rule
// would be two places to get the separator wrong.
func writeSteps(b *strings.Builder, a *sqlb.Args, steps []TriggerStep) {
	if len(steps) == 1 {
		steps[0].write(b, a)
		return
	}
	b.WriteString(kwBegin + " ")
	writeBlockSteps(b, a, steps)
	b.WriteString(kwEnd)
}

// writeBlockSteps writes each step terminated by a separator, which is what
// both a BEGIN ... END body and a procedural block need. Every statement
// inside either is terminated, the last one included: MySQL 8.4 refuses
// "IF c THEN SET x = 1 END IF" with ER_PARSE_ERROR and accepts
// "IF c THEN SET x = 1; END IF", measured through the driver rather than the
// mysql client, which splits on the separator and cannot send such a body at
// all.
func writeBlockSteps(b *strings.Builder, a *sqlb.Args, steps []TriggerStep) {
	for _, st := range steps {
		st.write(b, a)
		b.WriteString(kwStepSep)
	}
}

// DropTrigger drops a trigger.
func DropTrigger(tr Trigger) *DropStatement { return &DropStatement{kw: kwDropTrigger, name: tr.Name} }

// TruncateStatement is TRUNCATE TABLE.
type TruncateStatement struct{ t sqlb.Table }

// Truncate empties a table by recreating it, resetting AUTO_INCREMENT. It
// commits the transaction implicitly, as every MySQL DDL statement does, and
// is refused for a table another table's foreign key references.
func Truncate(t sqlb.Table) *TruncateStatement { return &TruncateStatement{t: t} }

// Build renders the statement.
func (x *TruncateStatement) Build() (ddl.Script, error) {
	return buildOne(x.t.Name, func(a *sqlb.Args) string { return kwTruncate + " " + x.t.Qualified(a.Dialect()) })
}

// RenameTableStatement is RENAME TABLE.
type RenameTableStatement struct {
	from, to sqlb.Table
	more     []renamePair
}

// renamePair is one further rename of a RENAME TABLE.
type renamePair struct{ from, to sqlb.Table }

// RenameTable renames a table, and can move it to another schema, which
// ALTER TABLE ... RENAME TO also does; this is the statement form.
func RenameTable(from, to sqlb.Table) *RenameTableStatement {
	return &RenameTableStatement{from: from, to: to}
}

// Build renders the statement.
func (x *RenameTableStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	for _, p := range x.more {
		if p.from.Name == "" || p.to.Name == "" {
			return nil, ErrEmptyName
		}
	}
	return buildOne(x.from.Name, func(a *sqlb.Args) string {
		d := a.Dialect()
		parts := make([]string, 0, len(x.more)+1)
		parts = append(parts, x.from.Qualified(d)+" "+kwTo+" "+x.to.Qualified(d))
		for _, p := range x.more {
			parts = append(parts, p.from.Qualified(d)+" "+kwTo+" "+p.to.Qualified(d))
		}
		return kwRenameTable + " " + strings.Join(parts, kwRenameTableSep)
	})
}

// ALTER VIEW and the procedural trigger steps.
//
// ALTER VIEW was left out until 2026-09-24 on the reading that CREATE OR
// REPLACE VIEW covers it. It does not, and the difference is not cosmetic:
// CREATE OR REPLACE creates the view when it is absent, so a typo in the name
// silently makes a second view instead of failing, while ALTER VIEW fails
// with ER_NO_SUCH_TABLE. A migration that means to redefine an existing view
// wants the statement that says so.
const (
	kwAlter       = "ALTER"
	kwView        = "VIEW"
	kwSet         = "SET"
	kwSignal      = "SIGNAL SQLSTATE"
	kwStepSep     = "; "
	kwMessageText = "SET MESSAGE_TEXT ="
)

// AlterViewQuery is ALTER VIEW with no body yet, which has no Build: a view
// is altered by giving its new definition, so there is nothing to build until
// As has been called.
type AlterViewQuery struct {
	v         View
	algorithm ViewAlgorithmKind
	security  ViewSecurity
	columns   []string
}

// AlterView starts a redefinition of an existing view. Unlike
// CreateView(v).OrReplace() it fails when the view does not exist.
func AlterView(v View) *AlterViewQuery { return &AlterViewQuery{v: v} }

// As gives the view's new definition and completes the statement.
func (x *AlterViewQuery) As(body *SelectQuery) *AlterViewStatement {
	return &AlterViewStatement{v: x.v, body: body,
		algorithm: x.algorithm, security: x.security, columns: slices.Clone(x.columns)}
}

// AlterViewStatement is a complete ALTER VIEW.
type AlterViewStatement struct {
	v         View
	body      *SelectQuery
	check     CheckOption
	algorithm ViewAlgorithmKind
	security  ViewSecurity
	columns   []string
}

// WithCheckOption refuses a write through the view that it would not show,
// as on CREATE VIEW. A view's check option is part of its definition, so an
// ALTER that leaves this out removes one the view had.
func (x *AlterViewStatement) WithCheckOption(o CheckOption) *AlterViewStatement {
	c := *x
	c.check = o
	return &c
}

// Build renders the statement.
func (x *AlterViewStatement) Build() (ddl.Script, error) {
	if x.body == nil {
		return nil, ddl.ErrZeroValue
	}
	return buildOne(x.v.Name, func(a *sqlb.Args) string {
		var b strings.Builder
		x.body.WriteSQL(&b, a)
		s := kwAlter + writeViewHead(a, x.algorithm, x.security) + " " + kwView + " " + a.Dialect().QuoteIdent(x.v.Name) +
			writeViewColumns(a, x.columns) + " " + kwAs + " " + b.String()
		switch x.check {
		case 0:
		case Local:
			s += " " + kwWithLocal
		case Cascaded:
			s += " " + kwWithCascaded
		default:
			a.Fail(ErrUnknownKeyword)
		}
		return s
	})
}

// StepSetNew assigns a value to a column of the row being written, the one
// thing only a BEFORE trigger can do: NEW.col is writable there and read-only
// in an AFTER trigger, where MySQL raises ER_TRIGGER_CANT_CHANGE_ROW rather
// than ignoring the assignment.
//
// The value is an ordinary expression, so NewRow and OldRow compose into it:
// StepSetNew(updated, mysql.UTCNow()) stamps a row, and
// StepSetNew(slug, sqlb.Lower(NewRow(title))) derives one column from
// another. It is typed, so the value must have the column's Go type.
func StepSetNew[T any](col sqlb.Expr[T], v sqlb.Expr[T]) TriggerStep {
	return TriggerStep{write: func(b *strings.Builder, a *sqlb.Args) {
		if col.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return
		}
		b.WriteString(kwSet + " " + kwNewRow + "." + a.Dialect().QuoteIdent(col.Name()) + " = " + v.SQL(a))
	}}
}

// ErrBadSQLState is returned for a SIGNAL whose SQLSTATE is not five
// characters, or is class 00, which MySQL reads as success and refuses to
// signal.
var ErrBadSQLState = errors.New("sqlb/mysql: a SIGNAL takes a five-character SQLSTATE outside class 00")

// UserSQLState is the SQLSTATE MySQL's own documentation uses for an
// application-raised error: class 45 is "unhandled user-defined exception"
// and is the one a trigger rejecting a row should raise, because every other
// class means something to the client driver.
const UserSQLState = "45000"

// StepSignal rejects the row with an error, which is how a trigger enforces a
// rule a CHECK constraint cannot express — one that reads another table, say.
// The statement aborts and, inside a transaction, the caller's rollback
// undoes the rest.
func StepSignal(sqlState, message string) TriggerStep {
	return TriggerStep{write: func(b *strings.Builder, a *sqlb.Args) {
		if len(sqlState) != 5 || strings.HasPrefix(sqlState, "00") {
			a.Fail(fmt.Errorf("%w: %q", ErrBadSQLState, sqlState))
			return
		}
		b.WriteString(kwSignal + " " + a.Literal(sqlState) + " " + kwMessageText + " " + a.Literal(message))
	}}
}

// StepRaw is a body statement written out, and it is the documented escape
// hatch for MySQL's procedural control flow: IF ... THEN ... END IF, WHILE,
// LOOP, REPEAT, CASE, DECLARE and a cursor.
//
// Those are a second language, not SQL: they have their own scoping,
// handlers and a local variable namespace, and a builder for them would be a
// compiler for a language this module does not otherwise speak. Every
// construct here renders SQL a dialect can check; none of that machinery
// applies to a procedural block, so building one would give the appearance of
// checking without the substance. The string is written into the statement
// unchanged and nothing validates it, so a value from outside the program
// must never reach it.
//
// A rule expressible as a statement should still be a statement: StepInsert,
// StepUpdate, StepDelete, StepSetNew and StepSignal cover a trigger body that
// needs no branch, and a branch over one statement is often a WHERE.
func StepRaw(sql string) TriggerStep {
	return TriggerStep{write: func(b *strings.Builder, a *sqlb.Args) {
		if strings.TrimSpace(sql) == "" {
			a.Fail(ddl.ErrZeroValue)
			return
		}
		b.WriteString(sql)
	}}
}

// StepBlock is raw procedural text wrapped around typed steps: the opener and
// the closer are the caller's words, and every statement between them is
// still a TriggerStep.
//
// It exists because three StepRaw values cannot express a guarded statement,
// which an end-to-end task found on 2026-09-24 and no construct test could
// have. A body of several steps renders BEGIN s1; s2; END, so every step is
// separated, and a raw step that OPENS a block is not a statement: written as
// three steps the guard renders "IF ... THEN; SIGNAL ...; END IF" and MySQL
// 8.4 answers ER_PARSE_ERROR on the separator after THEN. The next person
// will reach for three StepRaw values exactly as that task did, so the shape
// is recorded here rather than in a commit message.
//
// The separator rule is measured, not assumed, and it is not symmetric. There
// is NO separator after the opener, which is the bug this closes. There IS
// one after every inner statement including the last, because MySQL refuses
// "IF c THEN SET x = 1 END IF" and accepts "IF c THEN SET x = 1; END IF".
//
//	StepBlock("IF NEW.n < 0 THEN", "END IF", StepSignal(UserSQLState, "negative"))
//	// IF NEW.n < 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'negative'; END IF
//
// This models no IF and no WHILE and is not a control-flow builder: MySQL's
// stored-program language stays the caller's, for the reason StepRaw gives.
// What it gives back is that the statements inside a block are typed rather
// than more string, so an INSERT in the body of a loop is checked like any
// other. Nothing validates the opener or the closer, so a value from outside
// the program must never reach either.
func StepBlock(opener, closer string, inner ...TriggerStep) TriggerStep {
	steps := slices.Clone(inner)
	return TriggerStep{write: func(b *strings.Builder, a *sqlb.Args) {
		if strings.TrimSpace(opener) == "" || strings.TrimSpace(closer) == "" || len(steps) == 0 {
			a.Fail(ddl.ErrZeroValue)
			return
		}
		for _, st := range steps {
			if st.write == nil {
				a.Fail(ddl.ErrZeroValue)
				return
			}
		}
		b.WriteString(opener + " ")
		writeBlockSteps(b, a, steps)
		b.WriteString(closer)
	}}
}

// The index options, the trigger's order, the view's algorithm and security,
// and RENAME TABLE's list. Each is a form inside a statement the detector
// already called built, which is exactly the class of gap a `built` entry has
// no way to record; the audit on 2026-09-24 found them by reading MySQL 8.4's
// own HELP output for each statement rather than by memory.
const (
	kwKeyBlockSize   = "KEY_BLOCK_SIZE ="
	kwWithParser     = "WITH PARSER"
	kwIndexComment   = "COMMENT"
	kwSpatial        = "SPATIAL"
	kwFollows        = "FOLLOWS"
	kwPrecedes       = "PRECEDES"
	kwViewAlgorithm  = "ALGORITHM ="
	kwSQLSecurity    = "SQL SECURITY"
	kwCreateSpatial  = "CREATE SPATIAL INDEX"
	kwDefinerKeyword = "DEFINER"
	kwInvokerKeyword = "INVOKER"
)

// IndexOption is one of MySQL's index options, which CREATE INDEX and an
// inline key both take. The set is closed to the four that describe the index
// itself; ENGINE_ATTRIBUTE and SECONDARY_ENGINE_ATTRIBUTE are left out
// because each is a JSON blob whose meaning belongs to a storage engine this
// module does not target.
type IndexOption struct {
	render func(a *sqlb.Args) string
}

// IndexComment attaches a comment to the index, which SHOW INDEX reports. It
// is the one place a reason for an index can live beside the index.
func IndexComment(text string) IndexOption {
	return IndexOption{render: func(a *sqlb.Args) string { return kwIndexComment + " " + a.Literal(text) }}
}

// IndexKeyBlockSize suggests the index's page size in kilobytes. InnoDB takes
// it only on a COMPRESSED table and warns otherwise, which is why it is an
// option rather than a default.
func IndexKeyBlockSize(kb int) IndexOption {
	return IndexOption{render: func(a *sqlb.Args) string {
		if kb < 1 {
			a.Fail(ErrUnknownKeyword)
			return ""
		}
		return kwKeyBlockSize + " " + strconv.Itoa(kb)
	}}
}

// IndexWithParser names the full-text parser plugin for a FULLTEXT index, the
// clause a language needing its own word breaking requires. MySQL refuses it
// on any other kind of index.
func IndexWithParser(name string) IndexOption {
	return IndexOption{render: func(a *sqlb.Args) string {
		if name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		return kwWithParser + " " + a.Dialect().QuoteIdent(name)
	}}
}

// IndexInvisible creates the index hidden from the optimizer, which is how an
// index is added and measured before any query is allowed to depend on it.
// SetIndexInvisible is the ALTER TABLE counterpart.
func IndexInvisible() IndexOption {
	return IndexOption{render: func(*sqlb.Args) string { return kwInvisible }}
}

// IndexVisible creates the index available to the optimizer, which is the
// default; it exists so the visibility can be written out rather than implied.
func IndexVisible() IndexOption {
	return IndexOption{render: func(*sqlb.Args) string { return kwVisible }}
}

// writeIndexOptions renders the options, refusing a zero one.
func writeIndexOptions(a *sqlb.Args, opts []IndexOption) string {
	var b strings.Builder
	for _, o := range opts {
		if o.render == nil {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		b.WriteString(" " + o.render(a))
	}
	return b.String()
}

// Spatial makes a SPATIAL index, which MySQL requires over a NOT NULL
// geometry column and which the geometry predicates use. It is the third kind
// beside Unique and Fulltext.
func (x *CreateIndexQuery) Spatial() *CreateIndexQuery {
	c := x.core.derive()
	c.kind = kwCreateSpatial
	return &CreateIndexQuery{core: c}
}

// With adds index options.
func (x *CreateIndexReady) With(first IndexOption, more ...IndexOption) *CreateIndexReady {
	c := x.core.derive()
	c.options = append(append(c.options, first), more...)
	return &CreateIndexReady{core: c}
}

// Algorithm says how MySQL must build the index and fails when it cannot be
// done that way; see the ALTER TABLE clause of the same name.
func (x *CreateIndexReady) Algorithm(v AlterAlgorithm) *CreateIndexReady {
	c := x.core.derive()
	c.algorithm = v
	return &CreateIndexReady{core: c}
}

// Lock says how much concurrent access building the index must allow.
func (x *CreateIndexReady) Lock(v AlterLock) *CreateIndexReady {
	c := x.core.derive()
	c.lock = v
	return &CreateIndexReady{core: c}
}

// Algorithm says how MySQL must drop the index. CREATE INDEX and DROP INDEX
// are both mapped to ALTER TABLE by the server, which is why both take it.
func (x *DropIndexStatement) Algorithm(v AlterAlgorithm) *DropIndexStatement {
	c := *x
	c.algorithm = v
	return &c
}

// Lock says how much concurrent access dropping the index must allow.
func (x *DropIndexStatement) Lock(v AlterLock) *DropIndexStatement {
	c := *x
	c.lock = v
	return &c
}

// writeHow renders the ALGORITHM and LOCK clauses CREATE INDEX and DROP INDEX
// share, each omitted when it is the zero value.
//
// They are separated by a space and not by a comma. ALTER TABLE takes its
// algorithm and lock as ordinary alter_options, which are comma-separated, so
// the first version of this wrote a comma here too; MySQL 8.4 answers that
// with ER_PARSE_ERROR, and no rendering test could have known.
func writeHow(a *sqlb.Args, alg AlterAlgorithm, lock AlterLock) string {
	out := ""
	if alg != "" {
		if !alg.valid() || alg == AlgorithmInstant {
			// INSTANT is an ALTER TABLE algorithm only: MySQL has no instant
			// index build, and accepting it here would render a statement the
			// server always refuses.
			a.Fail(fmt.Errorf("%w: index algorithm %q", ErrUnknownKeyword, string(alg)))
			return ""
		}
		out += " " + kwAlgorithm + " " + string(alg)
	}
	if lock != "" {
		if !lock.valid() {
			a.Fail(fmt.Errorf("%w: index lock %q", ErrUnknownKeyword, string(lock)))
			return ""
		}
		out += " " + kwLockOption + " " + string(lock)
	}
	return out
}

// Follows makes this trigger run after other, and Precedes before it. MySQL
// fires several triggers of one timing and event in creation order unless one
// says otherwise, so a trigger added later that must run first needs this;
// without it the order is the order somebody happened to install them in.
func (x *TimedTriggerQuery) Follows(other Trigger) *TimedTriggerQuery {
	c := *x
	c.order, c.orderKw = other, kwFollows
	return &c
}

// Precedes makes this trigger run before other.
func (x *TimedTriggerQuery) Precedes(other Trigger) *TimedTriggerQuery {
	c := *x
	c.order, c.orderKw = other, kwPrecedes
	return &c
}

// ViewAlgorithmKind is how MySQL processes a view.
type ViewAlgorithmKind string

// The view algorithms.
const (
	// ViewMerge rewrites a query against the view into one against its
	// tables, so the outer WHERE reaches the indexes. It is what makes a view
	// free, and MySQL cannot use it for a view with an aggregate, DISTINCT,
	// LIMIT, UNION or a window function.
	ViewMerge ViewAlgorithmKind = "MERGE"
	// ViewTemptable materialises the view into a temporary table first, which
	// has no index and is therefore the slow path — asking for it explicitly
	// is how a view is kept from being merged into a query that would lock
	// its tables for longer.
	ViewTemptable ViewAlgorithmKind = "TEMPTABLE"
	// ViewUndefined lets MySQL choose, which is the default.
	ViewUndefined ViewAlgorithmKind = "UNDEFINED"
)

func (v ViewAlgorithmKind) valid() bool {
	switch v {
	case ViewMerge, ViewTemptable, ViewUndefined:
		return true
	}
	return false
}

// ViewSecurity is whose privileges MySQL checks when a view is read.
type ViewSecurity int

// The security contexts.
const (
	// ViewDefiner checks the view's definer, which is MySQL's default and is
	// how a view grants narrowed access to a table the reader cannot select
	// from directly.
	ViewDefiner ViewSecurity = iota + 1
	// ViewInvoker checks the reader's own privileges, so the view grants
	// nothing the reader did not already have.
	ViewInvoker
)

func (v ViewSecurity) keyword() (string, bool) {
	switch v {
	case ViewDefiner:
		return kwDefinerKeyword, true
	case ViewInvoker:
		return kwInvokerKeyword, true
	}
	return "", false
}

// writeViewHead renders the ALGORITHM and SQL SECURITY clauses, which sit
// between the leading keyword and VIEW in both CREATE VIEW and ALTER VIEW.
func writeViewHead(a *sqlb.Args, alg ViewAlgorithmKind, sec ViewSecurity) string {
	out := ""
	if alg != "" {
		if !alg.valid() {
			a.Fail(fmt.Errorf("%w: view algorithm %q", ErrUnknownKeyword, string(alg)))
			return ""
		}
		out += " " + kwViewAlgorithm + " " + string(alg)
	}
	if sec != 0 {
		kw, ok := sec.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: view security", ErrUnknownKeyword))
			return ""
		}
		out += " " + kwSQLSecurity + " " + kw
	}
	return out
}

// writeViewColumns renders the view's column list, which renames the query's
// columns; MySQL requires as many names as the query has columns and checks
// that when the view is created.
func writeViewColumns(a *sqlb.Args, names []string) string {
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

// Algorithm picks how MySQL processes the view.
func (x *CreateViewQuery) Algorithm(v ViewAlgorithmKind) *CreateViewQuery {
	c := *x
	c.algorithm = v
	return &c
}

// Security picks whose privileges are checked when the view is read.
func (x *CreateViewQuery) Security(v ViewSecurity) *CreateViewQuery {
	c := *x
	c.security = v
	return &c
}

// Columns renames the query's columns, which is how a view exposes a stable
// set of names over a query whose expressions change.
func (x *CreateViewQuery) Columns(first string, more ...string) *CreateViewQuery {
	c := *x
	c.columns = append(append(slices.Clone(x.columns), first), more...)
	return &c
}

// Algorithm picks how MySQL processes the view.
func (x *AlterViewQuery) Algorithm(v ViewAlgorithmKind) *AlterViewQuery {
	c := *x
	c.algorithm = v
	return &c
}

// Security picks whose privileges are checked when the view is read.
func (x *AlterViewQuery) Security(v ViewSecurity) *AlterViewQuery {
	c := *x
	c.security = v
	return &c
}

// Columns renames the query's columns.
func (x *AlterViewQuery) Columns(first string, more ...string) *AlterViewQuery {
	c := *x
	c.columns = append(append(slices.Clone(x.columns), first), more...)
	return &c
}

// And adds another rename to the same statement, which is the whole reason
// RENAME TABLE exists beside ALTER TABLE ... RENAME TO: MySQL performs every
// rename in one statement atomically and under one lock, so
// RenameTable(live, old).And(new, live) swaps two tables with no instant in
// which the name is missing. Two statements cannot do that.
func (x *RenameTableStatement) And(from, to sqlb.Table) *RenameTableStatement {
	c := *x
	c.more = append(slices.Clone(x.more), renamePair{from: from, to: to})
	return &c
}
