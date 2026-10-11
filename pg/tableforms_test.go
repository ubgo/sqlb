package pg_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The ALTER TABLE forms the detector had recorded as left out, rendered.

var (
	tfParent = sqlb.Table{Name: "people", Schema: "auth"}
	tfRow    = pg.Composite[string]{Schema: "auth", Name: "user_row"}
	tfRule   = pg.Rule{Name: "users_no_delete", Table: tUsers}
)

// TestTableForms_InheritanceAndTypedTables renders the four forms that are
// each a standalone statement in Postgres's grammar, so none of them shares an
// ALTER TABLE with anything else.
func TestTableForms_InheritanceAndTypedTables(t *testing.T) {
	got := buildAll(t,
		pg.AlterTable(tUsers).Clause(pg.Inherit(tfParent)),
		pg.AlterTable(tUsers).Clause(pg.NoInherit(tfParent)),
		pg.AlterTable(tUsers).Clause(pg.OfType(tfRow)),
		pg.AlterTable(tUsers).Clause(pg.NotOfType()),
	)
	want := `ALTER TABLE "auth"."users" INHERIT "auth"."people"
ALTER TABLE "auth"."users" NO INHERIT "auth"."people"
ALTER TABLE "auth"."users" OF "auth"."user_row"
ALTER TABLE "auth"."users" NOT OF`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableForms_InheritanceIsStandalone pins the grammar decision the four
// forms above depend on: each is written alone even when the caller hands
// several actions to one statement, because Postgres's grammar has no comma
// between them.
func TestTableForms_InheritanceIsStandalone(t *testing.T) {
	got := build(t)(pg.AlterTable(tUsers).Clause(
		pg.Inherit(tfParent),
		pg.NoInherit(tfParent),
		pg.SetNotNull(tEmail),
	).Build())
	want := `ALTER TABLE "auth"."users" INHERIT "auth"."people";
ALTER TABLE "auth"."users" NO INHERIT "auth"."people";
ALTER TABLE "auth"."users" ALTER COLUMN "email" SET NOT NULL`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableForms_RuleActions renders the four rule actions, which do share one
// statement: they are ordinary actions in the grammar, unlike the inheritance
// forms above.
func TestTableForms_RuleActions(t *testing.T) {
	got := build(t)(pg.AlterTable(tUsers).Clause(
		pg.DisableRule(tfRule),
		pg.EnableRule(tfRule),
		pg.EnableReplicaRule(tfRule),
		pg.EnableAlwaysRule(tfRule),
	).Build())
	want := `ALTER TABLE "auth"."users" DISABLE RULE "users_no_delete", ` +
		`ENABLE RULE "users_no_delete", ` +
		`ENABLE REPLICA RULE "users_no_delete", ` +
		`ENABLE ALWAYS RULE "users_no_delete"`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableForms_RuleOfAnotherTableIsRefused pins that a Rule carries its
// table for the reason a Trigger and a Policy do: the server would report a
// rule of another table as a rule that does not exist, which reads as a
// missing migration rather than as the wrong value.
func TestTableForms_RuleOfAnotherTableIsRefused(t *testing.T) {
	other := pg.Rule{Name: "r", Table: tSessions}
	if _, err := pg.AlterTable(tUsers).Clause(pg.EnableRule(other)).Build(); err == nil {
		t.Error("a rule of another table was accepted")
	}
}

// TestTableForms_IdentitySequenceOptions renders the last partial ALTER TABLE
// form: the options of the sequence behind an identity column, each carrying
// its own SET, which is the form Postgres takes and is why several of them
// share one ALTER COLUMN. Measured on 17.10 before it was built.
func TestTableForms_IdentitySequenceOptions(t *testing.T) {
	got := build(t)(pg.AlterTable(tUsers).Clause(
		pg.AlterIdentitySequence(tUserID).IncrementBy(2).StartWith(100).MinValue(1).MaxValue(1000).Cache(5).Cycle(),
		pg.AlterIdentitySequence(tUserID).NoCycle(),
	).Build())
	want := `ALTER TABLE "auth"."users" ALTER COLUMN "id" SET INCREMENT BY 2 SET START WITH 100 SET MINVALUE 1 ` +
		`SET MAXVALUE 1000 SET CACHE 5 SET CYCLE, ` +
		`ALTER COLUMN "id" SET NO CYCLE`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestTableForms_IdentitySequenceDoesNotWriteToItsReceiver pins the builder
// invariant for a clause, which the engine-builder gate walks only for types
// with a Build.
func TestTableForms_IdentitySequenceDoesNotWriteToItsReceiver(t *testing.T) {
	base := pg.AlterIdentitySequence(tUserID).IncrementBy(2)
	derived := base.Cycle()
	if !clauseHas(t, derived, "SET CYCLE") {
		t.Error("the derived clause lost its option")
	}
	if clauseHas(t, base, "CYCLE") {
		t.Error("IdentitySequenceClause.Cycle wrote to its receiver")
	}
}

// TestTableForms_RefuseIncompleteValues pins that each form fails Build rather
// than rendering an action with a name missing.
func TestTableForms_RefuseIncompleteValues(t *testing.T) {
	cases := []struct {
		name string
		want error
		run  func() (ddl.Script, error)
	}{
		{"unnamed parent", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.AlterTable(tUsers).Clause(pg.Inherit(sqlb.Table{})).Build()
		}},
		{"unnamed type", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.AlterTable(tUsers).Clause(pg.OfType(pg.Composite[string]{})).Build()
		}},
		{"unnamed rule", pg.ErrEmptyName, func() (ddl.Script, error) {
			return pg.AlterTable(tUsers).Clause(pg.EnableRule(pg.Rule{Table: tUsers})).Build()
		}},
		{"no options", ddl.ErrZeroValue, func() (ddl.Script, error) {
			return pg.AlterTable(tUsers).Clause(pg.AlterIdentitySequence(tUserID)).Build()
		}},
		{"a cache below one", pg.ErrBadCache, func() (ddl.Script, error) {
			return pg.AlterTable(tUsers).Clause(pg.AlterIdentitySequence(tUserID).Cache(0)).Build()
		}},
		{"a column of another table", ddl.ErrForeignColumn, func() (ddl.Script, error) {
			return pg.AlterTable(tUsers).Clause(pg.AlterIdentitySequence(tSessID).IncrementBy(1)).Build()
		}},
	}
	for _, c := range cases {
		if _, err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// TestTableForms_OfTypeIsTypedToItsComposite pins that a typed table names a
// declared Composite rather than a string, so the type and the table it shapes
// are one declaration; the rendering is checked above, and this is the half a
// string could not give.
func TestTableForms_OfTypeIsTypedToItsComposite(t *testing.T) {
	got := build(t)(pg.AlterTable(tUsers).Clause(pg.OfType(pg.Composite[int64]{Name: "n"})).Build())
	if !strings.HasSuffix(got, `OF "n"`) {
		t.Fatalf("got %s", got)
	}
}
