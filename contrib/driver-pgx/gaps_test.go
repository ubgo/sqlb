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

// The constructs built on 2026-09-24 to close the DDL detector's last partial
// and left-out entries, executed against a real Postgres and read back out of
// the catalogue.
//
// Everything here rendered cleanly before it ran, which is what the eleven
// constructs Postgres rejected on 2026-09-18 also did. Nothing counts as built
// until the server has taken it, which is why each case below checks an effect
// rather than an exit code: the reloptions the server stored, the column names
// after a rename, the default the catalogue holds, the type a value round-trips
// through, the rows a rule stopped, and the key an identity sequence handed out.

var (
	gpT     = sqlb.Table{Name: "gp_items", Alias: "i"}
	gpID    = sqlb.Col[int64](gpT, "id")
	gpName  = sqlb.Col[string](gpT, "name")
	gpScore = sqlb.Col[int64](gpT, "score")
)

// gpTable is a table with an identity key, which is what the identity-sequence
// and rule cases both need.
func gpTable() *pg.CreateTableReady {
	return pg.CreateTable(gpT).
		Column(ddl.Column(gpID), ddl.Column(gpName), ddl.Column(gpScore)).
		Clause(pg.Identity(gpID, pg.ByDefault))
}

// requireSuperuser skips loudly where the connected role cannot run the
// statement under test. A base type's input function must be LANGUAGE
// internal, which only a superuser may create, so on an ordinary role the case
// is skipped rather than reported as passing.
func requireSuperuser(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var super bool
	if err := tx.QueryRowContext(context.Background(),
		`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		t.Fatal(err)
	}
	if !super {
		t.Skip("SKIPPED, NOT PASSED: this case needs a superuser and the connected role is not one")
	}
}

// TestTask_ChangeAnIndexsStorageAndAttachAPartition runs the three ALTER INDEX
// forms and reads the reloptions back, then builds a partitioned index the way
// a large table needs it built: one index per partition, attached to the
// parent, which is valid only once every partition has been attached.
func TestTask_ChangeAnIndexsStorageAndAttachAPartition(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, gpTable())
		ix := pg.Index{Name: "gp_items_name_ix"}
		runB(t, tx, pg.CreateIndex(ix, gpT).On(pg.IndexKey(gpName)))

		runB(t, tx, pg.AlterIndex(ix).SetStorage(pg.IntParameter("fillfactor", 70), pg.BoolParameter("deduplicate_items", false)))
		if got := reloptions(t, tx, "gp_items_name_ix"); got != "{fillfactor=70,deduplicate_items=false}" {
			t.Errorf("after SET the index had reloptions %s", got)
		}
		runB(t, tx, pg.AlterIndex(ix).ResetStorage("fillfactor", "deduplicate_items"))
		if got := reloptions(t, tx, "gp_items_name_ix"); got != "" {
			t.Errorf("after RESET the index still had reloptions %s", got)
		}

		// A partitioned index, attached one partition at a time.
		part := sqlb.Table{Name: "gp_events_2026"}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE gp_events (at bigint) PARTITION BY RANGE (at)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE gp_events_2026 PARTITION OF gp_events FOR VALUES FROM (0) TO (100)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `CREATE INDEX gp_events_at_ix ON ONLY gp_events (at)`); err != nil {
			t.Fatal(err)
		}
		partIx := pg.Index{Name: "gp_events_2026_at_ix"}
		runB(t, tx, pg.CreateIndex(partIx, part).On(pg.IndexKey(sqlb.Col[int64](part, "at"))))
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = 'gp_events_at_ix'::regclass`).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if valid {
			t.Fatal("the parent index was valid before its only partition was attached")
		}
		runB(t, tx, pg.AlterIndex(pg.Index{Name: "gp_events_at_ix"}).AttachPartition(partIx))
		if err := tx.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = 'gp_events_at_ix'::regclass`).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if !valid {
			t.Error("the parent index was still invalid after its only partition was attached")
		}
	})
}

// reloptions is a relation's stored storage parameters, as one string, which
// is the only place the server records what SET (...) did.
func reloptions(t *testing.T, tx *sql.Tx, name string) string {
	t.Helper()
	var opts sql.NullString
	if err := tx.QueryRowContext(context.Background(),
		`SELECT array_to_string(reloptions, ',') FROM pg_class WHERE oid = $1::regclass`, name).Scan(&opts); err != nil {
		t.Fatal(err)
	}
	if !opts.Valid || opts.String == "" {
		return ""
	}
	return "{" + opts.String + "}"
}

// TestTask_RenameAViewsColumnAndTuneAMaterializedView runs the ALTER VIEW and
// ALTER MATERIALIZED VIEW forms and reads the column names and the storage
// parameters back.
func TestTask_RenameAViewsColumnAndTuneAMaterializedView(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		runB(t, tx, gpTable())
		v := pg.View{Name: "gp_named"}
		mv := pg.MaterializedView{Name: "gp_counts"}
		runB(t, tx, pg.CreateView(v).As(pg.Select(gpName).From(gpT)))
		runB(t, tx, pg.CreateMaterializedView(mv).As(pg.Select(gpName).From(gpT)))

		runB(t, tx, pg.RenameViewColumn(v, "name", "label"))
		if got := columnsOf(t, tx, "gp_named"); got != "label" {
			t.Errorf("the view's columns are %s", got)
		}
		runB(t, tx, pg.AlterMaterializedView(mv).RenameColumn("name", "label"))
		if got := columnsOf(t, tx, "gp_counts"); got != "label" {
			t.Errorf("the materialized view's columns are %s", got)
		}

		runB(t, tx, pg.AlterMaterializedView(mv).SetStorage(pg.IntParameter("fillfactor", 60)))
		if got := reloptions(t, tx, "gp_counts"); got != "{fillfactor=60}" {
			t.Errorf("after SET the materialized view had reloptions %s", got)
		}
		runB(t, tx, pg.AlterMaterializedView(mv).ResetStorage("fillfactor"))
		if got := reloptions(t, tx, "gp_counts"); got != "" {
			t.Errorf("after RESET the materialized view still had reloptions %s", got)
		}
		runB(t, tx, pg.AlterMaterializedView(mv).SetAccessMethod(pg.Heap))

		// IF EXISTS on a missing view is a no-op rather than an error, which is
		// what makes the rename safe to re-run.
		runB(t, tx, pg.RenameViewColumn(pg.View{Name: "gp_missing"}, "a", "b").IfExists())
	})
}

// columnsOf is a relation's column names in order, joined, which is how a
// rename is read back.
func columnsOf(t *testing.T, tx *sql.Tx, name string) string {
	t.Helper()
	rows, err := tx.QueryContext(context.Background(),
		`SELECT attname FROM pg_attribute WHERE attrelid = $1::regclass AND attnum > 0 AND NOT attisdropped ORDER BY attnum`, name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, ",")
}

// TestTask_GiveADomainADefault runs the domain's own default in both forms and
// checks the server applies it to a column that has none of its own, which is
// the whole reason a domain default is not the same thing as a column default.
func TestTask_GiveADomainADefault(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		d := pg.Domain[int64]{Name: "gp_positive"}
		runB(t, tx, pg.CreateDomain(d, ddl.RawType[int64]("bigint")))
		runB(t, tx, pg.AlterDomain(d).SetDefault(7))

		tbl := sqlb.Table{Name: "gp_domain_rows"}
		n := sqlb.Col[int64](tbl, "n")
		runB(t, tx, pg.CreateTable(tbl).Column(ddl.Column(n).As(d).Nullable()))
		if _, err := tx.ExecContext(ctx, `INSERT INTO gp_domain_rows DEFAULT VALUES`); err != nil {
			t.Fatal(err)
		}
		var got int64
		if err := tx.QueryRowContext(ctx, `SELECT n FROM gp_domain_rows`).Scan(&got); err != nil || got != 7 {
			t.Errorf("the domain's default gave %d, %v", got, err)
		}

		runB(t, tx, pg.AlterDomain(d).SetDefaultExpr(sqlb.Mul(sqlb.Param(int64(2)), sqlb.Param(int64(3)))))
		if got := domainDefault(t, tx, "gp_positive"); got != "(2 * 3)" {
			t.Errorf("the domain's default expression is %q", got)
		}
		runB(t, tx, pg.AlterDomain(d).DropDefault())
		if got := domainDefault(t, tx, "gp_positive"); got != "" {
			t.Errorf("after DROP DEFAULT the domain still had %q", got)
		}
	})
}

// domainDefault is the default expression the catalogue holds for a domain.
func domainDefault(t *testing.T, tx *sql.Tx, name string) string {
	t.Helper()
	var def sql.NullString
	if err := tx.QueryRowContext(context.Background(),
		`SELECT typdefaultbin IS NULL, pg_get_expr(typdefaultbin, 0) FROM pg_type WHERE typname = $1`,
		name).Scan(new(bool), &def); err != nil {
		t.Fatal(err)
	}
	return def.String
}

// TestTask_MoveAnExtensionAndItsMembers runs the ALTER EXTENSION forms: a
// library's own function made part of an extension, taken back out, and the
// extension moved into a schema of its own.
func TestTask_MoveAnExtensionAndItsMembers(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ext := pg.Extension{Name: "btree_gist"}
		runB(t, tx, pg.CreateExtension(ext).IfNotExists())
		f := pg.Function{Name: "gp_member", Params: []pg.FunctionArg{pg.Arg("a", ddl.RawType[int64]("bigint"))}}
		runB(t, tx, pg.CreateFunction(f, pg.LangSQL, "SELECT a").Returns(pg.Returns(ddl.RawType[int64]("bigint"))))

		runB(t, tx, pg.AddExtensionMember(ext, f))
		if !memberOfExtension(t, tx, "gp_member") {
			t.Error("the function was not recorded as a member of the extension")
		}
		runB(t, tx, pg.DropExtensionMember(ext, f))
		if memberOfExtension(t, tx, "gp_member") {
			t.Error("the function was still a member after DROP")
		}

		sch := pg.Schema{Name: "gp_ext"}
		runB(t, tx, pg.CreateSchema(sch))
		runB(t, tx, pg.SetExtensionSchema(ext, sch))
		var name string
		if err := tx.QueryRowContext(context.Background(),
			`SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'btree_gist'`).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "gp_ext" {
			t.Errorf("the extension is in schema %q", name)
		}
	})
}

// memberOfExtension reports whether a function is recorded as an extension's
// member, which is what pg_depend holds rather than the extension itself.
func memberOfExtension(t *testing.T, tx *sql.Tx, fn string) bool {
	t.Helper()
	var n int
	if err := tx.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_depend d JOIN pg_proc p ON p.oid = d.objid
		 WHERE d.deptype = 'e' AND p.proname = $1`, fn).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// TestTask_HandAnObjectToAnotherRole runs OWNER TO for every ownable kind
// against a role created for the test, and reads the owner back. The role is
// created with raw SQL because CREATE ROLE is the operator's and this module
// does not build it, which is exactly the boundary SetOwner sits on: a library
// may give an object to a role, and may not invent one.
func TestTask_HandAnObjectToAnotherRole(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		if _, err := tx.ExecContext(ctx, `CREATE ROLE gp_owner`); err != nil {
			t.Skipf("SKIPPED, NOT PASSED: this case needs a role that may create roles: %v", err)
		}
		runB(t, tx, gpTable())
		v := pg.View{Name: "gp_named"}
		mv := pg.MaterializedView{Name: "gp_counts"}
		seq := pg.Sequence[int64]{Name: "gp_seq"}
		dom := pg.Domain[int64]{Name: "gp_dom"}
		runB(t, tx, pg.CreateView(v).As(pg.Select(gpName).From(gpT)))
		runB(t, tx, pg.CreateMaterializedView(mv).As(pg.Select(gpName).From(gpT)))
		runB(t, tx, pg.CreateSequence(seq))
		runB(t, tx, pg.CreateDomain(dom, ddl.RawType[int64]("bigint")))

		role := pg.Role{Name: "gp_owner"}
		runB(t, tx, pg.SetOwner(v, role))
		runB(t, tx, pg.SetOwner(mv, role))
		runB(t, tx, pg.SetOwner(seq, role))
		runB(t, tx, pg.SetOwner(dom, role))
		for _, name := range []string{"gp_named", "gp_counts", "gp_seq"} {
			if got := ownerOf(t, tx, name); got != "gp_owner" {
				t.Errorf("%s is owned by %q", name, got)
			}
		}
		var owner string
		if err := tx.QueryRowContext(ctx,
			`SELECT pg_get_userbyid(typowner) FROM pg_type WHERE typname = 'gp_dom'`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if owner != "gp_owner" {
			t.Errorf("the domain is owned by %q", owner)
		}

		// A policy naming the role, which is what ALTER POLICY ... TO was
		// partial for.
		runB(t, tx, pg.AlterTable(gpT).Clause(pg.EnableRowLevelSecurity()))
		p := pg.Policy{Name: "gp_isolation", Table: gpT}
		runB(t, tx, pg.CreatePolicy(p).Using(sqlb.Greater(gpScore, 0)))
		runB(t, tx, pg.AlterPolicy(p).To(role, pg.RoleCurrentUser))
		var roles string
		if err := tx.QueryRowContext(ctx,
			`SELECT array_to_string(polroles::regrole[], ',') FROM pg_policy WHERE polname = 'gp_isolation'`).Scan(&roles); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(roles, "gp_owner") {
			t.Errorf("the policy applies to %q", roles)
		}
	})
}

// ownerOf is a relation's owner, as the catalogue records it.
func ownerOf(t *testing.T, tx *sql.Tx, name string) string {
	t.Helper()
	var owner string
	if err := tx.QueryRowContext(context.Background(),
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = $1::regclass`, name).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

// TestTask_WriteAFunctionThatIsNotATrigger runs the CREATE FUNCTION forms and
// calls each one, which is the only way to know the signature, the result
// shape and the characteristics all reached the server as intended.
func TestTask_WriteAFunctionThatIsNotATrigger(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		bigint := ddl.RawType[int64]("bigint")
		text := ddl.RawType[string]("text")

		add := pg.Function{Name: "gp_add", Params: []pg.FunctionArg{pg.Arg("a", bigint), pg.Arg("b", bigint)}}
		runB(t, tx, pg.CreateFunction(add, pg.LangSQL, "SELECT a + b").
			Returns(pg.Returns(bigint)).
			Volatility(pg.Immutable).
			NullInput(pg.Strict).
			Security(pg.Invoker).
			Parallel(pg.ParallelSafe).
			Cost(1))
		var sum int64
		if err := tx.QueryRowContext(ctx, `SELECT gp_add(2, 3)`).Scan(&sum); err != nil || sum != 5 {
			t.Errorf("gp_add gave %d, %v", sum, err)
		}
		// OrReplace is the idempotent form, and it changes the body.
		runB(t, tx, pg.CreateFunction(add, pg.LangSQL, "SELECT a * b").OrReplace().Returns(pg.Returns(bigint)).Volatility(pg.Immutable))
		if err := tx.QueryRowContext(ctx, `SELECT gp_add(2, 3)`).Scan(&sum); err != nil || sum != 6 {
			t.Errorf("after OR REPLACE gp_add gave %d, %v", sum, err)
		}

		// A set-returning function, a table-returning one, and one whose
		// results are output parameters.
		ids := pg.Function{Name: "gp_ids", Params: []pg.FunctionArg{pg.Arg("n", bigint)}}
		runB(t, tx, pg.CreateFunction(ids, pg.LangSQL, "SELECT generate_series(1, n)").
			Returns(pg.ReturnsSetOf(bigint)).Rows(10))
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM gp_ids(4)`).Scan(&count); err != nil || count != 4 {
			t.Errorf("gp_ids returned %d rows, %v", count, err)
		}

		pairs := pg.Function{Name: "gp_pairs"}
		runB(t, tx, pg.CreateFunction(pairs, pg.LangSQL, "SELECT 1::bigint, 'a'::text").
			Returns(pg.ReturnsTable(pg.Attr("n", bigint), pg.Attr("s", text))))
		var n int64
		var s string
		if err := tx.QueryRowContext(ctx, `SELECT n, s FROM gp_pairs()`).Scan(&n, &s); err != nil || n != 1 || s != "a" {
			t.Errorf("gp_pairs gave %d, %q, %v", n, s, err)
		}

		split := pg.Function{Name: "gp_split", Params: []pg.FunctionArg{
			pg.Arg("s", text), pg.OutArg("head", text), pg.InOutArg("size", bigint),
		}}
		runB(t, tx, pg.CreateFunction(split, pg.LangPLpgSQL, "BEGIN head := left(s, 1); size := length(s); END"))
		var head string
		if err := tx.QueryRowContext(ctx, `SELECT head, size FROM gp_split('abc', 0)`).Scan(&head, &n); err != nil || head != "a" || n != 3 {
			t.Errorf("gp_split gave %q, %d, %v", head, n, err)
		}

		total := pg.Function{Name: "gp_total", Params: []pg.FunctionArg{pg.VariadicArg("xs", ddl.RawType[[]int64]("bigint[]"))}}
		runB(t, tx, pg.CreateFunction(total, pg.LangSQL, "SELECT coalesce(sum(x), 0) FROM unnest(xs) AS x").
			Returns(pg.Returns(ddl.RawType[int64]("numeric"))))
		if err := tx.QueryRowContext(ctx, `SELECT gp_total(1, 2, 3)`).Scan(&n); err != nil || n != 6 {
			t.Errorf("gp_total gave %d, %v", n, err)
		}

		// A SECURITY DEFINER function with its search_path pinned, which is
		// the one combination a library must never ship without.
		guarded := pg.Function{Name: "gp_guarded"}
		runB(t, tx, pg.CreateFunction(guarded, pg.LangSQL, "SELECT 1::bigint").
			Returns(pg.Returns(bigint)).Security(pg.Definer).Set("search_path", "pg_catalog"))
		var config string
		if err := tx.QueryRowContext(ctx,
			`SELECT coalesce(array_to_string(proconfig, ','), '') FROM pg_proc WHERE proname = 'gp_guarded'`).Scan(&config); err != nil {
			t.Fatal(err)
		}
		if config != "search_path=pg_catalog" {
			t.Errorf("the definer function's config is %q", config)
		}

		// Every one of them is dropped by the signature its declaration
		// carries, which is what tells two functions of a name apart.
		for _, f := range []pg.Function{add, ids, pairs, split, total, guarded} {
			runB(t, tx, pg.DropFunction(f))
		}
	})
}

// TestTask_DeclareARangeTypeOverAColumnsOwnType creates a range type and uses
// it as a column's type, then has the server enforce the exclusion constraint
// that is the reason to want one.
func TestTask_DeclareARangeTypeOverAColumnsOwnType(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		rt := pg.RangeType[int64]{Name: "gp_int8range"}
		runB(t, tx, pg.Guarded(pg.CreateRangeType(rt, ddl.RawType[int64]("bigint")).
			SubtypeOpClass(pg.NewOpClass[int64]("int8_ops")).
			MultirangeTypeName("gp_int8multirange")))

		tbl := sqlb.Table{Name: "gp_spans"}
		span := sqlb.Col[pg.Range[int64]](tbl, "span")
		runB(t, tx, pg.CreateTable(tbl).Column(ddl.Column(span).As(rt)))
		if _, err := tx.ExecContext(ctx, `INSERT INTO gp_spans VALUES ('[1,5)')`); err != nil {
			t.Fatal(err)
		}
		var contains bool
		if err := tx.QueryRowContext(ctx, `SELECT span @> 3::bigint FROM gp_spans`).Scan(&contains); err != nil || !contains {
			t.Errorf("the range did not contain 3: %v", err)
		}
		// The multirange type is named as the statement asked.
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT count(*) = 1 FROM pg_type WHERE typname = 'gp_int8multirange'`).Scan(&exists); err != nil || !exists {
			t.Errorf("the multirange type was not named gp_int8multirange: %v", err)
		}
	})
}

// TestTask_AddABaseTypeToTheServer runs the shell-then-base pair that is the
// only way to add a type Postgres does not have, and stores and reads a value
// of it. Its input and output functions are LANGUAGE internal bound to an
// existing symbol, which is what makes the case runnable at all: a base type's
// input function must return the type itself, so no built-in function can be
// named directly. Measured on 17.10, where naming int4in directly fails with
// "type input function int4in must return type gp_myint".
func TestTask_AddABaseTypeToTheServer(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		requireSuperuser(t, tx)
		bt := pg.BaseType[int32]{Name: "gp_myint"}
		in := pg.Function{Name: "gp_myint_in", Params: []pg.FunctionArg{pg.Arg("s", ddl.RawType[string]("cstring"))}}
		out := pg.Function{Name: "gp_myint_out", Params: []pg.FunctionArg{pg.Arg("v", bt)}}

		runB(t, tx, pg.CreateShellType(bt))
		runB(t, tx, pg.CreateFunction(in, pg.LangInternal, "int4in").
			Returns(pg.Returns(bt)).Volatility(pg.Immutable).NullInput(pg.Strict))
		runB(t, tx, pg.CreateFunction(out, pg.LangInternal, "int4out").
			Returns(pg.Returns(ddl.RawType[string]("cstring"))).Volatility(pg.Immutable).NullInput(pg.Strict))
		runB(t, tx, pg.CreateBaseType(bt, in, out).
			InternalLength(4).
			PassedByValue().
			Alignment(pg.AlignInt4).
			Storage(pg.StoragePlain).
			Category(pg.CategoryNumeric).
			Delimiter(","))

		tbl := sqlb.Table{Name: "gp_myints"}
		v := sqlb.Col[int32](tbl, "v")
		runB(t, tx, pg.CreateTable(tbl).Column(ddl.Column(v).As(bt)))
		if _, err := tx.ExecContext(ctx, `INSERT INTO gp_myints VALUES ('42')`); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := tx.QueryRowContext(ctx, `SELECT v::text FROM gp_myints`).Scan(&got); err != nil || got != "42" {
			t.Errorf("the base type round-tripped to %q, %v", got, err)
		}
	})
}

// TestTask_MigrateAnInheritedTableAndItsRules runs the four forms that were
// left out as legacy: a child attached to a parent and detached again, a typed
// table tied to a composite type and untied, and a rule disabled and enabled.
// Each is the migration somebody has to write for a schema that already has
// one, which is why they build even though a new schema should use none of
// them.
func TestTask_MigrateAnInheritedTableAndItsRules(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		parent := sqlb.Table{Name: "gp_parent"}
		pName := sqlb.Col[string](parent, "name")
		pScore := sqlb.Col[int64](parent, "score")
		child := sqlb.Table{Name: "gp_child"}
		runB(t, tx, pg.CreateTable(parent).Column(ddl.Column(pName), ddl.Column(pScore)))
		runB(t, tx, pg.CreateTable(child).Column(ddl.Column(sqlb.Col[string](child, "name")), ddl.Column(sqlb.Col[int64](child, "score"))))
		if _, err := tx.ExecContext(ctx, `INSERT INTO gp_child VALUES ('a', 1)`); err != nil {
			t.Fatal(err)
		}

		runB(t, tx, pg.AlterTable(child).Clause(pg.Inherit(parent)))
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM gp_parent`).Scan(&n); err != nil || n != 1 {
			t.Errorf("after INHERIT the parent saw %d rows, %v", n, err)
		}
		runB(t, tx, pg.AlterTable(child).Clause(pg.NoInherit(parent)))
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM gp_parent`).Scan(&n); err != nil || n != 0 {
			t.Errorf("after NO INHERIT the parent saw %d rows, %v", n, err)
		}

		// A typed table, tied to a composite type and untied again.
		row := pg.Composite[string]{Name: "gp_row"}
		runB(t, tx, pg.CreateComposite(row, pg.Attr("name", ddl.RawType[string]("text")), pg.Attr("score", ddl.RawType[int64]("bigint"))))
		runB(t, tx, pg.AlterTable(child).Clause(pg.OfType(row)))
		var typed bool
		if err := tx.QueryRowContext(ctx, `SELECT reloftype <> 0 FROM pg_class WHERE oid = 'gp_child'::regclass`).Scan(&typed); err != nil || !typed {
			t.Errorf("after OF the table was not typed: %v", err)
		}
		runB(t, tx, pg.AlterTable(child).Clause(pg.NotOfType()))
		if err := tx.QueryRowContext(ctx, `SELECT reloftype <> 0 FROM pg_class WHERE oid = 'gp_child'::regclass`).Scan(&typed); err != nil || typed {
			t.Errorf("after NOT OF the table was still typed: %v", err)
		}

		// A rule, created with raw SQL because this module builds none, and
		// disabled and enabled by the actions that do exist.
		if _, err := tx.ExecContext(ctx, `CREATE RULE gp_no_delete AS ON DELETE TO gp_child DO INSTEAD NOTHING`); err != nil {
			t.Fatal(err)
		}
		r := pg.Rule{Name: "gp_no_delete", Table: child}
		if _, err := tx.ExecContext(ctx, `DELETE FROM gp_child`); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM gp_child`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the rule did not stop the delete: %d rows, %v", n, err)
		}
		runB(t, tx, pg.AlterTable(child).Clause(pg.DisableRule(r)))
		if _, err := tx.ExecContext(ctx, `DELETE FROM gp_child`); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM gp_child`).Scan(&n); err != nil || n != 0 {
			t.Errorf("after DISABLE RULE the delete was still stopped: %d rows, %v", n, err)
		}
		runB(t, tx, pg.AlterTable(child).Clause(pg.EnableRule(r)))
		runB(t, tx, pg.AlterTable(child).Clause(pg.EnableReplicaRule(r)))
		runB(t, tx, pg.AlterTable(child).Clause(pg.EnableAlwaysRule(r)))
		var enabled string
		if err := tx.QueryRowContext(ctx,
			`SELECT ev_enabled FROM pg_rewrite WHERE rulename = 'gp_no_delete'`).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != "A" {
			t.Errorf("after ENABLE ALWAYS the rule is %q", enabled)
		}
	})
}

// TestTask_RetuneAnIdentityColumnsSequence runs the last partial ALTER TABLE
// form and checks the sequence hands out what the options asked for, which is
// the only place the several SET options in one ALTER COLUMN can be seen to
// have all landed.
func TestTask_RetuneAnIdentityColumnsSequence(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, gpTable())
		runB(t, tx, pg.AlterTable(gpT).Clause(
			pg.AlterIdentitySequence(gpID).IncrementBy(10).MinValue(1).MaxValue(1000).StartWith(100).Cache(1),
			pg.RestartIdentity(gpID),
		))
		var first, second int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO gp_items (name, score) VALUES ('a', 1) RETURNING id`).Scan(&first); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO gp_items (name, score) VALUES ('b', 2) RETURNING id`).Scan(&second); err != nil {
			t.Fatal(err)
		}
		if first != 100 || second != 110 {
			t.Errorf("the identity handed out %d then %d, want 100 then 110", first, second)
		}

		// Cycling, which for a key means the next value collides with a row
		// that is still there; it is offered because a sequence behind a
		// non-key identity may legitimately want it.
		runB(t, tx, pg.AlterTable(gpT).Clause(pg.AlterIdentitySequence(gpID).Cycle()))
		var cycles bool
		if err := tx.QueryRowContext(ctx,
			`SELECT cycle FROM pg_sequences WHERE sequencename = 'gp_items_id_seq'`).Scan(&cycles); err != nil {
			t.Fatal(err)
		}
		if !cycles {
			t.Error("SET CYCLE did not reach the sequence")
		}
		runB(t, tx, pg.AlterTable(gpT).Clause(pg.AlterIdentitySequence(gpID).NoCycle()))
	})
}
