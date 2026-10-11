package sqlite

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
)

// The SQLite clauses for CREATE TABLE and ALTER TABLE: keys, references,
// AUTOINCREMENT, collations, generated columns, STRICT and WITHOUT ROWID.
// Every one is typed to the SQLite dialect, so on a Postgres statement it
// does not compile, and every one that names a column takes the column value,
// so the statement checks at Build that the table defines it.

const (
	kwConstraint    = "CONSTRAINT"
	kwPrimaryKey    = "PRIMARY KEY"
	kwUnique        = "UNIQUE"
	kwForeignKey    = "FOREIGN KEY"
	kwReferences    = "REFERENCES"
	kwOnDelete      = "ON DELETE"
	kwOnUpdate      = "ON UPDATE"
	kwOnConflict    = "ON CONFLICT"
	kwAutoincrement = "PRIMARY KEY AUTOINCREMENT"
	kwCollate       = "COLLATE"
	kwGeneratedAs   = "GENERATED ALWAYS AS"
	kwVirtual       = "VIRTUAL"
	kwStored        = "STORED"
	kwStrict        = "STRICT"
	kwWithoutRowID  = "WITHOUT ROWID"
	kwAddColumn     = "ADD COLUMN"
	kwRenameTo      = "RENAME TO"
	kwTemporary     = "TEMPORARY"
	kwDeferrable    = "DEFERRABLE"
	kwNotDeferrable = "NOT DEFERRABLE"
	kwInitDeferred  = "INITIALLY DEFERRED"
	kwInitImmediate = "INITIALLY IMMEDIATE"
)

// TableClause is a SQLite clause for CREATE TABLE.
type TableClause = ddl.TableClause[Dialect]

// AlterClause is a SQLite ALTER TABLE action.
type AlterClause = ddl.AlterClause[Dialect]

// ErrUnknownKeyword is returned for a value outside its named constants, such
// as a zero referential action or collation.
var ErrUnknownKeyword = errors.New("sqlb/sqlite: value outside its named constants")

// ErrMixedReferences is returned for a foreign key whose referenced columns
// are on more than one table.
var ErrMixedReferences = errors.New("sqlb/sqlite: a foreign key references one table")

// ErrTemporaryTable is a CREATE TEMP TABLE whose table names a schema other
// than temp, which SQLite refuses with "temporary table name must be
// unqualified", measured on 3.50.3.
var ErrTemporaryTable = errors.New("sqlb/sqlite: a temporary table cannot name a schema other than temp")

// ErrAddStored is returned for a stored generated column in ADD COLUMN, which
// SQLite refuses because every existing row would need computing; use
// GeneratedVirtual, or rebuild the table.
var ErrAddStored = errors.New("sqlb/sqlite: SQLite cannot add a STORED generated column")

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

// onConflict renders a constraint's conflict clause, or nothing for the
// absent one, and fails the build on an action outside the five constants.
func onConflict(a *sqlb.Args, action OrAction) string {
	if action == "" {
		return ""
	}
	if !action.valid() {
		a.Fail(fmt.Errorf("%w: OrAction %q", ErrUnknownKeyword, action))
		return ""
	}
	return " " + kwOnConflict + " " + string(action)
}

// KeyColumn is one column of a table-level key, with the collation and sort
// order SQLite indexes it under.
//
// A key is an index, so the same two decorations a CREATE INDEX key takes
// apply here, and they are not cosmetic: UNIQUE(email COLLATE NOCASE) is a
// different constraint from UNIQUE(email), and it is the one a library that
// stores email addresses means. Without it the uniqueness is byte-exact and
// two addresses differing only in case both fit.
type KeyColumn struct {
	col  ddl.ColumnRef
	coll Collation
	dir  string
}

// Key makes one column of a key, undecorated.
func Key(col ddl.ColumnRef) KeyColumn { return KeyColumn{col: col} }

// Collate indexes the key column under a collation, which a query must use
// too for the key's index to serve it.
func (k KeyColumn) Collate(c Collation) KeyColumn { k.coll = c; return k }

// Asc indexes the column ascending, the default, written out.
func (k KeyColumn) Asc() KeyColumn { k.dir = kwAsc; return k }

// Desc indexes the column descending. On a single INTEGER PRIMARY KEY this
// also stops the column being an alias for the rowid, which is a change of
// storage rather than of order.
func (k KeyColumn) Desc() KeyColumn { k.dir = kwDesc; return k }

// write renders the key column, its name bare.
func (k KeyColumn) write(b *strings.Builder, a *sqlb.Args) {
	if k.col == nil || k.col.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	b.WriteString(a.Dialect().QuoteIdent(k.col.Name()))
	if k.coll != 0 {
		kw, ok := k.coll.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: Collation", ErrUnknownKeyword))
			return
		}
		b.WriteString(" " + kwCollate + " " + kw)
	}
	if k.dir != "" {
		b.WriteString(" " + k.dir)
	}
}

// KeyClause is a PRIMARY KEY or UNIQUE table constraint.
type KeyClause struct {
	kw       string
	con      TableConstraint // zero for an unnamed key
	cols     []ddl.ColumnRef
	parts    []KeyColumn // set by PrimaryKeyOn and UniqueOn, empty otherwise
	conflict OrAction
}

// TableConstraint is a named constraint of one table, declared once. See
// ddl.TableConstraint.
type TableConstraint = ddl.TableConstraint

// namedHead writes CONSTRAINT "name" for a declared constraint, or nothing
// for an unnamed one, and records ErrZeroValue for a half-filled one.
func namedHead(a *sqlb.Args, c TableConstraint) string {
	if c == (TableConstraint{}) {
		return ""
	}
	if !c.Valid() {
		a.Fail(ddl.ErrZeroValue)
		return ""
	}
	return constraintHead(a, c.Name)
}

// ClauseTable is the key's declared table, empty for an unnamed key.
func (k KeyClause) ClauseTable() sqlb.Table { return k.con.Table }

// PrimaryKey makes the table's primary key. A single INTEGER column as the
// key is the rowid alias, SQLite's own row number, which is why an int64 id
// needs nothing else; AutoincrementKey is for when rowids must never be
// reused.
func PrimaryKey(first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwPrimaryKey, cols: append([]ddl.ColumnRef{first}, more...)}
}

// Unique makes a unique constraint over one or more columns.
func Unique(first ddl.ColumnRef, more ...ddl.ColumnRef) KeyClause {
	return KeyClause{kw: kwUnique, cols: append([]ddl.ColumnRef{first}, more...)}
}

// PrimaryKeyOn makes the table's primary key over decorated key columns, for
// a key that needs a collation or a sort order; PrimaryKey is the plain form.
func PrimaryKeyOn(first KeyColumn, more ...KeyColumn) KeyClause {
	return keyOn(kwPrimaryKey, first, more)
}

// UniqueOn makes a unique constraint over decorated key columns. A
// case-insensitive unique column is UniqueOn(Key(email).Collate(NoCase)).
func UniqueOn(first KeyColumn, more ...KeyColumn) KeyClause {
	return keyOn(kwUnique, first, more)
}

// keyOn builds a decorated key, keeping cols in step with parts so the
// table's column check and the schema description still see the columns.
func keyOn(kw string, first KeyColumn, more []KeyColumn) KeyClause {
	parts := append([]KeyColumn{first}, more...)
	cols := make([]ddl.ColumnRef, len(parts))
	for i, p := range parts {
		cols[i] = p.col
	}
	return KeyClause{kw: kw, cols: cols, parts: parts}
}

// Named gives the constraint a name.
func (k KeyClause) Named(c TableConstraint) KeyClause { k.con = c; return k }

// OnConflict makes a violation of this constraint resolve by action instead
// of failing. It is SQLite's own and has no equivalent elsewhere: a key that
// ignores duplicates makes every INSERT into the table an INSERT OR IGNORE.
func (k KeyClause) OnConflict(action OrAction) KeyClause { k.conflict = action; return k }

// Position places the constraint among the table's elements.
func (KeyClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the constraint's columns.
func (k KeyClause) NamedColumns() []ddl.ColumnRef { return k.cols }

// WriteSQL renders the constraint.
func (k KeyClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if k.kw == "" || len(k.cols) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	b.WriteString(namedHead(a, k.con) + k.kw + " ")
	if len(k.parts) == 0 {
		b.WriteString(quoteCols(a, k.cols))
	} else {
		b.WriteString("(")
		for i, p := range k.parts {
			if i > 0 {
				b.WriteString(", ")
			}
			p.write(b, a)
		}
		b.WriteString(")")
	}
	b.WriteString(onConflict(a, k.conflict))
}

// TableClauseOf marks the clause as SQLite's.
func (KeyClause) TableClauseOf(Dialect) {}

// RefAction is what a foreign key does when the row it references is deleted
// or updated. SQLite enforces foreign keys only with PRAGMA foreign_keys on,
// which contrib/driver-sqlite sets on every connection.
type RefAction int

// The referential actions.
const (
	RefNoAction RefAction = iota + 1
	RefRestrict
	RefCascade
	RefSetNull
	RefSetDefault
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
	case RefSetDefault:
		return "SET DEFAULT", true
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
	deferral           Deferrability
}

// ForeignKey makes a named foreign key from one or more pairs on one table.
func ForeignKey(c TableConstraint, first ColumnPair, more ...ColumnPair) ForeignKeyClause {
	return ForeignKeyClause{con: c, named: true, pairs: append([]ColumnPair{first}, more...)}
}

// ClauseTable is the constraint's declared table, empty for References.
func (f ForeignKeyClause) ClauseTable() sqlb.Table { return f.con.Table }

// References is the one-column foreign key.
func References[T any](local, ref sqlb.Expr[T]) ForeignKeyClause {
	return ForeignKeyClause{pairs: []ColumnPair{RefPair(local, ref)}}
}

// OnDelete says what happens to this row when the referenced one is deleted.
func (f ForeignKeyClause) OnDelete(r RefAction) ForeignKeyClause { f.onDelete = r; return f }

// OnUpdate says what happens when the referenced key changes.
func (f ForeignKeyClause) OnUpdate(r RefAction) ForeignKeyClause { f.onUpdate = r; return f }

// Deferrability is when SQLite checks a foreign key.
type Deferrability int

// The deferrability settings.
//
// Only DeferrableInitiallyDeferred changes anything: it moves the check to
// COMMIT, so a batch may leave a reference dangling in the middle and repair
// it before the end. NotDeferrable is the default and DeferrableInitiallyImmediate
// means the same thing at run time — the constraint is checked at the end of
// each statement either way. They are here because the spelling is recorded in
// the schema and read back by anything that reads sqlite_schema, so a schema
// that means to say "immediate on purpose" can say it.
const (
	NotDeferrable Deferrability = iota + 1
	DeferrableInitiallyDeferred
	DeferrableInitiallyImmediate
)

func (d Deferrability) keyword() (string, bool) {
	switch d {
	case NotDeferrable:
		return kwNotDeferrable, true
	case DeferrableInitiallyDeferred:
		return kwDeferrable + " " + kwInitDeferred, true
	case DeferrableInitiallyImmediate:
		return kwDeferrable + " " + kwInitImmediate, true
	}
	return "", false
}

// Deferrable sets when the constraint is checked.
//
// Deferring needs PRAGMA defer_foreign_keys or a DEFERRABLE INITIALLY DEFERRED
// key AND foreign-key enforcement on; with enforcement off nothing is checked
// at either time.
func (f ForeignKeyClause) Deferrable(d Deferrability) ForeignKeyClause { f.deferral = d; return f }

// Deferred checks the constraint at COMMIT rather than per statement. It is
// the shorthand for Deferrable(DeferrableInitiallyDeferred), the one setting
// that changes behaviour.
func (f ForeignKeyClause) Deferred() ForeignKeyClause {
	return f.Deferrable(DeferrableInitiallyDeferred)
}

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
	// SQLite's REFERENCES names a table in the same database; a schema here
	// would be an attached database, which a foreign key cannot cross.
	head := ""
	if f.named {
		if !f.con.Valid() {
			a.Fail(ddl.ErrZeroValue)
			return
		}
		head = constraintHead(a, f.con.Name)
	}
	b.WriteString(head + kwForeignKey + " " + quoteCols(a, f.NamedColumns()) +
		" " + kwReferences + " " + a.Dialect().QuoteIdent(target.Name) + " " + quoteCols(a, refs))
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
	if f.deferral != 0 {
		kw, ok := f.deferral.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: Deferrability", ErrUnknownKeyword))
			return
		}
		b.WriteString(" " + kw)
	}
}

// TableClauseOf marks the clause as SQLite's.
func (ForeignKeyClause) TableClauseOf(Dialect) {}

// integer is the Go types SQLite's INTEGER holds.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// AutoincrementKey makes col the INTEGER PRIMARY KEY with AUTOINCREMENT, so a
// rowid is never reused, even after the row with the largest one is deleted.
// It is written in the column's definition, the only place SQLite accepts
// it, and replaces PrimaryKey rather than joining it. Without it SQLite may
// reuse the largest rowid, which is the right default for almost every table.
func AutoincrementKey[T integer](col sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return autoincrementKey{ddl.NewColumnClause[Dialect](col, ddl.PartValueSource, func(*sqlb.Args) string { return kwAutoincrement })}
}

// ColumnKeyClause is a column-level PRIMARY KEY, with the sort order and the
// conflict resolution SQLite lets it carry.
type ColumnKeyClause struct {
	col      ddl.ColumnRef
	dir      string
	conflict OrAction
}

// ColumnKey makes col the table's primary key, written in the column's own
// definition rather than as a table constraint.
//
// The two spellings are not interchangeable. A single INTEGER column written
// as a column-level PRIMARY KEY is an alias for the rowid, SQLite's own row
// number, which is the cheapest key there is; the same column written as
// PrimaryKey(col) is too. What only this form can say is Desc and OnConflict,
// and Desc is the sharp one: INTEGER PRIMARY KEY DESC is explicitly NOT a
// rowid alias, so the column becomes an ordinary indexed column that may hold
// NULL. Reach for this when you want that, or when you want a conflict
// resolution on the key; otherwise PrimaryKey reads better.
//
// AutoincrementKey is the other column-level key and replaces this rather than
// joining it: SQLite accepts AUTOINCREMENT only after a plain ascending
// PRIMARY KEY.
func ColumnKey[T any](col sqlb.Expr[T]) ColumnKeyClause { return ColumnKeyClause{col: col} }

// Asc keys the column ascending, the default, written out.
func (c ColumnKeyClause) Asc() ColumnKeyClause { c.dir = kwAsc; return c }

// Desc keys the column descending, which on an INTEGER column also stops it
// being an alias for the rowid.
func (c ColumnKeyClause) Desc() ColumnKeyClause { c.dir = kwDesc; return c }

// OnConflict resolves a duplicate key by action instead of failing, which
// makes every INSERT into the table behave as INSERT OR <action> for this key.
func (c ColumnKeyClause) OnConflict(action OrAction) ColumnKeyClause {
	c.conflict = action
	return c
}

// Target is the column the clause belongs to.
func (c ColumnKeyClause) Target() ddl.ColumnRef { return c.col }

// Part places the key where a constraint that supplies the column's value
// goes, the same position AUTOINCREMENT takes.
func (ColumnKeyClause) Part() ddl.ColumnPart { return ddl.PartValueSource }

// Position marks the clause as a column clause.
func (ColumnKeyClause) Position() ddl.Position { return ddl.PositionColumn }

// TableClauseOf marks the clause as SQLite's.
func (ColumnKeyClause) TableClauseOf(Dialect) {}

// DescribeInto makes the column the primary key.
func (c ColumnKeyClause) DescribeInto(t *schema.Table) {
	if c.col != nil {
		t.PrimaryKey = []string{c.col.Name()}
	}
}

// WriteSQL renders the key.
func (c ColumnKeyClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if c.col == nil || c.col.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	b.WriteString(kwPrimaryKey)
	if c.dir != "" {
		b.WriteString(" " + c.dir)
	}
	b.WriteString(onConflict(a, c.conflict))
}

// NotNullConflictClause resolves a NOT NULL violation by an action instead of
// failing.
type NotNullConflictClause struct {
	col    ddl.ColumnRef
	action OrAction
}

// NotNullOnConflict says what SQLite does with a row that leaves col null,
// instead of refusing it.
//
// It is the one conflict clause in a column's definition that does anything,
// and what it does is measured rather than read, on 3.50.3 against a column
// declared NOT NULL with a default:
//
//   - OrReplace writes the column's DEFAULT and keeps the row, which is the
//     reason to reach for this at all: a column that is never null in the
//     table but may be absent from a write.
//   - OrIgnore skips the whole row and reports success, so a caller cannot
//     tell it from a row that was written.
//   - OrAbort, OrFail and OrRollback all fail the insert, as the plain NOT
//     NULL does; they differ only in how much they undo.
//
// **OrReplace needs a default**, and Build refuses one without it with
// ddl.ErrNoDefault. Without a default there is nothing to write in place of
// the null, and SQLite falls back to ABORT — measured on 3.50.3, where
// CREATE TABLE accepts the definition and only the first NULL written fails.
// That is why it is refused at Build rather than documented: the schema
// installs cleanly and the failure arrives later, in production. The clause
// declares the need through ddl.DefaultRequirer; ddl, which holds the column
// definition, makes the check.
//
// The clause renders immediately after NOT NULL, at ddl.PartAfterNotNull,
// because that is the only position SQLite accepts: written after the DEFAULT
// instead, the statement is a syntax error. On a column whose definition
// writes no NOT NULL — a nullable one, or a generated one — Build fails with
// ddl.ErrNoNotNull rather than rendering a bare ON CONFLICT, which SQLite
// would read as a constraint of its own.
func NotNullOnConflict[T any](col sqlb.Expr[T], action OrAction) NotNullConflictClause {
	return NotNullConflictClause{col: col, action: action}
}

// Target is the column the clause belongs to.
func (c NotNullConflictClause) Target() ddl.ColumnRef { return c.col }

// Part places the clause immediately after the column's NOT NULL.
func (NotNullConflictClause) Part() ddl.ColumnPart { return ddl.PartAfterNotNull }

// RequiresDefault reports that OrReplace writes the column's default in place
// of a NULL, so ddl refuses it on a column that declares none. The other four
// actions refuse or skip the row and never write the default.
func (c NotNullConflictClause) RequiresDefault() bool { return c.action == OrReplace }

// Position marks the clause as a column clause.
func (NotNullConflictClause) Position() ddl.Position { return ddl.PositionColumn }

// TableClauseOf marks the clause as SQLite's.
func (NotNullConflictClause) TableClauseOf(Dialect) {}

// WriteSQL renders the clause.
func (c NotNullConflictClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if c.col == nil || c.col.Name() == "" {
		a.Fail(ddl.ErrNotAColumn)
		return
	}
	if c.action == "" {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	// The shared helper writes a leading space; this clause is placed by the
	// column renderer, which supplies its own separator.
	b.WriteString(strings.TrimPrefix(onConflict(a, c.action), " "))
}

// Collation is one of SQLite's built-in collations.
type Collation int

// The collations. NoCase folds ASCII letters only, which is what a
// case-insensitive email wants and not what a name in another script does.
const (
	Binary Collation = iota + 1
	NoCase
	RTrim
)

func (c Collation) keyword() (string, bool) {
	switch c {
	case Binary:
		return "BINARY", true
	case NoCase:
		return "NOCASE", true
	case RTrim:
		return "RTRIM", true
	}
	return "", false
}

// collatable is the Go types a collation applies to: text, and nullable text.
type collatable interface{ ~string | *string }

// ColumnCollate gives a text column a collation, which every comparison,
// index and UNIQUE on it then uses.
func ColumnCollate[T collatable](col sqlb.Expr[T], c Collation) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartAfterType, func(a *sqlb.Args) string {
		kw, ok := c.keyword()
		if !ok {
			a.Fail(fmt.Errorf("%w: Collation", ErrUnknownKeyword))
		}
		return kwCollate + " " + kw
	})
}

// generated is a generated column clause, virtual or stored.
type generated struct {
	ddl.ColumnClause[Dialect]
	stored bool
}

// GeneratedVirtual makes col computed by e on every read and stored nowhere.
// It is the one generated column ADD COLUMN accepts.
func GeneratedVirtual[T any](col, e sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return generatedClause(col, e, false)
}

// GeneratedStored makes col computed by e on every write and stored, which an
// index on it needs to be cheap. ADD COLUMN refuses it.
func GeneratedStored[T any](col, e sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return generatedClause(col, e, true)
}

func generatedClause[T any](col, e sqlb.Expr[T], stored bool) ddl.ColumnClause[Dialect] {
	kind := kwVirtual
	if stored {
		kind = kwStored
	}
	return generated{
		ColumnClause: ddl.NewColumnClause[Dialect](col, ddl.PartComputed, func(a *sqlb.Args) string {
			return kwGeneratedAs + " (" + e.SQL(a) + ") " + kind
		}),
		stored: stored,
	}
}

// tableOption is STRICT or WITHOUT ROWID, which SQLite lists with commas.
type tableOption struct{ TableClause }

func (tableOption) ListedOption() {}

// Strict makes SQLite enforce column types, which a library that scans into
// typed Go values wants: without it a TEXT column accepts an integer and the
// scan fails later, far from the write. It restricts column types to INT,
// INTEGER, REAL, TEXT, BLOB and ANY, which the type table's SQLite spellings
// already are.
func Strict() TableClause {
	return tableOption{ddl.NewTableClause[Dialect](ddl.PositionOption, func(*sqlb.Args) string { return kwStrict })}
}

// Temporary makes the table live in the temp schema and vanish when the
// connection closes.
//
// It renders between CREATE and TABLE, which is the one position an engine
// clause can occupy there, and it checks the whole table rather than itself:
// SQLite refuses a temporary table whose name is qualified by any schema but
// temp, so a table declared with one fails Build with ErrTemporaryTable
// instead of reaching the server.
//
// The pooling warning matters more here than the syntax. With database/sql a
// temporary table created by one statement is invisible to the next unless
// both ran on the same connection, and the pool decides that. Create it inside
// a transaction, or on a connection taken with Conn, and never assume a later
// query will find it.
func Temporary() TableClause { return temporary{} }

// temporary is the TEMP modifier, which is also a TableChecker because the
// restriction it carries is about the table's name rather than about itself.
type temporary struct{}

func (temporary) Position() ddl.Position { return ddl.PositionModifier }

func (temporary) WriteSQL(b *strings.Builder, _ *sqlb.Args) { b.WriteString(kwTemporary) }

func (temporary) TableClauseOf(Dialect) {}

// CheckTable refuses a temporary table qualified by another schema.
func (temporary) CheckTable(t schema.Table) error {
	if t.Schema != "" && t.Schema != schemaTemp {
		return fmt.Errorf("%w: %s", ErrTemporaryTable, t.Schema)
	}
	return nil
}

// WithoutRowID stores the table as its primary key's index, which a table
// keyed by something other than an integer is smaller and faster as. It needs
// a primary key, and SQLite refuses the table without one.
func WithoutRowID() TableClause {
	return tableOption{ddl.NewTableClause[Dialect](ddl.PositionOption, func(*sqlb.Args) string { return kwWithoutRowID })}
}

// AddColumn adds a column with column clauses, a collation or a virtual
// generated expression, under the same refusals ddl.AddColumn has on SQLite:
// a NOT NULL column needs a literal default, a default cannot be an
// expression, and a key constraint needs a unique index instead. A stored
// generated column is refused with ErrAddStored.
func AddColumn(c ddl.ColumnSpec, with ...ddl.ColumnClause[Dialect]) AlterClause {
	return addColumn{spec: c, with: with}
}

type addColumn struct {
	spec ddl.ColumnSpec
	with []ddl.ColumnClause[Dialect]
}

func (x addColumn) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	for _, cc := range x.with {
		if g, ok := cc.(generated); ok && g.stored {
			a.Fail(fmt.Errorf("%w: %s", ErrAddStored, x.spec.ColumnName()))
			return
		}
	}
	b.WriteString(kwAddColumn + " ")
	ddl.WriteAddColumn(b, a, x.spec, x.with)
}
func (addColumn) Standalone() bool          { return false }
func (addColumn) AlterClauseOf(Dialect)     {}
func (x addColumn) ClauseTable() sqlb.Table { return x.spec.ColumnTable() }

// RenameTo renames the table to to's name. It takes the new declaration, the
// value the queries will use.
func RenameTo(to sqlb.Table) AlterClause {
	return ddl.NewStandaloneAlterClause[Dialect](func(a *sqlb.Args) string {
		return kwRenameTo + " " + a.Dialect().QuoteIdent(to.Name)
	})
}
