package pg

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
)

// The Postgres clauses for CREATE TABLE and DROP TABLE: keys, references,
// identity and generated columns, collation, UNLOGGED and CASCADE. Every one is
// typed to the Postgres dialect, so on a SQLite statement it does not compile,
// and every one that names a column takes the column value rather than its
// name, so the statement can check at Build that the table defines it.

const (
	kwConstraint        = "CONSTRAINT"
	kwPrimaryKey        = "PRIMARY KEY"
	kwUnique            = "UNIQUE"
	kwNullsNotDistinct  = "NULLS NOT DISTINCT"
	kwForeignKey        = "FOREIGN KEY"
	kwReferences        = "REFERENCES"
	kwOnDelete          = "ON DELETE"
	kwOnUpdate          = "ON UPDATE"
	kwGenerated         = "GENERATED"
	kwAsIdentity        = "AS IDENTITY"
	kwGeneratedAlwaysAs = "GENERATED ALWAYS AS"
	kwStored            = "STORED"
	kwCollate           = "COLLATE"
	kwUnlogged          = "UNLOGGED"
)

// TableClause is a Postgres clause for CREATE TABLE.
type TableClause = ddl.TableClause[Dialect]

// DropClause is a Postgres option after DROP TABLE's table name.
type DropClause = ddl.DropClause[Dialect]

// ErrMixedReferences is returned for a foreign key whose referenced columns
// are on more than one table, which no engine accepts.
var ErrMixedReferences = errors.New("sqlb/pg: a foreign key references one table")

// ErrUnknownKeyword is returned for a referential action, deferral or identity
// kind outside the named constants, such as a zero value.
var ErrUnknownKeyword = errors.New("sqlb/pg: value outside its named constants")

// RefAction is what a foreign key does when the row it references is deleted
// or updated. It is a closed set: a value outside the named constants fails
// Build with ErrUnknownKeyword rather than being written into the statement.
type RefAction int

// The referential actions. They carry a Ref prefix because pg.Cascade is the
// DROP TABLE clause and pg.SetDefault the ALTER TABLE action.
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
		return kwRestrict, true
	case RefCascade:
		return kwCascade, true
	case RefSetNull:
		return "SET NULL", true
	case RefSetDefault:
		return "SET DEFAULT", true
	}
	return "", false
}

// Deferral is when a deferrable constraint is checked.
type Deferral int

// The deferrals. InitiallyDeferred checks at COMMIT, which is what lets a
// transaction insert two rows that reference each other in either order.
const (
	InitiallyImmediate Deferral = iota + 1
	InitiallyDeferred
)

func (d Deferral) keyword() (string, bool) {
	switch d {
	case InitiallyImmediate:
		return "DEFERRABLE INITIALLY IMMEDIATE", true
	case InitiallyDeferred:
		return "DEFERRABLE INITIALLY DEFERRED", true
	}
	return "", false
}

// IdentityKind is whether an identity column refuses an explicit value.
type IdentityKind int

// The identity kinds. Always refuses an INSERT that names the column unless
// it says OVERRIDING SYSTEM VALUE; ByDefault takes an explicit value.
const (
	Always IdentityKind = iota + 1
	ByDefault
)

func (k IdentityKind) keyword() (string, bool) {
	switch k {
	case Always:
		return "ALWAYS", true
	case ByDefault:
		return "BY DEFAULT", true
	}
	return "", false
}

// keywordOf writes a closed-set value's keyword, failing Build on one outside
// its constants.
func keywordOf(a *sqlb.Args, kw string, ok bool, what string) string {
	if !ok {
		a.Fail(fmt.Errorf("%w: %s", ErrUnknownKeyword, what))
	}
	return kw
}

// quoteCols writes a parenthesised list of bare column names.
func quoteCols(a *sqlb.Args, cols []ddl.ColumnRef) string {
	d := a.Dialect()
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = d.QuoteIdent(c.Name())
	}
	return "(" + strings.Join(names, ", ") + ")"
}

// TableConstraint is a named constraint of one table, declared once. See
// ddl.TableConstraint.
type TableConstraint = ddl.TableConstraint

// constraintHead writes CONSTRAINT "name" and a space, or nothing when unnamed.
func constraintHead(a *sqlb.Args, name string) string {
	if name == "" {
		return ""
	}
	return kwConstraint + " " + a.Dialect().QuoteIdent(name) + " "
}

// namedHead is constraintHead for a constraint that must be named, recording
// ErrZeroValue for a TableConstraint missing its name or table.
func namedHead(a *sqlb.Args, c TableConstraint) string {
	if !c.Valid() {
		a.Fail(ddl.ErrZeroValue)
		return ""
	}
	return constraintHead(a, c.Name)
}

// PrimaryKeyClause is a PRIMARY KEY table constraint.
type PrimaryKeyClause struct {
	con  TableConstraint // zero for an unnamed key
	cols []ddl.ColumnRef
	// include and with are the index parameters the key's own index takes:
	// covering columns and storage parameters.
	include  []ddl.ColumnRef
	with     []StorageParameter
	overlaps bool // the last column is a period, WITHOUT OVERLAPS
}

// PrimaryKey makes the table's primary key from one or more of its columns.
// It is written as a table constraint, which means the same as the column
// form and is the only form a composite key has.
func PrimaryKey(first ddl.ColumnRef, more ...ddl.ColumnRef) PrimaryKeyClause {
	return PrimaryKeyClause{cols: append([]ddl.ColumnRef{first}, more...)}
}

// Named gives the key a declared name, which ALTER TABLE can later drop by.
// Unnamed, Postgres names it table_pkey.
func (k PrimaryKeyClause) Named(c TableConstraint) PrimaryKeyClause { k.con = c; return k }

// ClauseTable is the key's declared table, empty for an unnamed key.
func (k PrimaryKeyClause) ClauseTable() sqlb.Table { return k.con.Table }

// Position places the key among the table's elements.
func (PrimaryKeyClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the key's columns, which the statement checks it defines.
func (k PrimaryKeyClause) NamedColumns() []ddl.ColumnRef { return k.cols }

// WriteSQL renders the constraint.
func (k PrimaryKeyClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if len(k.cols) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	head := ""
	if k.con != (TableConstraint{}) {
		head = namedHead(a, k.con)
	}
	b.WriteString(head + kwPrimaryKey + " " + quoteKeyCols(a, k.cols, k.overlaps) + writeIndexParameters(a, k.include, k.with))
}

// TableClauseOf marks the clause as Postgres's.
func (PrimaryKeyClause) TableClauseOf(Dialect) {}

// UniqueClause is a UNIQUE table constraint.
type UniqueClause struct {
	con  TableConstraint
	cols []ddl.ColumnRef
	// include and with are the index parameters the constraint's own index
	// takes: covering columns and storage parameters.
	include  []ddl.ColumnRef
	with     []StorageParameter
	nullsNot bool
	overlaps bool // the last column is a period, WITHOUT OVERLAPS
}

// Unique makes a named unique constraint over one or more columns. The name is
// required because a unique constraint is the thing a later migration drops or
// renames, and the name Postgres would choose is not written anywhere a reader
// of this code can see.
func Unique(c TableConstraint, first ddl.ColumnRef, more ...ddl.ColumnRef) UniqueClause {
	return UniqueClause{con: c, cols: append([]ddl.ColumnRef{first}, more...)}
}

// ClauseTable is the constraint's declared table.
func (u UniqueClause) ClauseTable() sqlb.Table { return u.con.Table }

// NullsNotDistinct makes two NULLs collide, so a nullable column holds at most
// one NULL. Without it every NULL is distinct and any number are allowed.
// Postgres 15 or later.
func (u UniqueClause) NullsNotDistinct() UniqueClause { u.nullsNot = true; return u }

// Position places the constraint among the table's elements.
func (UniqueClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the constraint's columns.
func (u UniqueClause) NamedColumns() []ddl.ColumnRef { return u.cols }

// WriteSQL renders the constraint.
func (u UniqueClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if len(u.cols) == 0 {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	b.WriteString(namedHead(a, u.con) + kwUnique + " ")
	if u.nullsNot {
		b.WriteString(kwNullsNotDistinct + " ")
	}
	b.WriteString(quoteKeyCols(a, u.cols, u.overlaps) + writeIndexParameters(a, u.include, u.with))
}

// TableClauseOf marks the clause as Postgres's.
func (UniqueClause) TableClauseOf(Dialect) {}

// ColumnPair is one column of a foreign key and the column it references.
type ColumnPair struct{ local, ref ddl.ColumnRef }

// RefPair pairs a column with the column it references. Both are of one Go
// type, so a bigint referencing a text column does not compile. A composite
// key is a list of pairs rather than two column lists, because Go generics
// cannot express two variadic lists of matching types; that is the reason
// scan.Map2 is pairwise too.
func RefPair[T any](local, ref sqlb.Expr[T]) ColumnPair { return ColumnPair{local: local, ref: ref} }

// ForeignKeyClause is a FOREIGN KEY table constraint.
type ForeignKeyClause struct {
	con      TableConstraint // zero for an inline References
	pairs    []ColumnPair
	onDelete RefAction
	onUpdate RefAction
	// match is how a partly NULL composite key is treated, SIMPLE by default.
	match    MatchType
	deferral Deferral
	// named is true for ForeignKey, whose TableConstraint is required.
	named       bool
	notEnforced bool
	// period is the local and referenced period columns of a temporal
	// foreign key, nil for an ordinary one.
	period *[2]ddl.ColumnRef
}

// ForeignKey makes a named foreign key from one or more pairs, all
// referencing one table.
func ForeignKey(c TableConstraint, first ColumnPair, more ...ColumnPair) ForeignKeyClause {
	return ForeignKeyClause{con: c, named: true, pairs: append([]ColumnPair{first}, more...)}
}

// ClauseTable is the constraint's declared table, empty for References.
func (f ForeignKeyClause) ClauseTable() sqlb.Table { return f.con.Table }

// References is the one-column foreign key, left for Postgres to name.
func References[T any](local, ref sqlb.Expr[T]) ForeignKeyClause {
	return ForeignKeyClause{pairs: []ColumnPair{RefPair(local, ref)}}
}

// OnDelete says what happens to this row when the referenced one is deleted.
func (f ForeignKeyClause) OnDelete(r RefAction) ForeignKeyClause { f.onDelete = r; return f }

// OnUpdate says what happens when the referenced key changes.
func (f ForeignKeyClause) OnUpdate(r RefAction) ForeignKeyClause { f.onUpdate = r; return f }

// Deferrable makes the constraint deferrable, checked when d says.
func (f ForeignKeyClause) Deferrable(d Deferral) ForeignKeyClause { f.deferral = d; return f }

// Position places the constraint among the table's elements.
func (ForeignKeyClause) Position() ddl.Position { return ddl.PositionElement }

// NamedColumns is the constraint's local columns; the referenced ones are on
// another table by definition.
func (f ForeignKeyClause) NamedColumns() []ddl.ColumnRef {
	out := f.localColumns()
	if f.period != nil {
		out = append(out, f.period[0])
	}
	return out
}

// localColumns is the key's local columns without a temporal key's period,
// which is written after them with PERIOD.
func (f ForeignKeyClause) localColumns() []ddl.ColumnRef {
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
	local := quoteCols(a, f.localColumns())
	remote := quoteCols(a, refs)
	if f.period != nil {
		d := a.Dialect()
		local = strings.TrimSuffix(local, ")") + ", " + kwPeriod + " " + d.QuoteIdent(f.period[0].Name()) + ")"
		remote = strings.TrimSuffix(remote, ")") + ", " + kwPeriod + " " + d.QuoteIdent(f.period[1].Name()) + ")"
	}
	b.WriteString(head + kwForeignKey + " " + local)
	b.WriteString(" " + kwReferences + " " + target.Qualified(a.Dialect()) + " " + remote)
	if f.match != 0 {
		kw, ok := f.match.keyword()
		b.WriteString(" " + kwMatch + " " + keywordOf(a, kw, ok, "Match"))
	}
	if f.onDelete != 0 {
		kw, ok := f.onDelete.keyword()
		b.WriteString(" " + kwOnDelete + " " + keywordOf(a, kw, ok, "OnDelete"))
	}
	if f.onUpdate != 0 {
		kw, ok := f.onUpdate.keyword()
		b.WriteString(" " + kwOnUpdate + " " + keywordOf(a, kw, ok, "OnUpdate"))
	}
	if f.deferral != 0 {
		kw, ok := f.deferral.keyword()
		b.WriteString(" " + keywordOf(a, kw, ok, "Deferrable"))
	}
	if f.notEnforced {
		b.WriteString(" " + kwNotEnforced)
	}
}

// TableClauseOf marks the clause as Postgres's.
func (ForeignKeyClause) TableClauseOf(Dialect) {}

// CheckClause is a named CHECK table constraint, the same one ddl.Check writes.
type CheckClause struct {
	con TableConstraint
	c   ddl.Constraint
	// noInherit keeps the check off the tables that inherit this one.
	noInherit   bool
	notEnforced bool
}

// Check makes a named CHECK constraint. In CREATE TABLE it is the same as
// ddl.Check; it exists because ALTER TABLE ... ADD CONSTRAINT is Postgres's and
// takes a Postgres constraint.
func Check(c TableConstraint, p sqlb.Predicate) CheckClause {
	return CheckClause{con: c, c: ddl.Check(c, p)}
}

// ClauseTable is the constraint's declared table.
func (c CheckClause) ClauseTable() sqlb.Table { return c.con.Table }

// Position places the constraint among the table's elements.
func (CheckClause) Position() ddl.Position { return ddl.PositionElement }

// WriteSQL renders the constraint, with columns bare as a CHECK requires.
func (c CheckClause) WriteSQL(b *strings.Builder, a *sqlb.Args) {
	if c.c == nil {
		a.Fail(ddl.ErrZeroValue)
		return
	}
	c.c.WriteConstraint(b, a)
	if c.noInherit {
		b.WriteString(" " + kwNoInherit)
	}
	if c.notEnforced {
		b.WriteString(" " + kwNotEnforced)
	}
}

// TableClauseOf marks the clause as Postgres's.
func (CheckClause) TableClauseOf(Dialect) {}

// identityInteger is the Go types an identity column may have: Postgres
// accepts smallint, integer and bigint, and nothing else.
type identityInteger interface{ ~int16 | ~int32 | ~int64 }

// Identity makes col an identity column, its value drawn from a sequence
// Postgres owns. It is the standard form of what serial did, without serial's
// separately owned sequence. The column cannot also have a default, which
// Build refuses. Typed to the integer kinds Postgres accepts, so an identity
// on a text column does not compile.
func Identity[T identityInteger](col sqlb.Expr[T], k IdentityKind) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartValueSource, func(a *sqlb.Args) string {
		kw, ok := k.keyword()
		return kwGenerated + " " + keywordOf(a, kw, ok, "Identity") + " " + kwAsIdentity
	})
}

// GeneratedStored makes col a stored generated column computed by e, which is
// of the column's own Go type. The application never writes it, so NOT NULL is
// not written and a default is refused; e names the table's columns bare.
func GeneratedStored[T any](col, e sqlb.Expr[T]) ddl.ColumnClause[Dialect] {
	return ddl.NewColumnClause[Dialect](col, ddl.PartComputed, func(a *sqlb.Args) string {
		return kwGeneratedAlwaysAs + " (" + e.SQL(a) + ") " + kwStored
	})
}

// collatable is the Go types a collation applies to: text, and nullable
// text. A pointer to a named string type is not in the set, because a
// constraint cannot spell a pointer to an approximation; give that column its
// collation with ddl.RawType.
type collatable interface{ ~string | *string }

// ColumnCollate gives a text column a collation, written straight after its
// type. It is not Collate, which is the expression-level COLLATE a query uses.
func ColumnCollate[T collatable](col sqlb.Expr[T], collation string) ddl.ColumnClause[Dialect] {
	return ddl.NewRankedColumnClause[Dialect](colRankCollate, col, ddl.PartAfterType, func(a *sqlb.Args) string {
		return kwCollate + " " + a.Dialect().QuoteIdent(collation)
	})
}

// Unlogged makes the table skip the write-ahead log: faster, lost on a crash,
// and not replicated. For data a library can rebuild, such as rate-limit
// counters.
func Unlogged() TableClause {
	return ddl.NewTableClause[Dialect](ddl.PositionModifier, func(*sqlb.Args) string { return kwUnlogged })
}

// Cascade makes DROP TABLE also drop what depends on the table: views and the
// foreign keys of other tables, never the other tables themselves.
func Cascade() DropClause {
	return ddl.NewDropClause[Dialect](func(*sqlb.Args) string { return kwCascade })
}
