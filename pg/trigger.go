package pg

import (
	"errors"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

const (
	kwCreateFunction          = "CREATE FUNCTION"
	kwCreateOrReplaceFunction = "CREATE OR REPLACE FUNCTION"
	kwReturnsTrigger          = "RETURNS trigger LANGUAGE plpgsql AS"
	kwDropFunction            = "DROP FUNCTION"
	kwCreateTrigger           = "CREATE TRIGGER"
	kwCreateOrReplaceTrigger  = "CREATE OR REPLACE TRIGGER"
	kwDropTrigger             = "DROP TRIGGER"
	kwAlterTrigger            = "ALTER TRIGGER"
	kwBeforeTrigger           = "BEFORE"
	kwAfterTrigger            = "AFTER"
	kwOr                      = "OR"
	kwForEachRow              = "FOR EACH ROW"
	kwForEachStatement        = "FOR EACH STATEMENT"
	kwWhen                    = "WHEN"
	kwExecuteFunction         = "EXECUTE FUNCTION"
	kwOld                     = "OLD"
	kwNew                     = "NEW"
)

// ErrEmptyBody is returned for a trigger function with no body.
var ErrEmptyBody = errors.New("sqlb/pg: a function needs a body")

// Function is a Postgres function, declared once as a value and named by every
// statement that creates, drops, comments on or executes it.
//
// Params is part of the declaration rather than of one statement because a
// function's argument types are part of its name: Postgres allows several
// functions to share a bare name, so DROP FUNCTION, COMMENT ON FUNCTION and
// ALTER EXTENSION each identify one by its signature. Declaring the parameters
// once is what keeps those statements from having to be told again, and a
// trigger function simply leaves it empty, which is the one signature Postgres
// fixes for you.
type Function struct {
	Schema, Name string
	Params       []FunctionArg
}

// signature renders the parenthesised argument list, which is part of the name
// every statement but CREATE writes; a function with no parameters renders the
// empty list, which is how a trigger function is named.
func (f Function) signature(d sqlb.Dialect) (string, error) {
	parts := make([]string, len(f.Params))
	for i, p := range f.Params {
		s, err := p.write(d)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, ", ") + ")", nil
}

func (f Function) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	sig, err := f.signature(d)
	if err != nil {
		return "", "", "", err
	}
	return kwObjFunction, qualified(d, f.Schema, f.Name) + sig, f.Name, nil
}

// CreateFunctionStatement is CREATE FUNCTION ... RETURNS trigger.
type CreateFunctionStatement struct {
	f         Function
	body      string
	orReplace bool
}

// CreateTriggerFunction creates a PL/pgSQL trigger function with body, which
// is a program rather than an expression and so the one place a whole body
// is a string: the library author writes it, it is never built from input,
// and it is dollar-quoted with a tag it does not contain.
func CreateTriggerFunction(f Function, body string) *CreateFunctionStatement {
	return &CreateFunctionStatement{f: f, body: body}
}

// OrReplace replaces an existing function of the name, which is the
// idempotent form; Postgres has no CREATE FUNCTION IF NOT EXISTS.
func (x *CreateFunctionStatement) OrReplace() *CreateFunctionStatement {
	c := *x
	c.orReplace = true
	return &c
}

// Build renders the statement.
func (x *CreateFunctionStatement) Build() (ddl.Script, error) {
	if strings.TrimSpace(x.body) == "" {
		return nil, ErrEmptyBody
	}
	if len(x.f.Params) > 0 {
		return nil, ErrTriggerSignature
	}
	return buildOne(x.f.Name, true, func(a *sqlb.Args) string {
		kw := kwCreateFunction
		if x.orReplace {
			kw = kwCreateOrReplaceFunction
		}
		tag := dollarTag(x.body)
		return kw + " " + qualified(a.Dialect(), x.f.Schema, x.f.Name) + "() " + kwReturnsTrigger + " " + tag + x.body + tag
	})
}

// DropFunction drops a function, naming it by the signature its declaration
// carries, which is what tells two functions of one name apart. It fails while
// a trigger or another function uses it unless Cascade is given.
func DropFunction(f Function) *DropObjectStatement {
	sig, err := f.signature(Dialect{})
	return &DropObjectStatement{kw: kwDropFunction, schema: f.Schema, name: f.Name, suffix: sig, err: err}
}

// Trigger is a trigger on one table. Its name is unique per table, not per
// schema, which is why the table is part of the value.
type Trigger struct {
	Name  string
	Table sqlb.Table
}

func (tr Trigger) commentTarget(d sqlb.Dialect) (string, string, string, error) {
	return kwObjTrigger, d.QuoteIdent(tr.Name) + " " + kwOn + " " + tr.Table.Qualified(d), tr.Name, nil
}

// TriggerEvent is what fires a trigger: an INSERT, an UPDATE, an UPDATE of
// named columns, or a DELETE.
type TriggerEvent struct {
	kw   string
	cols []ddl.ColumnRef
}

// OnInsert fires on INSERT.
func OnInsert() TriggerEvent { return TriggerEvent{kw: "INSERT"} }

// OnUpdate fires on any UPDATE.
func OnUpdate() TriggerEvent { return TriggerEvent{kw: "UPDATE"} }

// OnUpdateOf fires on an UPDATE whose SET names one of cols. A generated
// column cannot be named: it is never in a SET.
func OnUpdateOf(first ddl.ColumnRef, more ...ddl.ColumnRef) TriggerEvent {
	return TriggerEvent{kw: "UPDATE", cols: append([]ddl.ColumnRef{first}, more...)}
}

// OnDelete fires on DELETE.
func OnDelete() TriggerEvent { return TriggerEvent{kw: "DELETE"} }

// Old is a column's value before the change, in a row trigger's WHEN.
func Old[T any](col sqlb.Expr[T]) sqlb.Expr[T] { return rowRef(kwOld, col) }

// New is a column's value after the change, in a row trigger's WHEN.
func New[T any](col sqlb.Expr[T]) sqlb.Expr[T] { return rowRef(kwNew, col) }

func rowRef[T any](which string, col sqlb.Expr[T]) sqlb.Expr[T] {
	return expr[T](constructOps, func(a *sqlb.Args) string {
		if col.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return ""
		}
		return which + "." + a.Dialect().QuoteIdent(col.Name())
	})
}

// triggerCore is what every stage of CREATE TRIGGER shares.
type triggerCore struct {
	tr        Trigger
	orReplace bool
	// constraint makes it a constraint trigger, which is the only kind that
	// can be deferred; from names the other table such a trigger points at.
	constraint bool
	from       sqlb.Table
	deferral   Deferral
	timing     string
	events     []TriggerEvent
	forEach    string
	// oldTable and newTable are a statement trigger's transition tables,
	// which are the only way it can see the rows the statement changed.
	oldTable, newTable string
	when               sqlb.Predicate
}

func (c triggerCore) derive() triggerCore {
	c.events = slices.Clone(c.events)
	return c
}

// CreateTriggerQuery is CREATE TRIGGER with no timing yet, which has no Build.
type CreateTriggerQuery struct{ core triggerCore }

// CreateTrigger starts a trigger.
func CreateTrigger(tr Trigger) *CreateTriggerQuery {
	return &CreateTriggerQuery{core: triggerCore{tr: tr, forEach: kwForEachStatement}}
}

// OrReplace replaces an existing trigger of the name on the table, the
// idempotent form since Postgres 14.
func (x *CreateTriggerQuery) OrReplace() *CreateTriggerQuery {
	c := x.core.derive()
	c.orReplace = true
	return &CreateTriggerQuery{core: c}
}

// Before fires the trigger before the change, where a row trigger can modify
// NEW or return NULL to skip the row.
func (x *CreateTriggerQuery) Before(first TriggerEvent, more ...TriggerEvent) *TimedTriggerQuery {
	return x.timed(kwBeforeTrigger, first, more)
}

// After fires the trigger after the change.
func (x *CreateTriggerQuery) After(first TriggerEvent, more ...TriggerEvent) *TimedTriggerQuery {
	return x.timed(kwAfterTrigger, first, more)
}

func (x *CreateTriggerQuery) timed(kw string, first TriggerEvent, more []TriggerEvent) *TimedTriggerQuery {
	c := x.core.derive()
	c.timing = kw
	c.events = append(append(c.events, first), more...)
	return &TimedTriggerQuery{core: c}
}

// TimedTriggerQuery is CREATE TRIGGER with its timing and events and no
// function yet, which has no Build.
type TimedTriggerQuery struct{ core triggerCore }

// ForEachRow fires once per row changed, the form that sees OLD and NEW.
// Without it the trigger fires once per statement.
func (x *TimedTriggerQuery) ForEachRow() *TimedTriggerQuery {
	c := x.core.derive()
	c.forEach = kwForEachRow
	return &TimedTriggerQuery{core: c}
}

// When fires the trigger only for rows matching p, written with Old and New.
func (x *TimedTriggerQuery) When(p sqlb.Predicate) *TimedTriggerQuery {
	c := x.core.derive()
	c.when = p
	return &TimedTriggerQuery{core: c}
}

// Execute names the trigger function, and so completes the statement.
func (x *TimedTriggerQuery) Execute(f Function) *CreateTriggerStatement {
	return &CreateTriggerStatement{core: x.core.derive(), fn: f}
}

// CreateTriggerStatement is a complete CREATE TRIGGER.
type CreateTriggerStatement struct {
	core triggerCore
	fn   Function
	// args are the constant arguments the function reads out of TG_ARGV.
	args []string
}

// Build renders the statement.
func (x *CreateTriggerStatement) Build() (ddl.Script, error) {
	c := x.core
	for _, e := range c.events {
		for _, r := range e.cols {
			if r.Name() == "" {
				return nil, ddl.ErrNotAColumn
			}
			if !onTable(r, c.tr.Table) {
				return nil, ddl.ErrForeignColumn
			}
		}
	}
	if x.fn.Name == "" || c.tr.Table.Name == "" {
		return nil, ErrEmptyName
	}
	// A trigger's function takes no arguments in the statement, so a declared
	// parameter would be dropped in silence rather than rendered.
	if len(x.fn.Params) > 0 {
		return nil, ErrTriggerSignature
	}
	return buildOne(c.tr.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		kw := kwCreateTrigger
		if c.orReplace {
			kw = kwCreateOrReplaceTrigger
		}
		if c.constraint {
			kw = strings.TrimSuffix(kw, " "+kwObjTrigger) + " " + kwConstraintTr + " " + kwObjTrigger
		}
		evs := make([]string, len(c.events))
		for i, e := range c.events {
			evs[i] = e.kw
			if len(e.cols) > 0 {
				names := make([]string, len(e.cols))
				for j, col := range e.cols {
					names[j] = d.QuoteIdent(col.Name())
				}
				evs[i] += " " + kwOf + " " + strings.Join(names, ", ")
			}
		}
		s := kw + " " + d.QuoteIdent(c.tr.Name) + " " + c.timing + " " + strings.Join(evs, " "+kwOr+" ") +
			" " + kwOn + " " + c.tr.Table.Qualified(d)
		if c.from.Name != "" {
			s += " " + kwFromTable + " " + c.from.Qualified(d)
		}
		if c.deferral != 0 {
			kw, ok := c.deferral.keyword()
			s += " " + keywordOf(a, kw, ok, "Deferrable")
		}
		if c.oldTable != "" || c.newTable != "" {
			s += " " + kwReferencing
			if c.oldTable != "" {
				s += " " + kwOldTable + " " + d.QuoteIdent(c.oldTable)
			}
			if c.newTable != "" {
				s += " " + kwNewTable + " " + d.QuoteIdent(c.newTable)
			}
		}
		s += " " + c.forEach
		if c.when != nil {
			w := a.Bare(func() string {
				var b strings.Builder
				c.when.WriteSQL(&b, a)
				return b.String()
			})
			s += " " + kwWhen + " (" + w + ")"
		}
		args := make([]string, len(x.args))
		for i, v := range x.args {
			args[i] = a.Literal(v)
		}
		return s + " " + kwExecuteFunction + " " + qualified(d, x.fn.Schema, x.fn.Name) + "(" + strings.Join(args, ", ") + ")"
	})
}

// DropTriggerStatement is DROP TRIGGER ... ON.
type DropTriggerStatement struct {
	tr                          Trigger
	ifExists, cascade, restrict bool
}

// DropTrigger drops a trigger from its table.
func DropTrigger(tr Trigger) *DropTriggerStatement { return &DropTriggerStatement{tr: tr} }

// IfExists makes dropping a missing trigger a no-op; the table must exist.
func (x *DropTriggerStatement) IfExists() *DropTriggerStatement {
	c := *x
	c.ifExists = true
	return &c
}

// Build renders the statement.
func (x *DropTriggerStatement) Build() (ddl.Script, error) {
	return buildOne(x.tr.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwDropTrigger + ifExists(x.ifExists) + " " + d.QuoteIdent(x.tr.Name) + " " + kwOn + " " + x.tr.Table.Qualified(d) +
			cascade(x.cascade) + restrict(x.restrict)
	})
}

// RenameTriggerStatement is ALTER TRIGGER ... RENAME TO.
type RenameTriggerStatement struct{ from, to Trigger }

// RenameTrigger renames a trigger on its table; to names the same table.
func RenameTrigger(from, to Trigger) *RenameTriggerStatement {
	return &RenameTriggerStatement{from: from, to: to}
}

// Build renders the statement.
func (x *RenameTriggerStatement) Build() (ddl.Script, error) {
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	if x.to.Table.Name != x.from.Table.Name || x.to.Table.Schema != x.from.Table.Schema {
		return nil, ddl.ErrForeignColumn
	}
	return buildOne(x.from.Name, true, func(a *sqlb.Args) string {
		d := a.Dialect()
		return kwAlterTrigger + " " + d.QuoteIdent(x.from.Name) + " " + kwOn + " " + x.from.Table.Qualified(d) + " " + kwRenameTo + " " + d.QuoteIdent(x.to.Name)
	})
}
