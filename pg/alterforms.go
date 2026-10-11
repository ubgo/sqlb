package pg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The ALTER forms the DDL detector recorded as missing until 2026-09-24.
//
// Each one here is a migration a library owning its own tables writes: the
// inverse of a form that was already built (DropIdentity against AddIdentity,
// DropExpression against ddl.Generated), the one step that finishes a
// zero-downtime index migration (AddUniqueUsingIndex after a CONCURRENTLY
// build), or the move into a library's own schema (SetObjectSchema). What is
// still missing is what an operator writes: tablespaces, storage parameters,
// ownership and roles, which a library cannot know the names of.

const (
	kwSetExpressionAs  = "SET EXPRESSION AS"
	kwDropExpression   = "DROP EXPRESSION"
	kwDropIdentity     = "DROP IDENTITY"
	kwSetGenerated     = "SET GENERATED"
	kwRestart          = "RESTART"
	kwUsingIndex       = "USING INDEX"
	kwSetLogged        = "SET LOGGED"
	kwSetUnlogged      = "SET UNLOGGED"
	kwReset            = "RESET"
	kwAlterObject      = "ALTER"
	optCheckOption     = "check_option"
	optSecurityBarrier = "security_barrier"
	optSecurityInvoker = "security_invoker"
	optLocal           = "local"
	optCascaded        = "cascaded"
	optTrue            = "true"
	optFalse           = "false"
)

// SetExpression changes a generated column's expression to e, an expression of
// the column's own Go type, so a column of another type does not compile.
// Postgres 17 and later. It rewrites the table under a lock that blocks reads,
// which is why the alternative on a large table is to add a second column and
// drop the first.
func SetExpression[T any](col, e sqlb.Expr[T]) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string {
		return kwSetExpressionAs + " (" + a.Bare(func() string { return e.SQL(a) }) + ")"
	})
}

// DropColumnPartClause is an ALTER COLUMN action that removes something a
// column carries and takes IF EXISTS: DROP EXPRESSION and DROP IDENTITY. Both
// keep the column and the values already in it, and both are one-way, so IF
// EXISTS is what makes the migration safe to run twice.
type DropColumnPartClause struct {
	alterAction
	kw       string
	ifExists bool
}

// DropExpression turns a generated column into a plain one, keeping the values
// it holds; from then on the column is written like any other.
func DropExpression[T any](col sqlb.Expr[T]) DropColumnPartClause {
	return DropColumnPartClause{alterAction: alterAction{cols: []ddl.ColumnRef{col}}, kw: kwDropExpression}
}

// DropIdentity stops a column generating values, keeping those it holds and
// dropping the sequence behind it. It is the inverse of AddIdentity.
func DropIdentity[T identityInteger](col sqlb.Expr[T]) DropColumnPartClause {
	return DropColumnPartClause{alterAction: alterAction{cols: []ddl.ColumnRef{col}}, kw: kwDropIdentity}
}

// IfExists makes the action a no-op on a column that carries nothing to drop,
// so a migration re-run is not an error.
func (x DropColumnPartClause) IfExists() DropColumnPartClause { x.ifExists = true; return x }

// WriteSQL renders the action.
func (x DropColumnPartClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if x.kw == "" || len(x.cols) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	col := x.cols[0]
	if col.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	b.WriteString(kwAlterColumn + " " + a.Dialect().QuoteIdent(col.Name()) + " " + x.kw)
	if x.ifExists {
		b.WriteString(" " + kwIfExists)
	}
}

// Standalone reports that the action may share a statement.
func (DropColumnPartClause) Standalone() bool { return false }

// AlterClauseOf marks the action as Postgres's.
func (DropColumnPartClause) AlterClauseOf(Dialect) {}

// SetIdentityKind changes whether an identity column refuses an explicit
// value, between Always and ByDefault. It is the migration a library writes
// when a caller must be allowed to supply its own key, and it changes nothing
// about the sequence or the values already stored.
func SetIdentityKind[T identityInteger](col sqlb.Expr[T], k IdentityKind) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string {
		kw, ok := k.keyword()
		return kwSetGenerated + " " + keywordOf(a, kw, ok, "SetIdentityKind")
	})
}

// RestartIdentityClause is the ALTER COLUMN action that sets an identity
// column's sequence back to its start value, or to With's.
type RestartIdentityClause[T identityInteger] struct {
	col sqlb.Expr[T]
	at  *T
}

// RestartIdentity restarts an identity column's sequence from the value the
// column was declared with. Use it after loading rows with explicit keys,
// where the sequence is behind the data and the next insert would collide.
func RestartIdentity[T identityInteger](col sqlb.Expr[T]) RestartIdentityClause[T] {
	return RestartIdentityClause[T]{col: col}
}

// With restarts from n instead, a value of the column's own Go type.
func (x RestartIdentityClause[T]) With(n T) RestartIdentityClause[T] { x.at = &n; return x }

// WriteSQL renders the action.
func (x RestartIdentityClause[T]) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	onColumn(x.col, func(a *sqlb.Args) string {
		if x.at == nil {
			return kwRestart
		}
		return kwRestartWith + " " + a.Literal(int64(*x.at))
	}).WriteSQL(b, a)
}

// Standalone reports that the action may share a statement.
func (RestartIdentityClause[T]) Standalone() bool { return false }

// AlterClauseOf marks the action as Postgres's.
func (RestartIdentityClause[T]) AlterClauseOf(Dialect) {}

// NamedColumns is the column being restarted.
func (x RestartIdentityClause[T]) NamedColumns() []ddl.ColumnRef { return []ddl.ColumnRef{x.col} }

// AddUniqueUsingIndex promotes an existing unique index to a named unique
// constraint. It is the second half of a zero-downtime migration: build the
// index with CreateIndex(...).Concurrently() outside a transaction, which
// scans without blocking writes, then attach it here, which takes the strong
// lock for the length of a catalogue update rather than a table scan. Postgres
// renames the index to the constraint's name, so declare the constraint and
// the index with the same name to keep both declarations true.
func AddUniqueUsingIndex(c TableConstraint, ix Index) AlterClause {
	return addUsingIndex(c, ix, kwUnique)
}

// AddPrimaryKeyUsingIndex promotes an existing unique index to the table's
// primary key, the same zero-downtime step as AddUniqueUsingIndex. Every
// column of the index must already be NOT NULL, which Postgres checks.
func AddPrimaryKeyUsingIndex(c TableConstraint, ix Index) AlterClause {
	return addUsingIndex(c, ix, kwPrimaryKey)
}

// ErrIndexSchema is returned when the Index handed to AddUniqueUsingIndex or
// AddPrimaryKeyUsingIndex declares a schema its table does not, which is a
// declaration the statement cannot honour: the index name is written bare, so
// a schema set here would otherwise be dropped in silence.
var ErrIndexSchema = errors.New("sqlb/pg: the index and its table are declared in different schemas")

// addUsingIndex renders ADD CONSTRAINT ... USING INDEX, naming the index bare.
//
// Bare is not a simplification, it is the only legal form, and the reason is
// worth keeping beside it: Postgres's grammar takes a plain name for an index
// both here and in CREATE INDEX, so an index cannot be created anywhere but
// its table's schema and a qualified name is a syntax error at the dot.
// Measured on 17.10, and so is the resolution rule that follows from it: with
// the index in schema qa, search_path set to qb and the table written qa.t,
// ALTER TABLE qa.t ADD CONSTRAINT ... USING INDEX ix found it and raised its
// rename notice. The bare name is therefore resolved in the table's schema,
// never through search_path, so qualifying the table is enough.
//
// Because there is nowhere to write it, an Index.Schema that disagrees with
// the table's is refused rather than dropped. The comparison is between the
// two declarations as written, not between resolved schemas, which is how the
// empty-table-schema case is resolved: a table naming no schema is resolved
// through search_path when the statement runs, so an index that names one is
// making a claim this package cannot check, and the safe reading of an
// unverifiable claim is to refuse it. Declare the schema on both values or on
// neither; matching values, "public" and "public" or "" and "", build.
func addUsingIndex(c TableConstraint, ix Index, kind string) AlterClause {
	return alterAction{table: c.Table, sql: func(a *sqlb.Args) string {
		if !c.Valid() {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		if ix.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		if ix.Schema != c.Table.Schema {
			a.Fail(fmt.Errorf("%w: index %q is declared in %q and table %q in %q",
				ErrIndexSchema, ix.Name, ix.Schema, c.Table.Name, c.Table.Schema))
			return ""
		}
		d := a.Dialect()
		return kwAdd + " " + kwConstraint + " " + d.QuoteIdent(c.Name) + " " + kind + " " + kwUsingIndex + " " + d.QuoteIdent(ix.Name)
	}}
}

// SetType writes AS, changing the sequence's integer type to T's. Declare the
// sequence with its new Go type, Sequence[int64] where it was Sequence[int32],
// and this migration follows from the declaration rather than from a string.
// Postgres adjusts MINVALUE and MAXVALUE to the new type's range unless the
// same statement sets them.
func (x *AlterSequenceStatement[T]) SetType() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwAs + " " + sqlTypeBySize[T]() })
}

// StartWith changes the value RESTART with no argument goes back to. It does
// not move the sequence: Restart does that.
func (x *AlterSequenceStatement[T]) StartWith(n T) *AlterSequenceStatement[T] {
	return x.with(literalOption(kwStartWith, int64(n)))
}

// SetLogged makes the sequence crash-safe again, the default. Postgres 15 and
// later.
func (x *AlterSequenceStatement[T]) SetLogged() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwSetLogged })
}

// SetUnlogged stops writing the sequence to the write-ahead log, which is
// faster and resets it to its start value after a crash. It is for a sequence
// whose gaps and losses do not matter, and a logged table's identity column
// may not use one. Postgres 15 and later.
func (x *AlterSequenceStatement[T]) SetUnlogged() *AlterSequenceStatement[T] {
	return x.with(func(*sqlb.Args) string { return kwSetUnlogged })
}

// SetType starts an ALTER SEQUENCE writing AS the sequence's own integer type.
func (x *AlterSequenceQuery[T]) SetType() *AlterSequenceStatement[T] { return x.start().SetType() }

// StartWith starts an ALTER SEQUENCE changing the start value.
func (x *AlterSequenceQuery[T]) StartWith(n T) *AlterSequenceStatement[T] {
	return x.start().StartWith(n)
}

// SetLogged starts an ALTER SEQUENCE making the sequence crash-safe.
func (x *AlterSequenceQuery[T]) SetLogged() *AlterSequenceStatement[T] { return x.start().SetLogged() }

// SetUnlogged starts an ALTER SEQUENCE making the sequence unlogged.
func (x *AlterSequenceQuery[T]) SetUnlogged() *AlterSequenceStatement[T] {
	return x.start().SetUnlogged()
}

// Movable is a declared object SET SCHEMA can move: a view, a materialized
// view, a sequence, an enum, a composite type or a domain. The set is closed
// to this package's object values, and the object kind comes from the Go type,
// so the statement cannot say VIEW of a sequence. A table moves with
// AlterTable's SetSchema, because ALTER TABLE takes the rest of its actions
// beside it; a schema and an extension cannot move at all.
type Movable interface {
	// commentTarget is the object's kind and qualified name, the same pair
	// COMMENT ON writes, which is why Movable does not embed Commentable: an
	// embedded name is claimed by this file in the generated reference.
	commentTarget(d sqlb.Dialect) (kind, qualified, bare string, err error)
	movable()
}

func (View) movable()             {}
func (MaterializedView) movable() {}
func (Sequence[T]) movable()      {}
func (Enum[T]) movable()          {}
func (Composite[T]) movable()     {}
func (Domain[T]) movable()        {}

// SetSchemaStatement is the SET SCHEMA statement of one declared object, whose
// ALTER keyword names the object kind.
type SetSchemaStatement struct {
	o  Movable
	to Schema
}

// SetObjectSchema moves a declared object into another schema, the migration a
// library writes when it stops living in the search path and takes a schema of
// its own. Every value naming the object, and every Table whose columns it
// types, must carry the new schema in the same release, which is why the
// object is a value declared once.
func SetObjectSchema(o Movable, to Schema) *SetSchemaStatement {
	return &SetSchemaStatement{o: o, to: to}
}

// Build renders the statement.
func (x *SetSchemaStatement) Build() (ddl.Script, error) {
	if x.o == nil {
		return nil, ddl.ErrZeroValue
	}
	if x.to.Name == "" {
		return nil, ErrEmptyName
	}
	kind, name, bare, err := x.o.commentTarget(Dialect{})
	if err != nil {
		return nil, err
	}
	return buildOne(bare, true, func(a *sqlb.Args) string {
		return kwAlterObject + " " + kind + " " + name + " " + kwSetSchema + " " + a.Dialect().QuoteIdent(x.to.Name)
	})
}

// ViewOption is a storage option of a view, the closed set Postgres defines
// for one. It names an option for Reset; the setters take the option's own
// value type, so a boolean option cannot be given a check option.
type ViewOption int

// The view options. ViewCheckOption is what WithCheckOption writes at CREATE
// VIEW; the two security options decide whose privileges the view's own query
// runs with, which is what an RLS-aware view turns on.
const (
	ViewCheckOption ViewOption = iota + 1
	ViewSecurityBarrier
	ViewSecurityInvoker
)

func (o ViewOption) name() (string, bool) {
	switch o {
	case ViewCheckOption:
		return optCheckOption, true
	case ViewSecurityBarrier:
		return optSecurityBarrier, true
	case ViewSecurityInvoker:
		return optSecurityInvoker, true
	}
	return "", false
}

// optionValue is a check option as a reloption's value, which is the bare word
// rather than the WITH ... CHECK OPTION clause keyword returns.
func (o CheckOption) optionValue() (string, bool) {
	switch o {
	case Local:
		return optLocal, true
	case Cascaded:
		return optCascaded, true
	}
	return "", false
}

// ViewSetting is one of a view's options with its value: the closed set of
// names Postgres defines for a view, each taking its own value type, so a
// boolean option cannot be given a check option. It exists as a value rather
// than only as a method because CREATE VIEW and ALTER VIEW both take the same
// options, and two spellings of one option list is the thing that drifts.
type ViewSetting struct{ write func(a *sqlb.Args) string }

// CheckOptionSetting refuses a write through the view that the view would not
// show, the option CreateView also writes as WITH ... CHECK OPTION.
func CheckOptionSetting(o CheckOption) ViewSetting {
	return ViewSetting{write: func(a *sqlb.Args) string {
		v, ok := o.optionValue()
		return optCheckOption + " = " + keywordOf(a, v, ok, "CheckOption")
	}}
}

// SecurityBarrierSetting stops Postgres pushing a caller's own WHERE condition
// below the view's, which would otherwise let a cheap function in that
// condition see rows the view hides. It costs plans, so it is off by default.
func SecurityBarrierSetting(on bool) ViewSetting {
	return ViewSetting{write: func(*sqlb.Args) string { return optSecurityBarrier + " = " + boolOption(on) }}
}

// SecurityInvokerSetting runs the view's own query with the caller's
// privileges rather than the view owner's, so the caller's row-level security
// policies apply to the tables under it. A library shipping a view over its
// own RLS-protected tables wants this on.
func SecurityInvokerSetting(on bool) ViewSetting {
	return ViewSetting{write: func(*sqlb.Args) string { return optSecurityInvoker + " = " + boolOption(on) }}
}

// writeSettings renders a parenthesised option list, failing the build on a
// zero value rather than writing an empty assignment.
func writeSettings(a *sqlb.Args, settings []ViewSetting) string {
	parts := make([]string, len(settings))
	for i, s := range settings {
		if s.write == nil {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		parts[i] = s.write(a)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// AlterViewOptionsStatement is the ALTER VIEW statement that sets and resets a
// view's options. Postgres keeps SET and RESET apart in its grammar, so a
// statement that both sets and resets builds a Script of two.
type AlterViewOptionsStatement struct {
	v     View
	set   []func(a *sqlb.Args) string
	reset []ViewOption
}

// AlterViewOptions starts a change to a view's options. The view's body is
// changed with CreateView(v).OrReplace() instead, which is the only way to
// rewrite one.
func AlterViewOptions(v View) *AlterViewOptionsStatement {
	return &AlterViewOptionsStatement{v: v}
}

// with derives a statement with one more option set.
func (x *AlterViewOptionsStatement) with(o func(a *sqlb.Args) string) *AlterViewOptionsStatement {
	c := *x
	c.set = append(slices.Clone(x.set), o)
	return &c
}

// CheckOption refuses a write through the view that the view would not show,
// the option CreateView writes as WITH ... CHECK OPTION. Setting it on an
// existing view is how it is turned on without rewriting the body.
func (x *AlterViewOptionsStatement) CheckOption(o CheckOption) *AlterViewOptionsStatement {
	return x.with(CheckOptionSetting(o).write)
}

// SecurityBarrier stops Postgres pushing a caller's own WHERE condition below
// the view's, which would otherwise let a cheap function in that condition see
// rows the view hides. It costs plans, so it is off by default.
func (x *AlterViewOptionsStatement) SecurityBarrier(on bool) *AlterViewOptionsStatement {
	return x.with(SecurityBarrierSetting(on).write)
}

// SecurityInvoker runs the view's own query with the caller's privileges
// rather than the view owner's, so the caller's row-level security policies
// apply to the tables under it. A library shipping a view over its own
// RLS-protected tables wants this on.
func (x *AlterViewOptionsStatement) SecurityInvoker(on bool) *AlterViewOptionsStatement {
	return x.with(SecurityInvokerSetting(on).write)
}

// Reset puts the named options back to their defaults. It renders as a second
// statement beside anything the same builder sets.
func (x *AlterViewOptionsStatement) Reset(o ViewOption, more ...ViewOption) *AlterViewOptionsStatement {
	c := *x
	c.reset = append(slices.Clone(x.reset), append([]ViewOption{o}, more...)...)
	return &c
}

// boolOption spells a boolean reloption's value, a closed set of two words.
func boolOption(on bool) string {
	if on {
		return optTrue
	}
	return optFalse
}

// Build renders the statement.
func (x *AlterViewOptionsStatement) Build() (ddl.Script, error) {
	if len(x.set) == 0 && len(x.reset) == 0 {
		return nil, ddl.ErrZeroValue
	}
	var out ddl.Script
	if len(x.set) > 0 {
		s, err := buildOne(x.v.Name, true, func(a *sqlb.Args) string {
			parts := make([]string, len(x.set))
			for i, o := range x.set {
				parts[i] = o(a)
			}
			return x.head(a) + " " + kwSet + " (" + strings.Join(parts, ", ") + ")"
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	if len(x.reset) > 0 {
		s, err := buildOne(x.v.Name, true, func(a *sqlb.Args) string {
			parts := make([]string, len(x.reset))
			for i, o := range x.reset {
				n, ok := o.name()
				parts[i] = keywordOf(a, n, ok, "Reset")
			}
			return x.head(a) + " " + kwReset + " (" + strings.Join(parts, ", ") + ")"
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}

// head is the statement's ALTER VIEW and the view's name.
func (x *AlterViewOptionsStatement) head(a *sqlb.Args) string {
	return kwAlterView + " " + qualified(a.Dialect(), x.v.Schema, x.v.Name)
}
