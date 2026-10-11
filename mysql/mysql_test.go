package mysql_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ubgo/sqlb/schema"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/stmt"
)

var (
	items = sqlb.Table{Name: "items", Alias: "i"}
	id    = sqlb.Col[int64](items, "id")
	hits  = sqlb.Col[int64](items, "hits")
	sku   = sqlb.Col[string](items, "sku")
	doc   = sqlb.Col[json.RawMessage](items, "doc")
)

func sqlOf(t *testing.T, b interface{ Build() (sqlb.Query, error) }) string {
	t.Helper()
	q, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return q.SQL
}

// TestMySQL_Renders pins the text of every write and read construct.
func TestMySQL_Renders(t *testing.T) {
	for name, tc := range map[string]struct {
		b    interface{ Build() (sqlb.Query, error) }
		want string
	}{
		"upsert": {mysql.Insert(items).Row(sqlb.Set(id, int64(1)), sqlb.Set(hits, int64(1))).
			Clause(mysql.OnDuplicateKeyUpdate(sqlb.SetExpr(hits, sqlb.Add(hits, mysql.New(hits))))),
			"INSERT INTO `items` (`id`, `hits`) VALUES (?, ?) AS `new` ON DUPLICATE KEY UPDATE `hits` = (`items`.`hits` + `new`.`hits`)"},
		"operators": {mysql.Select(mysql.IntDiv(hits, sqlb.Param(int64(2)))).From(items).Where(mysql.NullSafeEq(sku, sqlb.Param("a"))),
			"SELECT (`i`.`hits` DIV ?) FROM `items` AS `i` WHERE ((`i`.`sku` <=> ?))"},
		"insert ignore": {mysql.Insert(items).Row(sqlb.Set(id, int64(1))).Clause(mysql.InsertIgnore()),
			"INSERT IGNORE INTO `items` (`id`) VALUES (?)"},
		"update limit": {mysql.Update(items).Set(sqlb.Set(hits, int64(0))).All().Clause(mysql.UpdateLimit(10, sqlb.OrderBy(id, sqlb.SortAsc))),
			"UPDATE `items` AS `i` SET `hits` = ? ORDER BY `i`.`id` ASC LIMIT ?"},
		"delete limit": {mysql.Delete(items).All().Clause(mysql.DeleteLimit(5)),
			"DELETE FROM `items` AS `i` LIMIT ?"},
		"locking": {mysql.Select(id).From(items).Clause(mysql.ForUpdate(mysql.SkipLocked)),
			"SELECT `i`.`id` FROM `items` AS `i` FOR UPDATE SKIP LOCKED"},
		"locking of": {mysql.Select(id).From(items).Clause(mysql.ForUpdateOf(items, mysql.NoWait)),
			"SELECT `i`.`id` FROM `items` AS `i` FOR UPDATE OF `i` NOWAIT"},
		"share": {mysql.Select(id).From(items).Clause(mysql.ForShare()),
			"SELECT `i`.`id` FROM `items` AS `i` FOR SHARE"},
		"rollup": {mysql.Select(sku, sqlb.Sum(hits)).From(items).GroupBy(sku).Clause(mysql.WithRollup()),
			"SELECT `i`.`sku`, sum(`i`.`hits`) FROM `items` AS `i` GROUP BY `i`.`sku` WITH ROLLUP"},
		"functions": {mysql.Select(mysql.GroupConcat(sku, ","), mysql.CharLength(sku), mysql.JSONValue(doc, "$.a"), mysql.JSONExtract(doc, "$.b"), mysql.UUID(), mysql.UTCNow()).
			From(items).Where(mysql.RegexpLike(sku, "^a"), mysql.JSONContains(doc, json.RawMessage(`1`)), sqlb.Greater(mysql.Match("q", mysql.Boolean, sku), float64(0))),
			"SELECT group_concat(`i`.`sku` SEPARATOR ','), char_length(`i`.`sku`), json_unquote(json_extract(`i`.`doc`, ?)), json_extract(`i`.`doc`, ?), uuid(), utc_timestamp(6) FROM `items` AS `i` WHERE (regexp_like(`i`.`sku`, ?) AND json_contains(`i`.`doc`, ?) AND MATCH(`i`.`sku`) AGAINST(? IN BOOLEAN MODE) > ?)"},
	} {
		if got := sqlOf(t, tc.b); got != tc.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, got, tc.want)
		}
	}
}

// TestMySQL_FailsUnderAnotherDialect pins the guard: a MySQL expression in a
// Postgres statement fails Build naming the construct.
func TestMySQL_FailsUnderAnotherDialect(t *testing.T) {
	_, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(mysql.UUID()).Build()
	if !errors.Is(err, sqlb.ErrUnsupportedDialect) {
		t.Fatalf("got %v", err)
	}
}

// TestEntry_IsTheLongFormExactly pins that each constructor is only the long
// form with the dialect written out.
func TestEntry_IsTheLongFormExactly(t *testing.T) {
	d := mysql.Dialect{}
	for name, tc := range map[string][2]interface{ Build() (sqlb.Query, error) }{
		"select": {mysql.Select(id).From(items), stmt.NewSelect(d).Exprs(id).From(items)},
		"insert": {mysql.Insert(items).Row(sqlb.Set(id, int64(1))), stmt.NewInsert(d).Into(items).Row(sqlb.Set(id, int64(1)))},
		"update": {mysql.Update(items).Set(sqlb.Set(id, int64(1))).All(), stmt.NewUpdate(d).Table(items).Set(sqlb.Set(id, int64(1))).All()},
		"delete": {mysql.Delete(items).All(), stmt.NewDelete(d).From(items).All()},
	} {
		if a, b := sqlOf(t, tc[0]), sqlOf(t, tc[1]); a != b {
			t.Errorf("%s: %s != %s", name, a, b)
		}
	}
}

// TestIncomplete_ModifiersDoNotWriteToTheirReceiver walks the incomplete
// stages of the MySQL DDL statements, which have no Build.
func TestIncomplete_ModifiersDoNotWriteToTheirReceiver(t *testing.T) {
	build := func(b interface{ Build() (ddl.Script, error) }) string {
		t.Helper()
		s, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(s.SQL(), ";")
	}
	ix := mysql.CreateIndex(mysql.Index{Name: "i"}, items)
	on := func(q *mysql.CreateIndexQuery) string { return build(q.On(mysql.IndexKey(sku))) }
	before := on(ix)
	for name, m := range map[string]func(*mysql.CreateIndexQuery) *mysql.CreateIndexQuery{
		"Unique": (*mysql.CreateIndexQuery).Unique, "Fulltext": (*mysql.CreateIndexQuery).Fulltext,
		"Using": func(q *mysql.CreateIndexQuery) *mysql.CreateIndexQuery { return q.Using(mysql.Hash) },
	} {
		if d := m(ix); on(ix) != before || on(d) == before {
			t.Errorf("CreateIndexQuery.%s wrote to its receiver or changed nothing", name)
		}
	}
	v := mysql.CreateView(mysql.View{Name: "v"})
	body := mysql.Select(id).From(items)
	before = build(v.As(body))
	if d := v.OrReplace(); build(v.As(body)) != before || build(d.As(body)) == before {
		t.Error("CreateViewQuery.OrReplace wrote to its receiver or changed nothing")
	}
	tr := mysql.CreateTrigger(mysql.Trigger{Name: "t"})
	step := mysql.StepDelete(mysql.Delete(items).Where(sqlb.EqExpr(id, mysql.OldRow(id))))
	bt := func(q *mysql.CreateTriggerQuery) string { return build(q.After(mysql.OnDelete, items).Do(step)) }
	before = bt(tr)
	if d := tr.IfNotExists(); bt(tr) != before || bt(d) == before {
		t.Error("CreateTriggerQuery.IfNotExists wrote to its receiver or changed nothing")
	}
}

// TestDescribe_MySQLKeysAndReferences pins the MySQL clauses' descriptions:
// a plain inline index is a non-unique schema.Index, FULLTEXT is left out.
func TestDescribe_MySQLKeysAndReferences(t *testing.T) {
	got, err := mysql.CreateTable(items).Column(ddl.Column(id), ddl.Column(sku), ddl.Column(hits)).
		Clause(mysql.AutoIncrement(id), mysql.PrimaryKey(id), mysql.Unique(mysql.TableConstraint{Table: sku.Table(), Name: "items_sku"}, sku), mysql.TableIndex(mysql.Index{Name: "items_hits"}, hits),
			mysql.TableFulltext(mysql.Index{Name: "items_ft"}, sku), mysql.InnoDB()).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PrimaryKey) != 1 || len(got.Indexes) != 2 || !got.Indexes[0].Unique || got.Indexes[1].Unique {
		t.Fatalf("got %+v", got)
	}
}

// TestDescribe_MySQLForeignKeysOrderTables pins the MySQL foreign key's
// description, which migrate.Install orders tables by: a composite key keeps
// its column order on both sides, the referential actions carry over, a
// column-level References with no action describes none, and the described
// tables sort parent first.
func TestDescribe_MySQLForeignKeysOrderTables(t *testing.T) {
	var (
		orders  = sqlb.Table{Name: "orders", Alias: "o"}
		oID     = sqlb.Col[int64](orders, "id")
		oRegion = sqlb.Col[string](orders, "region")
		lines   = sqlb.Table{Name: "order_lines", Alias: "l"}
		lOrder  = sqlb.Col[int64](lines, "order_id")
		lRegion = sqlb.Col[string](lines, "region")
		lItem   = sqlb.Col[int64](lines, "item_id")
	)
	parent, err := mysql.CreateTable(orders).Column(ddl.Column(oID), ddl.Column(oRegion)).Clause(mysql.PrimaryKey(oID, oRegion)).Describe()
	if err != nil {
		t.Fatal(err)
	}
	child, err := mysql.CreateTable(lines).Column(ddl.Column(lOrder), ddl.Column(lRegion), ddl.Column(lItem)).
		Clause(mysql.ForeignKey(mysql.TableConstraint{Table: lOrder.Table(), Name: "lines_order_fk"}, mysql.RefPair(lOrder, oID), mysql.RefPair(lRegion, oRegion)).
			OnDelete(mysql.RefCascade).OnUpdate(mysql.RefRestrict),
			mysql.References(lItem, id)).Describe()
	if err != nil {
		t.Fatal(err)
	}
	want := []schema.ForeignKey{
		{Name: "lines_order_fk", Columns: []string{"order_id", "region"}, RefTable: "orders", RefColumns: []string{"id", "region"}, OnDelete: schema.Cascade, OnUpdate: schema.Restrict},
		{Columns: []string{"item_id"}, RefTable: "items", RefColumns: []string{"id"}},
	}
	if !reflect.DeepEqual(child.ForeignKeys, want) {
		t.Fatalf("got  %+v\nwant %+v", child.ForeignKeys, want)
	}
	itemsDesc, err := mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.PrimaryKey(id)).Describe()
	if err != nil {
		t.Fatal(err)
	}
	set, err := schema.New("app", child, parent, itemsDesc)
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := set.Sorted()
	if err != nil {
		t.Fatal(err)
	}
	if sorted[len(sorted)-1].Name != "order_lines" {
		t.Fatalf("order_lines must come after both tables it references: %v", sorted)
	}
}

// TestTableConstraint_MySQLRefusesAnotherTable pins that a declared
// constraint of one table is refused in a statement about another, in CREATE
// TABLE and in every ALTER action naming one, and that a rename stays within
// its table.
func TestTableConstraint_MySQLRefusesAnotherTable(t *testing.T) {
	other := sqlb.Table{Name: "other", Alias: "o"}
	otherID := sqlb.Col[int64](other, "id")
	key := mysql.TableConstraint{Table: items, Name: "items_sku"}
	for name, build := range map[string]func() (ddl.Script, error){
		"create unique": mysql.CreateTable(other).Column(ddl.Column(otherID)).Clause(mysql.Unique(key, otherID)).Build,
		"create check":  mysql.CreateTable(other).Column(ddl.Column(otherID)).Clause(mysql.Check(key, sqlb.Greater(otherID, 0))).Build,
		"add":           mysql.AlterTable(other).Clause(mysql.AddConstraint(mysql.Check(key, sqlb.Greater(otherID, 0)))).Build,
		"drop key":      mysql.AlterTable(other).Clause(mysql.DropKey(key)).Build,
		"drop check":    mysql.AlterTable(other).Clause(mysql.DropCheck(key)).Build,
		"drop fk":       mysql.AlterTable(other).Clause(mysql.DropForeignKey(key)).Build,
		"rename":        mysql.AlterTable(other).Clause(mysql.RenameKey(key, mysql.TableConstraint{Table: items, Name: "x"})).Build,
	} {
		if _, err := build(); !errors.Is(err, ddl.ErrForeignColumn) {
			t.Errorf("%s: %v, want ErrForeignColumn", name, err)
		}
	}
	if _, err := mysql.AlterTable(items).Clause(mysql.RenameKey(key, mysql.TableConstraint{Table: other, Name: "x"})).Build(); !errors.Is(err, mysql.ErrRenameAcrossTables) {
		t.Errorf("a rename to another table: %v", err)
	}
	if _, err := mysql.AlterTable(items).Clause(mysql.DropKey(mysql.TableConstraint{Table: items})).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("a nameless key: %v", err)
	}
	got, err := mysql.AlterTable(items).Clause(mysql.RenameIndex(mysql.Index{Name: "a"}, mysql.Index{Name: "b"})).Build()
	if err != nil || got[0].SQL != "ALTER TABLE `items` RENAME INDEX `a` TO `b`" {
		t.Errorf("RenameIndex: %v, %v", got, err)
	}
	if _, err := mysql.AlterTable(items).Clause(mysql.RenameIndex(mysql.Index{}, mysql.Index{Name: "b"})).Build(); !errors.Is(err, mysql.ErrEmptyName) {
		t.Errorf("RenameIndex with no name: %v", err)
	}
	if _, err := mysql.CreateTable(items).Column(ddl.Column(id)).Clause(mysql.TableIndex(mysql.Index{}, id)).Build(); !errors.Is(err, mysql.ErrEmptyName) {
		t.Errorf("an unnamed inline index: %v", err)
	}
}
