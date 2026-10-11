package drivermysql_test

import (
	"context"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

// TestDDL_PartitioningIsWrittenLastExecutes is the MySQL half of the proof for
// ddl.OptionRank. PARTITION BY is passed FIRST and the table options after it,
// which MySQL refuses as written — measured on 8.4, `PARTITION BY HASH(id)
// PARTITIONS 2 ENGINE=InnoDB` is ER_PARSE_ERROR — and the statement must still
// run, because the rank table in mysql/table.go puts partitioning after every
// table option.
//
// The table options are deliberately in an unusual order among themselves too.
// MySQL takes those in any order, so they share one rank and the stable sort
// keeps the caller's order; this proves that keeping it is harmless, and the
// effects are read back from information_schema rather than trusted from a
// clean Exec.
func TestDDL_PartitioningIsWrittenLastExecutes(t *testing.T) {
	ctx := context.Background()
	db := openOrSkip(t)
	tbl := sqlb.Table{Name: "oo_parts"}
	id := sqlb.Col[int64](tbl, "id")
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS oo_parts") })
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS oo_parts")

	s, err := mysql.CreateTable(tbl).Column(ddl.Column(id)).Clause(
		mysql.PartitionByHash(mysql.PartitionOn(id), 2),
		mysql.TableComment("order-free"),
		mysql.DefaultCharset("utf8mb4"),
		mysql.InnoDB(),
	).Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range s.SQL() {
		if _, err := db.ExecContext(ctx, st); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}

	var engine, comment string
	var parts int
	if err := db.QueryRowContext(ctx, `SELECT engine, table_comment FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'oo_parts'`).Scan(&engine, &comment); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.partitions WHERE table_schema = DATABASE() AND table_name = 'oo_parts'`).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if engine != "InnoDB" || comment != "order-free" || parts != 2 {
		t.Errorf("engine %q, comment %q, %d partitions; want InnoDB, order-free, 2", engine, comment, parts)
	}
}
