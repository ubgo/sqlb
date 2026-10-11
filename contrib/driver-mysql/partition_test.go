package drivermysql_test

import (
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

// TestPartitioning_ExecutesOnMySQL creates a table in every form the builder
// writes, puts rows in and reads back which partition each landed in, then
// runs every maintenance action on a real MySQL 8.4 and reads the partitions
// after each.
func TestPartitioning_ExecutesOnMySQL(t *testing.T) {
	db := openOrSkip(t)
	tb := sqlb.Table{Name: "pt_orders", Alias: "o"}
	id := sqlb.Col[int64](tb, "id")
	year := sqlb.Col[int64](tb, "year")
	at := sqlb.Col[time.Time](tb, "at")
	region := sqlb.Col[string](tb, "region")
	cols := []ddl.ColumnSpec{ddl.Column(id), ddl.Column(year), ddl.Column(at), ddl.Column(region)}
	pk := mysql.PrimaryKey(id, year, at, region)
	layout := func() string {
		return myScalar(t, db, `SELECT group_concat(concat(partition_name, '=', table_rows) ORDER BY partition_ordinal_position)
			FROM information_schema.partitions WHERE table_schema = DATABASE() AND table_name = 'pt_orders'`)
	}
	insert := func(rows ...string) {
		for _, r := range rows {
			if _, err := db.Exec(`INSERT INTO pt_orders VALUES ` + r); err != nil {
				t.Fatalf("%s: %v", r, err)
			}
		}
		if _, err := db.Exec(`ANALYZE TABLE pt_orders`); err != nil {
			t.Fatal(err)
		}
	}
	for name, p := range map[string]mysql.Partitioning{
		"range on a column": mysql.PartitionByRange(mysql.PartitionOn(year)).With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax")),
		"range by year":     mysql.PartitionByRange(mysql.PartitionYear(at)).With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax")),
		// TO_DAYS('2026-01-01') is 739982 and TO_SECONDS of its midnight is
		// 63934444800, read from the server, so each bound is the start of 2026.
		"range by days":    mysql.PartitionByRange(mysql.PartitionToDays(at)).With(mysql.LessThan("p2025", 739982), mysql.LessThanMax("pmax")),
		"range by seconds": mysql.PartitionByRange(mysql.PartitionToSeconds(at)).With(mysql.LessThan("p2025", 63934444800), mysql.LessThanMax("pmax")),
		"range columns":    mysql.PartitionByRangeColumns(year, region).With(mysql.LessThan("p2025", 2026, "a"), mysql.LessThanMax("pmax")),
		"list":             mysql.PartitionByList(mysql.PartitionOn(year)).With(mysql.In("p2025", 2025), mysql.In("pmax", 2026)),
		"list columns":     mysql.PartitionByListColumns(region).With(mysql.In("p2025", "eu"), mysql.In("pmax", "us")),
		"hash":             mysql.PartitionByHash(mysql.PartitionOn(year), 2),
		"key":              mysql.PartitionByKey(2, id),
	} {
		t.Run(name, func(t *testing.T) {
			cleanup(t, db, "DROP TABLE IF EXISTS pt_orders")
			_, _ = db.Exec("DROP TABLE IF EXISTS pt_orders")
			runDDL(t, db, mysql.CreateTable(tb).Column(cols[0], cols[1:]...).Clause(pk, p))
			insert(`(1, 2025, '2025-06-01 00:00:00', 'eu')`, `(2, 2026, '2026-06-01 00:00:00', 'us')`)
			got := layout()
			switch name {
			case "hash", "key":
				if n := myScalar(t, db, `SELECT count(*) FROM information_schema.partitions WHERE table_schema = DATABASE() AND table_name = 'pt_orders'`); n != "2" {
					t.Errorf("partitions %q", n)
				}
			default:
				if got != "p2025=1,pmax=1" {
					t.Errorf("rows landed %q, want one in each", got)
				}
			}
		})
	}

	t.Run("maintenance", func(t *testing.T) {
		cleanup(t, db, "DROP TABLE IF EXISTS pt_orders")
		_, _ = db.Exec("DROP TABLE IF EXISTS pt_orders")
		runDDL(t, db, mysql.CreateTable(tb).Column(cols[0], cols[1:]...).Clause(pk,
			mysql.PartitionByRange(mysql.PartitionOn(year)).With(mysql.LessThan("p2025", 2026), mysql.LessThanMax("pmax"))))
		insert(`(1, 2025, '2025-06-01', 'eu')`, `(2, 2026, '2026-06-01', 'us')`, `(3, 2030, '2030-06-01', 'us')`)
		// A new year arrives: split MAXVALUE's partition.
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.ReorganizePartitions([]string{"pmax"}, mysql.LessThan("p2026", 2027), mysql.LessThanMax("pmax"))))
		insert()
		if got := layout(); got != "p2025=1,p2026=1,pmax=1" {
			t.Fatalf("after reorganize %q", got)
		}
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.TruncatePartitions("p2025")))
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.DropPartitions("pmax")))
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.AddPartitions(mysql.LessThan("p2027", 2028))))
		insert()
		if got := layout(); got != "p2025=0,p2026=1,p2027=0" {
			t.Fatalf("after truncate, drop and add %q", got)
		}
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.RemovePartitioning()))
		if n := myScalar(t, db, `SELECT count(*) FROM pt_orders`); n != "1" {
			t.Errorf("rows kept by REMOVE PARTITIONING %q", n)
		}
	})

	t.Run("hash maintenance", func(t *testing.T) {
		cleanup(t, db, "DROP TABLE IF EXISTS pt_orders")
		_, _ = db.Exec("DROP TABLE IF EXISTS pt_orders")
		runDDL(t, db, mysql.CreateTable(tb).Column(cols[0], cols[1:]...).Clause(pk, mysql.PartitionByKey(3, id)))
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.CoalescePartitions(1)))
		runDDL(t, db, mysql.AlterTable(tb).Clause(mysql.AddHashPartitions(2)))
		if n := myScalar(t, db, `SELECT count(*) FROM information_schema.partitions WHERE table_schema = DATABASE() AND table_name = 'pt_orders'`); n != "4" {
			t.Errorf("partitions after coalesce and add %q", n)
		}
	})
}
