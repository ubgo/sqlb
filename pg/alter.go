package pg

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The Postgres ALTER TABLE actions. Each goes on pg.AlterTable(t) through
// Clause, is typed to the Postgres dialect, and takes the column value rather
// than its name, so an action on another table's column fails Build. Actions
// Postgres may join share one statement and one lock; RENAME, SET SCHEMA and
// the like are standalone forms in its grammar and get a statement each.

const (
	kwAddColumnIfNotExists = "ADD COLUMN IF NOT EXISTS"
	kwDropColumnIfExists   = "DROP COLUMN IF EXISTS"
	kwAlterColumn          = "ALTER COLUMN"
	kwSetDefault           = "SET DEFAULT"
	kwDropDefault          = "DROP DEFAULT"
	kwSetNotNull           = "SET NOT NULL"
	kwDropNotNull          = "DROP NOT NULL"
	kwSetDataType          = "SET DATA TYPE"
	kwAdd                  = "ADD"
	kwNotValid             = "NOT VALID"
	kwValidateConstraint   = "VALIDATE CONSTRAINT"
	kwDropConstraint       = "DROP CONSTRAINT"
	kwIfExists             = "IF EXISTS"
	kwRenameConstraint     = "RENAME CONSTRAINT"
	kwRenameTo             = "RENAME TO"
	kwTo                   = "TO"
	kwSetSchema            = "SET SCHEMA"
	kwEnableRLS            = "ENABLE ROW LEVEL SECURITY"
)

// AlterClause is a Postgres ALTER TABLE action.
type AlterClause = ddl.AlterClause[Dialect]

// alterAction is every action here: its text, whether it stands alone, and
// the columns it names, which the statement checks belong to its table.
type alterAction struct {
	sql        func(a *sqlb.Args) string
	standalone bool
	cols       []ddl.ColumnRef
	// table is the table the action names, a constraint's, which ALTER TABLE
	// checks is its own; empty for an action that names none.
	table sqlb.Table
}

// ClauseTable is the table the action names, for ALTER TABLE's check.
func (x alterAction) ClauseTable() sqlb.Table { return x.table }

func (x alterAction) WriteSQL(b *strings.Builder, a *sqlb.Args) { b.WriteString(x.sql(a)) }
func (x alterAction) Standalone() bool                          { return x.standalone }
func (alterAction) AlterClauseOf(Dialect)                       {}
func (x alterAction) NamedColumns() []ddl.ColumnRef             { return x.cols }

// onColumn is an ALTER COLUMN action on col.
func onColumn(col ddl.ColumnRef, rest func(a *sqlb.Args) string) alterAction {
	return alterAction{cols: []ddl.ColumnRef{col}, sql: func(a *sqlb.Args) string {
		if col.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return ""
		}
		return kwAlterColumn + " " + a.Dialect().QuoteIdent(col.Name()) + " " + rest(a)
	}}
}

// AddColumnIfNotExists adds a column unless it exists, which makes the action
// safe to run on every boot. Like IF NOT EXISTS on a table it checks the name
// only. The column clauses, a collation or an identity, must name the column.
func AddColumnIfNotExists(c ddl.ColumnSpec, with ...ddl.ColumnClause[Dialect]) AlterClause {
	return addColumnIfNotExists{spec: c, with: with}
}

type addColumnIfNotExists struct {
	spec ddl.ColumnSpec
	with []ddl.ColumnClause[Dialect]
}

func (x addColumnIfNotExists) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	b.WriteString(kwAddColumnIfNotExists + " ")
	ddl.WriteColumnWith(b, a, x.spec, x.with)
}
func (addColumnIfNotExists) Standalone() bool          { return false }
func (addColumnIfNotExists) AlterClauseOf(Dialect)     {}
func (x addColumnIfNotExists) ClauseTable() sqlb.Table { return x.spec.ColumnTable() }

// DropColumnClause is DROP COLUMN IF EXISTS, optionally with CASCADE.
type DropColumnClause struct {
	alterAction
	cascade bool
}

// DropColumnIfExists drops a column if it exists, so an uninstall or a second
// run of a migration is not an error.
func DropColumnIfExists[T any](col sqlb.Expr[T]) DropColumnClause {
	return DropColumnClause{alterAction: alterAction{cols: []ddl.ColumnRef{col}}}
}

// Cascade also drops what depends on the column: an index, a view, a
// constraint of another table.
func (x DropColumnClause) Cascade() DropColumnClause { x.cascade = true; return x }

// WriteSQL renders the action.
func (x DropColumnClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if len(x.cols) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	col := x.cols[0]
	if col.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	b.WriteString(kwDropColumnIfExists + " " + a.Dialect().QuoteIdent(col.Name()))
	if x.cascade {
		b.WriteString(" " + kwCascade)
	}
}

// SetDefault sets a column's default to a value of its own Go type, written as
// a literal. It affects rows inserted from now on, never existing ones.
func SetDefault[T any](col sqlb.Expr[T], v T) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string { return kwSetDefault + " " + a.Literal(v) })
}

// SetDefaultExpr sets a column's default to an expression of its own type.
func SetDefaultExpr[T any](col, e sqlb.Expr[T]) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string {
		return kwSetDefault + " (" + a.Bare(func() string { return e.SQL(a) }) + ")"
	})
}

// DropDefault removes a column's default.
func DropDefault[T any](col sqlb.Expr[T]) AlterClause {
	return onColumn(col, func(*sqlb.Args) string { return kwDropDefault })
}

// SetNotNull refuses NULL from now on. It scans the table and fails if any row
// holds NULL, which is why adding a NOT NULL column to a table with rows is
// three steps: add it nullable, backfill, then this.
func SetNotNull[T any](col sqlb.Expr[T]) AlterClause {
	return onColumn(col, func(*sqlb.Args) string { return kwSetNotNull })
}

// DropNotNull allows NULL.
func DropNotNull[T any](col sqlb.Expr[T]) AlterClause {
	return onColumn(col, func(*sqlb.Args) string { return kwDropNotNull })
}

// AlterTypeClause is ALTER COLUMN ... SET DATA TYPE, optionally with USING.
type AlterTypeClause[T any] struct {
	col       sqlb.Expr[T]
	typ       ddl.SQLType[T]
	collation string
	using     *sqlb.Expr[T]
}

// AlterType changes a column's type. col is the column's new declaration, so
// the SQL type is checked against its Go type as a column definition's is;
// the old declaration is what the code being changed still uses.
func AlterType[T any](col sqlb.Expr[T], t ddl.SQLType[T]) AlterTypeClause[T] {
	return AlterTypeClause[T]{col: col, typ: t}
}

// Collate gives the column a collation as its type changes, written straight
// after the type as Postgres's grammar requires.
func (x AlterTypeClause[T]) Collate(collation string) AlterTypeClause[T] {
	x.collation = collation
	return x
}

// Using converts each existing value with e, an expression of the new type.
// Without it Postgres casts implicitly, and refuses a type it has no implicit
// cast to.
func (x AlterTypeClause[T]) Using(e sqlb.Expr[T]) AlterTypeClause[T] { x.using = &e; return x }

// WriteSQL renders the action.
func (x AlterTypeClause[T]) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	onColumn(x.col, func(a *sqlb.Args) string {
		sp, err := x.typ.Spelling(a.Dialect())
		if err != nil {
			a.Fail(err)
			return ""
		}
		out := kwSetDataType + " " + sp
		if x.collation != "" {
			out += " " + kwCollate + " " + a.Dialect().QuoteIdent(x.collation)
		}
		if x.using != nil {
			u := *x.using
			out += " " + kwUsing + " " + a.Bare(func() string { return u.SQL(a) })
		}
		return out
	}).WriteSQL(b, a)
}

// Standalone reports that the action may share a statement.
func (AlterTypeClause[T]) Standalone() bool { return false }

// AlterClauseOf marks the action as Postgres's.
func (AlterTypeClause[T]) AlterClauseOf(Dialect) {}

// NamedColumns is the column being changed.
func (x AlterTypeClause[T]) NamedColumns() []ddl.ColumnRef { return []ddl.ColumnRef{x.col} }

// AddIdentity makes an existing integer column an identity column.
func AddIdentity[T identityInteger](col sqlb.Expr[T], k IdentityKind) AlterClause {
	return onColumn(col, func(a *sqlb.Args) string {
		kw, ok := k.keyword()
		return kwAdd + " " + kwGenerated + " " + keywordOf(a, kw, ok, "AddIdentity") + " " + kwAsIdentity
	})
}

// Constraint is a Postgres table constraint that ALTER TABLE can add: a
// primary key, a unique constraint, a foreign key or a check. The set is
// closed, so the unexported method keeps anything else out.
type Constraint interface {
	TableClause
	constraintName() string
}

// Validatable is a constraint Postgres can add NOT VALID: a foreign key or a
// check, and nothing else, so AddConstraintNotValid on a unique key does not
// compile rather than failing on the server.
type Validatable interface {
	Constraint
	isValidatable()
}

func (k PrimaryKeyClause) constraintName() string { return k.con.Name }
func (u UniqueClause) constraintName() string     { return u.con.Name }
func (f ForeignKeyClause) constraintName() string { return f.con.Name }
func (c CheckClause) constraintName() string      { return c.con.Name }
func (ForeignKeyClause) isValidatable()           {}
func (CheckClause) isValidatable()                {}

// AddConstraint adds a constraint to an existing table, checking every row
// under a lock that blocks writes for the whole scan. Postgres has no IF NOT
// EXISTS for it.
func AddConstraint(c Constraint) AlterClause { return addConstraint(c, false) }

// AddConstraintNotValid adds a foreign key or check without checking existing
// rows, so the strong lock is brief; ValidateConstraint then checks them under
// a lock that allows writes. On a large table the two steps are the difference
// between a migration and an outage.
func AddConstraintNotValid(c Validatable) AlterClause { return addConstraint(c, true) }

func addConstraint(c Constraint, notValid bool) AlterClause {
	var cols []ddl.ColumnRef
	if n, ok := c.(ddl.ColumnNamer); ok {
		cols = n.NamedColumns()
	}
	var table sqlb.Table
	if tn, ok := c.(ddl.TableNamer); ok {
		table = tn.ClauseTable()
	}
	return alterAction{cols: cols, table: table, sql: func(a *sqlb.Args) string {
		out := kwAdd + " " + a.Bare(func() string {
			var sb strings.Builder
			c.WriteSQL(&sb, a)
			return sb.String()
		})
		if notValid {
			out += " " + kwNotValid
		}
		return out
	}}
}

// ValidateConstraint checks existing rows against a constraint added NOT
// VALID. Build it separately from AddConstraintNotValid and run it in its own
// transaction: in one statement the strong lock is held through the scan,
// which is exactly what NOT VALID exists to avoid.
func ValidateConstraint(c TableConstraint) AlterClause {
	return alterAction{table: c.Table, sql: func(a *sqlb.Args) string {
		if !c.Valid() {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		return kwValidateConstraint + " " + a.Dialect().QuoteIdent(c.Name)
	}}
}

// ErrRenameAcrossTables is returned by RenameConstraint for two constraints
// of different tables: a constraint is renamed within its table and cannot
// move to another.
var ErrRenameAcrossTables = errors.New("sqlb/pg: RenameConstraint names constraints of two tables")

// RenameConstraint renames a constraint within its table, the one migration
// a declared constraint needs when its name changes. It is a statement of its
// own, as every RENAME is on Postgres. Declare the new name as a second
// TableConstraint and switch the code to it in the same release.
func RenameConstraint(from, to TableConstraint) AlterClause {
	return alterAction{table: from.Table, standalone: true, sql: func(a *sqlb.Args) string {
		if !from.Valid() || !to.Valid() {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		if from.Table.Name != to.Table.Name || from.Table.Schema != to.Table.Schema {
			a.Fail(fmt.Errorf("%w: %s and %s", ErrRenameAcrossTables, from.Table.Name, to.Table.Name))
			return ""
		}
		d := a.Dialect()
		return kwRenameConstraint + " " + d.QuoteIdent(from.Name) + " " + kwTo + " " + d.QuoteIdent(to.Name)
	}}
}

// DropConstraintClause is DROP CONSTRAINT, optionally IF EXISTS and CASCADE.
type DropConstraintClause struct {
	con      TableConstraint
	ifExists bool
	cascade  bool
}

// DropConstraint drops a declared constraint. ALTER TABLE refuses one of
// another table.
func DropConstraint(c TableConstraint) DropConstraintClause { return DropConstraintClause{con: c} }

// ClauseTable is the constraint's table, for ALTER TABLE's check.
func (x DropConstraintClause) ClauseTable() sqlb.Table { return x.con.Table }

// IfExists makes dropping a missing constraint a no-op.
func (x DropConstraintClause) IfExists() DropConstraintClause { x.ifExists = true; return x }

// Cascade also drops what depends on the constraint, a foreign key of another
// table referencing this unique key.
func (x DropConstraintClause) Cascade() DropConstraintClause { x.cascade = true; return x }

// WriteSQL renders the action.
func (x DropConstraintClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if !x.con.Valid() {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	b.WriteString(kwDropConstraint + " ")
	if x.ifExists {
		b.WriteString(kwIfExists + " ")
	}
	b.WriteString(a.Dialect().QuoteIdent(x.con.Name))
	if x.cascade {
		b.WriteString(" " + kwCascade)
	}
}

// Standalone reports that the action may share a statement.
func (DropConstraintClause) Standalone() bool { return false }

// AlterClauseOf marks the action as Postgres's.
func (DropConstraintClause) AlterClauseOf(Dialect) {}

// RenameTo renames the table to to's name; the schema is unchanged, which is
// SetSchema's job. It takes the new declaration, the value the queries will
// use, rather than a string. A statement of its own.
func RenameTo(to sqlb.Table) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string { return kwRenameTo + " " + a.Dialect().QuoteIdent(to.Name) }}
}

// SetSchema moves the table to another schema. A statement of its own.
func SetSchema(schema string) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string { return kwSetSchema + " " + a.Dialect().QuoteIdent(schema) }}
}

// EnableRowLevelSecurity turns on row security, after which a role that is not
// the owner sees no row until a policy grants it. The policies come with
// CREATE POLICY.
func EnableRowLevelSecurity() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwEnableRLS }}
}
