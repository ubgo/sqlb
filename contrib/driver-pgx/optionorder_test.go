package driverpgx_test

import (
	"database/sql"
	"testing"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// TestDDL_OptionsInAnyOrderExecute is the end-to-end proof for ddl.OptionRank
// on Postgres: every table option and every column clause that shares a part is
// passed in REVERSE of the grammar's order, and the statement must still run.
//
// Before 2026-09-24 the statement wrote options in the caller's order, so this
// exact call built cleanly and failed on the server with `syntax error at or
// near "USING"`. A rendering test can show the text is reordered; only a
// server can show the reordered text is what Postgres accepts, which is why
// this runs here. The effects are read back from the catalogue rather than
// trusted from a clean Exec.
func TestDDL_OptionsInAnyOrderExecute(t *testing.T) {
	inTx(t, func(tx *sql.Tx) {
		parent := sqlb.Table{Name: "oo_parent"}
		child := sqlb.Table{Name: "oo_child"}
		pid := sqlb.Col[int64](parent, "id")
		cid := sqlb.Col[int64](child, "id")
		body := sqlb.Col[string](child, "body")

		runB(t, tx, pg.CreateTable(parent).Column(ddl.Column(pid)))
		runB(t, tx, pg.CreateTable(child).Column(ddl.Column(cid), ddl.Column(body)).Clause(
			// Table options, reversed.
			pg.TableStorage(pg.IntParameter("fillfactor", 70)),
			pg.TableAccessMethod(pg.Heap),
			pg.Inherits(parent),
			// Column clauses on one column, reversed.
			pg.ColumnCollate(body, "C"),
			pg.ColumnCompression(body, "pglz"),
			pg.ColumnStorage(body, pg.StorageExternal),
		))

		if got := scalar(t, tx, `SELECT array_to_string(reloptions, ',') FROM pg_class WHERE relname = 'oo_child'`); got != "fillfactor=70" {
			t.Errorf("storage parameter %q, want fillfactor=70", got)
		}
		if got := scalar(t, tx, `SELECT inhparent::regclass::text FROM pg_inherits WHERE inhrelid = 'oo_child'::regclass`); got != "oo_parent" {
			t.Errorf("parent %q, want oo_parent", got)
		}
		if got := scalar(t, tx, `SELECT attstorage::text || ':' || attcompression::text || ':' || (SELECT collname FROM pg_collation WHERE oid = attcollation) FROM pg_attribute WHERE attrelid = 'oo_child'::regclass AND attname = 'body'`); got != "e:p:C" {
			t.Errorf("column storage:compression:collation %q, want e:p:C", got)
		}
	})
}
