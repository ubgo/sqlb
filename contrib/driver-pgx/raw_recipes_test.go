package driverpgx_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/schema"
)

// The raw recipes in docs/ddl/not-built.md, executed. Every Postgres entry the
// DDL detector marks partial or left out has a recipe there showing how to
// write it through the escape hatches anyway, and each recipe is here, run
// against a real Postgres and read back, so the page shows SQL that works
// rather than SQL that looks right.

// rawStatement is the statement-level escape hatch: any SQL the builders do
// not write, as a ddl.Statement a Script runner treats like any other.
func rawStatement(sql string) ddl.Script { return ddl.Script{{SQL: sql, Transactional: true}} }

// rawAction is the ALTER TABLE escape hatch: an action the builders do not
// write, typed to Postgres so it cannot land on another engine's statement,
// and joined with other actions like a built one.
func rawAction(sql string) pg.AlterClause {
	return ddl.NewAlterClause[pg.Dialect](func(*sqlb.Args) string { return sql })
}

// one scalar read, failing the test on error.
func scalar(t *testing.T, tx *sql.Tx, q string) string {
	t.Helper()
	var s sql.NullString
	if err := tx.QueryRowContext(context.Background(), q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func exec(t *testing.T, tx *sql.Tx, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := tx.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestRawRecipes_Postgres(t *testing.T) {
	d := pg.Dialect{}
	rr := sqlb.Table{Name: "rr_t", Alias: "t"}
	for _, tc := range []struct {
		entry string
		setup []string
		run   func(t *testing.T, tx *sql.Tx)
	}{
		{"ALTER DOMAIN", []string{`CREATE DOMAIN rr_d AS text`}, func(t *testing.T, tx *sql.Tx) {
			lit, err := d.QuoteLiteral("unknown")
			if err != nil {
				t.Fatal(err)
			}
			runTx(t, tx, rawStatement(`ALTER DOMAIN `+d.QuoteIdent("rr_d")+` SET DEFAULT `+lit), nil)
			if got := scalar(t, tx, `SELECT typdefault FROM pg_type WHERE typname = 'rr_d'`); got != "'unknown'::text" {
				t.Errorf("default %q", got)
			}
			runTx(t, tx, rawStatement(`ALTER DOMAIN "rr_d" DROP DEFAULT`), nil)
		}},
		{"ALTER EXTENSION", []string{`CREATE SCHEMA rr_ext`, `CREATE EXTENSION IF NOT EXISTS pg_trgm`}, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`ALTER EXTENSION "pg_trgm" SET SCHEMA "rr_ext"`), nil)
			if got := scalar(t, tx, `SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE extname = 'pg_trgm'`); got != "rr_ext" {
				t.Errorf("schema %q", got)
			}
		}},
		{"ALTER INDEX", []string{`CREATE TABLE rr_t (id bigint)`, `CREATE INDEX rr_i ON rr_t (id)`}, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`ALTER INDEX "rr_i" SET (fillfactor = 70)`), nil)
			if got := scalar(t, tx, `SELECT array_to_string(reloptions, ',') FROM pg_class WHERE relname = 'rr_i'`); got != "fillfactor=70" {
				t.Errorf("reloptions %q", got)
			}
		}},
		{"ALTER MATERIALIZED VIEW", []string{`CREATE SCHEMA rr_ext`, `CREATE MATERIALIZED VIEW rr_mv AS SELECT 1 AS n`}, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`ALTER MATERIALIZED VIEW "rr_mv" SET SCHEMA "rr_ext"`), nil)
			if got := scalar(t, tx, `SELECT schemaname FROM pg_matviews WHERE matviewname = 'rr_mv'`); got != "rr_ext" {
				t.Errorf("schema %q", got)
			}
		}},
		{"ALTER POLICY", []string{`CREATE ROLE rr_role NOLOGIN`, `CREATE TABLE rr_t (id bigint)`, `CREATE POLICY rr_p ON rr_t USING (true)`}, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`ALTER POLICY "rr_p" ON "rr_t" TO "rr_role"`), nil)
			if got := scalar(t, tx, `SELECT array_to_string(roles, ',') FROM pg_policies WHERE policyname = 'rr_p'`); got != "rr_role" {
				t.Errorf("roles %q", got)
			}
		}},
		{"ALTER SEQUENCE", []string{`CREATE SCHEMA rr_ext`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.CreateSequence(pg.Sequence[int64]{Name: "rr_s"}))
			runTx(t, tx, rawStatement(`ALTER SEQUENCE "rr_s" AS integer`), nil)
			runTx(t, tx, rawStatement(`ALTER SEQUENCE "rr_s" SET SCHEMA "rr_ext"`), nil)
			if got := scalar(t, tx, `SELECT schemaname || ':' || data_type::text FROM pg_sequences WHERE sequencename = 'rr_s'`); got != "rr_ext:integer" {
				t.Errorf("sequence %q", got)
			}
		}},
		{"ALTER TYPE", []string{`CREATE SCHEMA rr_ext`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.CreateComposite(pg.Composite[string]{Name: "rr_money"}, pg.Attr("amount", ddl.RawType[string]("numeric"))))
			runTx(t, tx, rawStatement(`ALTER TYPE "rr_money" SET SCHEMA "rr_ext"`), nil)
			if got := scalar(t, tx, `SELECT n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE typname = 'rr_money'`); got != "rr_ext" {
				t.Errorf("type schema %q", got)
			}
		}},
		{"ALTER VIEW", []string{`CREATE SCHEMA rr_ext`, `CREATE TABLE rr_t (id bigint, n int)`, `CREATE VIEW rr_v AS SELECT id, n FROM rr_t`}, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`ALTER VIEW "rr_v" SET (security_barrier = true)`), nil)
			if got := scalar(t, tx, `SELECT array_to_string(reloptions, ',') FROM pg_class WHERE relname = 'rr_v'`); got != "security_barrier=true" {
				t.Errorf("view options %q", got)
			}
			runTx(t, tx, rawStatement(`ALTER VIEW "rr_v" SET SCHEMA "rr_ext"`), nil)
		}},
		{"CREATE FUNCTION", nil, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`CREATE OR REPLACE FUNCTION "rr_add"(a integer, b integer) RETURNS integer LANGUAGE sql IMMUTABLE AS $$ SELECT a + b $$`), nil)
			var n int64
			q, err := pg.Select().Columns(sqlb.Bind(pg.Func[int64]("rr.add", "rr_add", sqlb.Param(int64(2)), sqlb.Param(int64(3))), &n)).Build()
			if err != nil {
				t.Fatal(err)
			}
			if err := sqlb.One(context.Background(), tx, q); err != nil || n != 5 {
				t.Errorf("rr_add(2, 3) = %d, %v", n, err)
			}
		}},
		{"CREATE TYPE", nil, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`CREATE TYPE "rr_floatrange" AS RANGE (subtype = float8)`), nil)
			if got := scalar(t, tx, `SELECT '[1.5, 2.5)'::rr_floatrange @> 2.0::float8`); got != "true" {
				t.Errorf("range containment %q", got)
			}
		}},

		// ALTER TABLE forms, through the typed action hatch on pg.AlterTable.
		{"ALTER COLUMN SET EXPRESSION AS", []string{`CREATE TABLE rr_t (a int, x int GENERATED ALWAYS AS (a) STORED)`, `INSERT INTO rr_t (a) VALUES (3)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`ALTER COLUMN "x" SET EXPRESSION AS ("a" * 2)`)))
			if got := scalar(t, tx, `SELECT x FROM rr_t`); got != "6" {
				t.Errorf("x = %q", got)
			}
		}},
		{"ALTER COLUMN DROP EXPRESSION IF EXISTS", []string{`CREATE TABLE rr_t (a int, x int GENERATED ALWAYS AS (a) STORED)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`ALTER COLUMN "x" DROP EXPRESSION IF EXISTS`)))
			exec(t, tx, `INSERT INTO rr_t VALUES (1, 99)`)
		}},
		{"ALTER COLUMN SET GENERATED ALWAYS BY DEFAULT SET RESTART WITH", []string{`CREATE TABLE rr_t (id bigint GENERATED ALWAYS AS IDENTITY)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`ALTER COLUMN "id" SET GENERATED BY DEFAULT`), rawAction(`ALTER COLUMN "id" RESTART WITH 100`)))
			exec(t, tx, `INSERT INTO rr_t DEFAULT VALUES`, `INSERT INTO rr_t VALUES (5)`)
			if got := scalar(t, tx, `SELECT max(id) FROM rr_t`); got != "100" {
				t.Errorf("max id %q", got)
			}
		}},
		{"ALTER COLUMN DROP IDENTITY IF EXISTS", []string{`CREATE TABLE rr_t (id bigint GENERATED ALWAYS AS IDENTITY)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`ALTER COLUMN "id" DROP IDENTITY IF EXISTS`)))
			if got := scalar(t, tx, `SELECT is_identity FROM information_schema.columns WHERE table_name = 'rr_t'`); got != "NO" {
				t.Errorf("identity %q", got)
			}
		}},
		{"ADD", []string{`CREATE TABLE rr_t (id bigint NOT NULL)`, `CREATE UNIQUE INDEX rr_t_idx ON rr_t (id)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`ADD CONSTRAINT "rr_t_key" UNIQUE USING INDEX "rr_t_idx"`)))
			if got := scalar(t, tx, `SELECT contype FROM pg_constraint WHERE conname = 'rr_t_key'`); got != "u" {
				t.Errorf("constraint %q", got)
			}
		}},
		{"ALTER CONSTRAINT DEFERRABLE NOT DEFERRABLE INITIALLY DEFERRED INITIALLY IMMEDIATE", []string{`CREATE TABLE rr_p (id bigint PRIMARY KEY)`, `CREATE TABLE rr_t (pid bigint CONSTRAINT rr_fk REFERENCES rr_p)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`ALTER CONSTRAINT "rr_fk" DEFERRABLE INITIALLY DEFERRED`)))
			if got := scalar(t, tx, `SELECT condeferred FROM pg_constraint WHERE conname = 'rr_fk'`); got != "true" {
				t.Errorf("deferred %q", got)
			}
		}},
		{"DISABLE RULE", []string{`CREATE TABLE rr_t (id bigint)`, `CREATE RULE rr_rule AS ON INSERT TO rr_t DO ALSO NOTHING`}, func(t *testing.T, tx *sql.Tx) {
			for _, form := range []string{`DISABLE RULE "rr_rule"`, `ENABLE RULE "rr_rule"`, `ENABLE REPLICA RULE "rr_rule"`, `ENABLE ALWAYS RULE "rr_rule"`} {
				runB(t, tx, pg.AlterTable(rr).Clause(rawAction(form)))
			}
			if got := scalar(t, tx, `SELECT ev_enabled FROM pg_rewrite WHERE rulename = 'rr_rule'`); got != "A" {
				t.Errorf("rule state %q", got)
			}
		}},
		{"INHERIT", []string{`CREATE TABLE rr_parent (id bigint)`, `CREATE TABLE rr_t (id bigint)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`INHERIT "rr_parent"`)))
			exec(t, tx, `INSERT INTO rr_t VALUES (1)`)
			if got := scalar(t, tx, `SELECT count(*) FROM rr_parent`); got != "1" {
				t.Errorf("parent sees %q", got)
			}
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`NO INHERIT "rr_parent"`)))
		}},
		{"OF", []string{`CREATE TYPE rr_row AS (id bigint)`, `CREATE TABLE rr_t (id bigint)`}, func(t *testing.T, tx *sql.Tx) {
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`OF "rr_row"`)))
			if got := scalar(t, tx, `SELECT reloftype::regtype::text FROM pg_class WHERE relname = 'rr_t'`); got != "rr_row" {
				t.Errorf("typed as %q", got)
			}
			runB(t, tx, pg.AlterTable(rr).Clause(rawAction(`NOT OF`)))
		}},
		{"operator", []string{`CREATE TABLE rr_t (id bigint)`}, func(t *testing.T, tx *sql.Tx) {
			runTx(t, tx, rawStatement(`ANALYZE `+rr.Qualified(d)), nil)
		}},
	} {
		t.Run(strings.ReplaceAll(tc.entry, " ", "_"), func(t *testing.T) {
			inTx(t, func(tx *sql.Tx) {
				exec(t, tx, tc.setup...)
				tc.run(t, tx)
			})
		})
	}
}

// TestDDL_ConstraintValuesExecute runs the declared-constraint statements on
// Postgres and reads each back from the catalogue: a unique key created from
// a TableConstraint, commented on, renamed and dropped through the same value,
// and a comment on an extended statistics object.
func TestDDL_ConstraintValuesExecute(t *testing.T) {
	tb := sqlb.Table{Name: "cv_t", Alias: "t"}
	code := sqlb.Col[string](tb, "code")
	a := sqlb.Col[int64](tb, "a")
	key := pg.TableConstraint{Table: tb, Name: "cv_code_key"}
	renamed := pg.TableConstraint{Table: tb, Name: "cv_code_uq"}
	stats := pg.Statistics{Name: "cv_stats"}
	inTx(t, func(tx *sql.Tx) {
		runB(t, tx, pg.CreateTable(tb).Column(ddl.Column(code), ddl.Column(a)).Clause(pg.Unique(key, code)))
		runB(t, tx, pg.CommentOnConstraint(key).Is("one code per row"))
		if got := scalar(t, tx, `SELECT obj_description(oid, 'pg_constraint') FROM pg_constraint WHERE conname = 'cv_code_key'`); got != "one code per row" {
			t.Errorf("constraint comment %q", got)
		}
		runB(t, tx, pg.AlterTable(tb).Clause(pg.RenameConstraint(key, renamed)))
		if got := scalar(t, tx, `SELECT count(*) FROM pg_constraint WHERE conname = 'cv_code_uq'`); got != "1" {
			t.Errorf("renamed constraint count %q", got)
		}
		runB(t, tx, pg.CreateStatistics(stats, tb, code, a))
		runB(t, tx, pg.CommentOn(stats).Is("code and a move together"))
		if got := scalar(t, tx, `SELECT obj_description(oid, 'pg_statistic_ext') FROM pg_statistic_ext WHERE stxname = 'cv_stats'`); got != "code and a move together" {
			t.Errorf("statistics comment %q", got)
		}
		runB(t, tx, pg.AlterTable(tb).Clause(pg.DropConstraint(renamed)))
		if got := scalar(t, tx, `SELECT count(*) FROM pg_constraint WHERE conrelid = 'cv_t'::regclass AND contype = 'u'`); got != "0" {
			t.Errorf("unique constraints left %q", got)
		}
	})
}

// TestDDL_JSONDefaultExecutes inserts a row that takes a JSON column's
// default and reads it back as the same JSON value.
func TestDDL_JSONDefaultExecutes(t *testing.T) {
	tb := sqlb.Table{Name: "jd_t", Alias: "t"}
	id := sqlb.Col[int64](tb, "id")
	doc := sqlb.Col[json.RawMessage](tb, "doc")
	inTx(t, func(tx *sql.Tx) {
		runB(t, tx, pg.CreateTable(tb).Column(ddl.Column(id),
			ddl.Column(doc).As(ddl.Portable[json.RawMessage](schema.TypeJSON)).Default(json.RawMessage(`{"a":[1,"it's"]}`))))
		exec(t, tx, `INSERT INTO jd_t (id) VALUES (1)`)
		if got := scalar(t, tx, `SELECT doc = '{"a":[1,"it''s"]}'::jsonb FROM jd_t`); got != "true" {
			t.Errorf("the default is not the JSON written: %q", got)
		}
	})
}

// TestDDL_Tier5Executes runs the Postgres forms built after the four tiers
// and reads each result back from the catalogue: sequence options and a
// rename, a view column's default used by an insert through the view, row
// security forced and released, and a composite type through its whole
// attribute lifecycle with a column of it holding a value.
func TestDDL_Tier5Executes(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		seq := pg.Sequence[int64]{Name: "t5_seq"}
		runB(t, tx, pg.CreateSequence(seq))
		runB(t, tx, pg.AlterSequence(seq).IncrementBy(5).MinValue(1).MaxValue(1000).Cache(10).Cycle())
		if got := scalar(t, tx, `SELECT increment_by || ',' || min_value || ',' || max_value || ',' || cache_size || ',' || cycle FROM pg_sequences WHERE sequencename = 't5_seq'`); got != "5,1,1000,10,true" {
			t.Errorf("sequence options %q", got)
		}
		runB(t, tx, pg.AlterSequence(seq).RenameTo(pg.Sequence[int64]{Name: "t5_seq2"}))
		if got := scalar(t, tx, `SELECT count(*) FROM pg_sequences WHERE sequencename = 't5_seq2'`); got != "1" {
			t.Errorf("renamed sequence %q", got)
		}

		tb := sqlb.Table{Name: "t5_t", Alias: "t"}
		id := sqlb.Col[int64](tb, "id")
		note := sqlb.Col[string](tb, "note")
		runB(t, tx, pg.CreateTable(tb).Column(ddl.Column(id), ddl.Column(note)))
		exec(t, tx, `CREATE VIEW t5_v AS SELECT id, note FROM t5_t`)
		view := pg.View{Name: "t5_v"}
		vnote := sqlb.Col[string](sqlb.Table{Name: "t5_v"}, "note")
		runB(t, tx, pg.SetViewColumnDefault(view, vnote, "from the view"))
		exec(t, tx, `INSERT INTO t5_v (id) VALUES (1)`)
		if got := scalar(t, tx, `SELECT note FROM t5_t`); got != "from the view" {
			t.Errorf("view default %q", got)
		}
		runB(t, tx, pg.DropViewColumnDefault(view, vnote))

		runB(t, tx, pg.AlterTable(tb).Clause(pg.EnableRowLevelSecurity(), pg.ForceRowLevelSecurity()))
		if got := scalar(t, tx, `SELECT relrowsecurity || ',' || relforcerowsecurity FROM pg_class WHERE relname = 't5_t'`); got != "true,true" {
			t.Errorf("forced row security %q", got)
		}
		runB(t, tx, pg.AlterTable(tb).Clause(pg.NoForceRowLevelSecurity(), pg.DisableRowLevelSecurity()))
		if got := scalar(t, tx, `SELECT relrowsecurity || ',' || relforcerowsecurity FROM pg_class WHERE relname = 't5_t'`); got != "false,false" {
			t.Errorf("released row security %q", got)
		}

		money := pg.Composite[string]{Name: "t5_money"}
		runB(t, tx, pg.CreateComposite(money, pg.Attr("amount", ddl.RawType[string]("numeric")), pg.Attr("currency", ddl.RawType[string]("text"))))
		runB(t, tx, pg.AlterComposite(money).AddAttribute(pg.Attr("note", ddl.RawType[string]("text"))))
		runB(t, tx, pg.AlterComposite(money).AlterAttributeType(pg.Attr("amount", ddl.RawType[string]("numeric(12,2)"))))
		runB(t, tx, pg.AlterComposite(money).RenameAttribute("note", "memo"))
		runB(t, tx, pg.AlterComposite(money).DropAttribute("memo"))
		price := sqlb.Col[string](sqlb.Table{Name: "t5_prices"}, "price")
		runB(t, tx, pg.CreateTable(sqlb.Table{Name: "t5_prices"}).Column(ddl.Column(price).As(money)))
		exec(t, tx, `INSERT INTO t5_prices VALUES (ROW(9.5, 'EUR'))`)
		if got := scalar(t, tx, `SELECT (price).amount || ' ' || (price).currency FROM t5_prices`); got != "9.50 EUR" {
			t.Errorf("composite value %q", got)
		}
		if got := scalar(t, tx, `SELECT string_agg(attname, ',' ORDER BY attnum) FROM pg_attribute WHERE attrelid = 't5_money'::regclass AND attnum > 0 AND NOT attisdropped`); got != "amount,currency" {
			t.Errorf("composite attributes %q", got)
		}
		runB(t, tx, pg.Guarded(pg.CreateComposite(money, pg.Attr("amount", ddl.RawType[string]("numeric")))))
	})
}
