package mysql_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

// The forms found inside statements the detector already called built, by
// reading MySQL 8.4's own HELP output for each one on 2026-09-24. A `built`
// entry names a constructor and carries no limit, so none of these had
// anything reporting it; that is the defect, and these are its instances.

var arch = sqlb.Table{Name: "arch", Alias: "a"}

// TestForms_Render pins the text of each.
func TestForms_Render(t *testing.T) {
	for name, tc := range map[string]struct {
		b    interface{ Build() (ddl.Script, error) }
		want string
	}{
		"temporary": {mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.Temporary(), mysql.RawTableOption("STATS_PERSISTENT = 1")),
			"CREATE TEMPORARY TABLE `items` (`id` BIGINT NOT NULL) STATS_PERSISTENT = 1"},
		"like": {mysql.CreateTableLike(arch, items).IfNotExists().Temporary(),
			"CREATE TEMPORARY TABLE IF NOT EXISTS `arch` LIKE `items`"},
		"like plain": {mysql.CreateTableLike(arch, items), "CREATE TABLE `arch` LIKE `items`"},
		"as select": {mysql.CreateTableAs(arch, mysql.Select(id).From(items)).Temporary().OnConflict(mysql.ConflictIgnore),
			"CREATE TEMPORARY TABLE `arch` IGNORE AS SELECT `i`.`id` FROM `items` AS `i`"},
		"as select replace": {mysql.CreateTableAs(arch, mysql.Select(id).From(items)).IfNotExists().OnConflict(mysql.ConflictReplace),
			"CREATE TABLE IF NOT EXISTS `arch` REPLACE AS SELECT `i`.`id` FROM `items` AS `i`"},
		"drop temporary": {mysql.DropTemporaryTable(arch, items).IfExists(),
			"DROP TEMPORARY TABLE IF EXISTS `arch`; DROP TEMPORARY TABLE IF EXISTS `items`"},
		// The separator here is a space and not a comma, which only the
		// server could say; see writeHow.
		"index options": {mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).
			With(mysql.IndexComment("why"), mysql.IndexKeyBlockSize(8), mysql.IndexInvisible()).
			Algorithm(mysql.AlgorithmInplace).Lock(mysql.LockNone),
			"CREATE INDEX `i` ON `items` (`sku`) COMMENT 'why' KEY_BLOCK_SIZE = 8 INVISIBLE ALGORITHM = INPLACE LOCK = NONE"},
		"visible index": {mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).With(mysql.IndexVisible()),
			"CREATE INDEX `i` ON `items` (`sku`) VISIBLE"},
		"fulltext parser": {mysql.CreateIndex(mysql.Index{Name: "f"}, items).Fulltext().On(mysql.IndexKey(sku)).With(mysql.IndexWithParser("ngram")),
			"CREATE FULLTEXT INDEX `f` ON `items` (`sku`) WITH PARSER `ngram`"},
		"spatial index": {mysql.CreateIndex(mysql.Index{Name: "g"}, items).Spatial().On(mysql.IndexKey(sku)),
			"CREATE SPATIAL INDEX `g` ON `items` (`sku`)"},
		"drop index how": {mysql.DropIndex(mysql.Index{Name: "i"}, items).Algorithm(mysql.AlgorithmInplace).Lock(mysql.LockNone),
			"DROP INDEX `i` ON `items` ALGORITHM = INPLACE LOCK = NONE"},
		"inline key options": {mysql.CreateTable(items).Column(ddl.Column(id), ddl.Column(sku)).
			Clause(mysql.PrimaryKey(id).Using(mysql.BTree), mysql.TableIndex(mysql.Index{Name: "x"}, sku).With(mysql.IndexComment("c"))),
			"CREATE TABLE `items` (`id` BIGINT NOT NULL, `sku` VARCHAR(255) NOT NULL, PRIMARY KEY USING BTREE (`id`), INDEX `x` (`sku`) COMMENT 'c')"},
		"spatial inline": {mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.TableSpatial(mysql.Index{Name: "g"}, id)),
			"CREATE TABLE `items` (`id` BIGINT NOT NULL, SPATIAL INDEX `g` (`id`))"},
		"trigger order": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).
			Follows(mysql.Trigger{Name: "t0"}).Do(mysql.StepRaw("SET @x = 1")),
			"CREATE TRIGGER `t` BEFORE INSERT ON `items` FOR EACH ROW FOLLOWS `t0` SET @x = 1"},
		"trigger precedes": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).
			Precedes(mysql.Trigger{Name: "t0"}).Do(mysql.StepRaw("SET @x = 1")),
			"CREATE TRIGGER `t` BEFORE INSERT ON `items` FOR EACH ROW PRECEDES `t0` SET @x = 1"},
		"view head": {mysql.CreateView(mysql.View{Name: "v"}).OrReplace().Algorithm(mysql.ViewMerge).
			Security(mysql.ViewInvoker).Columns("n").As(mysql.Select(id).From(items)),
			"CREATE OR REPLACE ALGORITHM = MERGE SQL SECURITY INVOKER VIEW `v` (`n`) AS SELECT `i`.`id` FROM `items` AS `i`"},
		"alter view head": {mysql.AlterView(mysql.View{Name: "v"}).Algorithm(mysql.ViewTemptable).
			Security(mysql.ViewDefiner).Columns("n").As(mysql.Select(id).From(items)),
			"ALTER ALGORITHM = TEMPTABLE SQL SECURITY DEFINER VIEW `v` (`n`) AS SELECT `i`.`id` FROM `items` AS `i`"},
		"plain view still renders": {mysql.CreateView(mysql.View{Name: "v"}).As(mysql.Select(id).From(items)),
			"CREATE VIEW `v` AS SELECT `i`.`id` FROM `items` AS `i`"},
		"atomic swap": {mysql.RenameTable(items, arch).And(arch, items),
			"RENAME TABLE `items` TO `arch`, `arch` TO `items`"},
		"column visibility": {mysql.AlterTable(items).Clause(mysql.SetColumnInvisible(sku), mysql.DropPrimaryKey(),
			mysql.DropConstraint(mysql.TableConstraint{Table: items, Name: "c"})),
			"ALTER TABLE `items` ALTER COLUMN `sku` SET INVISIBLE, DROP PRIMARY KEY, DROP CONSTRAINT `c`"},
		"column visible again": {mysql.AlterTable(items).Clause(mysql.SetColumnVisible(sku)),
			"ALTER TABLE `items` ALTER COLUMN `sku` SET VISIBLE"},
		"change and place": {mysql.AlterTable(items).Clause(mysql.ChangeColumn(sku, ddl.Column(hits)).At(mysql.First()),
			mysql.AddColumn(ddl.Column(hits)).At(mysql.After(id))),
			"ALTER TABLE `items` CHANGE COLUMN `sku` `hits` BIGINT NOT NULL FIRST, ADD COLUMN `hits` BIGINT NOT NULL AFTER `id`"},
		"modify at": {mysql.AlterTable(items).Clause(mysql.ModifyColumnAt(ddl.Column(hits), mysql.First())),
			"ALTER TABLE `items` MODIFY COLUMN `hits` BIGINT NOT NULL FIRST"},
		"invisible column": {mysql.CreateTable(items).Column(ddl.Column(id), ddl.Column(sku)).Clause(mysql.ColumnInvisible(sku)),
			"CREATE TABLE `items` (`id` BIGINT NOT NULL, `sku` VARCHAR(255) NOT NULL INVISIBLE)"},
		"visible column": {mysql.CreateTable(items).Column(ddl.Column(id), ddl.Column(sku)).Clause(mysql.ColumnVisible(sku)),
			"CREATE TABLE `items` (`id` BIGINT NOT NULL, `sku` VARCHAR(255) NOT NULL VISIBLE)"},
		"unenforced check": {mysql.AlterTable(items).Clause(mysql.AddConstraint(
			mysql.Check(mysql.TableConstraint{Table: items, Name: "c"}, sqlb.Greater(hits, int64(0))).NotEnforced())),
			"ALTER TABLE `items` ADD CONSTRAINT `c` CHECK (`hits` > 0) NOT ENFORCED"},
		"relative event": {mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAfter(1, mysql.UnitHour)).Do(mysql.StepRaw("SET @x = 1")),
			"CREATE EVENT `e` ON SCHEDULE AT CURRENT_TIMESTAMP + INTERVAL 1 HOUR DO SET @x = 1"},
		"routine comment": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Comment("why").Body("BEGIN END"),
			"CREATE PROCEDURE `p` () COMMENT 'why' BEGIN END"},
		"alter routine comment": {mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Comment("why2"),
			"ALTER PROCEDURE `p` COMMENT 'why2'"},
		"alter routine both": {mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.NoSQL).Comment("why2"),
			"ALTER PROCEDURE `p` NO SQL COMMENT 'why2'"},
	} {
		if got := scriptOf(t, tc.b); got != tc.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, got, tc.want)
		}
	}
}

// TestForms_ClosedSetsAreRefused pins the refusals the new forms carry,
// including the one the Placement exemption in zerovalue_test.go claims: a
// placement After something that is not a column.
func TestForms_ClosedSetsAreRefused(t *testing.T) {
	con := mysql.TableConstraint{Table: items, Name: "c"}
	for name, tc := range map[string]struct {
		b    interface{ Build() (ddl.Script, error) }
		want error
	}{
		"after a non-column": {mysql.AlterTable(items).Clause(mysql.AddColumn(ddl.Column(id)).At(mysql.After(sqlb.Param(int64(1))))),
			ddl.ErrNotAColumn},
		"change from a non-column": {mysql.AlterTable(items).Clause(mysql.ChangeColumn(sqlb.Param(int64(1)), ddl.Column(id))),
			ddl.ErrNotAColumn},
		"like nothing":       {mysql.CreateTableLike(arch, sqlb.Table{}), mysql.ErrEmptyName},
		"like into nothing":  {mysql.CreateTableLike(sqlb.Table{}, items), mysql.ErrEmptyName},
		"as nothing":         {mysql.CreateTableAs(arch, nil), ddl.ErrZeroValue},
		"conflict":           {mysql.CreateTableAs(arch, mysql.Select(id).From(items)).OnConflict(mysql.RowConflict(9)), mysql.ErrUnknownKeyword},
		"empty table option": {mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.RawTableOption(" ")), ddl.ErrZeroValue},
		"key block size":     {mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).With(mysql.IndexKeyBlockSize(0)), mysql.ErrUnknownKeyword},
		"parser name":        {mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).With(mysql.IndexWithParser("")), mysql.ErrEmptyName},
		// INSTANT is an ALTER TABLE algorithm: MySQL has no instant index
		// build, so accepting it here would render a statement always refused.
		"instant index": {mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).Algorithm(mysql.AlgorithmInstant), mysql.ErrUnknownKeyword},
		"index algorithm": {mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).Algorithm(mysql.AlterAlgorithm("FAST")),
			mysql.ErrUnknownKeyword},
		"index lock": {mysql.DropIndex(mysql.Index{Name: "i"}, items).Lock(mysql.AlterLock("SOME")), mysql.ErrUnknownKeyword},
		"view algorithm": {mysql.CreateView(mysql.View{Name: "v"}).Algorithm(mysql.ViewAlgorithmKind("FAST")).As(mysql.Select(id).From(items)),
			mysql.ErrUnknownKeyword},
		"view security": {mysql.AlterView(mysql.View{Name: "v"}).Security(mysql.ViewSecurity(9)).As(mysql.Select(id).From(items)),
			mysql.ErrUnknownKeyword},
		"view column name": {mysql.CreateView(mysql.View{Name: "v"}).Columns("").As(mysql.Select(id).From(items)), mysql.ErrEmptyName},
		"trigger order name": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).
			Follows(mysql.Trigger{}).Do(mysql.StepRaw("SET @x = 1")), mysql.ErrEmptyName},
		"rename to nothing":           {mysql.RenameTable(items, arch).And(arch, sqlb.Table{}), mysql.ErrEmptyName},
		"rename from nothing":         {mysql.RenameTable(items, arch).And(sqlb.Table{}, items), mysql.ErrEmptyName},
		"drop temporary none":         {mysql.DropTemporaryTable(sqlb.Table{}), mysql.ErrEmptyName},
		"relative unit":               {mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAfter(1, mysql.IntervalUnit("FORTNIGHT"))).Do(mysql.StepRaw("SET @x = 1")), mysql.ErrBadSchedule},
		"relative count":              {mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAfter(0, mysql.UnitDay)).Do(mysql.StepRaw("SET @x = 1")), mysql.ErrBadSchedule},
		"drop constraint zero":        {mysql.AlterTable(items).Clause(mysql.DropConstraint(mysql.TableConstraint{Table: items})), ddl.ErrZeroValue},
		"constraint of another table": {mysql.AlterTable(arch).Clause(mysql.DropConstraint(con)), ddl.ErrForeignColumn},
	} {
		if _, err := tc.b.Build(); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

// TestForms_BuildersDoNotWriteToTheirReceiver walks the new multi-stage
// builders and the new methods on the existing ones.
func TestForms_BuildersDoNotWriteToTheirReceiver(t *testing.T) {
	like := mysql.CreateTableLike(arch, items)
	likeBefore := scriptOf(t, like)
	for name, m := range map[string]func(*mysql.CreateTableLikeStatement) *mysql.CreateTableLikeStatement{
		"IfNotExists": (*mysql.CreateTableLikeStatement).IfNotExists,
		"Temporary":   (*mysql.CreateTableLikeStatement).Temporary,
	} {
		if d := m(like); scriptOf(t, like) != likeBefore || scriptOf(t, d) == likeBefore {
			t.Errorf("CreateTableLikeStatement.%s wrote to its receiver or changed nothing", name)
		}
	}
	as := mysql.CreateTableAs(arch, mysql.Select(id).From(items))
	asBefore := scriptOf(t, as)
	for name, m := range map[string]func(*mysql.CreateTableAsStatement) *mysql.CreateTableAsStatement{
		"IfNotExists": (*mysql.CreateTableAsStatement).IfNotExists,
		"Temporary":   (*mysql.CreateTableAsStatement).Temporary,
		"OnConflict": func(x *mysql.CreateTableAsStatement) *mysql.CreateTableAsStatement {
			return x.OnConflict(mysql.ConflictIgnore)
		},
	} {
		if d := m(as); scriptOf(t, as) != asBefore || scriptOf(t, d) == asBefore {
			t.Errorf("CreateTableAsStatement.%s wrote to its receiver or changed nothing", name)
		}
	}
	dt := mysql.DropTemporaryTable(arch)
	dtBefore := scriptOf(t, dt)
	if d := dt.IfExists(); scriptOf(t, dt) != dtBefore || scriptOf(t, d) == dtBefore {
		t.Error("DropTemporaryTableStatement.IfExists wrote to its receiver or changed nothing")
	}
	ready := mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku))
	readyBefore := scriptOf(t, ready)
	for name, m := range map[string]func(*mysql.CreateIndexReady) *mysql.CreateIndexReady{
		"With":      func(x *mysql.CreateIndexReady) *mysql.CreateIndexReady { return x.With(mysql.IndexComment("c")) },
		"Algorithm": func(x *mysql.CreateIndexReady) *mysql.CreateIndexReady { return x.Algorithm(mysql.AlgorithmInplace) },
		"Lock":      func(x *mysql.CreateIndexReady) *mysql.CreateIndexReady { return x.Lock(mysql.LockNone) },
	} {
		if d := m(ready); scriptOf(t, ready) != readyBefore || scriptOf(t, d) == readyBefore {
			t.Errorf("CreateIndexReady.%s wrote to its receiver or changed nothing", name)
		}
	}
	di := mysql.DropIndex(mysql.Index{Name: "i"}, items)
	diBefore := scriptOf(t, di)
	for name, m := range map[string]func(*mysql.DropIndexStatement) *mysql.DropIndexStatement{
		"Algorithm": func(x *mysql.DropIndexStatement) *mysql.DropIndexStatement {
			return x.Algorithm(mysql.AlgorithmInplace)
		},
		"Lock": func(x *mysql.DropIndexStatement) *mysql.DropIndexStatement { return x.Lock(mysql.LockNone) },
	} {
		if d := m(di); scriptOf(t, di) != diBefore || scriptOf(t, d) == diBefore {
			t.Errorf("DropIndexStatement.%s wrote to its receiver or changed nothing", name)
		}
	}
	rt := mysql.RenameTable(items, arch)
	rtBefore := scriptOf(t, rt)
	if d := rt.And(arch, items); scriptOf(t, rt) != rtBefore || scriptOf(t, d) == rtBefore {
		t.Error("RenameTableStatement.And wrote to its receiver or changed nothing")
	}
	// The value clauses derive too: each method returns a copy, so a key or a
	// check built once and configured twice gives two clauses.
	key := mysql.TableIndex(mysql.Index{Name: "x"}, sku)
	keyOf := func(k mysql.KeyClause) string {
		return scriptOf(t, mysql.CreateTable(items).Column(ddl.Column(id), ddl.Column(sku)).Clause(k))
	}
	keyBefore := keyOf(key)
	for name, m := range map[string]func(mysql.KeyClause) mysql.KeyClause{
		"Using": func(k mysql.KeyClause) mysql.KeyClause { return k.Using(mysql.BTree) },
		"With":  func(k mysql.KeyClause) mysql.KeyClause { return k.With(mysql.IndexComment("c")) },
	} {
		if d := m(key); keyOf(key) != keyBefore || keyOf(d) == keyBefore {
			t.Errorf("KeyClause.%s wrote to its receiver or changed nothing", name)
		}
	}
	chk := mysql.Check(mysql.TableConstraint{Table: items, Name: "c"}, sqlb.Greater(hits, int64(0)))
	chkOf := func(c mysql.CheckClause) string {
		return scriptOf(t, mysql.AlterTable(items).Clause(mysql.AddConstraint(c)))
	}
	chkBefore := chkOf(chk)
	if d := chk.NotEnforced(); chkOf(chk) != chkBefore || chkOf(d) == chkBefore {
		t.Error("CheckClause.NotEnforced wrote to its receiver or changed nothing")
	}
	ca := mysql.AddColumn(ddl.Column(hits))
	caOf := func(c mysql.ColumnAction) string { return scriptOf(t, mysql.AlterTable(items).Clause(c)) }
	caBefore := caOf(ca)
	if d := ca.At(mysql.First()); caOf(ca) != caBefore || caOf(d) == caBefore {
		t.Error("ColumnAction.At wrote to its receiver or changed nothing")
	}
	vq := mysql.CreateView(mysql.View{Name: "v"})
	vqOf := func(q *mysql.CreateViewQuery) string { return scriptOf(t, q.As(mysql.Select(id).From(items))) }
	vqBefore := vqOf(vq)
	for name, m := range map[string]func(*mysql.CreateViewQuery) *mysql.CreateViewQuery{
		"Algorithm": func(q *mysql.CreateViewQuery) *mysql.CreateViewQuery { return q.Algorithm(mysql.ViewMerge) },
		"Security":  func(q *mysql.CreateViewQuery) *mysql.CreateViewQuery { return q.Security(mysql.ViewInvoker) },
		"Columns":   func(q *mysql.CreateViewQuery) *mysql.CreateViewQuery { return q.Columns("n") },
	} {
		if d := m(vq); vqOf(vq) != vqBefore || vqOf(d) == vqBefore {
			t.Errorf("CreateViewQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	avq := mysql.AlterView(mysql.View{Name: "v"})
	avqOf := func(q *mysql.AlterViewQuery) string { return scriptOf(t, q.As(mysql.Select(id).From(items))) }
	avqBefore := avqOf(avq)
	for name, m := range map[string]func(*mysql.AlterViewQuery) *mysql.AlterViewQuery{
		"Algorithm": func(q *mysql.AlterViewQuery) *mysql.AlterViewQuery { return q.Algorithm(mysql.ViewMerge) },
		"Security":  func(q *mysql.AlterViewQuery) *mysql.AlterViewQuery { return q.Security(mysql.ViewInvoker) },
		"Columns":   func(q *mysql.AlterViewQuery) *mysql.AlterViewQuery { return q.Columns("n") },
	} {
		if d := m(avq); avqOf(avq) != avqBefore || avqOf(d) == avqBefore {
			t.Errorf("AlterViewQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	tq := mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items)
	tqOf := func(q *mysql.TimedTriggerQuery) string { return scriptOf(t, q.Do(mysql.StepRaw("SET @x = 1"))) }
	tqBefore := tqOf(tq)
	for name, m := range map[string]func(*mysql.TimedTriggerQuery) *mysql.TimedTriggerQuery{
		"Follows": func(q *mysql.TimedTriggerQuery) *mysql.TimedTriggerQuery { return q.Follows(mysql.Trigger{Name: "t0"}) },
		"Precedes": func(q *mysql.TimedTriggerQuery) *mysql.TimedTriggerQuery {
			return q.Precedes(mysql.Trigger{Name: "t0"})
		},
	} {
		if d := m(tq); tqOf(tq) != tqBefore || tqOf(d) == tqBefore {
			t.Errorf("TimedTriggerQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	rq := mysql.CreateProcedure(mysql.Procedure{Name: "p"})
	rqOf := func(q *mysql.CreateRoutineQuery) string { return scriptOf(t, q.Body("BEGIN END")) }
	rqBefore := rqOf(rq)
	if d := rq.Comment("c"); rqOf(rq) != rqBefore || rqOf(d) == rqBefore {
		t.Error("CreateRoutineQuery.Comment wrote to its receiver or changed nothing")
	}
	ar := mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.NoSQL)
	arBefore := scriptOf(t, ar)
	if d := ar.Comment("c"); scriptOf(t, ar) != arBefore || scriptOf(t, d) == arBefore {
		t.Error("AlterRoutineStatement.Comment wrote to its receiver or changed nothing")
	}
	sch := mysql.EventAfter(1, mysql.UnitHour)
	if _, err := mysql.CreateEvent(mysql.Event{Name: "e"}).On(sch).Do(mysql.StepRaw("SET @x = 1")).Build(); err != nil {
		t.Fatal(err)
	}
}

// TestForms_SkipIfMethodsDerive covers the two methods the cross-engine gate
// in pg cannot walk. CreateIndexReady.IfNotExists and DropIndexStatement.
// IfExists change no SQL at all: MySQL has neither IF NOT EXISTS for CREATE
// INDEX nor IF EXISTS for DROP INDEX, so both write a catalogue query into
// ddl.Statement.SkipIf and leave the statement text alone.
//
// That gate compares Script.SQL() and would read the unchanged text as a
// method that did nothing, so neither can be listed there, and the receiver
// check for them lives here instead — comparing the field they actually
// write.
//
// So the omission from ddlBuilders is deliberate and is not a bug to fix. A
// method whose whole effect is on SkipIf belongs in THIS test; adding it to
// ddlBuilders makes the gate fail, and listing its type there with a bare nil
// to keep the gate quiet is worse still, because a nil entry passes
// vacuously and the type is then counted as covered while nothing walks it.
// Two of this package's entries were in exactly that state before 2026-09-24:
// mysql.CreateIndexReady and mysql.DropIndexStatement carried nil while each
// already had a deriving method.
func TestForms_SkipIfMethodsDerive(t *testing.T) {
	ix := mysql.Index{Name: "i"}
	create := mysql.CreateIndex(ix, items).On(mysql.IndexKey(sku))
	drop := mysql.DropIndex(ix, items)

	guardOf := func(b interface{ Build() (ddl.Script, error) }) (string, string) {
		t.Helper()
		s, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(s.SQL(), "; "), s[0].SkipIf
	}

	for name, tc := range map[string]struct {
		base    interface{ Build() (ddl.Script, error) }
		derived interface{ Build() (ddl.Script, error) }
	}{
		"CreateIndexReady.IfNotExists": {create, create.IfNotExists()},
		"DropIndexStatement.IfExists":  {drop, drop.IfExists()},
	} {
		baseSQL, baseGuard := guardOf(tc.base)
		if baseGuard != "" {
			t.Errorf("%s: the base already carries a guard %q", name, baseGuard)
		}
		derivedSQL, derivedGuard := guardOf(tc.derived)
		if derivedSQL != baseSQL {
			t.Errorf("%s: the text changed, so this belongs in ddlBuilders after all:\n%s\n%s", name, baseSQL, derivedSQL)
		}
		if derivedGuard == "" {
			t.Errorf("%s: the derived statement carries no guard, so the method did nothing", name)
		}
		if _, after := guardOf(tc.base); after != "" {
			t.Errorf("%s wrote to its receiver: the base now carries a guard %q", name, after)
		}
	}
}

// TestClosedStringSets_RefuseAHostileValue is the injection audit of
// 2026-09-24, prompted by sqlite.OrAction: a named string type with a fixed
// list of constants and no validation is not a closed set, it is a hole, and
// a caller writing IndexMethod("...") puts whatever follows straight into the
// statement text.
//
// This package declares eight such types. Each case below drives one of them
// at EVERY site that renders it — IndexMethod has two, which is the trap, and
// the fix that only covered one would pass a test that only drove one — with
// a value that closes the statement and appends another. Each must fail the
// build.
//
// The second half matters as much: a valid() that refuses everything passes
// the first half and breaks the package, so every real constant is built
// here too and must succeed. Verified by making each valid() return true
// unconditionally and watching the refusals fail.
func TestClosedStringSets_RefuseAHostileValue(t *testing.T) {
	const hostile = "BTREE; DROP TABLE items; -- "
	con := mysql.TableConstraint{Table: items, Name: "c"}

	refused := map[string]interface{ Build() (ddl.Script, error) }{
		// IndexMethod, site 1 of 2: the CREATE INDEX statement.
		"IndexMethod/CreateIndex": mysql.CreateIndex(mysql.Index{Name: "i"}, items).
			Using(mysql.IndexMethod(hostile)).On(mysql.IndexKey(sku)),
		// IndexMethod, site 2 of 2: an inline key in CREATE TABLE. This is
		// the one a fix applied to CreateIndex alone would have left open.
		"IndexMethod/InlineKey": mysql.CreateTable(items).Column(ddl.Column(id)).
			Clause(mysql.PrimaryKey(id).Using(mysql.IndexMethod(hostile))),
		"IndexMethod/InlineUnique": mysql.CreateTable(items).Column(ddl.Column(id)).
			Clause(mysql.Unique(con, id).Using(mysql.IndexMethod(hostile))),
		"TriggerEvent": mysql.CreateTrigger(mysql.Trigger{Name: "t"}).
			After(mysql.TriggerEvent(hostile), items).Do(mysql.StepRaw("SET @x = 1")),
		"ViewAlgorithmKind/Create": mysql.CreateView(mysql.View{Name: "v"}).
			Algorithm(mysql.ViewAlgorithmKind(hostile)).As(mysql.Select(id).From(items)),
		"ViewAlgorithmKind/Alter": mysql.AlterView(mysql.View{Name: "v"}).
			Algorithm(mysql.ViewAlgorithmKind(hostile)).As(mysql.Select(id).From(items)),
		"Engine/CreateTable": mysql.CreateTable(items).Column(ddl.Column(id)).
			Clause(mysql.TableEngine(mysql.Engine(hostile))),
		"Engine/AlterTable": mysql.AlterTable(items).Clause(mysql.SetEngine(mysql.Engine(hostile))),
		"RowFormatKind/CreateTable": mysql.CreateTable(items).Column(ddl.Column(id)).
			Clause(mysql.RowFormat(mysql.RowFormatKind(hostile))),
		"RowFormatKind/AlterTable":  mysql.AlterTable(items).Clause(mysql.SetRowFormat(mysql.RowFormatKind(hostile))),
		"AlterAlgorithm/AlterTable": mysql.AlterTable(items).Clause(mysql.Algorithm(mysql.AlterAlgorithm(hostile))),
		"AlterAlgorithm/CreateIndex": mysql.CreateIndex(mysql.Index{Name: "i"}, items).
			On(mysql.IndexKey(sku)).Algorithm(mysql.AlterAlgorithm(hostile)),
		"AlterAlgorithm/DropIndex": mysql.DropIndex(mysql.Index{Name: "i"}, items).
			Algorithm(mysql.AlterAlgorithm(hostile)),
		"AlterLock/AlterTable": mysql.AlterTable(items).Clause(mysql.Lock(mysql.AlterLock(hostile))),
		"AlterLock/CreateIndex": mysql.CreateIndex(mysql.Index{Name: "i"}, items).
			On(mysql.IndexKey(sku)).Lock(mysql.AlterLock(hostile)),
		"AlterLock/DropIndex": mysql.DropIndex(mysql.Index{Name: "i"}, items).Lock(mysql.AlterLock(hostile)),
		"IntervalUnit/Every": mysql.CreateEvent(mysql.Event{Name: "e"}).
			On(mysql.EventEvery(1, mysql.IntervalUnit(hostile))).Do(mysql.StepRaw("SET @x = 1")),
		"IntervalUnit/After": mysql.CreateEvent(mysql.Event{Name: "e"}).
			On(mysql.EventAfter(1, mysql.IntervalUnit(hostile))).Do(mysql.StepRaw("SET @x = 1")),
	}
	for name, b := range refused {
		s, err := b.Build()
		if err == nil {
			t.Errorf("%s: a hostile value built, rendering %q", name, strings.Join(s.SQL(), "; "))
			continue
		}
		if !errors.Is(err, mysql.ErrUnknownKeyword) && !errors.Is(err, mysql.ErrBadSchedule) {
			t.Errorf("%s: refused with %v, which is neither ErrUnknownKeyword nor ErrBadSchedule", name, err)
		}
	}

	// The other half: every real constant of every one of the eight types
	// still builds. A valid() that refuses everything would pass the loop
	// above and be useless.
	accepted := map[string]interface{ Build() (ddl.Script, error) }{}
	for _, m := range []mysql.IndexMethod{mysql.BTree, mysql.Hash} {
		accepted["IndexMethod/CreateIndex/"+string(m)] = mysql.CreateIndex(mysql.Index{Name: "i"}, items).Using(m).On(mysql.IndexKey(sku))
		accepted["IndexMethod/InlineKey/"+string(m)] = mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.PrimaryKey(id).Using(m))
		accepted["IndexMethod/InlineUnique/"+string(m)] = mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.Unique(con, id).Using(m))
	}
	for _, e := range []mysql.TriggerEvent{mysql.OnInsert, mysql.OnUpdate, mysql.OnDelete} {
		accepted["TriggerEvent/"+string(e)] = mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(e, items).Do(mysql.StepRaw("SET @x = 1"))
	}
	for _, v := range []mysql.ViewAlgorithmKind{mysql.ViewMerge, mysql.ViewTemptable, mysql.ViewUndefined} {
		accepted["ViewAlgorithmKind/Create/"+string(v)] = mysql.CreateView(mysql.View{Name: "v"}).Algorithm(v).As(mysql.Select(id).From(items))
		accepted["ViewAlgorithmKind/Alter/"+string(v)] = mysql.AlterView(mysql.View{Name: "v"}).Algorithm(v).As(mysql.Select(id).From(items))
	}
	for _, e := range []mysql.Engine{mysql.EngineInnoDB, mysql.EngineMemory, mysql.EngineMyISAM} {
		accepted["Engine/CreateTable/"+string(e)] = mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.TableEngine(e))
		accepted["Engine/AlterTable/"+string(e)] = mysql.AlterTable(items).Clause(mysql.SetEngine(e))
	}
	for _, f := range []mysql.RowFormatKind{mysql.RowFormatDynamic, mysql.RowFormatCompressed, mysql.RowFormatCompact, mysql.RowFormatRedundant, mysql.RowFormatDefault} {
		accepted["RowFormatKind/CreateTable/"+string(f)] = mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.RowFormat(f))
		accepted["RowFormatKind/AlterTable/"+string(f)] = mysql.AlterTable(items).Clause(mysql.SetRowFormat(f))
	}
	for _, x := range []mysql.AlterAlgorithm{mysql.AlgorithmInstant, mysql.AlgorithmInplace, mysql.AlgorithmCopy, mysql.AlgorithmDefault} {
		accepted["AlterAlgorithm/AlterTable/"+string(x)] = mysql.AlterTable(items).Clause(mysql.Algorithm(x))
		// INSTANT is refused for an index on purpose: MySQL has no instant
		// index build, so it is not part of this type's set there.
		if x != mysql.AlgorithmInstant {
			accepted["AlterAlgorithm/CreateIndex/"+string(x)] = mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).Algorithm(x)
			accepted["AlterAlgorithm/DropIndex/"+string(x)] = mysql.DropIndex(mysql.Index{Name: "i"}, items).Algorithm(x)
		}
	}
	for _, l := range []mysql.AlterLock{mysql.LockNone, mysql.LockShared, mysql.LockExclusive, mysql.LockDefault} {
		accepted["AlterLock/AlterTable/"+string(l)] = mysql.AlterTable(items).Clause(mysql.Lock(l))
		accepted["AlterLock/CreateIndex/"+string(l)] = mysql.CreateIndex(mysql.Index{Name: "i"}, items).On(mysql.IndexKey(sku)).Lock(l)
		accepted["AlterLock/DropIndex/"+string(l)] = mysql.DropIndex(mysql.Index{Name: "i"}, items).Lock(l)
	}
	for _, u := range []mysql.IntervalUnit{mysql.UnitSecond, mysql.UnitMinute, mysql.UnitHour, mysql.UnitDay, mysql.UnitWeek, mysql.UnitMonth, mysql.UnitQuarter, mysql.UnitYear} {
		accepted["IntervalUnit/Every/"+string(u)] = mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventEvery(1, u)).Do(mysql.StepRaw("SET @x = 1"))
		accepted["IntervalUnit/After/"+string(u)] = mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAfter(1, u)).Do(mysql.StepRaw("SET @x = 1"))
	}
	if len(accepted) < 60 {
		t.Fatalf("the accepting half drives only %d cases; it is meant to cover every constant of all eight types", len(accepted))
	}
	for name, b := range accepted {
		if _, err := b.Build(); err != nil {
			t.Errorf("%s: a real constant was refused: %v", name, err)
		}
	}
}

// TestStepBlock_SeparatorRuleIsTheServers pins the exact text StepBlock
// writes, because the separator rule is the whole construct and it is not
// symmetric: none after the opener, one after every inner statement including
// the last before the closer. Both halves are MySQL 8.4's, measured through
// the driver — "IF c THEN; ..." and "... SET x = 1 END IF" are each
// ER_PARSE_ERROR — and contrib/driver-mysql executes what these render.
func TestStepBlock_SeparatorRuleIsTheServers(t *testing.T) {
	trig := func(step mysql.TriggerStep, more ...mysql.TriggerStep) interface {
		Build() (ddl.Script, error)
	} {
		return mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).Do(step, more...)
	}
	head := "CREATE TRIGGER `t` BEFORE INSERT ON `items` FOR EACH ROW "
	for name, tc := range map[string]struct {
		b    interface{ Build() (ddl.Script, error) }
		want string
	}{
		// One inner statement: a separator before the closer and none after
		// the opener. Written as three StepRaw values this rendered
		// "IF ... THEN; SIGNAL ...; END IF", which is the bug.
		"one inner": {trig(mysql.StepBlock("IF NEW.n < 0 THEN", "END IF",
			mysql.StepSignal(mysql.UserSQLState, "negative"))),
			head + "IF NEW.n < 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'negative'; END IF"},
		// Several inner statements: each terminated, including the last.
		"two inner": {trig(mysql.StepBlock("IF NEW.n > 10 THEN", "END IF",
			mysql.StepSetNew(sku, sqlb.Param("big")),
			mysql.StepSignal(mysql.UserSQLState, "too big"))),
			head + "IF NEW.n > 10 THEN SET NEW.`sku` = 'big'; SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'too big'; END IF"},
		// A block beside another step: the body wraps in BEGIN ... END and
		// the block is terminated like any other statement, which MySQL
		// accepts.
		"block beside a step": {trig(
			mysql.StepBlock("IF NEW.n > 10 THEN", "END IF", mysql.StepRaw("SET @x = 1")),
			mysql.StepSetNew(sku, sqlb.Param("done"))),
			head + "BEGIN IF NEW.n > 10 THEN SET @x = 1; END IF; SET NEW.`sku` = 'done'; END"},
		// Nested, because the inner steps are ordinary steps and one of them
		// may be another block.
		"nested": {trig(mysql.StepBlock("IF NEW.n > 0 THEN", "END IF",
			mysql.StepBlock("IF NEW.n > 10 THEN", "END IF", mysql.StepRaw("SET @x = 1")))),
			head + "IF NEW.n > 0 THEN IF NEW.n > 10 THEN SET @x = 1; END IF; END IF"},
	} {
		if got := scriptOf(t, tc.b); got != tc.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, got, tc.want)
		}
	}

	// The zero value and every incomplete shape are refused rather than
	// rendering a block with nothing in it.
	for name, b := range map[string]interface{ Build() (ddl.Script, error) }{
		"no opener":       trig(mysql.StepBlock("  ", "END IF", mysql.StepRaw("SET @x = 1"))),
		"no closer":       trig(mysql.StepBlock("IF 1 THEN", "", mysql.StepRaw("SET @x = 1"))),
		"no inner":        trig(mysql.StepBlock("IF 1 THEN", "END IF")),
		"zero inner step": trig(mysql.StepBlock("IF 1 THEN", "END IF", mysql.TriggerStep{})),
	} {
		if _, err := b.Build(); !errors.Is(err, ddl.ErrZeroValue) {
			t.Errorf("%s: %v, want ErrZeroValue", name, err)
		}
	}

	// Value semantics: the inner steps are cloned, so a caller reusing the
	// slice it passed cannot change a block already built.
	inner := []mysql.TriggerStep{mysql.StepRaw("SET @x = 1")}
	block := mysql.StepBlock("IF 1 THEN", "END IF", inner...)
	before := scriptOf(t, trig(block))
	inner[0] = mysql.StepRaw("SET @x = 2")
	if after := scriptOf(t, trig(block)); after != before {
		t.Errorf("StepBlock kept the caller's slice:\nbefore %s\nafter  %s", before, after)
	}
}
