package drivermysql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/mysql"
)

var (
	cItems   = sqlb.Table{Name: "c_items", Alias: "i"}
	cID      = sqlb.Col[int64](cItems, "id")
	cSKU     = sqlb.Col[string](cItems, "sku")
	cHits    = sqlb.Col[int64](cItems, "hits")
	cGroup   = sqlb.Col[string](cItems, "grp")
	cDoc     = sqlb.Col[json.RawMessage](cItems, "doc")
	cBody    = sqlb.Col[string](cItems, "body")
	cNote    = sqlb.Col[*string](cItems, "note")
	cCreated = sqlb.Col[time.Time](cItems, "created_at")
)

func setupItems(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		"DROP TABLE IF EXISTS c_items",
		`CREATE TABLE c_items (id BIGINT PRIMARY KEY, sku VARCHAR(64) NOT NULL UNIQUE, hits BIGINT NOT NULL DEFAULT 0,
			grp VARCHAR(16) NOT NULL DEFAULT 'a', doc JSON NULL, body TEXT NULL, note VARCHAR(64) NULL,
			created_at DATETIME(6) NULL, FULLTEXT KEY c_items_body (body))`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS c_items") })
}

func execQ(t *testing.T, db *sql.DB, b interface{ Build() (sqlb.Query, error) }) sql.Result {
	t.Helper()
	q, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.ExecContext(context.Background(), q.SQL, q.Args...)
	if err != nil {
		t.Fatalf("%s: %v", q.SQL, err)
	}
	return r
}

// TestMySQLConstructs_Writes executes the upsert with its row alias, INSERT
// IGNORE, and UPDATE and DELETE with ORDER BY and LIMIT.
func TestMySQLConstructs_Writes(t *testing.T) {
	db := openOrSkip(t)
	setupItems(t, db)
	ctx := context.Background()
	for i := range 2 {
		execQ(t, db, mysql.Insert(cItems).Row(sqlb.Set(cID, int64(1)), sqlb.Set(cSKU, "a"), sqlb.Set(cHits, int64(5))).
			Clause(mysql.OnDuplicateKeyUpdate(sqlb.SetExpr(cHits, sqlb.Add(cHits, mysql.New(cHits))))))
		_ = i
	}
	var hits int64
	if err := db.QueryRowContext(ctx, "SELECT hits FROM c_items WHERE id = 1").Scan(&hits); err != nil || hits != 10 {
		t.Errorf("upsert hits %d, want 10: %v", hits, err)
	}
	r := execQ(t, db, mysql.Insert(cItems).Row(sqlb.Set(cID, int64(2)), sqlb.Set(cSKU, "a")).Clause(mysql.InsertIgnore()))
	if n, _ := r.RowsAffected(); n != 0 {
		t.Errorf("INSERT IGNORE wrote %d rows over a duplicate", n)
	}
	for i := int64(10); i < 15; i++ {
		execQ(t, db, mysql.Insert(cItems).Row(sqlb.Set(cID, i), sqlb.Set(cSKU, "s"+string(rune('a'+i)))))
	}
	r = execQ(t, db, mysql.Update(cItems).Set(sqlb.Set(cHits, int64(99))).Where(sqlb.Greater(cID, int64(9))).
		Clause(mysql.UpdateLimit(2, sqlb.OrderBy(cID, sqlb.SortAsc))))
	if n, _ := r.RowsAffected(); n != 2 {
		t.Errorf("UpdateLimit changed %d rows, want 2", n)
	}
	r = execQ(t, db, mysql.Delete(cItems).Where(sqlb.Greater(cID, int64(9))).Clause(mysql.DeleteLimit(3, sqlb.OrderBy(cID, sqlb.SortDesc))))
	if n, _ := r.RowsAffected(); n != 3 {
		t.Errorf("DeleteLimit removed %d rows, want 3", n)
	}
	var left string
	if err := db.QueryRowContext(ctx, "SELECT GROUP_CONCAT(id ORDER BY id) FROM c_items WHERE id > 9").Scan(&left); err != nil || left != "10,11" {
		t.Errorf("after the ordered delete %q, %v", left, err)
	}
}

// TestMySQLConstructs_Reads executes locking reads, WITH ROLLUP, the text,
// time and JSON functions and a full-text match.
func TestMySQLConstructs_Reads(t *testing.T) {
	db := openOrSkip(t)
	setupItems(t, db)
	ctx := context.Background()
	rows := []struct {
		id        int64
		sku, grp  string
		hits      int64
		doc, body string
	}{
		{1, "Ab", "a", 1, `{"tags": ["x"], "name": "one"}`, "the quick brown fox"},
		{2, "cd", "a", 2, `{"tags": ["y"]}`, "lazy dogs sleep"},
		{3, "ef", "b", 4, `{"name": "three"}`, "a quick answer"},
	}
	for _, r := range rows {
		execQ(t, db, mysql.Insert(cItems).Row(sqlb.Set(cID, r.id), sqlb.Set(cSKU, r.sku), sqlb.Set(cGroup, r.grp), sqlb.Set(cHits, r.hits),
			sqlb.Set(cDoc, json.RawMessage(r.doc)), sqlb.Set(cBody, r.body)))
	}

	var id int64
	for _, cl := range []mysql.SelectClause{mysql.ForUpdate(), mysql.ForUpdate(mysql.SkipLocked), mysql.ForShare(mysql.NoWait), mysql.ForUpdateOf(cItems, mysql.NoWait)} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		q, err := mysql.Select().Columns(sqlb.Bind(cID, &id)).From(cItems).Where(sqlb.Eq(cID, int64(1))).Clause(cl).Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlb.One(ctx, tx, q); err != nil {
			t.Errorf("%s: %v", q.SQL, err)
		}
		_ = tx.Rollback()
	}

	var grp *string
	var total int64
	q, err := mysql.Select().Columns(sqlb.Bind(sqlb.Nullable(cGroup), &grp), sqlb.Bind(sqlb.Sum(cHits), &total)).
		From(cItems).GroupBy(cGroup).Clause(mysql.WithRollup()).Build()
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]int64{}
	if err := sqlb.Each(ctx, db, q, func() error {
		k := "(total)"
		if grp != nil {
			k = *grp
		}
		sums[k] = total
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sums["a"] != 3 || sums["b"] != 4 || sums["(total)"] != 7 {
		t.Errorf("WITH ROLLUP sums %v", sums)
	}

	var concat, name string
	var skuLen int64
	var missing *string
	q, err = mysql.Select().Columns(
		sqlb.Bind(mysql.GroupConcat(cSKU, "|"), &concat),
	).From(cItems).Where(sqlb.Eq(cGroup, "a")).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || (concat != "Ab|cd" && concat != "cd|Ab") {
		t.Errorf("GroupConcat %q, %v", concat, err)
	}
	q, err = mysql.Select().Columns(
		sqlb.Bind(mysql.IfNull(mysql.JSONValue(cDoc, "$.name"), sqlb.Param("?")), &name),
		sqlb.Bind(mysql.CharLength(cSKU), &skuLen),
		sqlb.Bind(mysql.JSONValue(cDoc, "$.none"), &missing),
	).From(cItems).Where(sqlb.Eq(cID, int64(1)), mysql.RegexpLike(cSKU, "^A"), mysql.JSONContains(cDoc, json.RawMessage(`{"tags": ["x"]}`))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || name != "one" || skuLen != 2 || missing != nil {
		t.Errorf("functions: %q %d %v, %v", name, skuLen, missing, err)
	}
	var raw json.RawMessage
	q, err = mysql.Select().Columns(sqlb.Bind(mysql.JSONExtract(cDoc, "$.tags"), &raw)).From(cItems).Where(sqlb.Eq(cID, int64(2))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || string(raw) != `["y"]` {
		t.Errorf("JSONExtract %s, %v", raw, err)
	}

	execQ(t, db, mysql.Update(cItems).Set(sqlb.SetExpr(cCreated, mysql.UTCNow()), sqlb.SetExpr(cNote, sqlb.Nullable(mysql.UUID()))).All())
	var created time.Time
	var note *string
	if err := db.QueryRowContext(ctx, "SELECT created_at, note FROM c_items WHERE id = 1").Scan(&created, &note); err != nil ||
		time.Since(created) > time.Minute || time.Since(created) < -time.Minute || note == nil || len(*note) != 36 {
		t.Errorf("UTCNow %v and UUID %v: %v", created, note, err)
	}

	var half int64
	var nulls int64
	q, err = mysql.Select().Columns(sqlb.Bind(mysql.IntDiv(cHits, sqlb.Param(int64(2))), &half)).From(cItems).Where(sqlb.Eq(cID, int64(3))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.One(ctx, db, q); err != nil || half != 2 {
		t.Errorf("4 DIV 2 = %d, %v", half, err)
	}
	q, err = mysql.Select().Columns(sqlb.Bind(sqlb.Count(), &nulls)).From(cItems).
		Where(mysql.NullSafeEq(mysql.JSONValue(cDoc, "$.name"), sqlb.Literal[*string]("NULL"))).Build()
	if err != nil {
		t.Fatal(err)
	}
	// Only row 2's document has no name. <=> NULL matches it; = NULL would
	// match nothing, which is the whole point of the operator.
	if err := sqlb.One(ctx, db, q); err != nil || nulls != 1 {
		t.Errorf("name <=> NULL matched %d rows, want 1: %v", nulls, err)
	}

	var matched []int64
	q, err = mysql.Select().Columns(sqlb.Bind(cID, &id)).From(cItems).
		Where(sqlb.Greater(mysql.Match("quick", mysql.NaturalLanguage, cBody), float64(0))).
		Order(sqlb.OrderBy(cID, sqlb.SortAsc)).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.Each(ctx, db, q, func() error { matched = append(matched, id); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(matched) != 2 || matched[0] != 1 || matched[1] != 3 {
		t.Errorf("MATCH quick: %v", matched)
	}
	matched = nil
	q, err = mysql.Select().Columns(sqlb.Bind(cID, &id)).From(cItems).
		Where(sqlb.Greater(mysql.Match("+quick -fox", mysql.Boolean, cBody), float64(0))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlb.Each(ctx, db, q, func() error { matched = append(matched, id); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0] != 3 {
		t.Errorf("MATCH boolean: %v", matched)
	}
}
