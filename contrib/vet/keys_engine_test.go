package vet_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/sqlb/contrib/vet"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/expr"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// A parent and a child with every default kind the DDL writes, a named unique
// key and a foreign key with an action, declared once and created on each
// engine by that engine's builders.
var (
	kParent   = expr.Table{Name: "k_parent", Alias: "p"}
	kParentID = expr.Col[int64](kParent, "id")
	kChild    = expr.Table{Name: "k_child", Alias: "c"}
	kID       = expr.Col[int64](kChild, "id")
	kParentFK = expr.Col[int64](kChild, "parent_id")
	kCode     = expr.Col[string](kChild, "code")
	kStatus   = expr.Col[string](kChild, "status")
	kN        = expr.Col[int64](kChild, "n")
	kFlag     = expr.Col[bool](kChild, "flag")
	kF        = expr.Col[float64](kChild, "f")
	kAt       = expr.Col[time.Time](kChild, "at")
	kStamp    = expr.Col[time.Time](kChild, "stamp")
	kStampAt  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	kDec      = expr.Col[float64](kChild, "dec")
	kDoc      = expr.Col[json.RawMessage](kChild, "doc")
	// kDocDef is the JSON default, written compact so Postgres's normalised
	// reprint, {"a": 1}, has to be compared as JSON to match.
	kDocDef = ddl.Column(kDoc).As(ddl.Portable[json.RawMessage](schema.TypeJSON)).Default(json.RawMessage(`{"a":1}`))
)

type tableStmt interface {
	Build() (ddl.Script, error)
	Describe() (schema.Table, error)
}

// keysCheck creates the tables, requires CheckTables to find nothing, applies
// the drift, and requires exactly the findings named: each want is a
// substring of one finding, and every finding matches one want.
func keysCheck(t *testing.T, db *sql.DB, dialect string, stmts []tableStmt, drift []string, want []string) {
	t.Helper()
	ctx := context.Background()
	var descs []schema.Table
	for _, st := range stmts {
		s, err := st.Build()
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range s {
			if _, err := db.ExecContext(ctx, x.SQL); err != nil {
				t.Fatalf("%s: %v", x.SQL, err)
			}
		}
		d, err := st.Describe()
		if err != nil {
			t.Fatal(err)
		}
		descs = append(descs, d)
	}
	got, err := vet.CheckTables(ctx, db, dialect, descs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("the tables the DDL just created differ from it: %v", got)
	}
	for _, q := range drift {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	got, err = vet.CheckTables(ctx, db, dialect, descs)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range got {
		kind := "error"
		if f.Warning {
			kind = "warning"
		}
		lines = append(lines, kind+" "+f.Pos+": "+f.Message)
	}
	used := make([]bool, len(lines))
	for _, w := range want {
		found := false
		for i, l := range lines {
			if !used[i] && strings.Contains(l, w) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			t.Errorf("no finding contains %q", w)
		}
	}
	for i, l := range lines {
		if !used[i] {
			t.Errorf("unexpected finding: %s", l)
		}
	}
}

// The drift every engine is given, as the findings it must produce.
var keysDriftWant = []string{
	"error k_child.status: the DDL's default is 'act''ive', the database's is",
	"error k_child.n: the DDL's default is -5, the database's is none",
	"error k_child: the DDL's unique index on (code) is not in the database",
	"error k_child: the foreign key on (parent_id) is ON DELETE CASCADE in the DDL and RESTRICT in the database",
	"warning k_child: the database has a unique index on (n) the DDL does not create",
}

func TestCheckTables_KeysAndDefaults_SQLite(t *testing.T) {
	db := memorySQLite(t)
	parent := sqlite.CreateTable(kParent).Column(ddl.Column(kParentID)).Clause(sqlite.PrimaryKey(kParentID))
	child := sqlite.CreateTable(kChild).Column(
		ddl.Column(kID), ddl.Column(kParentFK), ddl.Column(kCode),
		ddl.Column(kStatus).Default("act'ive"), ddl.Column(kN).Default(-5), ddl.Column(kFlag).Default(true),
		kDocDef, ddl.Column(kF).Default(1.5), ddl.Column(kAt).As(ddl.RawType[time.Time]("TEXT")).DefaultExpr(expr.Literal[time.Time]("datetime('now')")),
	).Clause(sqlite.PrimaryKey(kID), sqlite.Unique(kCode).Named(sqlite.TableConstraint{Table: kCode.Table(), Name: "k_child_code"}),
		sqlite.ForeignKey(sqlite.TableConstraint{Table: kParentFK.Table(), Name: "k_child_parent_fk"}, sqlite.RefPair(kParentFK, kParentID)).OnDelete(sqlite.RefCascade))
	// SQLite alters none of this, so the drift is the table recreated.
	keysCheck(t, db, render.DialectSQLite, []tableStmt{parent, child}, []string{
		`DROP TABLE k_child`,
		`CREATE TABLE k_child (id INTEGER NOT NULL, parent_id INTEGER NOT NULL, code TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'x', n INTEGER NOT NULL, flag INTEGER NOT NULL DEFAULT 1, doc TEXT NOT NULL DEFAULT ('{"a":1}'), f FLOAT NOT NULL DEFAULT 1.5,
			at TEXT NOT NULL DEFAULT (datetime('now')), PRIMARY KEY (id),
			FOREIGN KEY (parent_id) REFERENCES k_parent (id) ON DELETE RESTRICT)`,
		`CREATE UNIQUE INDEX k_extra ON k_child (n)`,
	}, append(keysDriftWant, "error k_child.f: the DDL says float, the database has float"))
}

func TestCheckTables_KeysAndDefaults_Postgres(t *testing.T) {
	dsn := os.Getenv("SQLB_PG_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_PG_DSN to run the Postgres key check")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS k_child, k_parent`); _ = db.Close() })
	parent := pg.CreateTable(kParent).Column(ddl.Column(kParentID)).Clause(pg.PrimaryKey(kParentID))
	child := pg.CreateTable(kChild).Column(
		ddl.Column(kID), ddl.Column(kParentFK), ddl.Column(kCode),
		ddl.Column(kStatus).Default("act'ive"), ddl.Column(kN).Default(-5), ddl.Column(kFlag).Default(true),
		kDocDef, ddl.Column(kF).Default(1.5), ddl.Column(kAt).DefaultExpr(pg.Now()), ddl.Column(kStamp).Default(kStampAt),
	).Clause(pg.PrimaryKey(kID), pg.Unique(pg.TableConstraint{Table: kCode.Table(), Name: "k_child_code"}, kCode),
		pg.ForeignKey(pg.TableConstraint{Table: kParentFK.Table(), Name: "k_child_parent_fk"}, pg.RefPair(kParentFK, kParentID)).OnDelete(pg.RefCascade))
	keysCheck(t, db, render.DialectPostgres, []tableStmt{parent, child}, []string{
		`ALTER TABLE k_child ALTER COLUMN status SET DEFAULT 'x', ALTER COLUMN n DROP DEFAULT, DROP CONSTRAINT k_child_code,
			DROP CONSTRAINT k_child_parent_fk, ADD CONSTRAINT k_child_parent_fk FOREIGN KEY (parent_id) REFERENCES k_parent (id) ON DELETE RESTRICT`,
		`CREATE UNIQUE INDEX k_extra ON k_child (n)`,
		// The same instant with its time zone dropped reads back differently:
		// the type drift the lenient readings used to pass.
		`ALTER TABLE k_child ALTER COLUMN stamp TYPE timestamp`,
	}, append(keysDriftWant, "error k_child.stamp: the DDL says timestamp, the database has timestamp without time zone"))
}

func TestCheckTables_KeysAndDefaults_MySQL(t *testing.T) {
	dsn := os.Getenv("SQLB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("SKIPPED, NOT PASSED: set SQLB_MYSQL_DSN to run the MySQL key check")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS k_child, k_parent`); _ = db.Close() })
	parent := mysql.CreateTable(kParent).Column(ddl.Column(kParentID)).Clause(mysql.PrimaryKey(kParentID))
	child := mysql.CreateTable(kChild).Column(
		ddl.Column(kID), ddl.Column(kParentFK), ddl.Column(kCode),
		ddl.Column(kStatus).Default("act'ive"), ddl.Column(kN).Default(-5), ddl.Column(kFlag).Default(true),
		kDocDef, ddl.Column(kF).Default(1.5), ddl.Column(kAt).DefaultExpr(mysql.UTCNow()), ddl.Column(kStamp).Default(kStampAt),
		// MySQL reads DEFAULT 2.5 on a DECIMAL(10,2) back as 2.50.
		ddl.Column(kDec).As(ddl.RawType[float64]("DECIMAL(10,2)")).Default(2.5),
	).Clause(mysql.PrimaryKey(kID), mysql.Unique(mysql.TableConstraint{Table: kCode.Table(), Name: "k_child_code"}, kCode),
		mysql.ForeignKey(mysql.TableConstraint{Table: kParentFK.Table(), Name: "k_child_parent_fk"}, mysql.RefPair(kParentFK, kParentID)).OnDelete(mysql.RefCascade))
	keysCheck(t, db, render.DialectMySQL, []tableStmt{parent, child}, []string{
		"ALTER TABLE k_child ALTER COLUMN status SET DEFAULT 'x', ALTER COLUMN n DROP DEFAULT, DROP INDEX k_child_code, DROP FOREIGN KEY k_child_parent_fk",
		"ALTER TABLE k_child ADD CONSTRAINT k_child_parent_fk FOREIGN KEY (parent_id) REFERENCES k_parent (id) ON DELETE RESTRICT",
		"CREATE UNIQUE INDEX k_extra ON k_child (n)",
		"ALTER TABLE k_child MODIFY COLUMN f FLOAT NOT NULL DEFAULT 1.5",
	}, append(keysDriftWant, "error k_child.f: the DDL says float, the database has float"))
}
