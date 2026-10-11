package ddl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ubgo/sqlb/render"
)

// The ADD COLUMN refusals for a dialect without render.UnrestrictedAddColumn.
// Each names the rule SQLite would otherwise report as an error about syntax,
// measured on SQLite 3.50.3, and says what to write instead.
//
// SQLite refuses a key constraint always, but the other three only when the
// table has rows: on an empty table it accepts them. Build refuses them
// unconditionally on purpose, because the empty table is the test database
// and the table with rows is production, so accepting them is how a migration
// passes CI and fails on deploy. contrib/driver-sqlite measures both halves.
var (
	// ErrAddNotNull: a NOT NULL column needs a literal default, because the
	// existing rows get it. Give one with Default, or make it Nullable.
	ErrAddNotNull = errors.New("sqlb/ddl: this dialect cannot add a NOT NULL column without a literal default")
	// ErrAddNonConstantDefault: the existing rows would need the expression
	// evaluated for each, which SQLite refuses. Add it nullable and backfill.
	ErrAddNonConstantDefault = errors.New("sqlb/ddl: this dialect cannot add a column with an expression default")
	// ErrAddConstraint: a key on an added column needs an index built over
	// the existing rows. Add the column, then create a unique index.
	ErrAddConstraint = errors.New("sqlb/ddl: this dialect cannot add a column with a key constraint")
)

// checkAddColumn refuses what a restricted dialect's ADD COLUMN cannot do.
func checkAddColumn(a *render.Args, spec ColumnSpec, x ColumnExtras) bool {
	if u, ok := a.Dialect().(render.UnrestrictedAddColumn); ok && u.SupportsUnrestrictedAddColumn() {
		return true
	}
	if x.ValueSource || len(x.Constraints) > 0 && !x.Computed {
		a.Fail(fmt.Errorf("%w: %s", ErrAddConstraint, spec.ColumnName()))
		return false
	}
	fr, ok := spec.(FactsReporter)
	if !ok {
		return true
	}
	f := fr.ColumnFacts()
	switch {
	case f.Default == DefaultExpression:
		a.Fail(fmt.Errorf("%w: %s", ErrAddNonConstantDefault, spec.ColumnName()))
		return false
	case f.NotNull && f.Default == DefaultNone && !x.Computed:
		a.Fail(fmt.Errorf("%w: %s", ErrAddNotNull, spec.ColumnName()))
		return false
	}
	return true
}

// WriteAddColumn renders an added column with its column clauses and the
// restricted-dialect checks, for an engine's own ADD COLUMN action, so the
// engine's action and AddColumn refuse the same things.
func WriteAddColumn[D render.Dialect](b *strings.Builder, a *render.Args, spec ColumnSpec, clauses []ColumnClause[D]) {
	var x ColumnExtras
	for _, cc := range clauses {
		t := cc.Target()
		switch {
		case t == nil || t.Name() == "":
			a.Fail(ErrNoColumnTarget)
			return
		case !sameTable(t.Table(), spec.ColumnTable()) || t.Name() != spec.ColumnName():
			a.Fail(fmt.Errorf("%w: %s is not the column being added, %s", ErrUnknownColumn, t.Name(), spec.ColumnName()))
			return
		}
		if !x.add(a, cc) {
			return
		}
	}
	if !checkAddColumn(a, spec, x) {
		return
	}
	spec.WriteColumn(b, a, x)
}

// ListedOption is implemented by a CREATE TABLE option clause that belongs to
// a comma-separated list. SQLite writes its table options that way, ") STRICT,
// WITHOUT ROWID", where Postgres separates its options with spaces; the
// statement joins consecutive listed options with commas.
type ListedOption interface {
	ListedOption()
}
