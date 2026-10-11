package driverpgx_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// refused runs q in a savepoint and reports whether the server refused it,
// rolling back to the savepoint so the transaction carries on.
func refused(t *testing.T, tx *sql.Tx, q string) bool {
	t.Helper()
	ctx := context.Background()
	exec(t, tx, "SAVEPOINT probe")
	_, err := tx.ExecContext(ctx, q)
	exec(t, tx, "ROLLBACK TO SAVEPOINT probe")
	return err != nil
}

// TestDDL_PG18Executes runs the Postgres 18 DDL on a Postgres 18 server and
// reads each result back: a virtual column computed when read, a temporal
// primary key refusing an overlapping period, a temporal foreign key refusing
// a period its target does not cover, an unenforced check accepting a row it
// would refuse and then enforced, and ALTER CONSTRAINT in each form. It skips
// loudly on an older server.
func TestDDL_PG18Executes(t *testing.T) {
	db := openOrSkip(t)
	if v := versionOf(t, db); v < 180000 {
		t.Skipf("SKIPPED, NOT PASSED: the Postgres 18 DDL needs Postgres 18, server is %d", v/10000)
	}
	tsrange := ddl.RawType[pg.Range[time.Time]]("tstzrange")
	inTx(t, func(tx *sql.Tx) {
		exec(t, tx, `CREATE EXTENSION IF NOT EXISTS btree_gist`)

		calc := sqlb.Table{Name: "p18_calc", Alias: "c"}
		base := sqlb.Col[int64](calc, "base")
		doubled := sqlb.Col[int64](calc, "doubled")
		runB(t, tx, pg.CreateTable(calc).Column(ddl.Column(base), ddl.Column(doubled)).
			Clause(pg.GeneratedVirtual(doubled, sqlb.Mul(base, sqlb.Literal[int64]("2")))))
		exec(t, tx, `INSERT INTO p18_calc (base) VALUES (3)`)
		if got := scalar(t, tx, `SELECT doubled || ':' || (SELECT attgenerated::text FROM pg_attribute WHERE attrelid = 'p18_calc'::regclass AND attname = 'doubled') FROM p18_calc`); got != "6:v" {
			t.Errorf("virtual column %q", got)
		}

		rooms := sqlb.Table{Name: "p18_rooms", Alias: "r"}
		roomID := sqlb.Col[int64](rooms, "id")
		valid := sqlb.Col[pg.Range[time.Time]](rooms, "valid")
		runB(t, tx, pg.CreateTable(rooms).Column(ddl.Column(roomID), ddl.Column(valid).As(tsrange)).
			Clause(pg.PrimaryKey(roomID).Named(pg.TableConstraint{Table: rooms, Name: "p18_rooms_key"}).WithoutOverlaps(pg.Period(valid))))
		exec(t, tx, `INSERT INTO p18_rooms VALUES (1, '[2026-01-01, 2027-01-01)')`)
		if !refused(t, tx, `INSERT INTO p18_rooms VALUES (1, '[2026-06-01, 2026-07-01)')`) {
			t.Error("the temporal primary key accepted an overlapping period")
		}
		exec(t, tx, `INSERT INTO p18_rooms VALUES (1, '[2027-01-01, 2028-01-01)')`)

		bookings := sqlb.Table{Name: "p18_bookings", Alias: "b"}
		bRoom := sqlb.Col[int64](bookings, "room_id")
		during := sqlb.Col[pg.Range[time.Time]](bookings, "during")
		amount := sqlb.Col[int64](bookings, "amount")
		fk := pg.TableConstraint{Table: bookings, Name: "p18_bookings_room_fk"}
		positive := pg.TableConstraint{Table: bookings, Name: "p18_bookings_amount_positive"}
		runB(t, tx, pg.CreateTable(bookings).Column(ddl.Column(bRoom), ddl.Column(during).As(tsrange), ddl.Column(amount)).
			Clause(pg.ForeignKey(fk, pg.RefPair(bRoom, roomID)).Period(pg.Period(during), pg.Period(valid)).NotEnforced(),
				pg.Check(positive, sqlb.Greater(amount, 0)).NotEnforced()))
		exec(t, tx, `INSERT INTO p18_bookings VALUES (1, '[2026-03-01, 2026-03-05)', 10)`)
		// Neither constraint is enforced: a period no room covers and a
		// negative amount are both accepted.
		exec(t, tx, `INSERT INTO p18_bookings VALUES (1, '[2030-01-01, 2030-01-02)', -5)`)
		// Enforcing the foreign key checks every row and fails on that one.
		s, err := pg.AlterTable(bookings).Clause(pg.AlterConstraint(fk).Enforced()).Build()
		if err != nil {
			t.Fatal(err)
		}
		if !refused(t, tx, s[0].SQL) {
			t.Error("enforcing the foreign key passed over a row it does not cover")
		}
		exec(t, tx, `DELETE FROM p18_bookings WHERE amount < 0`)
		runB(t, tx, pg.AlterTable(bookings).Clause(pg.AlterConstraint(fk).Enforced()))
		if !refused(t, tx, `INSERT INTO p18_bookings VALUES (1, '[2030-01-01, 2030-01-02)', 10)`) {
			t.Error("the enforced temporal foreign key accepted a period no room covers")
		}
		// A check's enforceability cannot change on Postgres 18; the refusal
		// the doc comment records.
		s, err = pg.AlterTable(bookings).Clause(pg.AlterConstraint(positive).Enforced()).Build()
		if err != nil {
			t.Fatal(err)
		}
		if !refused(t, tx, s[0].SQL) {
			t.Error("Postgres 18 changed a check's enforceability; the doc comment is now wrong")
		}
		runB(t, tx, pg.AlterTable(bookings).Clause(pg.AlterConstraint(fk).Deferrable(pg.InitiallyDeferred)))
		if got := scalar(t, tx, `SELECT string_agg(conname || ':' || conenforced, ',' ORDER BY conname) FROM pg_constraint WHERE conname IN ('p18_bookings_room_fk', 'p18_bookings_amount_positive')`); got != "p18_bookings_amount_positive:false,p18_bookings_room_fk:true" {
			t.Errorf("constraint states %q", got)
		}
		if got := scalar(t, tx, `SELECT condeferrable || ':' || condeferred FROM pg_constraint WHERE conname = 'p18_bookings_room_fk'`); got != "true:true" {
			t.Errorf("deferrable foreign key %q", got)
		}
		runB(t, tx, pg.AlterTable(bookings).Clause(pg.AlterConstraint(pg.TableConstraint{Table: bookings, Name: "p18_bookings_amount_not_null"}).NoInherit()))
		if got := scalar(t, tx, `SELECT connoinherit FROM pg_constraint WHERE conname = 'p18_bookings_amount_not_null'`); got != "true" {
			t.Errorf("NO INHERIT on the NOT NULL constraint %q", got)
		}
	})
}
