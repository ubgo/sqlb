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

// The CREATE TABLE, CREATE INDEX, CREATE VIEW, CREATE MATERIALIZED VIEW and
// CREATE TRIGGER forms found by the second pass, executed.
//
// The order of a table's options is why these run rather than only render:
// Postgres's grammar fixes it, WITH (...) USING heap is a syntax error where
// USING heap WITH (...) is not, and no rendering test would have told them
// apart.

var (
	tsT     = sqlb.Table{Name: "ts_items", Alias: "i"}
	tsID    = sqlb.Col[int64](tsT, "id")
	tsName  = sqlb.Col[string](tsT, "name")
	tsBody  = sqlb.Col[string](tsT, "body")
	tsScore = sqlb.Col[int64](tsT, "score")
)

// TestTask_CreateATableWithEveryOption builds a table carrying every option
// the second pass added, in the one order Postgres's grammar accepts, and
// reads the effects back out of the catalogue.
func TestTask_CreateATableWithEveryOption(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		parent := sqlb.Table{Name: "ts_parent"}
		runB(t, tx, pg.CreateTable(parent).Column(
			ddl.Column(sqlb.Col[int64](parent, "id")),
			ddl.Column(sqlb.Col[string](parent, "name")),
			ddl.Column(sqlb.Col[string](parent, "body")),
			ddl.Column(sqlb.Col[int64](parent, "score")),
		).Clause(
			// The parent's column carries the same collation, because an
			// inherited column whose collation differs from its parent's is a
			// conflict the server refuses.
			pg.ColumnCollate(sqlb.Col[string](parent, "body"), "C"),
		))
		runB(t, tx, pg.CreateTable(tsT).
			Column(
				ddl.Column(tsID),
				ddl.Column(tsName),
				// STORAGE and COMPRESSION come before COLLATE, which is the
				// one order Postgres's grammar takes.
				ddl.Column(tsBody),
				ddl.Column(tsScore),
			).
			Clause(
				pg.ColumnStorage(tsBody, pg.StorageExternal),
				pg.ColumnCollate(tsBody, "C"),
				pg.PrimaryKey(tsID).Include(tsName).With(pg.IntParameter("fillfactor", 80)),
				pg.Unique(pg.TableConstraint{Table: tsT, Name: "ts_items_name_key"}, tsName).
					Include(tsScore).With(pg.IntParameter("fillfactor", 90)),
				pg.Check(pg.TableConstraint{Table: tsT, Name: "ts_items_score_positive"}, sqlb.Greater(tsScore, 0)).NoInherit(),
				pg.Inherits(parent),
				pg.TableAccessMethod(pg.Heap),
				pg.TableStorage(pg.IntParameter("fillfactor", 70), pg.BoolParameter("autovacuum_enabled", true)),
			))

		if got := reloptions(t, tx, "ts_items"); !strings.Contains(got, "fillfactor=70") {
			t.Errorf("the table's reloptions are %s", got)
		}
		if got := reloptions(t, tx, "ts_items_pkey"); got != "{fillfactor=80}" {
			t.Errorf("the key's index has reloptions %s", got)
		}
		// The covering column is in the index and not in the key.
		var keyCols, allCols int
		if err := tx.QueryRowContext(ctx,
			`SELECT indnkeyatts, indnatts FROM pg_index WHERE indexrelid = 'ts_items_pkey'::regclass`).Scan(&keyCols, &allCols); err != nil {
			t.Fatal(err)
		}
		if keyCols != 1 || allCols != 2 {
			t.Errorf("the key has %d key columns of %d", keyCols, allCols)
		}
		// The inherited parent sees the child's rows.
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_items (id, name, body, score) VALUES (1, 'a', 'b', 5)`); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ts_parent`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the parent saw %d rows, %v", n, err)
		}
		// The check refuses, and it is marked NO INHERIT.
		if !failsAt(t, tx, `INSERT INTO ts_items (id, name, body, score) VALUES (2, 'c', 'd', 0)`) {
			t.Error("the check accepted a non-positive score")
		}
		var noInherit bool
		if err := tx.QueryRowContext(ctx,
			`SELECT connoinherit FROM pg_constraint WHERE conname = 'ts_items_score_positive'`).Scan(&noInherit); err != nil {
			t.Fatal(err)
		}
		if !noInherit {
			t.Error("the check is not NO INHERIT")
		}
		// A column's storage strategy, on a table of its own. It is checked
		// here rather than on ts_items because an inherited column takes its
		// parent's strategy and the child's is silently dropped: measured on
		// 17.10, where the same column reads extended under INHERITS and
		// external without it. That is the server's rule, and it is the reason
		// ColumnStorage says so.
		own := sqlb.Table{Name: "ts_own"}
		ownBody := sqlb.Col[string](own, "body")
		runB(t, tx, pg.CreateTable(own).Column(ddl.Column(ownBody)).
			Clause(pg.ColumnStorage(ownBody, pg.StorageExternal), pg.ColumnCompression(ownBody, "pglz"), pg.ColumnCollate(ownBody, "C")))
		var storage, compression string
		if err := tx.QueryRowContext(ctx,
			`SELECT attstorage, attcompression FROM pg_attribute WHERE attrelid = 'ts_own'::regclass AND attname = 'body'`).
			Scan(&storage, &compression); err != nil {
			t.Fatal(err)
		}
		if storage != "e" {
			t.Errorf("the column's storage is %q, want external", storage)
		}
		if compression != "p" {
			t.Errorf("the column's compression is %q, want pglz", compression)
		}
	})
}

// TestTask_ATemporaryTableForOneTransaction builds the temporary table a
// migration uses as scratch space, with the ON COMMIT that makes it last
// exactly as long as the work.
func TestTask_ATemporaryTableForOneTransaction(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		tmp := sqlb.Table{Name: "ts_scratch"}
		runB(t, tx, pg.CreateTable(tmp).
			Column(ddl.Column(sqlb.Col[int64](tmp, "id"))).
			Clause(pg.Temporary(), pg.OnCommit(pg.DeleteRows)))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_scratch VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		var temp bool
		if err := tx.QueryRowContext(ctx,
			`SELECT relpersistence = 't' FROM pg_class WHERE relname = 'ts_scratch'`).Scan(&temp); err != nil {
			t.Fatal(err)
		}
		if !temp {
			t.Error("the table is not temporary")
		}
		// ON COMMIT on an ordinary table is the server's own refusal, which is
		// why the two go together.
		plain := sqlb.Table{Name: "ts_plain"}
		s, err := pg.CreateTable(plain).Column(ddl.Column(sqlb.Col[int64](plain, "id"))).Clause(pg.OnCommit(pg.DeleteRows)).Build()
		if err != nil {
			t.Fatal(err)
		}
		if !failsAt(t, tx, s[0].SQL) {
			t.Error("ON COMMIT was accepted on a permanent table")
		}
	})
}

// TestTask_AKeyThatIsWhollyPresentOrWhollyAbsent runs MATCH FULL, which is the
// difference between a composite reference that may be half filled in and one
// that may not.
func TestTask_AKeyThatIsWhollyPresentOrWhollyAbsent(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		parent := sqlb.Table{Name: "ts_keys"}
		pa := sqlb.Col[int64](parent, "a")
		pb := sqlb.Col[int64](parent, "b")
		runB(t, tx, pg.CreateTable(parent).Column(ddl.Column(pa), ddl.Column(pb)).
			Clause(pg.Unique(pg.TableConstraint{Table: parent, Name: "ts_keys_ab"}, pa, pb)))
		child := sqlb.Table{Name: "ts_refs"}
		ca := sqlb.Col[*int64](child, "a")
		cb := sqlb.Col[*int64](child, "b")
		runB(t, tx, pg.CreateTable(child).Column(ddl.Column(ca).Nullable(), ddl.Column(cb).Nullable()).
			Clause(pg.ForeignKey(pg.TableConstraint{Table: child, Name: "ts_refs_ab"},
				pg.RefPair(ca, sqlb.Col[*int64](parent, "a")), pg.RefPair(cb, sqlb.Col[*int64](parent, "b"))).
				Match(pg.MatchFull)))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_keys VALUES (1, 2)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_refs VALUES (NULL, NULL)`); err != nil {
			t.Errorf("MATCH FULL refused an all-NULL reference: %v", err)
		}
		if !failsAt(t, tx, `INSERT INTO ts_refs VALUES (1, NULL)`) {
			t.Error("MATCH FULL accepted a half-filled reference")
		}
		var match string
		if err := tx.QueryRowContext(ctx,
			`SELECT confmatchtype FROM pg_constraint WHERE conname = 'ts_refs_ab'`).Scan(&match); err != nil {
			t.Fatal(err)
		}
		if match != "f" {
			t.Errorf("the key's match type is %q, want full", match)
		}
	})
}

// TestTask_BuildAPartitionedIndexWithoutALongLock runs ON ONLY, which is the
// half of that migration the first pass had to write in raw SQL: it exists
// here because the previous test for AttachPartition could not create the
// parent index with this package at all.
func TestTask_BuildAPartitionedIndexWithoutALongLock(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		parent := sqlb.Table{Name: "ts_events"}
		at := sqlb.Col[int64](parent, "at")
		runB(t, tx, pg.CreateTable(parent).Column(ddl.Column(at)).Clause(pg.PartitionByRange(at)))
		part := sqlb.Table{Name: "ts_events_2026"}
		runB(t, tx, pg.CreatePartition(part, parent).For(pg.FromTo(at, 0, 100)))

		parentIx := pg.Index{Name: "ts_events_at_ix"}
		runB(t, tx, pg.CreateIndex(parentIx, parent).On(pg.IndexKey(at)).OnOnly())
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = 'ts_events_at_ix'::regclass`).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if valid {
			t.Fatal("ON ONLY produced a valid index, so it did not skip the partitions")
		}
		// An operator class with its own parameters, which is the only way an
		// extension's index method is tuned; pg_trgm's GiST class takes the
		// signature length.
		runB(t, tx, pg.CreateExtension(pg.Extension{Name: "pg_trgm"}).IfNotExists())
		words := sqlb.Table{Name: "ts_words"}
		word := sqlb.Col[string](words, "word")
		runB(t, tx, pg.CreateTable(words).Column(ddl.Column(word)))
		runB(t, tx, pg.CreateIndex(pg.Index{Name: "ts_words_trgm_ix"}, words).Using(pg.GiST).
			On(pg.IndexKey(word).OpclassWith(pg.NewOpClass[string]("gist_trgm_ops"), pg.IntParameter("siglen", 64))))
		var opts string
		if err := tx.QueryRowContext(ctx,
			`SELECT coalesce(array_to_string(indoption, ','), '') FROM pg_index WHERE indexrelid = 'ts_words_trgm_ix'::regclass`).Scan(&opts); err != nil {
			t.Fatal(err)
		}

		partIx := pg.Index{Name: "ts_events_2026_at_ix"}
		runB(t, tx, pg.CreateIndex(partIx, part).On(pg.IndexKey(sqlb.Col[int64](part, "at"))))
		runB(t, tx, pg.AlterIndex(parentIx).AttachPartition(partIx))
		if err := tx.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = 'ts_events_at_ix'::regclass`).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if !valid {
			t.Error("the parent index is still invalid after its only partition was attached")
		}
	})
}

// TestTask_ShipAViewWithItsInterfaceDeclared runs the CREATE VIEW forms that
// were missing: the column list that makes the view's interface a declaration,
// and the options that must be set at creation rather than a statement later.
func TestTask_ShipAViewWithItsInterfaceDeclared(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, pg.CreateTable(tsT).Column(ddl.Column(tsID), ddl.Column(tsName), ddl.Column(tsBody), ddl.Column(tsScore)))
		v := pg.View{Name: "ts_named"}
		runB(t, tx, pg.CreateView(v).As(pg.Select(tsName).From(tsT)).
			Columns("label").
			WithOptions(pg.SecurityInvokerSetting(true), pg.SecurityBarrierSetting(true)))
		if got := columnsOf(t, tx, "ts_named"); got != "label" {
			t.Errorf("the view's columns are %s", got)
		}
		if got := reloptions(t, tx, "ts_named"); !strings.Contains(got, "security_invoker=true") {
			t.Errorf("the view's options are %s", got)
		}

		// A recursive view, which needs its column list and is refused
		// without one.
		if _, err := pg.CreateView(pg.View{Name: "x"}).As(pg.Select(tsID).From(tsT)).Recursive().Build(); err == nil {
			t.Error("a recursive view with no column list was built")
		}
		if _, err := tx.ExecContext(ctx, `CREATE RECURSIVE VIEW ts_rec (n) AS SELECT 1 UNION ALL SELECT n + 1 FROM ts_rec WHERE n < 3`); err != nil {
			t.Fatal(err)
		}

		// A temporary view, which lives in the session's temporary schema.
		tv := pg.View{Name: "ts_tmp"}
		runB(t, tx, pg.CreateView(tv).As(pg.Select(tsName).From(tsT)).Temporary())
		var temp bool
		if err := tx.QueryRowContext(ctx,
			`SELECT n.nspname LIKE 'pg_temp%' FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relname = 'ts_tmp'`).Scan(&temp); err != nil {
			t.Fatal(err)
		}
		if !temp {
			t.Error("the view is not temporary")
		}

		// A materialized view with its column list, access method and storage
		// parameters.
		mv := pg.MaterializedView{Name: "ts_counts"}
		runB(t, tx, pg.CreateMaterializedView(mv).As(pg.Select(tsName).From(tsT)).
			Columns("label").Using(pg.Heap).WithStorage(pg.IntParameter("fillfactor", 60)))
		if got := columnsOf(t, tx, "ts_counts"); got != "label" {
			t.Errorf("the materialized view's columns are %s", got)
		}
		if got := reloptions(t, tx, "ts_counts"); got != "{fillfactor=60}" {
			t.Errorf("the materialized view's reloptions are %s", got)
		}
	})
}

// TestTask_ATriggerThatSeesWhatTheStatementChanged runs the CREATE TRIGGER
// forms that were missing: transition tables, a TRUNCATE trigger, a deferred
// constraint trigger, INSTEAD OF on a view, and the constant arguments a
// function reads out of TG_ARGV.
func TestTask_ATriggerThatSeesWhatTheStatementChanged(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, pg.CreateTable(tsT).Column(ddl.Column(tsID), ddl.Column(tsName), ddl.Column(tsBody), ddl.Column(tsScore)))
		audit := sqlb.Table{Name: "ts_audit"}
		runB(t, tx, pg.CreateTable(audit).Column(ddl.Column(sqlb.Col[string](audit, "what")), ddl.Column(sqlb.Col[int64](audit, "rows"))))

		// One call per statement, counting the rows out of the transition
		// table, which is the whole reason the form exists: without it this
		// is a row trigger and a call per row.
		count := pg.Function{Name: "ts_count_changes"}
		runB(t, tx, pg.CreateTriggerFunction(count,
			`BEGIN INSERT INTO ts_audit SELECT TG_ARGV[0], count(*) FROM new_rows; RETURN NULL; END`))
		tr := pg.Trigger{Name: "ts_after_insert", Table: tsT}
		runB(t, tx, pg.CreateTrigger(tr).After(pg.OnInsert()).
			Referencing("", "new_rows").
			ExecuteWith(count, "insert"))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_items VALUES (1, 'a', 'b', 1), (2, 'c', 'd', 2), (3, 'e', 'f', 3)`); err != nil {
			t.Fatal(err)
		}
		var what string
		var rows int64
		if err := tx.QueryRowContext(ctx, `SELECT what, rows FROM ts_audit`).Scan(&what, &rows); err != nil {
			t.Fatal(err)
		}
		if what != "insert" || rows != 3 {
			t.Errorf("the statement trigger recorded %q and %d rows, want insert and 3", what, rows)
		}

		// A TRUNCATE trigger, the one event a row trigger cannot have.
		truncated := pg.Function{Name: "ts_on_truncate"}
		runB(t, tx, pg.CreateTriggerFunction(truncated, `BEGIN INSERT INTO ts_audit VALUES ('truncate', 0); RETURN NULL; END`))
		runB(t, tx, pg.CreateTrigger(pg.Trigger{Name: "ts_after_truncate", Table: tsT}).
			After(pg.OnTruncate()).Execute(truncated))
		if _, err := tx.ExecContext(ctx, `TRUNCATE ts_items`); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ts_audit WHERE what = 'truncate'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the truncate trigger fired %d times, %v", n, err)
		}

		// A deferred constraint trigger: it sees the finished work rather than
		// the half of it the first statement wrote.
		guard := pg.Function{Name: "ts_guard"}
		runB(t, tx, pg.CreateTriggerFunction(guard,
			`BEGIN IF (SELECT count(*) FROM ts_items) % 2 <> 0 THEN RAISE EXCEPTION 'odd'; END IF; RETURN NULL; END`))
		runB(t, tx, pg.CreateTrigger(pg.Trigger{Name: "ts_pairs", Table: tsT}).Constraint().
			After(pg.OnInsert()).Deferrable(pg.InitiallyDeferred).ForEachRow().Execute(guard))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_items VALUES (10, 'a', 'b', 1), (11, 'c', 'd', 2)`); err != nil {
			t.Errorf("the deferred trigger fired before the statement finished: %v", err)
		}
		var deferred bool
		if err := tx.QueryRowContext(ctx,
			`SELECT tgdeferrable AND tginitdeferred FROM pg_trigger WHERE tgname = 'ts_pairs'`).Scan(&deferred); err != nil {
			t.Fatal(err)
		}
		if !deferred {
			t.Error("the constraint trigger is not initially deferred")
		}

		// INSTEAD OF on a view, which is how a view Postgres cannot update
		// automatically is made writable.
		v := pg.View{Name: "ts_writable"}
		runB(t, tx, pg.CreateView(v).As(pg.Select(tsName).From(tsT)))
		writer := pg.Function{Name: "ts_write"}
		runB(t, tx, pg.CreateTriggerFunction(writer, `BEGIN INSERT INTO ts_audit VALUES ('instead', 1); RETURN NEW; END`))
		runB(t, tx, pg.CreateTrigger(pg.Trigger{Name: "ts_instead", Table: sqlb.Table{Name: "ts_writable"}}).
			InsteadOf(pg.OnInsert()).ForEachRow().Execute(writer))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_writable VALUES ('x')`); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ts_audit WHERE what = 'instead'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("the INSTEAD OF trigger fired %d times, %v", n, err)
		}
	})
}

// TestTask_RebuildASummaryTableFromAQuery runs CREATE TABLE ... AS, the
// statement a library writes when the table it wants is the result of a query
// it has already written.
func TestTask_RebuildASummaryTableFromAQuery(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, pg.CreateTable(tsT).Column(ddl.Column(tsID), ddl.Column(tsName), ddl.Column(tsBody), ddl.Column(tsScore)))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ts_items VALUES (1, 'a', 'b', 5), (2, 'c', 'd', 7)`); err != nil {
			t.Fatal(err)
		}
		summary := sqlb.Table{Name: "ts_summary"}
		runB(t, tx, pg.CreateTableAs(summary, pg.Select(tsName).From(tsT)).
			Columns("label").Unlogged().WithStorage(pg.IntParameter("fillfactor", 70)))
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ts_summary`).Scan(&n); err != nil || n != 2 {
			t.Errorf("the summary holds %d rows, %v", n, err)
		}
		if got := columnsOf(t, tx, "ts_summary"); got != "label" {
			t.Errorf("the summary's columns are %s", got)
		}
		var persistence string
		if err := tx.QueryRowContext(ctx, `SELECT relpersistence FROM pg_class WHERE relname = 'ts_summary'`).Scan(&persistence); err != nil {
			t.Fatal(err)
		}
		if persistence != "u" {
			t.Errorf("the summary's persistence is %q, want unlogged", persistence)
		}

		// IF NOT EXISTS means the query does not run either, so a second
		// install does not append the rows again.
		runB(t, tx, pg.CreateTableAs(summary, pg.Select(tsName).From(tsT)).IfNotExists().Columns("label"))
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ts_summary`).Scan(&n); err != nil || n != 2 {
			t.Errorf("after a second run the summary holds %d rows, %v", n, err)
		}

		// WITH NO DATA makes the shape and none of the rows.
		empty := sqlb.Table{Name: "ts_shape"}
		runB(t, tx, pg.CreateTableAs(empty, pg.Select(tsName).From(tsT)).WithNoData())
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ts_shape`).Scan(&n); err != nil || n != 0 {
			t.Errorf("WITH NO DATA left %d rows, %v", n, err)
		}
	})
}

// TestTask_ChangeAColumnsStorageAfterwards runs the ALTER half of the two
// column options and reads both back, and the CASCADE that carries an
// attribute change into a typed table.
func TestTask_ChangeAColumnsStorageAfterwards(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		ctx := context.Background()
		runB(t, tx, pg.CreateTable(tsT).Column(ddl.Column(tsID), ddl.Column(tsName), ddl.Column(tsBody), ddl.Column(tsScore)))
		runB(t, tx, pg.AlterTable(tsT).Clause(
			pg.SetColumnStorage(tsBody, pg.StorageExternal),
			pg.SetColumnCompression(tsBody, "pglz"),
		))
		var storage, compression string
		if err := tx.QueryRowContext(ctx,
			`SELECT attstorage, attcompression FROM pg_attribute WHERE attrelid = 'ts_items'::regclass AND attname = 'body'`).
			Scan(&storage, &compression); err != nil {
			t.Fatal(err)
		}
		if storage != "e" || compression != "p" {
			t.Errorf("after the ALTER the column is %q/%q, want e/p", storage, compression)
		}

		// A typed table, and an attribute dropped from under it with CASCADE.
		row := pg.Composite[string]{Name: "ts_row"}
		runB(t, tx, pg.CreateComposite(row,
			pg.Attr("a", ddl.RawType[int64]("bigint")), pg.Attr("b", ddl.RawType[int64]("bigint"))))
		typed := sqlb.Table{Name: "ts_typed"}
		runB(t, tx, pg.CreateTable(typed).
			Column(ddl.Column(sqlb.Col[int64](typed, "a")).Nullable(), ddl.Column(sqlb.Col[int64](typed, "b")).Nullable()))
		runB(t, tx, pg.AlterTable(typed).Clause(pg.OfType(row)))
		runB(t, tx, pg.AlterComposite(row).DropAttribute("b").Cascade())
		if got := columnsOf(t, tx, "ts_typed"); got != "a" {
			t.Errorf("after the cascade the typed table has columns %s", got)
		}
	})
}
