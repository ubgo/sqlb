package mysql

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The MySQL clauses for CREATE TABLE and ALTER TABLE. Every one is typed to
// the MySQL dialect, so on another engine's statement it does not compile,
// and every one naming a column takes the column value, so the statement can
// check the table defines it.

const (
	kwConstraint     = "CONSTRAINT"
	kwPrimaryKey     = "PRIMARY KEY"
	kwUnique         = "UNIQUE"
	kwIndex          = "INDEX"
	kwFulltext       = "FULLTEXT"
	kwForeignKey     = "FOREIGN KEY"
	kwReferences     = "REFERENCES"
	kwOnDelete       = "ON DELETE"
	kwOnUpdate       = "ON UPDATE"
	kwAutoIncrement  = "AUTO_INCREMENT"
	kwCollate        = "COLLATE"
	kwComment        = "COMMENT"
	kwGeneratedAs    = "GENERATED ALWAYS AS"
	kwVirtual        = "VIRTUAL"
	kwStored         = "STORED"
	kwEngine         = "ENGINE ="
	kwDefaultCharset = "DEFAULT CHARSET ="
	kwTableCollate   = "COLLATE ="
	kwTableComment   = "COMMENT ="
	kwModifyColumn   = "MODIFY COLUMN"
	kwAlterColumn    = "ALTER COLUMN"
	kwSetDefault     = "SET DEFAULT"
	kwDropDefault    = "DROP DEFAULT"
	kwAdd            = "ADD"
	kwDropForeignKey = "DROP FOREIGN KEY"
	kwDropCheck      = "DROP CHECK"
	kwDropIndex      = "DROP INDEX"
	kwRenameIndex    = "RENAME INDEX"
	kwRenameTo       = "RENAME TO"
	kwTo             = "TO"
)

// TableClause is a MySQL clause for CREATE TABLE.
type TableClause = ddl.TableClause[Dialect]

// AlterClause is a MySQL ALTER TABLE action.
type AlterClause = ddl.AlterClause[Dialect]

// ErrUnknownKeyword is returned for a value outside its named constants.
var ErrUnknownKeyword = errors.New("sqlb/mysql: value outside its named constants")

// ErrMixedReferences is returned for a foreign key referencing two tables.
var ErrMixedReferences = errors.New("sqlb/mysql: a foreign key references one table")

func quoteCols(a *sqlb.Args, cols []ddl.ColumnRef) string {
	d := a.Dialect()
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = d.QuoteIdent(c.Name())
	}
	return "(" + strings.Join(names, ", ") + ")"
}

func constraintHead(a *sqlb.Args, name string) string {
	if name == "" {
		return ""
	}
	return kwConstraint + " " + a.Dialect().QuoteIdent(name) + " "
}

// TableConstraint is a named constraint of one table, declared once. See
// ddl.TableConstraint. MySQL keeps unique keys and indexes in one namespace
// per table; a plain index is an Index value instead.
type TableConstraint = ddl.TableConstraint

// namedHead writes CONSTRAINT "name" for a constraint that must be named,
// recording ErrZeroValue for a TableConstraint missing its name or table.
func namedHead(a *sqlb.Args, c TableConstraint) string {
	if !c.Valid() {
		a.Fail(ddl.ErrZeroValue)
		return ""
	}
	return constraintHead(a, c.Name)
}

// KeyClause is a key in CREATE TABLE: PRIMARY KEY, UNIQUE, INDEX or FULLTEXT.
type KeyClause struct {
	kw, name string
	// con is a unique key's declared constraint, zero for the others.
	con     TableConstraint
	cols    []ddl.ColumnRef
	method  IndexMethod
	options []IndexOption
}

// ClauseTable is a unique key's declared table, empty for the other keys.
func (k KeyClause) ClauseTable() sqlb.Table { return k.con.Table }

// PrimaryKey makes the table's primary key. InnoDB stores the table in its
// order, so a short, increasing key is the fast one. MySQL names every
// primary key PRIMARY, so there is no Named.
func PrimaryKey(first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwPrimaryKey, cols: append([]ddl.ColumnRef{first}, more...)}
}

// Unique makes a named unique key. A TEXT or BLOB column needs a prefix
// length to be a key, which this does not take; the type table spells text as
// VARCHAR(255) for that reason.
func Unique(c TableConstraint, first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwUnique, name: c.Name, con: c, cols: append([]ddl.ColumnRef{first}, more...)}
}

// TableIndex makes a named plain index inside CREATE TABLE, which MySQL
// allows and the other engines do not; CreateIndex is the statement form.
func TableIndex(ix Index, first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwIndex, name: ix.Name, cols: append([]ddl.ColumnRef{first}, more...)}
}

// TableFulltext makes a named FULLTEXT index inside CREATE TABLE, which Match
// needs over exactly these columns.
func TableFulltext(ix Index, first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwFulltext + " " + kwIndex, name: ix.Name, cols: append([]ddl.ColumnRef{first}, more...)}
}

// Position places the key among the table's elements.
func (KeyClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the key's columns.
func (k KeyClause) NamedColumns() []ddl.ColumnRef { return k.cols }

// WriteSQL renders the key.
func (k KeyClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if k.kw == "" || len(k.cols) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	using := writeUsing(a, k.method)
	switch k.kw {
	case kwPrimaryKey:
		b.WriteString(kwPrimaryKey + using + " " + quoteCols(a, k.cols))
	case kwUnique:
		b.WriteString(namedHead(a, k.con) + kwUnique + using + " " + quoteCols(a, k.cols))
	default:
		if k.name == "" {
			a.Fail(ErrEmptyName)
			return
		}
		b.WriteString(k.kw + " " + a.Dialect().QuoteIdent(k.name) + using + " " + quoteCols(a, k.cols))
	}
	b.WriteString(writeIndexOptions(a, k.options))
}

// TableClauseOf marks the clause as MySQL's.
func (KeyClause) TableClauseOf(Dialect) {}

// RefAction is what a foreign key does when the referenced row goes. There is
// no SET DEFAULT: InnoDB rejects it.
type RefAction int

// The referential actions InnoDB accepts.
const (
	RefNoAction RefAction = iota + 1
	RefRestrict
	RefCascade
	RefSetNull
)

func (r RefAction) keyword() (string, bool) {
	switch r {
	case RefNoAction:
		return "NO ACTION", true
	case RefRestrict:
		return "RESTRICT", true
	case RefCascade:
		return "CASCADE", true
	case RefSetNull:
		return "SET NULL", true
	}
	return "", false
}

// ColumnPair is one column of a foreign key and the column it references.
type ColumnPair struct{ local, ref ddl.ColumnRef }

// RefPair pairs a column with the one it references, of one Go type.
func RefPair[T any](local, ref sqlb.Expr[T]) ColumnPair { return ColumnPair{local: local, ref: ref} }

// ForeignKeyClause is a FOREIGN KEY table constraint.
type ForeignKeyClause struct {
	con                TableConstraint // zero for an inline References
	named              bool
	pairs              []ColumnPair
	onDelete, onUpdate RefAction
}

// ForeignKey makes a named foreign key; InnoDB indexes its columns if no index
// does.
func ForeignKey(c TableConstraint, first ColumnPair, more ...ColumnPair) ForeignKeyClause {
	return ForeignKeyClause{con: c, named: true, pairs: append([]ColumnPair{first}, more...)}
}

// ClauseTable is the constraint's declared table, empty for References.
func (f ForeignKeyClause) ClauseTable() sqlb.Table { return f.con.Table }

// References is the one-column foreign key.
func References[T any](local, ref sqlb.Expr[T]) ForeignKeyClause {
	return ForeignKeyClause{pairs: []ColumnPair{RefPair(local, ref)}}
}

// OnDelete says what happens when the referenced row is deleted.
func (f ForeignKeyClause) OnDelete(r RefAction) ForeignKeyClause { f.onDelete = r; return f }

// OnUpdate says what happens when the referenced key changes.
func (f ForeignKeyClause) OnUpdate(r RefAction) ForeignKeyClause { f.onUpdate = r; return f }

// Position places the constraint among the table's elements.
func (ForeignKeyClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the constraint's local columns.
func (f ForeignKeyClause) NamedColumns() []ddl.ColumnRef {
	out := make([]ddl.ColumnRef, len(f.pairs))
	for i, p := range f.pairs {
		out[i] = p.local
	}
	return out
}

// WriteSQL renders the constraint.
func (f ForeignKeyClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if len(f.pairs) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	refs := make([]ddl.ColumnRef, len(f.pairs))
	for i, p := range f.pairs {
		if p.ref == nil || p.ref.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return
		}
		refs[i] = p.ref
	}
	target := refs[0].Table()
	for _, r := range refs[1:] {
		if r.Table().Name != target.Name || r.Table().Schema != target.Schema {
			a.Fail(fmt.Errorf("%w: %s and %s", ErrMixedReferences, target.Name, r.Table().Name))
			return
		}
	}
	head := ""
	if f.named {
		head = namedHead(a, f.con)
	}
	b.WriteString(head + kwForeignKey + " " + quoteCols(a, f.NamedColumns()) +
		" " + kwReferences + " " + target.Qualified(a.Dialect()) + " " + quoteCols(a, refs))
	for _, o := range []struct {
		kw string
		r  RefAction
	}{{kwOnDelete, f.onDelete}, {kwOnUpdate, f.onUpdate}} {
		if o.r == 0 {
			continue
		}
		kw, ok := o.r.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: %s", ErrUnknownKeyword, o.kw))
			return
		}
		b.WriteString(" " + o.kw + " " + kw)
	}
}

// TableClauseOf marks the clause as MySQL's.
func (ForeignKeyClause) TableClauseOf(Dialect) {}

// CheckClause is a named CHECK constraint, the same one ddl.Check writes, for
// ALTER TABLE ... ADD, which is MySQL's.
type CheckClause struct {
	con         TableConstraint
	c           ddl.Constraint
	notEnforced bool
}

// Check makes a named CHECK constraint. MySQL enforces them since 8.0.16;
// before that it parsed and ignored them, which is why the version matters.
func Check(c TableConstraint, p sqlb.Predicate) CheckClause {
	return CheckClause{con: c, c: ddl.Check(c, p)}
}

// ClauseTable is the constraint's declared table.
func (c CheckClause) ClauseTable() sqlb.Table { return c.con.Table }

// Position places the constraint among the table's elements.
func (CheckClause) Position() ddl.Position { return ddl.PositionElement }

// WriteSQL renders the constraint.
func (c CheckClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if c.c == nil {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	c.c.WriteConstraint(b, a)
	if c.notEnforced {
		b.WriteString(" " + kwNotEnforced)
	}
}

// TableClauseOf marks the clause as MySQL's.
func (CheckClause) TableClauseOf(Dialect) {}

// Constraint is a MySQL table constraint ALTER TABLE can add: a unique key, a
// foreign key or a check. The set is closed.
type Constraint interface {
	TableClause
	isConstraint()
}

func (KeyClause) isConstraint()        {}
func (ForeignKeyClause) isConstraint() {}
func (CheckClause) isConstraint()      {}

// integer is the Go types an AUTO_INCREMENT column may have.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// AutoIncrement makes col take the next value when an INSERT gives none. The
// column must be a key, usually the primary key, which MySQL checks.
func AutoIncrement[T integer](col sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartValueSource, func(*sqlb.Args) string { return kwAutoIncrement })
}

// collatable is the Go types a collation applies to.
type collatable interface{ ~string | *string }

// ColumnCollate gives a text column a collation, utf8mb4_bin for a
// case-sensitive key, say; the default utf8mb4_0900_ai_ci is case- and
// accent-insensitive, so a UNIQUE on it refuses "Ada" beside "ada".
func ColumnCollate[T collatable](col sqlb.Expr[T], collation string) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartAfterType, func(a *sqlb.Args) string {
		return kwCollate + " " + a.Dialect().QuoteIdent(collation)
	})
}

// ColumnComment attaches a comment to a column, where MySQL keeps it.
func ColumnComment[T any](col sqlb.Expr[T], text string) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartConstraint, func(a *sqlb.Args) string {
		return kwComment + " " + a.Literal(text)
	})
}

// GeneratedVirtual makes col computed by e on read and stored nowhere.
func GeneratedVirtual[T any](col, e sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartComputed, func(a *sqlb.Args) string {
		return kwGeneratedAs + " (" + e.SQL(a) + ") " + kwVirtual
	})
}

// GeneratedStored makes col computed by e on write and stored.
func GeneratedStored[T any](col, e sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartComputed, func(a *sqlb.Args) string {
		return kwGeneratedAs + " (" + e.SQL(a) + ") " + kwStored
	})
}

// MySQL's CREATE TABLE options, in the order its grammar accepts.
//
// Two ranks, not one per option, because MySQL's grammar has two groups
// rather than a sequence. Measured on MySQL 8.4 on 2026-09-24: its table
// options are accepted in any order among themselves — `DEFAULT
// CHARSET=utf8mb4 ENGINE=InnoDB COMMENT='c'` and `COMMENT='c'
// ROW_FORMAT=DYNAMIC ENGINE=InnoDB` both build — but partitioning only last:
// `PARTITION BY HASH(id) PARTITIONS 2 ENGINE=InnoDB` is ER_PARSE_ERROR. So
// every table option shares one rank, and the stable sort in ddl keeps the
// caller's order among them, which is the order the caller chose and MySQL
// does not care about. Only Partitioning is moved, to after them. See
// ddl.OptionRank for why the statement sorts rather than refusing.
const (
	rankTableOption ddl.OptionRank = iota + 1
	rankPartitioning
)

// tableOption makes one of MySQL's table options, at the rank every table
// option shares.
func tableOption(render func(a *sqlb.Args) string) TableClause {
	return ddl.NewOptionClause[Dialect](rankTableOption, render)
}

// InnoDB is the storage engine a library's table wants: transactional, with
// foreign keys and row locks. It is the server default, and writing it makes
// the table not depend on the server's default.
func InnoDB() TableClause {
	return TableEngine(EngineInnoDB)
}

// DefaultCharset sets the table's character set, utf8mb4 for all of Unicode;
// the older utf8 is three bytes and cannot store an emoji.
func DefaultCharset(charset string) TableClause {
	return tableOption(func(a *sqlb.Args) string { return kwDefaultCharset + " " + a.Dialect().QuoteIdent(charset) })
}

// TableCollate sets the table's default collation.
func TableCollate(collation string) TableClause {
	return tableOption(func(a *sqlb.Args) string { return kwTableCollate + " " + a.Dialect().QuoteIdent(collation) })
}

// TableComment attaches a comment to the table.
func TableComment(text string) TableClause {
	return tableOption(func(a *sqlb.Args) string { return kwTableComment + " " + a.Literal(text) })
}

// alterAction is every MySQL ALTER TABLE action here.
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

// named renders an action on a declared constraint, refusing a zero one.
func named(c TableConstraint, render func(a *sqlb.Args) string) alterAction {
	return alterAction{table: c.Table, sql: func(a *sqlb.Args) string {
		if !c.Valid() {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		return render(a)
	}}
}

func (x alterAction) WriteSQL(b *strings.Builder, a *sqlb.Args) { b.WriteString(x.sql(a)) }
func (x alterAction) Standalone() bool                          { return x.standalone }
func (alterAction) AlterClauseOf(Dialect)                       {}
func (x alterAction) NamedColumns() []ddl.ColumnRef             { return x.cols }

// ModifyColumn redefines a column in place, its type, nullability and default
// together: MySQL has no ALTER COLUMN ... TYPE, so a type change restates the
// whole definition, and a property left out is dropped. The column clauses
// must name it.
func ModifyColumn(c ddl.ColumnSpec, with ...ddl.ColumnClause[Dialect]) AlterClause {
	return alterAction{cols: []ddl.ColumnRef{colRef{c}}, sql: func(a *sqlb.Args) string {
		var b strings.Builder
		b.WriteString(kwModifyColumn + " ")
		ddl.WriteColumnWith(&b, a, c, with)
		return b.String()
	}}
}

// colRef adapts a ColumnSpec to a ColumnRef for the table check.
type colRef struct{ c ddl.ColumnSpec }

func (r colRef) Name() string      { return r.c.ColumnName() }
func (r colRef) Table() sqlb.Table { return r.c.ColumnTable() }

// SetDefault sets a column's default to a value of its type.
func SetDefault[T any](col sqlb.Expr[T], v T) AlterClause {
	return alterAction{cols: []ddl.ColumnRef{col}, sql: func(a *sqlb.Args) string {
		return kwAlterColumn + " " + a.Dialect().QuoteIdent(col.Name()) + " " + kwSetDefault + " " + a.Literal(v)
	}}
}

// DropDefault removes a column's default.
func DropDefault[T any](col sqlb.Expr[T]) AlterClause {
	return alterAction{cols: []ddl.ColumnRef{col}, sql: func(a *sqlb.Args) string {
		return kwAlterColumn + " " + a.Dialect().QuoteIdent(col.Name()) + " " + kwDropDefault
	}}
}

// AddConstraint adds a unique key, a foreign key or a check.
func AddConstraint(c Constraint) AlterClause {
	var cols []ddl.ColumnRef
	if n, ok := c.(ddl.ColumnNamer); ok {
		cols = n.NamedColumns()
	}
	var table sqlb.Table
	if tn, ok := c.(ddl.TableNamer); ok {
		table = tn.ClauseTable()
	}
	return alterAction{cols: cols, table: table, sql: func(a *sqlb.Args) string {
		return kwAdd + " " + a.Bare(func() string {
			var b strings.Builder
			c.WriteSQL(&b, a)
			return b.String()
		})
	}}
}

// DropForeignKey drops a declared foreign key; its index stays.
func DropForeignKey(c TableConstraint) AlterClause {
	return named(c, func(a *sqlb.Args) string { return kwDropForeignKey + " " + a.Dialect().QuoteIdent(c.Name) })
}

// DropCheck drops a declared CHECK constraint.
func DropCheck(c TableConstraint) AlterClause {
	return named(c, func(a *sqlb.Args) string { return kwDropCheck + " " + a.Dialect().QuoteIdent(c.Name) })
}

// DropKey drops a declared unique key.
func DropKey(c TableConstraint) AlterClause {
	return named(c, func(a *sqlb.Args) string { return kwDropIndex + " " + a.Dialect().QuoteIdent(c.Name) })
}

// ErrRenameAcrossTables is returned by RenameKey for two constraints of
// different tables: a key is renamed within its table.
var ErrRenameAcrossTables = errors.New("sqlb/mysql: RenameKey names constraints of two tables")

// RenameKey renames a declared unique key within its table.
func RenameKey(from, to TableConstraint) AlterClause {
	return named(from, func(a *sqlb.Args) string {
		if !to.Valid() {
			a.Fail(ddl.ErrZeroValue)
			return ""
		}
		if from.Table.Name != to.Table.Name || from.Table.Schema != to.Table.Schema {
			a.Fail(fmt.Errorf("%w: %s and %s", ErrRenameAcrossTables, from.Table.Name, to.Table.Name))
			return ""
		}
		d := a.Dialect()
		return kwRenameIndex + " " + d.QuoteIdent(from.Name) + " " + kwTo + " " + d.QuoteIdent(to.Name)
	})
}

// RenameIndex renames a plain index of the table, the Index counterpart of
// RenameKey.
func RenameIndex(from, to Index) AlterClause {
	return alterAction{sql: func(a *sqlb.Args) string {
		if from.Name == "" || to.Name == "" {
			a.Fail(ErrEmptyName)
			return ""
		}
		d := a.Dialect()
		return kwRenameIndex + " " + d.QuoteIdent(from.Name) + " " + kwTo + " " + d.QuoteIdent(to.Name)
	}}
}

// RenameTo renames the table to to's name, as its own statement.
func RenameTo(to sqlb.Table) AlterClause {
	return alterAction{standalone: true, sql: func(a *sqlb.Args) string { return kwRenameTo + " " + to.Qualified(a.Dialect()) }}
}

// The column-level forms and the ALTER TABLE actions the 2026-09-24 audit
// found inside statements the detector already called built: column
// visibility, column placement, CHANGE COLUMN, DROP PRIMARY KEY, the generic
// DROP CONSTRAINT, an unenforced CHECK on the way in, and an inline key's
// index type and options.
const (
	kwChangeColumn   = "CHANGE COLUMN"
	kwAddColumn      = "ADD COLUMN"
	kwFirst          = "FIRST"
	kwAfter          = "AFTER"
	kwSetVisible     = "SET VISIBLE"
	kwSetInvisible   = "SET INVISIBLE"
	kwDropPrimaryKey = "DROP PRIMARY KEY"
	kwDropConstraint = "DROP CONSTRAINT"
)

// ColumnInvisible hides a column from SELECT *, so it is returned only when
// named. It is how a column is added to a table an application selects with a
// star from, without that application seeing it before it is ready; a
// column's visibility is not a privilege and anyone naming it reads it.
func ColumnInvisible[T any](col sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartConstraint, func(*sqlb.Args) string { return kwInvisible })
}

// ColumnVisible returns a column to SELECT *, which is the default; it exists
// so a ModifyColumn that must keep the column visible can say so, since a
// property left out of a MySQL column redefinition is dropped.
func ColumnVisible[T any](col sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartConstraint, func(*sqlb.Args) string { return kwVisible })
}

// SetColumnVisible and SetColumnInvisible change a column's visibility
// without restating its definition, which ModifyColumn would require.
func SetColumnVisible[T any](col sqlb.Expr[T]) AlterClause {
	return columnVisibility(col, kwSetVisible)
}

// SetColumnInvisible hides a column from SELECT *; see ColumnInvisible.
func SetColumnInvisible[T any](col sqlb.Expr[T]) AlterClause {
	return columnVisibility(col, kwSetInvisible)
}

func columnVisibility[T any](col sqlb.Expr[T], kw string) AlterClause {
	return alterAction{cols: []ddl.ColumnRef{col}, sql: func(a *sqlb.Args) string {
		return kwAlterColumn + " " + a.Dialect().QuoteIdent(col.Name()) + " " + kw
	}}
}

// Placement is where a column goes among the table's columns. MySQL stores
// columns in a fixed order and reports it in SHOW CREATE TABLE and in
// SELECT *, so a column added at the end of one deployment and in the middle
// of another gives two tables that are equal in every other respect and
// differ in what SELECT * returns.
type Placement struct {
	first bool
	after ddl.ColumnRef
}

// First places the column before every other.
func First() Placement { return Placement{first: true} }

// After places the column immediately after col.
func After[T any](col sqlb.Expr[T]) Placement { return Placement{after: col} }

// write renders the placement, or nothing when none was given.
func (p Placement) write(a *sqlb.Args) string {
	switch {
	case p.first:
		return " " + kwFirst
	case p.after != nil:
		if p.after.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return ""
		}
		return " " + kwAfter + " " + a.Dialect().QuoteIdent(p.after.Name())
	}
	return ""
}

// ColumnAction is an ALTER TABLE action that defines a column and can say
// where it goes: ADD COLUMN, MODIFY COLUMN and CHANGE COLUMN.
type ColumnAction struct {
	kw        string
	from      ddl.ColumnRef
	spec      ddl.ColumnSpec
	with      []ddl.ColumnClause[Dialect]
	placement Placement
}

// AddColumn adds a column, with the MySQL placement the universal
// ddl.AddColumn cannot express. Use ddl.AddColumn where the position does not
// matter; this is the same action with FIRST or AFTER available.
func AddColumn(c ddl.ColumnSpec, with ...ddl.ColumnClause[Dialect]) ColumnAction {
	return ColumnAction{kw: kwAddColumn, spec: c, with: with}
}

// ChangeColumn renames a column and restates its definition in one action,
// which is the only way MySQL does both at once. MODIFY cannot rename and
// RENAME COLUMN cannot redefine, so a rename that also widens a type is this
// or it is two statements and two rebuilds.
func ChangeColumn[T any](from sqlb.Expr[T], c ddl.ColumnSpec, with ...ddl.ColumnClause[Dialect]) ColumnAction {
	return ColumnAction{kw: kwChangeColumn, from: from, spec: c, with: with}
}

// ModifyColumnAt is ModifyColumn with a placement, which also moves the
// column: MySQL rebuilds the table to do it.
func ModifyColumnAt(c ddl.ColumnSpec, p Placement, with ...ddl.ColumnClause[Dialect]) ColumnAction {
	return ColumnAction{kw: kwModifyColumn, spec: c, with: with, placement: p}
}

// At places the column.
func (x ColumnAction) At(p Placement) ColumnAction { x.placement = p; return x }

// Standalone is false: MySQL combines a column action with others.
func (ColumnAction) Standalone() bool { return false }

// AlterClauseOf marks the action as MySQL's.
func (ColumnAction) AlterClauseOf(Dialect) {}

// ClauseTable is none: the action's column carries the table.
func (ColumnAction) ClauseTable() sqlb.Table { return sqlb.Table{} }

// NamedColumns is the column the action defines, and the one it renames from,
// so ALTER TABLE checks both belong to the table.
func (x ColumnAction) NamedColumns() []ddl.ColumnRef {
	if x.spec == nil {
		return nil
	}
	out := []ddl.ColumnRef{colRef{x.spec}}
	if x.from != nil {
		out = append(out, x.from)
	}
	return out
}

// WriteSQL renders the action.
func (x ColumnAction) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if x.kw == "" || x.spec == nil {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	b.WriteString(x.kw + " ")
	if x.kw == kwChangeColumn {
		if x.from == nil || x.from.Name() == "" {
			a.Fail(ddl.ErrNotAColumn)
			return
		}
		b.WriteString(a.Dialect().QuoteIdent(x.from.Name()) + " ")
	}
	ddl.WriteColumnWith(b, a, x.spec, x.with)
	b.WriteString(x.placement.write(a))
}

// DropPrimaryKey drops the table's primary key. It is its own action because
// MySQL names every primary key PRIMARY and DropKey takes a declared
// TableConstraint, which a primary key never has.
func DropPrimaryKey() AlterClause {
	return alterAction{sql: func(*sqlb.Args) string { return kwDropPrimaryKey }}
}

// DropConstraint drops a declared constraint whatever its kind, which is what
// a migration undoing an earlier one wants when the kind is not at hand;
// DropCheck, DropKey and DropForeignKey are the specific forms, and MySQL
// refuses each of those on a constraint of another kind.
func DropConstraint(c TableConstraint) AlterClause {
	return named(c, func(a *sqlb.Args) string {
		return kwDropConstraint + " " + a.Dialect().QuoteIdent(c.Name)
	})
}

// NotEnforced adds the CHECK turned off, which is how a constraint is
// declared over a table whose existing rows do not satisfy it yet; without it
// the ADD validates every row and fails. SetCheckEnforced turns it on once
// the rows are fixed.
func (c CheckClause) NotEnforced() CheckClause { c.notEnforced = true; return c }

// Using picks the index type of an inline key. InnoDB builds a BTREE whatever
// is asked, so this only bites on a MEMORY table, where HASH is the default
// and BTREE must be asked for to get a range scan.
func (k KeyClause) Using(m IndexMethod) KeyClause { k.method = m; return k }

// With adds index options to an inline key; see IndexOption.
func (k KeyClause) With(first IndexOption, more ...IndexOption) KeyClause {
	k.options = append(append(slices.Clone(k.options), first), more...)
	return k
}

// TableSpatial makes a named SPATIAL index inside CREATE TABLE, over geometry
// columns MySQL requires to be NOT NULL.
func TableSpatial(ix Index, first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwSpatial + " " + kwIndex, name: ix.Name, cols: append([]ddl.ColumnRef{first}, more...)}
}
