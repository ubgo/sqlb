package ddl_test

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/dialect/postgres"
	"github.com/ubgo/sqlb/render"
)

// opt makes a ranked option; raw makes an unranked one, the way a caller's own
// clause from outside the module is made.
func opt(rank ddl.OptionRank, s string) ddl.TableClause[postgres.Dialect] {
	return ddl.NewOptionClause[postgres.Dialect](rank, func(*render.Args) string { return s })
}

func raw(s string) ddl.TableClause[postgres.Dialect] {
	return ddl.NewTableClause[postgres.Dialect](ddl.PositionOption, func(*render.Args) string { return s })
}

// tail renders a CREATE TABLE and returns what follows the closing
// parenthesis, which is where the options land.
func tail(t *testing.T, cls ...ddl.TableClause[postgres.Dialect]) string {
	t.Helper()
	sql := one(t)(ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).Clause(cls...).Build())
	const head = `CREATE TABLE "users" ("id" BIGINT NOT NULL)`
	if len(sql) < len(head) || sql[:len(head)] != head {
		t.Fatalf("unexpected head: %s", sql)
	}
	return sql[len(head):]
}

// TestOptionRank_RankedOptionsRenderInRankOrder is the fix itself: options
// passed in any order render in the order their ranks give. See
// ddl.OptionRank for why the statement sorts rather than refusing.
func TestOptionRank_RankedOptionsRenderInRankOrder(t *testing.T) {
	want := " A B C"
	for name, cls := range map[string][]ddl.TableClause[postgres.Dialect]{
		"already in order": {opt(1, "A"), opt(2, "B"), opt(3, "C")},
		"reversed":         {opt(3, "C"), opt(2, "B"), opt(1, "A")},
		"shuffled":         {opt(2, "B"), opt(3, "C"), opt(1, "A")},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tail(t, cls...); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// TestOptionRank_UnrankedOptionsStayWhereTheCallerPutThem pins the rule for
// an option the module does not understand: it does not move, and the ranked
// options sort only among the positions ranked options already held. No
// default place for an unknown option is right in every case, so keeping the
// caller's placement is the one choice that never makes a correct statement
// wrong.
func TestOptionRank_UnrankedOptionsStayWhereTheCallerPutThem(t *testing.T) {
	// Positions 0 and 2 are ranked and swap; the raw one at 1 does not move.
	if got, want := tail(t, opt(2, "B"), raw("RAW"), opt(1, "A")), " A RAW B"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A raw option last stays last, which is where a raw TABLESPACE belongs
	// on Postgres.
	if got, want := tail(t, opt(2, "B"), opt(1, "A"), raw("TABLESPACE x")), " A B TABLESPACE x"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// With no ranked option at all nothing is reordered.
	if got, want := tail(t, raw("Z"), raw("Y")), " Z Y"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestOptionRank_EqualRanksKeepTheCallersOrder pins that the sort is stable,
// which MySQL depends on: its table options share one rank because MySQL takes
// them in any order, so the caller's order among them is kept and only
// PARTITION BY, at a higher rank, moves.
func TestOptionRank_EqualRanksKeepTheCallersOrder(t *testing.T) {
	if got, want := tail(t, opt(2, "PART"), opt(1, "ENGINE"), opt(1, "CHARSET"), opt(1, "COMMENT")),
		" ENGINE CHARSET COMMENT PART"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestOptionRank_SortingDoesNotReorderTheTemplate pins value semantics end to
// end: building a statement derived from a template, then the template again,
// gives the template's own answer both times, and the derived statement gets
// its added option sorted into place.
//
// It does NOT guard the copy in orderOptions, and says so because that was
// checked by breaking it: Build groups clauses into a map made afresh on every
// call, so an in-place sort would touch only that one Build's slice and this
// test stays green without the copy. What it guards is the property itself,
// against any future change that makes the options shared state.
func TestOptionRank_SortingDoesNotReorderTheTemplate(t *testing.T) {
	base := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id)).
		Clause(opt(3, "C"), raw("RAW"), opt(1, "A"))
	first := one(t)(base.Build())
	derived := one(t)(base.Clause(opt(2, "B")).Build())
	again := one(t)(base.Build())
	if first != again {
		t.Fatalf("building a derived statement changed its template:\n%s\n%s", first, again)
	}
	if want := `CREATE TABLE "users" ("id" BIGINT NOT NULL) A RAW C`; first != want {
		t.Fatalf("got  %s\nwant %s", first, want)
	}
	if want := `CREATE TABLE "users" ("id" BIGINT NOT NULL) A RAW B C`; derived != want {
		t.Fatalf("got  %s\nwant %s", derived, want)
	}
}

// TestDefaultRequirer_RefusesAColumnWithNoDefault pins the ddl half of the
// check, with no engine involved: a column clause that says it needs a
// default is refused on a column that declares none, and accepted on one that
// does. The SQLite clause that motivated it is tested in contrib/driver-sqlite
// against a real database.
func TestDefaultRequirer_RefusesAColumnWithNoDefault(t *testing.T) {
	needs := needsDefault{ddl.NewColumnClause[postgres.Dialect](email, ddl.PartConstraint, func(*render.Args) string { return "NEEDS" })}

	_, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id), ddl.Column(email)).Clause(needs).Build()
	if !errors.Is(err, ddl.ErrNoDefault) {
		t.Fatalf("want ErrNoDefault, got %v", err)
	}
	// The control: a default makes the same clause legal.
	if _, err := ddl.NewCreateTable(postgres.Dialect{}, users).Column(ddl.Column(id), ddl.Column(email).Default("x")).Clause(needs).Build(); err != nil {
		t.Fatalf("with a default the clause must build: %v", err)
	}
}

// needsDefault wraps a column clause and adds DefaultRequirer, the way an
// engine clause declares the need.
type needsDefault struct {
	ddl.ColumnClause[postgres.Dialect]
}

func (needsDefault) RequiresDefault() bool { return true }
