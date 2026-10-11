package driverpgx_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// The ALTER forms built on 2026-09-24, executed. Each one rendered cleanly
// before it ran, which is exactly what the eleven constructs Postgres rejected
// in 2026-09-18 also did, so nothing here counts as built until the server has
// taken it and the effect has been read back out of the catalogue.

var (
	afT     = sqlb.Table{Name: "af_items", Alias: "i"}
	afID    = sqlb.Col[int64](afT, "id")
	afPrice = sqlb.Col[int64](afT, "price")
	afTotal = sqlb.Col[int64](afT, "total")
	afCode  = sqlb.Col[string](afT, "code")
)

// afTable is a table with an identity key, a stored generated column and a
// plain column, which is every column shape the new actions change.
func afTable() *pg.CreateTableReady {
	return pg.CreateTable(afT).
		Column(ddl.Column(afID), ddl.Column(afPrice), ddl.Column(afTotal), ddl.Column(afCode)).
		Clause(pg.Identity(afID, pg.Always), pg.GeneratedStored(afTotal, sqlb.Mul(afPrice, sqlb.Param(int64(2)))))
}

// TestDDL_PostgresChangesAGeneratedColumnAndAnIdentity runs the ALTER COLUMN
// actions and reads each effect back: the expression the server stored, the
// identity's kind and its next value, and the column left plain after the
// identity and the expression are dropped.
func TestDDL_PostgresChangesAGeneratedColumnAndAnIdentity(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, afTable())

		// A generated column's expression, changed in place.
		runB(t, tx, pg.AlterTable(afT).Clause(pg.SetExpression(afTotal, sqlb.Mul(afPrice, sqlb.Param(int64(3))))))
		if _, err := tx.ExecContext(ctx, `INSERT INTO af_items (price, code) VALUES (10, 'a')`); err != nil {
			t.Fatal(err)
		}
		var total int64
		if err := tx.QueryRowContext(ctx, `SELECT total FROM af_items`).Scan(&total); err != nil || total != 30 {
			t.Errorf("after SET EXPRESSION total %d, %v", total, err)
		}

		// An identity that refuses an explicit value, until it does not.
		if !failsAt(t, tx, `INSERT INTO af_items (id, price, code) VALUES (5, 1, 'b')`) {
			t.Error("GENERATED ALWAYS accepted an explicit key")
		}
		runB(t, tx, pg.AlterTable(afT).Clause(pg.SetIdentityKind(afID, pg.ByDefault)))
		if _, err := tx.ExecContext(ctx, `INSERT INTO af_items (id, price, code) VALUES (5, 1, 'b')`); err != nil {
			t.Fatalf("BY DEFAULT refused an explicit key: %v", err)
		}
		// The sequence is now behind the data, which is what RESTART fixes.
		runB(t, tx, pg.AlterTable(afT).Clause(pg.RestartIdentity(afID).With(100)))
		var id int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO af_items (price, code) VALUES (2, 'c') RETURNING id`).Scan(&id); err != nil || id != 100 {
			t.Errorf("after RESTART WITH 100 the key was %d, %v", id, err)
		}
		runB(t, tx, pg.AlterTable(afT).Clause(pg.RestartIdentity(afID)))
		if err := tx.QueryRowContext(ctx, `INSERT INTO af_items (price, code) VALUES (2, 'd') RETURNING id`).Scan(&id); err != nil || id != 1 {
			t.Errorf("after a bare RESTART the key was %d, %v", id, err)
		}

		// Both drops, each run twice under IF EXISTS.
		for range 2 {
			runB(t, tx, pg.AlterTable(afT).Clause(
				pg.DropIdentity(afID).IfExists(),
				pg.DropExpression(afTotal).IfExists(),
			))
		}
		var identity, generated string
		if err := tx.QueryRowContext(ctx, `SELECT a.attidentity, a.attgenerated FROM pg_attribute a
			WHERE a.attrelid = 'af_items'::regclass AND a.attname = 'id'`).Scan(&identity, &generated); err != nil {
			t.Fatal(err)
		}
		if identity != "" {
			t.Errorf("the column is still an identity: %q", identity)
		}
		if err := tx.QueryRowContext(ctx, `SELECT a.attgenerated FROM pg_attribute a
			WHERE a.attrelid = 'af_items'::regclass AND a.attname = 'total'`).Scan(&generated); err != nil {
			t.Fatal(err)
		}
		if generated != "" {
			t.Errorf("the column is still generated: %q", generated)
		}
		// A plain column now takes a written value, and keeps what it held.
		if _, err := tx.ExecContext(ctx, `INSERT INTO af_items (id, price, total, code) VALUES (900, 1, 7, 'e')`); err != nil {
			t.Fatalf("the plain columns refused a written value: %v", err)
		}
		var kept int64
		if err := tx.QueryRowContext(ctx, `SELECT total FROM af_items WHERE code = 'a'`).Scan(&kept); err != nil || kept != 30 {
			t.Errorf("DROP EXPRESSION lost the stored value: %d, %v", kept, err)
		}
	})
}

// TestDDL_PostgresPromotesAnIndexToAConstraint runs the second half of a
// zero-downtime key migration and reads the constraint back out of
// pg_constraint, including that the index Postgres kept is the one built.
func TestDDL_PostgresPromotesAnIndexToAConstraint(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, afTable())
		codeKey := pg.TableConstraint{Table: afT, Name: "af_items_code_key"}
		idKey := pg.TableConstraint{Table: afT, Name: "af_items_pkey"}
		runB(t, tx, pg.CreateIndex(pg.Index{Name: "af_items_code_key"}, afT).Unique().On(pg.IndexKey(afCode)))
		runB(t, tx, pg.CreateIndex(pg.Index{Name: "af_items_pkey"}, afT).Unique().On(pg.IndexKey(afID)))
		runB(t, tx, pg.AlterTable(afT).Clause(
			pg.AddUniqueUsingIndex(codeKey, pg.Index{Name: "af_items_code_key"}),
			pg.AddPrimaryKeyUsingIndex(idKey, pg.Index{Name: "af_items_pkey"}),
		))
		rows := map[string]string{}
		q, err := tx.QueryContext(ctx, `SELECT conname, contype FROM pg_constraint WHERE conrelid = 'af_items'::regclass`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = q.Close() }()
		for q.Next() {
			var name, typ string
			if err := q.Scan(&name, &typ); err != nil {
				t.Fatal(err)
			}
			rows[name] = typ
		}
		if err := q.Err(); err != nil {
			t.Fatal(err)
		}
		if rows["af_items_code_key"] != "u" || rows["af_items_pkey"] != "p" {
			t.Errorf("constraints after USING INDEX: %v", rows)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO af_items (price, code) VALUES (1, 'x'), (2, 'x')`); err == nil {
			t.Error("the promoted unique constraint accepted a duplicate")
		}
	})
}

// TestDDL_PostgresPromotesAnIndexInASchema is the same step on a table that
// names a schema, the case pg.ErrIndexSchema exists for: an Index declaring
// its table's schema builds and runs, because the name Postgres resolves is
// the bare one and it resolves it in the table's schema. An index cannot be
// created anywhere else, so this and two empty schemas are the only pairs
// AddUniqueUsingIndex accepts.
func TestDDL_PostgresPromotesAnIndexInASchema(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		schema := pg.Schema{Name: "af_schema"}
		scoped := sqlb.Table{Schema: schema.Name, Name: "af_scoped", Alias: "s"}
		code := sqlb.Col[string](scoped, "code")
		runB(t, tx, pg.CreateSchema(schema))
		runB(t, tx, pg.CreateTable(scoped).Column(ddl.Column(code)))
		runB(t, tx, pg.CreateIndex(pg.Index{Schema: schema.Name, Name: "af_scoped_code_key"}, scoped).Unique().On(pg.IndexKey(code)))
		runB(t, tx, pg.AlterTable(scoped).Clause(pg.AddUniqueUsingIndex(
			pg.TableConstraint{Table: scoped, Name: "af_scoped_code_key"},
			pg.Index{Schema: schema.Name, Name: "af_scoped_code_key"},
		)))
		// Read the constraint by name, not by table. A table has several rows
		// in pg_constraint and they come back unordered, so asking for the
		// table's constraint returns an arbitrary one: on Postgres 18 this
		// test failed on a fresh container with contype "n", because 18
		// catalogues NOT NULL constraints there and 17 does not. Naming the
		// constraint asks the question the test means, on both versions.
		var typ string
		const byName = `SELECT contype FROM pg_constraint WHERE conrelid = 'af_schema.af_scoped'::regclass AND conname = 'af_scoped_code_key'`
		if err := tx.QueryRowContext(ctx, byName).Scan(&typ); err != nil || typ != "u" {
			t.Errorf("constraint type %q, %v", typ, err)
		}
	})
}

// TestDDL_PostgresSequenceTypeStartAndLogging runs the sequence options the
// detector recorded as missing. The type change is the one that is read back
// from information_schema, because widening an identity's sequence from
// integer to bigint is the migration the option exists for.
func TestDDL_PostgresSequenceTypeStartAndLogging(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		small := pg.Sequence[int32]{Name: "af_seq"}
		wide := pg.Sequence[int64]{Name: "af_seq"}
		runB(t, tx, pg.CreateSequence(small).Start(5))
		var typ string
		if err := tx.QueryRowContext(ctx, `SELECT data_type FROM information_schema.sequences WHERE sequence_name = 'af_seq'`).Scan(&typ); err != nil || typ != "integer" {
			t.Fatalf("sequence type %q, %v", typ, err)
		}
		runB(t, tx, pg.AlterSequence(wide).SetType().StartWith(1000))
		var start string
		if err := tx.QueryRowContext(ctx, `SELECT data_type, start_value FROM information_schema.sequences WHERE sequence_name = 'af_seq'`).Scan(&typ, &start); err != nil {
			t.Fatal(err)
		}
		if typ != "bigint" || start != "1000" {
			t.Errorf("after AS bigint START WITH 1000: type %q start %s", typ, start)
		}
		runB(t, tx, pg.AlterSequence(wide).SetUnlogged())
		var persistence string
		if err := tx.QueryRowContext(ctx, `SELECT relpersistence FROM pg_class WHERE relname = 'af_seq'`).Scan(&persistence); err != nil || persistence != "u" {
			t.Errorf("after SET UNLOGGED relpersistence %q, %v", persistence, err)
		}
		runB(t, tx, pg.AlterSequence(wide).SetLogged())
		if err := tx.QueryRowContext(ctx, `SELECT relpersistence FROM pg_class WHERE relname = 'af_seq'`).Scan(&persistence); err != nil || persistence != "p" {
			t.Errorf("after SET LOGGED relpersistence %q, %v", persistence, err)
		}
		runB(t, tx, pg.DropSequence(wide))
	})
}

// TestDDL_PostgresMovesEveryMovableObject runs SetObjectSchema over every
// Movable and reads each object's schema back from the catalogue, which is the
// only thing that proves the object kind the Go type chose was the right one:
// a wrong keyword here is a syntax error, and a right keyword for the wrong
// object moves nothing.
func TestDDL_PostgresMovesEveryMovableObject(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		archive := pg.Schema{Name: "af_archive"}
		runB(t, tx, pg.CreateSchema(archive))
		runB(t, tx, afTable())

		view := pg.View{Name: "af_view"}
		mat := pg.MaterializedView{Name: "af_mat"}
		seq := pg.Sequence[int64]{Name: "af_moved_seq"}
		enum := pg.Enum[ddlStatus]{Name: "af_status"}
		comp := pg.Composite[afMoney]{Name: "af_money"}
		dom := pg.Domain[string]{Name: "af_code"}
		body := pg.Select(afCode).From(afT)
		runB(t, tx, pg.CreateView(view).As(body))
		runB(t, tx, pg.CreateMaterializedView(mat).As(body))
		runB(t, tx, pg.CreateSequence(seq))
		runB(t, tx, pg.CreateEnum(enum, "active", "banned"))
		runB(t, tx, pg.CreateComposite(comp, pg.Attr("cents", ddl.RawType[int64]("bigint"))))
		runB(t, tx, pg.CreateDomain(dom, ddl.RawType[string]("text")))

		for name, o := range map[string]pg.Movable{
			"af_view":      view,
			"af_mat":       mat,
			"af_moved_seq": seq,
			"af_status":    enum,
			"af_money":     comp,
			"af_code":      dom,
		} {
			runB(t, tx, pg.SetObjectSchema(o, archive))
			var schema string
			if err := tx.QueryRowContext(ctx, `SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relname = $1
				UNION ALL
				SELECT n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE t.typname = $1 AND t.typtype IN ('e', 'd')`, name).Scan(&schema); err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if schema != archive.Name {
				t.Errorf("%s is in schema %q, want %q", name, schema, archive.Name)
			}
		}
	})
}

// afMoney is the Go type a composite column would scan into; the test moves
// the type rather than using it, so it needs no methods.
type afMoney struct{ Cents int64 }

// TestDDL_PostgresViewOptions runs ALTER VIEW SET and RESET and reads the
// reloptions back. security_invoker is the one a library shipping a view over
// its own row-level-secured tables needs, and nothing about the view's text
// says whether it is on.
func TestDDL_PostgresViewOptions(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		runB(t, tx, afTable())
		view := pg.View{Name: "af_opt_view"}
		runB(t, tx, pg.CreateView(view).As(pg.Select(afCode).From(afT)))
		runB(t, tx, pg.AlterViewOptions(view).SecurityInvoker(true).SecurityBarrier(true).CheckOption(pg.Cascaded))
		opts := readReloptions(t, tx, "af_opt_view")
		for _, want := range []string{"security_invoker=true", "security_barrier=true", "check_option=cascaded"} {
			if !hasOption(opts, want) {
				t.Errorf("reloptions %v do not hold %s", opts, want)
			}
		}
		// A write the view does not show is refused while check_option is on.
		if !failsAt(t, tx, `INSERT INTO af_opt_view (code) VALUES ('z')`) {
			t.Log("the view is not updatable, so CHECK OPTION had nothing to refuse")
		}
		runB(t, tx, pg.AlterViewOptions(view).Reset(pg.ViewCheckOption, pg.ViewSecurityBarrier, pg.ViewSecurityInvoker))
		if opts := readReloptions(t, tx, "af_opt_view"); len(opts) != 0 {
			t.Errorf("after RESET the view still holds %v", opts)
		}
	})
}

// readReloptions is the options a relation carries, which is where Postgres
// stores a view's settings.
func readReloptions(t *testing.T, tx *sql.Tx, name string) []string {
	t.Helper()
	rows, err := tx.QueryContext(context.Background(), `SELECT unnest(coalesce(reloptions, '{}')) FROM pg_class WHERE relname = $1`, name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasOption(opts []string, want string) bool {
	for _, o := range opts {
		if o == want {
			return true
		}
	}
	return false
}

// TestRawRecipes_PostgresAlterFormsThatStayPartial executes the workarounds
// docs/ddl/not-built.md gives for what the ALTER forms still leave out, so
// each recipe on that page is a statement the server has taken rather than a
// sentence somebody wrote. OWNER TO names a role, which a library cannot know,
// so each recipe names the role it is already running as.
func TestRawRecipes_PostgresAlterFormsThatStayPartial(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, afTable())
		view := pg.View{Name: "af_raw_view"}
		mat := pg.MaterializedView{Name: "af_raw_mat"}
		seq := pg.Sequence[int64]{Name: "af_raw_seq"}
		enum := pg.Enum[ddlStatus]{Name: "af_raw_status"}
		body := pg.Select(afCode).From(afT)
		runB(t, tx, pg.CreateView(view).As(body))
		runB(t, tx, pg.CreateMaterializedView(mat).As(body))
		runB(t, tx, pg.CreateSequence(seq))
		runB(t, tx, pg.CreateEnum(enum, "active"))
		for _, sql := range []string{
			`ALTER SEQUENCE "af_raw_seq" OWNER TO CURRENT_USER`,
			`ALTER TYPE "af_raw_status" OWNER TO CURRENT_USER`,
			`ALTER VIEW "af_raw_view" OWNER TO CURRENT_USER`,
			`ALTER VIEW "af_raw_view" RENAME COLUMN "code" TO "item_code"`,
			`ALTER MATERIALIZED VIEW "af_raw_mat" RENAME COLUMN "code" TO "item_code"`,
			`ALTER TABLE "af_items" ALTER COLUMN "id" SET INCREMENT BY 10`,
		} {
			if _, err := tx.ExecContext(ctx, sql); err != nil {
				t.Errorf("%s: %v", sql, err)
			}
		}
		var id int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO af_items (price, code) VALUES (1, 'a') RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO af_items (price, code) VALUES (1, 'b') RETURNING id`).Scan(&id); err != nil || id != 11 {
			t.Errorf("after SET INCREMENT BY 10 the second key was %d, %v", id, err)
		}
	})
}
