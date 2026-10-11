package mysql_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/schema"
)

// The statements and actions that closed the MySQL DDL detector's last two
// partial entries and its ten left-out ones on 2026-09-24: the ALTER TABLE
// table options and its remaining actions, the partition maintenance
// actions, the procedural trigger steps, ALTER VIEW, the EVENT and the stored
// routines. Each is pinned here as text and executed in
// contrib/driver-mysql, because a rendering test proves the text and only
// execution proves the SQL.

// scriptOf builds b and joins its statements, failing the test on an error.
func scriptOf(t *testing.T, b interface{ Build() (ddl.Script, error) }) string {
	t.Helper()
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(s.SQL(), "; ")
}

var eventAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func aStep() mysql.TriggerStep { return mysql.StepDelete(mysql.Delete(items).All()) }

func bigint() ddl.SQLType[int64] { return ddl.RawType[int64]("BIGINT") }

// TestGaps_Render pins the text of every construct built to close the
// detector.
func TestGaps_Render(t *testing.T) {
	for name, tc := range map[string]struct {
		b    interface{ Build() (ddl.Script, error) }
		want string
	}{
		"table options": {mysql.AlterTable(items).Clause(mysql.SetEngine(mysql.EngineInnoDB), mysql.SetRowFormat(mysql.RowFormatDynamic),
			mysql.SetTableComment("x"), mysql.SetAutoIncrement(100), mysql.SetDefaultCharset("utf8mb4"), mysql.SetTableCollate("utf8mb4_bin")),
			"ALTER TABLE `items` ENGINE = InnoDB, ROW_FORMAT = DYNAMIC, COMMENT = 'x', AUTO_INCREMENT = 100, DEFAULT CHARACTER SET = `utf8mb4`, COLLATE = `utf8mb4_bin`"},
		"convert and how": {mysql.AlterTable(items).Clause(mysql.ConvertToCharset("utf8mb4"), mysql.Algorithm(mysql.AlgorithmCopy), mysql.Lock(mysql.LockShared)),
			"ALTER TABLE `items` CONVERT TO CHARACTER SET `utf8mb4`, ALGORITHM = COPY, LOCK = SHARED"},
		"index visibility": {mysql.AlterTable(items).Clause(mysql.SetIndexInvisible(mysql.Index{Name: "i"}), mysql.ForceRebuild(), mysql.EnableKeys()),
			"ALTER TABLE `items` ALTER INDEX `i` INVISIBLE, FORCE, ENABLE KEYS"},
		"visible again": {mysql.AlterTable(items).Clause(mysql.SetIndexVisible(mysql.Index{Name: "i"}), mysql.DisableKeys(), mysql.WithValidation()),
			"ALTER TABLE `items` ALTER INDEX `i` VISIBLE, DISABLE KEYS, WITH VALIDATION"},
		"tablespace": {mysql.AlterTable(items).Clause(mysql.DiscardTablespace()), "ALTER TABLE `items` DISCARD TABLESPACE"},
		"import":     {mysql.AlterTable(items).Clause(mysql.ImportTablespace()), "ALTER TABLE `items` IMPORT TABLESPACE"},
		"no validation": {mysql.AlterTable(items).Clause(mysql.WithoutValidation()),
			"ALTER TABLE `items` WITHOUT VALIDATION"},
		"check enforcement": {mysql.AlterTable(items).Clause(mysql.SetCheckNotEnforced(mysql.TableConstraint{Table: items, Name: "c"})),
			"ALTER TABLE `items` ALTER CHECK `c` NOT ENFORCED"},
		"check enforced": {mysql.AlterTable(items).Clause(mysql.SetCheckEnforced(mysql.TableConstraint{Table: items, Name: "c"})),
			"ALTER TABLE `items` ALTER CHECK `c` ENFORCED"},
		"exchange": {mysql.AlterTable(items).Clause(mysql.ExchangePartition("p0", sqlb.Table{Name: "arch"}).WithoutValidation()),
			"ALTER TABLE `items` EXCHANGE PARTITION `p0` WITH TABLE `arch` WITHOUT VALIDATION"},
		"exchange validated": {mysql.AlterTable(items).Clause(mysql.ExchangePartition("p0", sqlb.Table{Name: "arch"})),
			"ALTER TABLE `items` EXCHANGE PARTITION `p0` WITH TABLE `arch`"},
		"maintenance all": {mysql.AlterTable(items).Clause(mysql.CheckPartitions(mysql.AllPartitions)),
			"ALTER TABLE `items` CHECK PARTITION ALL"},
		"rebuild named": {mysql.AlterTable(items).Clause(mysql.RebuildPartitions("p0", "p1")),
			"ALTER TABLE `items` REBUILD PARTITION `p0`, `p1`"},
		"analyze":  {mysql.AlterTable(items).Clause(mysql.AnalyzePartitions("p0")), "ALTER TABLE `items` ANALYZE PARTITION `p0`"},
		"optimize": {mysql.AlterTable(items).Clause(mysql.OptimizePartitions("p0")), "ALTER TABLE `items` OPTIMIZE PARTITION `p0`"},
		"repair":   {mysql.AlterTable(items).Clause(mysql.RepairPartitions("p0")), "ALTER TABLE `items` REPAIR PARTITION `p0`"},
		"raw":      {mysql.AlterTable(items).Clause(mysql.RawAlter("KEY_BLOCK_SIZE = 8")), "ALTER TABLE `items` KEY_BLOCK_SIZE = 8"},
		"alter view": {mysql.AlterView(mysql.View{Name: "v"}).As(mysql.Select(id).From(items)).WithCheckOption(mysql.Local),
			"ALTER VIEW `v` AS SELECT `i`.`id` FROM `items` AS `i` WITH LOCAL CHECK OPTION"},
		"procedural trigger": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).
			Do(mysql.StepSetNew(sku, sqlb.Lower(mysql.NewRow(sku))), mysql.StepSignal(mysql.UserSQLState, "no"), mysql.StepRaw("IF 1 THEN SET @x = 1; END IF")),
			"CREATE TRIGGER `t` BEFORE INSERT ON `items` FOR EACH ROW BEGIN SET NEW.`sku` = lower(NEW.`sku`); SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'no'; IF 1 THEN SET @x = 1; END IF; END"},
		"event": {mysql.CreateEvent(mysql.Event{Name: "e"}).IfNotExists().On(mysql.EventEvery(1, mysql.UnitDay).Starts(eventAt).Ends(eventAt)).
			OnCompletion(mysql.Preserve).Status(mysql.EventDisable).Comment("c").Do(aStep()),
			"CREATE EVENT IF NOT EXISTS `e` ON SCHEDULE EVERY 1 DAY STARTS '2026-01-02 03:04:05' ENDS '2026-01-02 03:04:05' ON COMPLETION PRESERVE DISABLE COMMENT 'c' DO DELETE FROM `items` AS `i`"},
		"one-shot event": {mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAt(eventAt)).OnCompletion(mysql.NotPreserve).Do(aStep()),
			"CREATE EVENT `e` ON SCHEDULE AT '2026-01-02 03:04:05' ON COMPLETION NOT PRESERVE DO DELETE FROM `items` AS `i`"},
		"alter event": {mysql.AlterEvent(mysql.Event{Name: "e"}).Status(mysql.EventEnable).Rename(mysql.Event{Name: "e2"}).Comment("d"),
			"ALTER EVENT `e` RENAME TO `e2` ENABLE COMMENT 'd'"},
		"alter event schedule": {mysql.AlterEvent(mysql.Event{Name: "e"}).On(mysql.EventEvery(2, mysql.UnitHour)).Do(aStep()),
			"ALTER EVENT `e` ON SCHEDULE EVERY 2 HOUR DO DELETE FROM `items` AS `i`"},
		"replica event": {mysql.AlterEvent(mysql.Event{Name: "e"}).Status(mysql.EventDisableOnReplica),
			"ALTER EVENT `e` DISABLE ON REPLICA"},
		"procedure": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).IfNotExists().
			Param(mysql.RoutineParam("n", bigint()), mysql.RoutineParam("o", bigint()).Mode(mysql.ModeOut)).
			Characteristic(mysql.ModifiesSQLData, mysql.SecurityInvoker).Body("BEGIN SET o = n + 1; END"),
			"CREATE PROCEDURE IF NOT EXISTS `p` (`n` BIGINT, OUT `o` BIGINT) MODIFIES SQL DATA SQL SECURITY INVOKER BEGIN SET o = n + 1; END"},
		"function": {mysql.CreateFunction(mysql.Function{Name: "f"}, bigint()).Param(mysql.RoutineParam("n", bigint())).
			Characteristic(mysql.Deterministic, mysql.NoSQL).Body("RETURN n + 1"),
			"CREATE FUNCTION `f` (`n` BIGINT) RETURNS BIGINT DETERMINISTIC NO SQL RETURN n + 1"},
		"alter routine": {mysql.AlterFunction(mysql.Function{Name: "f"}).Characteristic(mysql.SecurityInvoker).Characteristic(mysql.LanguageSQL),
			"ALTER FUNCTION `f` SQL SECURITY INVOKER LANGUAGE SQL"},
		"alter procedure": {mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.ReadsSQLData),
			"ALTER PROCEDURE `p` READS SQL DATA"},
		"drop routines":  {mysql.DropFunction(mysql.Function{Name: "f"}).IfExists(), "DROP FUNCTION IF EXISTS `f`"},
		"drop procedure": {mysql.DropProcedure(mysql.Procedure{Name: "p"}).IfExists(), "DROP PROCEDURE IF EXISTS `p`"},
		"drop event":     {mysql.DropEvent(mysql.Event{Name: "e"}).IfExists(), "DROP EVENT IF EXISTS `e`"},
		"portable param": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Param(mysql.RoutineParam("s", ddl.Portable[string](schema.TypeText))).Body("BEGIN END"),
			"CREATE PROCEDURE `p` (`s` VARCHAR(255)) BEGIN END"},
	} {
		if got := scriptOf(t, tc.b); got != tc.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, got, tc.want)
		}
	}
}

// TestGaps_ClosedSetsAreRefused pins every closed set the new constructs
// carry. Each is a named type over string or int, so a value outside the
// constants compiles, and MySQL's own reaction to one is the reason each is
// checked here: an unknown ENGINE is a warning and the server's default, an
// unknown row format is an error far from the call, and two characteristics
// of one kind are silently resolved to the last.
func TestGaps_ClosedSetsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		b    interface{ Build() (ddl.Script, error) }
		want error
	}{
		"engine":         {mysql.AlterTable(items).Clause(mysql.SetEngine(mysql.Engine("Falcon"))), mysql.ErrUnknownKeyword},
		"table engine":   {mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.TableEngine(mysql.Engine(""))), mysql.ErrUnknownKeyword},
		"row format":     {mysql.AlterTable(items).Clause(mysql.SetRowFormat(mysql.RowFormatKind("PACKED"))), mysql.ErrUnknownKeyword},
		"algorithm":      {mysql.AlterTable(items).Clause(mysql.Algorithm(mysql.AlterAlgorithm("FAST"))), mysql.ErrUnknownKeyword},
		"lock":           {mysql.AlterTable(items).Clause(mysql.Lock(mysql.AlterLock("SOME"))), mysql.ErrUnknownKeyword},
		"auto increment": {mysql.AlterTable(items).Clause(mysql.SetAutoIncrement(0)), mysql.ErrAutoIncrementZero},
		"empty raw":      {mysql.AlterTable(items).Clause(mysql.RawAlter("  ")), ddl.ErrZeroValue},
		"no partition":   {mysql.AlterTable(items).Clause(mysql.RebuildPartitions()), mysql.ErrPartitionDefinition},
		"empty index":    {mysql.AlterTable(items).Clause(mysql.SetIndexInvisible(mysql.Index{})), mysql.ErrEmptyName},
		"exchange table": {mysql.AlterTable(items).Clause(mysql.ExchangePartition("p0", sqlb.Table{})), mysql.ErrEmptyName},
		"sqlstate": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).Do(mysql.StepSignal("00000", "x")),
			mysql.ErrBadSQLState},
		"short sqlstate": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).Do(mysql.StepSignal("450", "x")),
			mysql.ErrBadSQLState},
		"empty raw step": {mysql.CreateTrigger(mysql.Trigger{Name: "t"}).Before(mysql.OnInsert, items).Do(mysql.StepRaw("")),
			ddl.ErrZeroValue},
		"interval unit": {mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventEvery(1, mysql.IntervalUnit("FORTNIGHT"))).Do(aStep()),
			mysql.ErrBadSchedule},
		"interval count": {mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventEvery(0, mysql.UnitDay)).Do(aStep()),
			mysql.ErrBadSchedule},
		"event status":      {mysql.AlterEvent(mysql.Event{Name: "e"}).Status(mysql.EventStatus(9)), mysql.ErrUnknownKeyword},
		"completion":        {mysql.AlterEvent(mysql.Event{Name: "e"}).OnCompletion(mysql.Completion(9)), mysql.ErrUnknownKeyword},
		"rename to nothing": {mysql.AlterEvent(mysql.Event{Name: "e"}).Rename(mysql.Event{}), mysql.ErrEmptyName},
		"param mode": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Param(mysql.RoutineParam("n", bigint()).Mode(mysql.ParamMode(9))).Body("BEGIN END"),
			mysql.ErrUnknownKeyword},
		"characteristic": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.RoutineCharacteristic(99)).Body("BEGIN END"),
			mysql.ErrUnknownKeyword},
		"two of a kind": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.NoSQL, mysql.ModifiesSQLData).Body("BEGIN END"),
			mysql.ErrConflictingCharacteristics},
		"not alterable": {mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.Deterministic),
			mysql.ErrCharacteristicNotAlterable},
		"empty body": {mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Body("  "), mysql.ErrEmptyBody},
		// MySQL's grammar gives a mode to a procedure's parameter only, and
		// only execution said so: the rendering test asserted the IN form.
		"function param mode": {mysql.CreateFunction(mysql.Function{Name: "f"}, bigint()).
			Param(mysql.RoutineParam("n", bigint()).Mode(mysql.ModeIn)).Body("RETURN 1"), mysql.ErrFunctionParamMode},
	} {
		if _, err := tc.b.Build(); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

// TestGaps_BuildersDoNotWriteToTheirReceiver walks the new multi-stage
// builders, which stmt's receiver-write test cannot see because it lives in
// stmt and cannot import this package. Each derivation must leave its
// receiver alone and must change something, so a method that quietly does
// nothing fails too.
func TestGaps_BuildersDoNotWriteToTheirReceiver(t *testing.T) {
	ev := mysql.CreateEvent(mysql.Event{Name: "e"}).On(mysql.EventAt(eventAt))
	build := func(q *mysql.ScheduledEventQuery) string { return scriptOf(t, q.Do(aStep())) }
	before := build(ev)
	for name, m := range map[string]func(*mysql.ScheduledEventQuery) *mysql.ScheduledEventQuery{
		"OnCompletion": func(q *mysql.ScheduledEventQuery) *mysql.ScheduledEventQuery { return q.OnCompletion(mysql.Preserve) },
		"Status":       func(q *mysql.ScheduledEventQuery) *mysql.ScheduledEventQuery { return q.Status(mysql.EventDisable) },
		"Comment":      func(q *mysql.ScheduledEventQuery) *mysql.ScheduledEventQuery { return q.Comment("c") },
	} {
		if d := m(ev); build(ev) != before || build(d) == before {
			t.Errorf("ScheduledEventQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	ae := mysql.AlterEvent(mysql.Event{Name: "e"}).Status(mysql.EventEnable)
	aeBefore := scriptOf(t, ae)
	for name, m := range map[string]func(*mysql.AlterEventStatement) *mysql.AlterEventStatement{
		"On":           func(q *mysql.AlterEventStatement) *mysql.AlterEventStatement { return q.On(mysql.EventAt(eventAt)) },
		"OnCompletion": func(q *mysql.AlterEventStatement) *mysql.AlterEventStatement { return q.OnCompletion(mysql.Preserve) },
		"Comment":      func(q *mysql.AlterEventStatement) *mysql.AlterEventStatement { return q.Comment("c") },
		"Rename": func(q *mysql.AlterEventStatement) *mysql.AlterEventStatement {
			return q.Rename(mysql.Event{Name: "e2"})
		},
		"Do":     func(q *mysql.AlterEventStatement) *mysql.AlterEventStatement { return q.Do(aStep()) },
		"Status": func(q *mysql.AlterEventStatement) *mysql.AlterEventStatement { return q.Status(mysql.EventDisable) },
	} {
		if d := m(ae); scriptOf(t, ae) != aeBefore || scriptOf(t, d) == aeBefore {
			t.Errorf("AlterEventStatement.%s wrote to its receiver or changed nothing", name)
		}
	}
	p := mysql.CreateProcedure(mysql.Procedure{Name: "p"})
	pb := func(q *mysql.CreateRoutineQuery) string { return scriptOf(t, q.Body("BEGIN END")) }
	pBefore := pb(p)
	for name, m := range map[string]func(*mysql.CreateRoutineQuery) *mysql.CreateRoutineQuery{
		"IfNotExists": (*mysql.CreateRoutineQuery).IfNotExists,
		"Param": func(q *mysql.CreateRoutineQuery) *mysql.CreateRoutineQuery {
			return q.Param(mysql.RoutineParam("n", bigint()))
		},
		"Characteristic": func(q *mysql.CreateRoutineQuery) *mysql.CreateRoutineQuery { return q.Characteristic(mysql.NoSQL) },
	} {
		if d := m(p); pb(p) != pBefore || pb(d) == pBefore {
			t.Errorf("CreateRoutineQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	ar := mysql.AlterProcedure(mysql.Procedure{Name: "p"}).Characteristic(mysql.NoSQL)
	arBefore := scriptOf(t, ar)
	if d := ar.Characteristic(mysql.SecurityInvoker); scriptOf(t, ar) != arBefore || scriptOf(t, d) == arBefore {
		t.Error("AlterRoutineStatement.Characteristic wrote to its receiver or changed nothing")
	}
	av := mysql.AlterView(mysql.View{Name: "v"}).As(mysql.Select(id).From(items))
	avBefore := scriptOf(t, av)
	if d := av.WithCheckOption(mysql.Cascaded); scriptOf(t, av) != avBefore || scriptOf(t, d) == avBefore {
		t.Error("AlterViewStatement.WithCheckOption wrote to its receiver or changed nothing")
	}
	ex := mysql.ExchangePartition("p0", items)
	exOf := func(c ddl.AlterClause[mysql.Dialect]) string { return scriptOf(t, mysql.AlterTable(items).Clause(c)) }
	exBefore := exOf(ex)
	if d := ex.WithoutValidation(); exOf(ex) != exBefore || exOf(d) == exBefore {
		t.Error("ExchangePartitionClause.WithoutValidation wrote to its receiver or changed nothing")
	}
	s := mysql.EventEvery(1, mysql.UnitDay)
	sOf := func(x mysql.Schedule) string {
		return scriptOf(t, mysql.CreateEvent(mysql.Event{Name: "e"}).On(x).Do(aStep()))
	}
	sBefore := sOf(s)
	for name, m := range map[string]func(mysql.Schedule) mysql.Schedule{
		"Starts": func(x mysql.Schedule) mysql.Schedule { return x.Starts(eventAt) },
		"Ends":   func(x mysql.Schedule) mysql.Schedule { return x.Ends(eventAt) },
	} {
		if d := m(s); sOf(s) != sBefore || sOf(d) == sBefore {
			t.Errorf("Schedule.%s wrote to its receiver or changed nothing", name)
		}
	}
	prm := mysql.RoutineParam("n", bigint())
	prmOf := func(x mysql.Param) string {
		return scriptOf(t, mysql.CreateProcedure(mysql.Procedure{Name: "p"}).Param(x).Body("BEGIN END"))
	}
	prmBefore := prmOf(prm)
	if d := prm.Mode(mysql.ModeOut); prmOf(prm) != prmBefore || prmOf(d) == prmBefore {
		t.Error("Param.Mode wrote to its receiver or changed nothing")
	}
}

// TestGaps_PartitionMaintenanceIsStandalone pins that each maintenance action
// takes its own statement: MySQL refuses a partition action beside any other,
// so grouping them would produce SQL the server rejects.
func TestGaps_PartitionMaintenanceIsStandalone(t *testing.T) {
	got := scriptOf(t, mysql.AlterTable(items).Clause(mysql.RebuildPartitions("p0"), mysql.AnalyzePartitions("p0"),
		mysql.ExchangePartition("p0", sqlb.Table{Name: "arch"}), mysql.SetTableComment("x")))
	want := "ALTER TABLE `items` REBUILD PARTITION `p0`; ALTER TABLE `items` ANALYZE PARTITION `p0`; " +
		"ALTER TABLE `items` EXCHANGE PARTITION `p0` WITH TABLE `arch`; ALTER TABLE `items` COMMENT = 'x'"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
