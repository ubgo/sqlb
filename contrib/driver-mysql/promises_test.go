package drivermysql_test

import (
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
)

// promise:mysql-constraint-kind
func TestPromise_MySQLRejectsWrongConstraintKind(t *testing.T) {
	db := openOrSkip(t)
	table := sqlb.Table{Name: "promise_constraint_kind"}
	id := sqlb.Col[int64](table, "id")
	check := mysql.TableConstraint{Table: table, Name: "promise_positive"}
	key := mysql.TableConstraint{Table: table, Name: "promise_unique"}
	cleanup(t, db, "DROP TABLE IF EXISTS promise_constraint_kind")
	runDDL(t, db, mysql.CreateTable(table).Column(ddl.Column(id)).Clause(mysql.Check(check, sqlb.Greater(id, int64(0))), mysql.Unique(key, id)))
	for _, clause := range []mysql.AlterClause{mysql.DropKey(check), mysql.DropForeignKey(check), mysql.DropCheck(key)} {
		script, err := mysql.AlterTable(table).Clause(clause).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(script[0].SQL); err == nil {
			t.Fatalf("server accepted wrong kind: %s", script[0].SQL)
		}
	}
	// The generic operation works for either declared kind, as documented.
	runDDL(t, db, mysql.AlterTable(table).Clause(mysql.DropConstraint(check)))
	runDDL(t, db, mysql.AlterTable(table).Clause(mysql.DropConstraint(key)))
	if got := myScalar(t, db, `SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND table_name = 'promise_constraint_kind'`); got != "0" {
		t.Fatalf("generic drops left %s constraints", got)
	}
}
