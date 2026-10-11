package driverpgx_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The second pass: the forms inside a statement that already had a
// constructor. The detector said "built" for each of these commands while a
// form of it could not be written at all, so the same rule applies one level
// down — a form is not built until the server has taken it.

var (
	ofT    = sqlb.Table{Name: "of_items", Alias: "i"}
	ofID   = sqlb.Col[int64](ofT, "id")
	ofName = sqlb.Col[string](ofT, "name")
	ofKind = sqlb.Col[string](ofT, "kind")
)

func ofTable() *pg.CreateTableReady {
	return pg.CreateTable(ofT).Column(ddl.Column(ofID), ddl.Column(ofName), ddl.Column(ofKind))
}

// TestTask_InstallAnExtensionIntoItsOwnSchema runs the CREATE EXTENSION forms
// that were missing: a version pinned, a schema of the library's own, and the
// cascade that installs a dependency rather than failing on it.
func TestTask_InstallAnExtensionIntoItsOwnSchema(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		sch := pg.Schema{Name: "of_ext"}
		runB(t, tx, pg.CreateSchema(sch))
		ext := pg.Extension{Name: "btree_gist"}
		// Dropped first, because IF NOT EXISTS on an extension the database
		// already has is a no-op and would leave SCHEMA unverified.
		runB(t, tx, pg.DropExtension(ext).IfExists().Cascade())
		runB(t, tx, pg.CreateExtension(ext).IfNotExists().Schema(sch).Cascade())
		var name string
		if err := tx.QueryRowContext(ctx,
			`SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'btree_gist'`).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "of_ext" {
			t.Errorf("the extension landed in schema %q", name)
		}
		// A pinned version, read back from the catalogue. The version is the
		// one the server reports for what it just installed, so the test does
		// not hard-code a number that differs per package build.
		var version string
		if err := tx.QueryRowContext(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'btree_gist'`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		runB(t, tx, pg.DropExtension(ext))
		runB(t, tx, pg.CreateExtension(ext).Schema(sch).Version(version))
		var got string
		if err := tx.QueryRowContext(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'btree_gist'`).Scan(&got); err != nil || got != version {
			t.Errorf("the pinned version gave %q, want %q, %v", got, version, err)
		}
	})
}

// TestTask_CreateASchemaForAnotherRole runs CREATE SCHEMA AUTHORIZATION and
// ALTER SCHEMA OWNER TO, the two halves of installing a library's schema under
// the role that will run the application.
func TestTask_CreateASchemaForAnotherRole(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		if _, err := tx.ExecContext(ctx, `CREATE ROLE of_owner`); err != nil {
			t.Skipf("SKIPPED, NOT PASSED: this case needs a role that may create roles: %v", err)
		}
		role := pg.Role{Name: "of_owner"}
		runB(t, tx, pg.CreateSchema(pg.Schema{Name: "of_a"}).Authorization(role))
		runB(t, tx, pg.CreateSchema(pg.Schema{Name: "of_b"}))
		runB(t, tx, pg.SetOwner(pg.Schema{Name: "of_b"}, role))
		for _, s := range []string{"of_a", "of_b"} {
			var owner string
			if err := tx.QueryRowContext(ctx,
				`SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = $1`, s).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner != "of_owner" {
				t.Errorf("schema %s is owned by %q", s, owner)
			}
		}
	})
}

// TestTask_GiveADomainItsWholeDeclaration runs every ALTER DOMAIN form that
// was missing and the two CREATE DOMAIN options, then has the server enforce
// each.
func TestTask_GiveADomainItsWholeDeclaration(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		d := pg.Domain[string]{Name: "of_code"}
		runB(t, tx, pg.CreateDomain(d, ddl.RawType[string]("text")).
			Collate(pg.Collation{Name: "C"}).
			Default("none").
			Check(pg.DomainConstraint[string]{Domain: d, Name: "of_code_len"}, sqlb.NotEq(sqlb.Lower(pg.DomainValue[string]()), "")))

		tbl := sqlb.Table{Name: "of_codes"}
		c := sqlb.Col[string](tbl, "c")
		runB(t, tx, pg.CreateTable(tbl).Column(ddl.Column(c).As(d).Nullable()))
		if _, err := tx.ExecContext(ctx, `INSERT INTO of_codes DEFAULT VALUES`); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := tx.QueryRowContext(ctx, `SELECT c FROM of_codes`).Scan(&got); err != nil || got != "none" {
			t.Errorf("the domain's default gave %q, %v", got, err)
		}

		// NOT NULL over every column of the type, which is what makes it a
		// property of the domain rather than of one column.
		runB(t, tx, pg.AlterDomain(d).SetNotNull())
		if !failsAt(t, tx, `INSERT INTO of_codes VALUES (NULL)`) {
			t.Error("SET NOT NULL did not reach the domain")
		}
		runB(t, tx, pg.AlterDomain(d).DropNotNull())
		if _, err := tx.ExecContext(ctx, `INSERT INTO of_codes VALUES (NULL)`); err != nil {
			t.Errorf("DROP NOT NULL left the domain refusing NULL: %v", err)
		}

		// A renamed constraint, then the constraint dropped with CASCADE, then
		// the domain renamed.
		from := pg.DomainConstraint[string]{Domain: d, Name: "of_code_len"}
		to := pg.DomainConstraint[string]{Domain: d, Name: "of_code_length"}
		runB(t, tx, pg.AlterDomain(d).RenameConstraint(from, to))
		if n := constraintCount(t, tx, "of_code_length"); n != 1 {
			t.Errorf("after RENAME CONSTRAINT the new name has %d constraints", n)
		}
		runB(t, tx, pg.AlterDomain(d).DropConstraint(to).Cascade())
		if n := constraintCount(t, tx, "of_code_length"); n != 0 {
			t.Errorf("after DROP CONSTRAINT CASCADE the constraint is still there")
		}
		renamed := pg.Domain[string]{Name: "of_code_v2"}
		runB(t, tx, pg.AlterDomain(d).RenameTo(renamed))
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT count(*) = 1 FROM pg_type WHERE typname = 'of_code_v2'`).Scan(&exists); err != nil || !exists {
			t.Errorf("the domain was not renamed: %v", err)
		}
		// A comment on a domain's constraint, which is spelled ON DOMAIN and
		// not ON as a table's is.
		runB(t, tx, pg.AlterDomain(renamed).AddConstraint(pg.DomainConstraint[string]{Domain: renamed, Name: "of_code_v2_len"},
			sqlb.NotEq(sqlb.Lower(pg.DomainValue[string]()), "")))
		runB(t, tx, pg.CommentOnDomainConstraint(pg.DomainConstraint[string]{Domain: renamed, Name: "of_code_v2_len"}).Is("not empty"))
	})
}

// constraintCount is how many constraints of a name the catalogue holds.
func constraintCount(t *testing.T, tx *sql.Tx, name string) int {
	t.Helper()
	var n int
	if err := tx.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_constraint WHERE conname = $1`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTask_DeclareACollationTheDeploymentsWay runs the CREATE COLLATION forms
// that were missing: the libc provider's two halves, an ICU collation with
// tailoring rules, a recorded library version, and the copy of an existing
// collation under a name of the library's own.
func TestTask_DeclareACollationTheDeploymentsWay(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		// A name of the library's own for whatever the deployment's default
		// collation is, which is what the FROM form is for.
		runB(t, tx, pg.CreateCollationFrom(pg.Collation{Name: "of_default"}, pg.Collation{Name: "C"}))
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_collation WHERE collname = 'of_default'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the FROM form made %d collations, %v", n, err)
		}

		runB(t, tx, pg.CreateCollation(pg.Collation{Name: "of_icu"}, "und-u-ks-level2").ICU().Nondeterministic())
		runB(t, tx, pg.CreateCollation(pg.Collation{Name: "of_libc"}, "").LcCollate("C").LcCtype("C"))
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_collation WHERE collname IN ('of_icu', 'of_libc')`).Scan(&n); err != nil || n != 2 {
			t.Errorf("the option forms made %d collations, %v", n, err)
		}

		// Tailoring rules are ICU and Postgres 16 and later; a recorded
		// version is what lets the server warn when an upgrade changes the
		// sort order under an index.
		if versionOf(t, openOrSkip(t)) >= 160000 {
			runB(t, tx, pg.CreateCollation(pg.Collation{Name: "of_rules"}, "und").ICU().Rules("&a < g"))
			var rules sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT collicurules FROM pg_collation WHERE collname = 'of_rules'`).Scan(&rules); err != nil {
				t.Fatal(err)
			}
			if rules.String != "&a < g" {
				t.Errorf("the collation's rules are %q", rules.String)
			}
		}
		// A recorded library version, which the server stores as given: it is
		// the value an upgrade is compared against, so the declaration is what
		// decides it rather than the server. It may only be recorded for a
		// collation whose provider reports one at all, which ICU always does
		// and the libc C locale never does: Postgres 18.6 refuses that pair
		// with "collation has no actual version, but a version was recorded",
		// where 17.10 on macOS accepted it. ICU is the portable half.
		runB(t, tx, pg.CreateCollation(pg.Collation{Name: "of_versioned"}, "und").ICU().Version("2.36"))
		var stored sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT collversion FROM pg_collation WHERE collname = 'of_versioned'`).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored.String != "2.36" {
			t.Errorf("the collation's recorded version is %q", stored.String)
		}
	})
}

// TestTask_MeasureAnExpressionThePlannerCannotGuess runs the CREATE STATISTICS
// forms over an expression, and checks the planner stored statistics for the
// expression rather than for a column.
func TestTask_MeasureAnExpressionThePlannerCannotGuess(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, ofTable())
		if _, err := tx.ExecContext(ctx, `INSERT INTO of_items (id, name, kind) SELECT i, 'n' || i, 'k' FROM generate_series(1, 50) AS i`); err != nil {
			t.Fatal(err)
		}
		lower := sqlb.Lower(ofName)
		runB(t, tx, pg.CreateStatisticsExpr(pg.Statistics{Name: "of_lower"}, ofT, lower))
		runB(t, tx, pg.CreateStatistics(pg.Statistics{Name: "of_mixed"}, ofT, ofID, ofKind).Expression(lower).Kinds(pg.NDistinct))
		if _, err := tx.ExecContext(ctx, `ANALYZE of_items`); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_statistic_ext WHERE stxname IN ('of_lower', 'of_mixed')`).Scan(&n); err != nil || n != 2 {
			t.Errorf("the server holds %d of the two statistics objects, %v", n, err)
		}
		// The expression's own statistics exist, which is the whole point of
		// the form: without them a filter on lower(name) is a guess.
		var exprs int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_statistic_ext_data d JOIN pg_statistic_ext e ON e.oid = d.stxoid
			 WHERE e.stxname = 'of_lower' AND d.stxdexpr IS NOT NULL`).Scan(&exprs); err != nil {
			t.Fatal(err)
		}
		if exprs == 0 {
			t.Error("ANALYZE stored no statistics for the expression")
		}
	})
}

// TestTask_DeclareASequenceInFull runs the CREATE SEQUENCE options that were
// missing and reads each back from pg_sequences, then checks OWNED BY ties the
// sequence's life to its column.
func TestTask_DeclareASequenceInFull(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, ofTable())
		seq := pg.Sequence[int64]{Name: "of_items_id_seq"}
		runB(t, tx, pg.CreateSequence(seq).
			IncrementBy(5).MinValue(10).MaxValue(50).Start(10).Cache(1).Cycle().OwnedBy(ofID))
		var inc, minv, maxv int64
		var cycle bool
		if err := tx.QueryRowContext(ctx,
			`SELECT increment_by, min_value, max_value, cycle FROM pg_sequences WHERE sequencename = 'of_items_id_seq'`).
			Scan(&inc, &minv, &maxv, &cycle); err != nil {
			t.Fatal(err)
		}
		if inc != 5 || minv != 10 || maxv != 50 || !cycle {
			t.Errorf("the sequence is %d/%d/%d cycle=%v", inc, minv, maxv, cycle)
		}
		// CYCLE means the sequence comes back to its minimum rather than
		// failing, which is the only way to see the flag do anything.
		for range 9 {
			if _, err := tx.ExecContext(ctx, `SELECT nextval('of_items_id_seq')`); err != nil {
				t.Fatal(err)
			}
		}
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT nextval('of_items_id_seq')`).Scan(&next); err != nil || next != 10 {
			t.Errorf("after the maximum the sequence gave %d, want 10, %v", next, err)
		}

		// OWNED BY: dropping the column drops the sequence.
		runB(t, tx, pg.AlterTable(ofT).Clause(pg.DropColumnIfExists(ofID)))
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_class WHERE relname = 'of_items_id_seq'`).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != 0 {
			t.Error("the owned sequence outlived its column")
		}

		// The bounds removed again, and the unlogged and temporary forms.
		free := pg.Sequence[int64]{Name: "of_free_seq"}
		runB(t, tx, pg.CreateSequence(free).NoMinValue().NoMaxValue().NoCycle().OwnedByNone())
		runB(t, tx, pg.AlterSequence(free).MinValue(1))
		runB(t, tx, pg.AlterSequence(free).NoMinValue())
		runB(t, tx, pg.AlterSequence(free).NoMaxValue())
		if err := tx.QueryRowContext(ctx,
			`SELECT min_value, max_value FROM pg_sequences WHERE sequencename = 'of_free_seq'`).Scan(&minv, &maxv); err != nil {
			t.Fatal(err)
		}
		if minv != 1 || maxv != 9223372036854775807 {
			t.Errorf("after NO MINVALUE and NO MAXVALUE the bounds are %d and %d", minv, maxv)
		}
		runB(t, tx, pg.CreateSequence(pg.Sequence[int64]{Name: "of_unlogged_seq"}).Unlogged())
		runB(t, tx, pg.CreateSequence(pg.Sequence[int64]{Name: "of_temp_seq"}).Temporary())
	})
}

// TestTask_TieAnIndexToAnExtension runs DEPENDS ON EXTENSION, which is the
// answer for the two objects ALTER EXTENSION ADD refuses. That refusal is
// measured here rather than read: an index and a statistics object are both
// rejected with "cannot add an object of this type to an extension", which is
// why neither is an ExtensionMember.
func TestTask_TieAnIndexToAnExtension(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ext := pg.Extension{Name: "btree_gist"}
		runB(t, tx, pg.CreateExtension(ext).IfNotExists())
		runB(t, tx, ofTable())
		ix := pg.Index{Name: "of_items_name_ix"}
		runB(t, tx, pg.CreateIndex(ix, ofT).On(pg.IndexKey(ofName)))

		if !failsAt(t, tx, `ALTER EXTENSION btree_gist ADD INDEX of_items_name_ix`) {
			t.Error("the server accepted an index as an extension member, which is why it is not one here")
		}
		runB(t, tx, pg.DependsOnExtension(ix, ext))
		if n := extensionDependencies(t, tx, "of_items_name_ix"); n != 1 {
			t.Errorf("after DEPENDS ON the index has %d extension dependencies", n)
		}
		runB(t, tx, pg.NoDependsOnExtension(ix, ext))
		if n := extensionDependencies(t, tx, "of_items_name_ix"); n != 0 {
			t.Errorf("after NO DEPENDS ON the index still has %d", n)
		}
	})
}

// extensionDependencies counts an object's 'x' dependencies, which is how the
// catalogue records DEPENDS ON EXTENSION rather than membership.
func extensionDependencies(t *testing.T, tx *sql.Tx, name string) int {
	t.Helper()
	var n int
	if err := tx.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_depend WHERE deptype = 'x' AND objid = $1::regclass`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTask_FillInABaseTypesFunctionsAfterwards runs ALTER TYPE ... SET, which
// is the only way a base type gets the functions that take or return it: they
// cannot exist when CREATE TYPE runs, and CREATE TYPE cannot be re-run.
func TestTask_FillInABaseTypesFunctionsAfterwards(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		requireSuperuser(t, tx)
		bt := pg.BaseType[int32]{Name: "of_myint"}
		in := pg.Function{Name: "of_myint_in", Params: []pg.FunctionArg{pg.Arg("s", ddl.RawType[string]("cstring"))}}
		out := pg.Function{Name: "of_myint_out", Params: []pg.FunctionArg{pg.Arg("v", bt)}}
		recv := pg.Function{Name: "of_myint_recv", Params: []pg.FunctionArg{pg.Arg("b", ddl.RawType[[]byte]("internal"))}}
		send := pg.Function{Name: "of_myint_send", Params: []pg.FunctionArg{pg.Arg("v", bt)}}

		runB(t, tx, pg.CreateShellType(bt))
		runB(t, tx, pg.CreateFunction(in, pg.LangInternal, "int4in").Returns(pg.Returns(bt)).Volatility(pg.Immutable).NullInput(pg.Strict))
		runB(t, tx, pg.CreateFunction(out, pg.LangInternal, "int4out").Returns(pg.Returns(ddl.RawType[string]("cstring"))).Volatility(pg.Immutable).NullInput(pg.Strict))
		runB(t, tx, pg.CreateBaseType(bt, in, out).InternalLength(4).PassedByValue().Alignment(pg.AlignInt4))

		// The binary functions can only be written now, because each names the
		// type the CREATE TYPE above brought into being.
		runB(t, tx, pg.CreateFunction(recv, pg.LangInternal, "int4recv").Returns(pg.Returns(bt)).Volatility(pg.Immutable).NullInput(pg.Strict))
		runB(t, tx, pg.CreateFunction(send, pg.LangInternal, "int4send").Returns(pg.Returns(ddl.RawType[[]byte]("bytea"))).Volatility(pg.Immutable).NullInput(pg.Strict))
		runB(t, tx, pg.AlterBaseType(bt).SetProperties().Receive(recv).Send(send))
		var recvName, sendName string
		if err := tx.QueryRowContext(ctx,
			`SELECT typreceive::text, typsend::text FROM pg_type WHERE typname = 'of_myint'`).Scan(&recvName, &sendName); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(recvName, "of_myint_recv") || !strings.Contains(sendName, "of_myint_send") {
			t.Errorf("the type's binary functions are %q and %q", recvName, sendName)
		}

		renamed := pg.BaseType[int32]{Name: "of_myint2"}
		runB(t, tx, pg.AlterBaseType(bt).RenameTo(renamed))
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_type WHERE typname = 'of_myint2'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the base type was not renamed: %v", err)
		}
	})
}

// TestTask_RenameARangeAndACompositeType runs the ALTER TYPE ... RENAME TO
// form for the two type kinds that had no rename at all.
func TestTask_RenameARangeAndACompositeType(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		rt := pg.RangeType[int64]{Name: "of_range"}
		runB(t, tx, pg.CreateRangeType(rt, ddl.RawType[int64]("bigint")))
		runB(t, tx, pg.AlterRangeType(rt).RenameTo(pg.RangeType[int64]{Name: "of_range2"}))

		ct := pg.Composite[string]{Name: "of_row"}
		runB(t, tx, pg.CreateComposite(ct, pg.Attr("a", ddl.RawType[int64]("bigint"))))
		runB(t, tx, pg.AlterComposite(ct).RenameTo(pg.Composite[string]{Name: "of_row2"}))

		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_type WHERE typname IN ('of_range2', 'of_row2')`).Scan(&n); err != nil || n != 2 {
			t.Errorf("the catalogue holds %d of the two renamed types, %v", n, err)
		}
	})
}

// TestTask_ChangeAPolicyInOneStatement runs ALTER POLICY with its roles, its
// USING and its WITH CHECK together, which is the form that matters: three
// statements leave two windows in which the policy is half changed.
func TestTask_ChangeAPolicyInOneStatement(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, ofTable())
		runB(t, tx, pg.AlterTable(ofT).Clause(pg.EnableRowLevelSecurity()))
		p := pg.Policy{Name: "of_isolation", Table: ofT}
		runB(t, tx, pg.CreatePolicy(p).Using(sqlb.Eq(ofKind, "k")))
		runB(t, tx, pg.AlterPolicy(p).
			To(pg.RolePublic).
			Using(sqlb.Eq(ofKind, "j")).
			WithCheck(sqlb.Eq(ofKind, "j")))
		var using, check string
		if err := tx.QueryRowContext(ctx,
			`SELECT coalesce(pg_get_expr(polqual, polrelid), ''), coalesce(pg_get_expr(polwithcheck, polrelid), '')
			 FROM pg_policy WHERE polname = 'of_isolation'`).Scan(&using, &check); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(using, "'j'") || !strings.Contains(check, "'j'") {
			t.Errorf("the policy is USING %q WITH CHECK %q", using, check)
		}
	})
}

// TestTask_DropWithRestrictAndCascade runs the CASCADE and RESTRICT forms the
// drops were missing, and checks RESTRICT is the refusal rather than a no-op.
func TestTask_DropWithRestrictAndCascade(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		runB(t, tx, ofTable())
		v := pg.View{Name: "of_view"}
		runB(t, tx, pg.CreateView(v).As(pg.Select(ofName).From(ofT)))
		dependent := pg.View{Name: "of_view2"}
		runB(t, tx, pg.CreateView(dependent).As(pg.Select(sqlb.Col[string](sqlb.Table{Name: "of_view"}, "name")).From(sqlb.Table{Name: "of_view"})))

		s, err := pg.DropView(v).Restrict().Build()
		if err != nil {
			t.Fatal(err)
		}
		if !failsAt(t, tx, s[0].SQL) {
			t.Error("RESTRICT dropped a view another view depends on")
		}
		runB(t, tx, pg.DropView(v).Cascade())

		// A trigger and a policy, which had neither keyword.
		runB(t, tx, pg.AlterTable(ofT).Clause(pg.EnableRowLevelSecurity()))
		p := pg.Policy{Name: "of_p", Table: ofT}
		runB(t, tx, pg.CreatePolicy(p).Using(sqlb.Eq(ofKind, "k")))
		runB(t, tx, pg.DropPolicy(p).Restrict())
		f := pg.Function{Name: "of_touch"}
		runB(t, tx, pg.CreateTriggerFunction(f, "BEGIN RETURN NEW; END"))
		tr := pg.Trigger{Name: "of_tr", Table: ofT}
		runB(t, tx, pg.CreateTrigger(tr).Before(pg.OnInsert()).ForEachRow().Execute(f))
		runB(t, tx, pg.DropTrigger(tr).Restrict())
		runB(t, tx, pg.DropFunction(f).Restrict())
	})
}

// TestTask_AClosedSetValueCannotReachTheServer is the other half of the
// closed-set audit, and the half a rendering test cannot do: it shows on a
// real server that the payload pg refuses at Build would have been an
// injection in that position, rather than a string the server would have
// rejected anyway.
//
// The audit itself is pg/closedsets_test.go, which refuses the payload at
// every render site. This is why that matters.
func TestTask_AClosedSetValueCannotReachTheServer(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, ofTable())

		// What the library builds now: the value is refused and no SQL is
		// produced at all.
		if _, err := pg.NewLockTable(pg.Dialect{}, ofT).In(pg.TableLockMode(`ACCESS SHARE MODE; DROP TABLE of_items; --`)).Build(); err == nil {
			t.Fatal("a hostile lock mode built a statement")
		}

		// What that value would have done had it reached the statement, which
		// is the whole claim: the table is gone inside this transaction.
		hostile := `LOCK TABLE of_items IN ACCESS SHARE MODE; DROP TABLE of_items; --`
		if _, err := tx.ExecContext(ctx, hostile); err != nil {
			t.Fatalf("the payload did not run, so it proves nothing: %v", err)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) = 1 FROM pg_class WHERE relname = 'of_items'`).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Error("the payload left the table standing, so it is not the injection this test claims")
		}
	})
}

// TestTask_SampleWithAnExtensionsMethod is the regression for an over-closure
// that shipped for a few hours on 2026-09-24: SampleMethod was validated
// against a list of two, and TABLESAMPLE takes whatever sampling method the
// server has loaded. tsm_system_rows brings SYSTEM_ROWS, which takes a row
// count rather than a percentage, and the repository's own snippet catalogue
// builds it.
//
// The set is the server's, so the check is a shape and not a list. This test
// runs an extension's method on a real server, which is the only thing that
// could have caught the list.
func TestTask_SampleWithAnExtensionsMethod(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		if _, err := tx.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS tsm_system_rows`); err != nil {
			t.Skipf("SKIPPED, NOT PASSED: this case needs tsm_system_rows: %v", err)
		}
		runB(t, tx, ofTable())
		if _, err := tx.ExecContext(ctx, `INSERT INTO of_items (id, name, kind) SELECT i, 'n', 'k' FROM generate_series(1, 20) AS i`); err != nil {
			t.Fatal(err)
		}
		q, err := pg.Select(ofID).From(ofT).
			Clause(pg.TableSample(pg.SampleMethod("SYSTEM_ROWS"), 5, -1)).Build()
		if err != nil {
			t.Fatalf("an extension's sampling method was refused at Build: %v", err)
		}
		rows, err := tx.QueryContext(ctx, q.SQL)
		if err != nil {
			t.Fatalf("%s: %v", q.SQL, err)
		}
		defer func() { _ = rows.Close() }()
		n := 0
		for rows.Next() {
			n++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if n != 5 {
			t.Errorf("SYSTEM_ROWS (5) returned %d rows", n)
		}
		// The payload is still refused, which is what the shape check is for.
		if _, err := pg.Select(ofID).From(ofT).
			Clause(pg.TableSample(pg.SampleMethod(`SYSTEM (10); DROP TABLE of_items; --`), 5, -1)).Build(); err == nil {
			t.Error("a hostile sampling method built a statement")
		}
	})
}

// TestTask_EveryDateFieldIsAcceptedByTheServer is what makes DateField's list
// safe to validate against: a list that refuses a real value is worse than no
// list, and this one named fifteen of the twenty-two units extract takes until
// the same day the check was added. Each is executed rather than read.
//
// date_trunc takes a narrower set — nine of the twenty-two are refused there,
// measured — and that is deliberately NOT a second list: the server's error
// names the unit, and a second closed set is a second thing to keep correct
// across versions.
func TestTask_EveryDateFieldIsAcceptedByTheServer(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, ofTable())
		for _, f := range pg.DateFieldValues {
			q, err := pg.Select().Exprs(pg.Extract(f, pg.Now())).Build()
			if err != nil {
				t.Errorf("extract %q was refused at Build: %v", f, err)
				continue
			}
			if _, err := tx.ExecContext(ctx, q.SQL); err != nil {
				t.Errorf("extract %q: the server refused %s: %v", f, q.SQL, err)
			}
		}
		if len(pg.DateFieldValues) != 22 {
			t.Errorf("extract takes 22 units on Postgres 17 and this package names %d", len(pg.DateFieldValues))
		}
	})
}

// TestTask_EveryJSONKindIsAcceptedByTheServer does the same for IS JSON, whose
// list was missing the VALUE spelling the grammar also takes.
func TestTask_EveryJSONKindIsAcceptedByTheServer(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		if versionOf(t, openOrSkip(t)) < 160000 {
			t.Skip("SKIPPED, NOT PASSED: IS JSON needs Postgres 16")
		}
		for _, k := range pg.JSONKindValues {
			q, err := pg.Select().Exprs(sqlb.BoolExpr(pg.IsJSON(sqlb.Param("{}"), k, false))).Build()
			if err != nil {
				t.Errorf("IS JSON %q was refused at Build: %v", k, err)
				continue
			}
			if _, err := tx.ExecContext(ctx, q.SQL, q.Args...); err != nil {
				t.Errorf("IS JSON %q: the server refused %s: %v", k, q.SQL, err)
			}
		}
	})
}
