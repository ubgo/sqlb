package pg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
)

// TestPG18_Renders pins the Postgres 18 DDL: virtual generated columns,
// unenforced constraints, ALTER CONSTRAINT in each form, and temporal keys.
func TestPG18_Renders(t *testing.T) {
	rooms := sqlb.Table{Name: "bookings", Alias: "b"}
	room := sqlb.Col[int64](rooms, "room_id")
	during := sqlb.Col[pg.Range[time.Time]](rooms, "during")
	base := sqlb.Col[int64](rooms, "base")
	doubled := sqlb.Col[int64](rooms, "doubled")
	tsrange := ddl.RawType[pg.Range[time.Time]]("tstzrange")
	key := pg.TableConstraint{Table: rooms, Name: "bookings_key"}
	fk := pg.TableConstraint{Table: rooms, Name: "bookings_room_fk"}
	chk := pg.TableConstraint{Table: rooms, Name: "bookings_base_positive"}
	roomsT := sqlb.Table{Name: "rooms", Alias: "r"}
	roomID := sqlb.Col[int64](roomsT, "id")
	roomValid := sqlb.Col[pg.Range[time.Time]](roomsT, "valid")
	cases := map[string]struct {
		got  string
		want string
	}{
		"virtual column": {build(t)(pg.CreateTable(rooms).Column(ddl.Column(base), ddl.Column(doubled)).Clause(pg.GeneratedVirtual(doubled, sqlb.Mul(base, sqlb.Literal[int64]("2")))).Build()),
			`CREATE TABLE "bookings" ("base" BIGINT NOT NULL, "doubled" BIGINT GENERATED ALWAYS AS (("base" * 2)) VIRTUAL)`},
		"temporal primary key and an unenforced check": {build(t)(pg.CreateTable(rooms).Column(ddl.Column(room), ddl.Column(during).As(tsrange), ddl.Column(base)).
			Clause(pg.PrimaryKey(room).Named(key).WithoutOverlaps(pg.Period(during)), pg.Check(chk, sqlb.Greater(base, 0)).NotEnforced()).Build()),
			`CREATE TABLE "bookings" ("room_id" BIGINT NOT NULL, "during" tstzrange NOT NULL, "base" BIGINT NOT NULL, CONSTRAINT "bookings_key" PRIMARY KEY ("room_id", "during" WITHOUT OVERLAPS), CONSTRAINT "bookings_base_positive" CHECK ("base" > 0) NOT ENFORCED)`},
		"temporal foreign key, unenforced": {build(t)(pg.CreateTable(rooms).Column(ddl.Column(room), ddl.Column(during).As(tsrange)).
			Clause(pg.ForeignKey(fk, pg.RefPair(room, roomID)).Period(pg.Period(during), pg.Period(roomValid)).NotEnforced()).Build()),
			`CREATE TABLE "bookings" ("room_id" BIGINT NOT NULL, "during" tstzrange NOT NULL, CONSTRAINT "bookings_room_fk" FOREIGN KEY ("room_id", PERIOD "during") REFERENCES "rooms" ("id", PERIOD "valid") NOT ENFORCED)`},
		"temporal unique key": {build(t)(pg.CreateTable(rooms).Column(ddl.Column(room), ddl.Column(during).As(tsrange)).
			Clause(pg.Unique(key, room).WithoutOverlaps(pg.Period(during))).Build()),
			`CREATE TABLE "bookings" ("room_id" BIGINT NOT NULL, "during" tstzrange NOT NULL, CONSTRAINT "bookings_key" UNIQUE ("room_id", "during" WITHOUT OVERLAPS))`},
		"alter constraint": {build(t)(pg.AlterTable(rooms).Clause(pg.AlterConstraint(fk).Deferrable(pg.InitiallyDeferred).Enforced()).Build()),
			`ALTER TABLE "bookings" ALTER CONSTRAINT "bookings_room_fk" DEFERRABLE INITIALLY DEFERRED ENFORCED`},
		"alter constraint, not deferrable, not enforced": {build(t)(pg.AlterTable(rooms).Clause(pg.AlterConstraint(fk).NotDeferrable().NotEnforced()).Build()),
			`ALTER TABLE "bookings" ALTER CONSTRAINT "bookings_room_fk" NOT DEFERRABLE NOT ENFORCED`},
		"alter constraint inherit": {build(t)(pg.AlterTable(rooms).Clause(pg.AlterConstraint(chk).NoInherit()).Build()),
			`ALTER TABLE "bookings" ALTER CONSTRAINT "bookings_base_positive" NO INHERIT`},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\ngot  %s\nwant %s", name, c.got, c.want)
		}
	}
	if _, err := pg.AlterTable(rooms).Clause(pg.AlterConstraint(chk).Inherit().NotEnforced()).Build(); !errors.Is(err, pg.ErrConstraintAlterMix) {
		t.Errorf("INHERIT with another option: %v", err)
	}
	if _, err := pg.AlterTable(rooms).Clause(pg.AlterConstraint(chk)).Build(); !errors.Is(err, ddl.ErrZeroValue) {
		t.Errorf("ALTER CONSTRAINT with no option: %v", err)
	}
	if _, err := pg.AlterTable(roomsT).Clause(pg.AlterConstraint(chk).Enforced()).Build(); !errors.Is(err, ddl.ErrForeignColumn) {
		t.Errorf("another table's constraint: %v", err)
	}
	if _, err := pg.CreateTable(rooms).Column(ddl.Column(room)).Clause(pg.PrimaryKey(room).WithoutOverlaps(pg.Period(during))).Build(); !errors.Is(err, ddl.ErrUnknownColumn) {
		t.Errorf("a period the table does not define: %v", err)
	}
}
