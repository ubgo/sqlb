package driverpgx_test

import (
	"database/sql"
	"encoding/json"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// This executes the built test binary, so a missing environment variable must
// produce an observable skip, not an accidental connection or a silent pass.
// promise:pg-skip
func TestPromise_PostgresSuiteSkipsWithoutDSN(t *testing.T) {
	cmd := osexec.Command(os.Args[0], "-test.run=^TestConformance$", "-test.v")
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "SQLB_PG_DSN=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("missing-DSN probe: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- SKIP: TestConformance") || !strings.Contains(string(out), "SQLB_PG_DSN not set") {
		t.Fatalf("suite did not report its missing database: %s", out)
	}
}

// promise:pg-websearch
func TestPromise_WebsearchAcceptsMalformedQueryText(t *testing.T) {
	db := openOrSkip(t)
	for _, input := range []string{"", `"unterminated`, `((( & | ! :`, `foo OR -`, `'; DROP TABLE users; --`} {
		q, err := pg.Select(sqlb.Cast[string](pg.WebsearchToTSQuery("english", input), "text")).Build()
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := db.QueryRow(q.SQL, q.Args...).Scan(&got); err != nil {
			t.Errorf("websearch input %q: %v", input, err)
		}
	}
}

// promise:pg-json-wrapper
func TestPromise_JSONWrapperWrapsMatchedResult(t *testing.T) {
	db := openOrSkip(t)
	for _, doc := range []string{`{"v": 1}`, `{"v": {"a": 2}}`, `{"v": [1, 2]}`} {
		q, err := pg.Select(pg.JSONQuery(sqlb.Param(json.RawMessage(doc)), "$.v", pg.WithWrapper())).Build()
		if err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if err := db.QueryRow(q.SQL, q.Args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result []json.RawMessage
		if err := json.Unmarshal(raw, &result); err != nil || len(result) != 1 {
			t.Fatalf("wrapper for %s: %s, %v", doc, raw, err)
		}
	}
}

// promise:pg-cascade-live
func TestPromise_CascadePreservesReferencingTable(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		parent := sqlb.Table{Name: "promise_parent"}
		child := sqlb.Table{Name: "promise_child"}
		pid := sqlb.Col[int64](parent, "id")
		cid := sqlb.Col[int64](child, "parent_id")
		runB(t, tx, pg.CreateTable(parent).Column(ddl.Column(pid)).Clause(pg.PrimaryKey(pid)))
		runB(t, tx, pg.CreateTable(child).Column(ddl.Column(cid)).Clause(pg.References(cid, pid)))
		if _, err := tx.Exec(`INSERT INTO promise_parent VALUES (1); INSERT INTO promise_child VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		runB(t, tx, pg.DropTable(parent).Clause(pg.Cascade()))
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM promise_child`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("referencing table or its rows lost: %d, %v", n, err)
		}
		if _, err := tx.Exec(`INSERT INTO promise_child VALUES (99)`); err != nil {
			t.Fatalf("foreign key survived its referenced table: %v", err)
		}
	})
}
