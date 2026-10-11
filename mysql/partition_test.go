package mysql_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

var (
	orders     = sqlb.Table{Name: "orders", Alias: "o"}
	orderID    = sqlb.Col[int64](orders, "id")
	orderYear  = sqlb.Col[int64](orders, "year")
	orderAt    = sqlb.Col[time.Time](orders, "at")
	orderReg   = sqlb.Col[string](orders, "region")
	orderOwner = sqlb.Col[int64](orders, "owner_id")
)

func partSQL(t *testing.T, p mysql.Partitioning, keys ...mysql.TableClause) string {
	t.Helper()
	cl := append([]mysql.TableClause{mysql.PrimaryKey(orderID, orderYear, orderAt, orderReg)}, keys...)
	s, err := mysql.CreateTable(orders).Column(ddl.Column(orderID), ddl.Column(orderYear), ddl.Column(orderAt), ddl.Column(orderReg)).
		Clause(append(cl, p)...).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(s[0].SQL, "PARTITION BY")
	if !ok {
		t.Fatalf("no PARTITION BY in %s", s[0].SQL)
	}
	return "PARTITION BY" + after
}

// TestPartitioning_Renders pins each form MySQL has, as the server was shown
// to accept it: a key or one of the allowed functions, the COLUMNS forms,
// HASH and KEY with a count, and MAXVALUE once per column in parentheses.
func TestPartitioning_Renders(t *testing.T) {
	for name, c := range map[string]struct {
		p    mysql.Partitioning
		want string
	}{
		"range on a column": {mysql.PartitionByRange(mysql.PartitionOn(orderYear)).With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax")),
			"PARTITION BY RANGE (`year`) (PARTITION `p2025` VALUES LESS THAN (2026), PARTITION `pmax` VALUES LESS THAN (MAXVALUE))"},
		"range by year": {mysql.PartitionByRange(mysql.PartitionYear(orderAt)).With(mysql.LessThan("a", 2026)),
			"PARTITION BY RANGE (YEAR(`at`)) (PARTITION `a` VALUES LESS THAN (2026))"},
		"range by days": {mysql.PartitionByRange(mysql.PartitionToDays(orderAt)).With(mysql.LessThan("a", 739000)),
			"PARTITION BY RANGE (TO_DAYS(`at`)) (PARTITION `a` VALUES LESS THAN (739000))"},
		"range by seconds": {mysql.PartitionByRange(mysql.PartitionToSeconds(orderAt)).With(mysql.LessThan("a", 1)),
			"PARTITION BY RANGE (TO_SECONDS(`at`)) (PARTITION `a` VALUES LESS THAN (1))"},
		"range columns": {mysql.PartitionByRangeColumns(orderYear, orderReg).With(mysql.LessThan("a", 2026, "m"), mysql.LessThanMax("z")),
			"PARTITION BY RANGE COLUMNS (`year`, `region`) (PARTITION `a` VALUES LESS THAN (2026, 'm'), PARTITION `z` VALUES LESS THAN (MAXVALUE, MAXVALUE))"},
		"list": {mysql.PartitionByList(mysql.PartitionOn(orderYear)).With(mysql.In("old", 2024, 2025), mysql.In("new", 2026)),
			"PARTITION BY LIST (`year`) (PARTITION `old` VALUES IN (2024, 2025), PARTITION `new` VALUES IN (2026))"},
		"list columns": {mysql.PartitionByListColumns(orderReg).With(mysql.In("eu", "de", "fr"), mysql.In("us", "us")),
			"PARTITION BY LIST COLUMNS (`region`) (PARTITION `eu` VALUES IN ('de', 'fr'), PARTITION `us` VALUES IN ('us'))"},
		"hash": {mysql.PartitionByHash(mysql.PartitionOn(orderYear), 4), "PARTITION BY HASH (`year`) PARTITIONS 4"},
		"key":  {mysql.PartitionByKey(3, orderID, orderYear), "PARTITION BY KEY (`id`, `year`) PARTITIONS 3"},
	} {
		if got := partSQL(t, c.p); got != c.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, got, c.want)
		}
	}
}

// TestPartitioning_RefusesWhatMySQLRefuses pins each rule, with the error the
// server raises for it recorded in partition.go: a key without the partition
// column, a foreign key, bounds that do not increase, MAXVALUE not last, a
// duplicated or missing name, the wrong number of values, the wrong kind of
// definition, and no definitions at all.
func TestPartitioning_RefusesWhatMySQLRefuses(t *testing.T) {
	byYear := mysql.PartitionByRange(mysql.PartitionOn(orderYear))
	build := func(p mysql.Partitioning, keys ...mysql.TableClause) error {
		_, err := mysql.CreateTable(orders).Column(ddl.Column(orderID), ddl.Column(orderYear), ddl.Column(orderOwner)).Clause(append(keys, p)...).Build()
		return err
	}
	pk := mysql.PrimaryKey(orderID, orderYear)
	for name, c := range map[string]struct {
		err  error
		want error
	}{
		"a primary key without the column": {build(byYear.With(mysql.LessThan("a", 1)), mysql.PrimaryKey(orderID)), mysql.ErrPartitionKeyNotInKey},
		"a unique key without the column": {build(byYear.With(mysql.LessThan("a", 1)), pk,
			mysql.Unique(mysql.TableConstraint{Table: orders, Name: "u"}, orderID)), mysql.ErrPartitionKeyNotInKey},
		"a foreign key":               {build(byYear.With(mysql.LessThan("a", 1)), pk, mysql.References(orderOwner, orderID)), mysql.ErrPartitionForeignKey},
		"bounds that do not increase": {build(byYear.With(mysql.LessThan("a", 10), mysql.LessThan("b", 5)), pk), mysql.ErrPartitionBounds},
		"equal bounds":                {build(byYear.With(mysql.LessThan("a", 5), mysql.LessThan("b", 5)), pk), mysql.ErrPartitionBounds},
		"MAXVALUE not last":           {build(byYear.With(mysql.LessThanMax("a"), mysql.LessThan("b", 5)), pk), mysql.ErrPartitionBounds},
		"too many values":             {build(byYear.With(mysql.LessThan("a", 1, 2)), pk), mysql.ErrPartitionBounds},
		"a name twice":                {build(byYear.With(mysql.LessThan("a", 1), mysql.LessThan("A", 2)), pk), mysql.ErrPartitionDefinition},
		"no name":                     {build(byYear.With(mysql.LessThan("", 1)), pk), mysql.ErrPartitionDefinition},
		"a LIST definition in RANGE":  {build(byYear.With(mysql.In("a", 1)), pk), mysql.ErrPartitionDefinition},
		"an empty LIST":               {build(mysql.PartitionByList(mysql.PartitionOn(orderYear)).With(mysql.In("a")), pk), mysql.ErrPartitionDefinition},
		"no definitions":              {build(byYear, pk), mysql.ErrPartitionDefinition},
		"HASH with definitions":       {build(mysql.PartitionByHash(mysql.PartitionOn(orderYear), 2).With(mysql.LessThan("a", 1)), pk), mysql.ErrPartitionDefinition},
		"HASH of zero":                {build(mysql.PartitionByHash(mysql.PartitionOn(orderYear), 0), pk), mysql.ErrPartitionDefinition},
		"a column the table lacks":    {build(mysql.PartitionByKey(2, orderAt), pk), ddl.ErrUnknownColumn},
	} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: %v, want %v", name, c.err, c.want)
		}
	}
	if err := build(byYear.With(mysql.LessThan("a", 1), mysql.LessThanMax("b")), pk); err != nil {
		t.Errorf("the control failed, so the cases above test nothing: %v", err)
	}
}

// TestPartitionActions_Render pins the maintenance actions, each a statement
// of its own as MySQL requires, and their refusals.
func TestPartitionActions_Render(t *testing.T) {
	for name, c := range map[string]struct {
		a    mysql.AlterClause
		want string
	}{
		"add":      {mysql.AddPartitions(mysql.LessThan("p2027", 2028)), "ALTER TABLE `orders` ADD PARTITION (PARTITION `p2027` VALUES LESS THAN (2028))"},
		"add list": {mysql.AddPartitions(mysql.In("asia", "jp")), "ALTER TABLE `orders` ADD PARTITION (PARTITION `asia` VALUES IN ('jp'))"},
		"add hash": {mysql.AddHashPartitions(2), "ALTER TABLE `orders` ADD PARTITION PARTITIONS 2"},
		"drop":     {mysql.DropPartitions("a", "b"), "ALTER TABLE `orders` DROP PARTITION `a`, `b`"},
		"truncate": {mysql.TruncatePartitions("a"), "ALTER TABLE `orders` TRUNCATE PARTITION `a`"},
		"coalesce": {mysql.CoalescePartitions(1), "ALTER TABLE `orders` COALESCE PARTITION 1"},
		"remove":   {mysql.RemovePartitioning(), "ALTER TABLE `orders` REMOVE PARTITIONING"},
		"reorganize": {mysql.ReorganizePartitions([]string{"pmax"}, mysql.LessThan("p2027", 2028, "m"), mysql.LessThanMax("pmax")),
			"ALTER TABLE `orders` REORGANIZE PARTITION `pmax` INTO (PARTITION `p2027` VALUES LESS THAN (2028, 'm'), PARTITION `pmax` VALUES LESS THAN (MAXVALUE, MAXVALUE))"},
	} {
		s, err := mysql.AlterTable(orders).Clause(c.a).Build()
		if err != nil || len(s) != 1 || s[0].SQL != c.want {
			t.Errorf("%s: %v, %v\nwant %s", name, s, err, c.want)
		}
	}
	two, err := mysql.AlterTable(orders).Clause(mysql.TruncatePartitions("a"), mysql.RemovePartitioning()).Build()
	if err != nil || len(two) != 2 {
		t.Errorf("partition actions must each be their own statement: %v, %v", two, err)
	}
	for name, a := range map[string]mysql.AlterClause{
		"no names":          mysql.DropPartitions(),
		"an empty name":     mysql.TruncatePartitions(""),
		"coalesce zero":     mysql.CoalescePartitions(0),
		"add zero":          mysql.AddHashPartitions(0),
		"reorganize badly":  mysql.ReorganizePartitions([]string{"a"}, mysql.LessThan("b", 5), mysql.LessThan("c", 1)),
		"reorganize nobody": mysql.ReorganizePartitions(nil, mysql.LessThan("b", 5)),
		"add badly":         mysql.AddPartitions(mysql.LessThanMax("x"), mysql.LessThan("y", 1)),
	} {
		if _, err := mysql.AlterTable(orders).Clause(a).Build(); err == nil {
			t.Errorf("%s: built", name)
		}
	}
}

// TestIndexGuards_AndMultiStepTriggers pins the two MySQL forms that were raw
// recipes: an index written repeatable through a SkipIf catalogue query, in
// both directions, and a trigger whose body is several statements.
func TestIndexGuards_AndMultiStepTriggers(t *testing.T) {
	ix := mysql.Index{Name: "orders_year"}
	s, err := mysql.CreateIndex(ix, orders).On(mysql.IndexKey(orderYear)).IfNotExists().Build()
	if err != nil || len(s) != 1 {
		t.Fatal(s, err)
	}
	if s[0].SQL != "CREATE INDEX `orders_year` ON `orders` (`year`)" ||
		s[0].SkipIf != "SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'orders' AND index_name = 'orders_year'" {
		t.Errorf("create: %q / %q", s[0].SQL, s[0].SkipIf)
	}
	inSchema := sqlb.Table{Schema: "app", Name: "orders"}
	s, err = mysql.DropIndex(ix, inSchema).IfExists().Build()
	if err != nil || s[0].SkipIf != "SELECT 1 FROM DUAL WHERE NOT EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = 'app' AND table_name = 'orders' AND index_name = 'orders_year')" {
		t.Errorf("drop: %v, %v", s, err)
	}
	if s, _ := mysql.CreateIndex(ix, orders).On(mysql.IndexKey(orderYear)).Build(); s[0].SkipIf != "" {
		t.Errorf("an unguarded index has a guard: %q", s[0].SkipIf)
	}
	audit := sqlb.Table{Name: "audit", Alias: "a"}
	auditID := sqlb.Col[int64](audit, "id")
	step := mysql.StepInsert(mysql.Insert(audit).Row(sqlb.Set(auditID, int64(1))))
	s, err = mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, orders).Do(step, step).Build()
	if err != nil || !strings.HasSuffix(s[0].SQL, "FOR EACH ROW BEGIN INSERT INTO `audit` (`id`) VALUES (1); INSERT INTO `audit` (`id`) VALUES (1); END") {
		t.Errorf("multi-step trigger: %v, %v", s, err)
	}
	s, err = mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, orders).Do(step).Build()
	if err != nil || strings.Contains(s[0].SQL, "BEGIN") {
		t.Errorf("a one-step trigger is written without BEGIN: %v, %v", s, err)
	}
	if _, err := mysql.CreateTrigger(mysql.Trigger{Name: "t"}).After(mysql.OnDelete, orders).Do(step, mysql.TriggerStep{}).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("a zero step among several: %v", err)
	}
}
