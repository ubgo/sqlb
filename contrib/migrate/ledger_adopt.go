package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ubgo/sqlb/ddl"
)

// A library that already keeps its own history of applied migrations, in a
// ledger table of its own, cannot move onto this ledger by running Apply: the
// migrations ran long ago, most are ALTER TABLE ... ADD COLUMN that cannot be
// written to repeat safely, and Apply would run them again. Adopt records
// them as applied without running them. A team that applies schema changes by
// hand or with its own tooling needs the other half: the ledger's own DDL and
// the row each migration records, as statements it can write into a script,
// which Script, TableScript and RecordStatement return. Both are built from
// the same code Apply runs, so a database brought up either way is one Apply
// recognises.

// clock is the ledger's time source. It sits behind a pointer so that a
// Ledger, which held only comparable fields before WithClock existed, can
// still be compared with ==.
type clock struct{ now func() time.Time }

// now is the time the ledger records a migration at: WithClock's source, or
// time.Now.
func (l Ledger) now() time.Time {
	if l.clock == nil {
		return time.Now()
	}
	return l.clock.now()
}

// WithClock returns a copy of l that reads the time it records in applied_at
// from now instead of time.Now, for tests that assert the recorded time and
// for a Script whose text must be the same on every run. A nil now restores
// time.Now. l itself is unchanged.
func (l Ledger) WithClock(now func() time.Time) Ledger {
	if now == nil {
		l.clock = nil
		return l
	}
	l.clock = &clock{now: now}
	return l
}

// Ran is Adopt's question about one migration: did it already run, and when.
// A caller moving from a ledger of its own answers it from that ledger's
// rows. ok false leaves the migration for Apply to run. A zero at records it
// at the ledger's clock, the time of the adoption.
type Ran func(m Migration) (at time.Time, ok bool)

// Adopt records migrations as applied without running them; it returns the
// names it recorded, as "owner/name", in the order given. It is how a
// database whose schema an earlier ledger, or a person, already brought up to
// date moves onto this one: adopt what ran, then Apply runs the rest.
//
// ms is every migration the code knows, not only the ones that ran; ran says
// which of them did, and a nil ran adopts them all. Each row holds
// Checksum(m.Script), the checksum Apply would record, so a later Apply
// recognises the migration and skips it.
//
// It refuses what Apply refuses, before recording anything: a bad migration
// is ErrBadMigration; a migration already recorded with another checksum,
// whether ran selects it or not, is ErrChecksumMismatch; and from a Ledger
// made with RefuseUnknown, a recorded migration ms does not contain, under an
// owner ms does, is ErrUnknownMigration. A migration already recorded with
// the same checksum is left as it is, so adopting twice records nothing the
// second time.
//
// It takes the ledger's lock, as Apply does, so two processes adopting at
// once, or one adopting while another applies, serialize: the second finds
// the first one's rows and records only what is still missing. Every row is
// written in one transaction, on all three engines, since the rows are
// INSERTs rather than DDL: a failure records none of them.
func (l Ledger) Adopt(ctx context.Context, conn *sql.Conn, ran Ran, ms ...Migration) (adopted []string, err error) {
	err = l.locked(ctx, conn, ms, func(done map[[2]string]string, begin beginner) error {
		var rows ddl.Script
		var names []string
		for _, m := range ms {
			sum := Checksum(m.Script)
			if prev, ok := done[[2]string{m.Owner, m.Name}]; ok {
				if prev != sum {
					return fmt.Errorf("%w: %s/%s", ErrChecksumMismatch, m.Owner, m.Name)
				}
				continue
			}
			var at time.Time
			if ran != nil {
				when, ok := ran(m)
				if !ok {
					continue
				}
				at = when
			}
			if at.IsZero() {
				at = l.now()
			}
			row, err := l.record(m, sum, at)
			if err != nil {
				return err
			}
			rows = append(rows, row)
			names = append(names, m.Owner+"/"+m.Name)
		}
		if len(rows) == 0 {
			return nil
		}
		if err := execute(ctx, conn, rows, begin); err != nil {
			return fmt.Errorf("migrate: adopt: %w", err)
		}
		adopted = names
		return nil
	})
	return adopted, err
}

// AdoptDB pins one connection from db and runs Adopt on it, waiting out a
// SQLite open refused for contention as ApplyDB does.
func (l Ledger) AdoptDB(ctx context.Context, db *sql.DB, ran Ran, ms ...Migration) ([]string, error) {
	conn, err := l.pinConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return l.Adopt(ctx, conn, ran, ms...)
}

// TableScript is the statement that creates the ledger table if it is not
// there, CREATE TABLE IF NOT EXISTS for the ledger's dialect: the statement
// Apply runs before anything else.
func (l Ledger) TableScript() (ddl.Script, error) {
	if l.dialect == nil {
		return nil, fmt.Errorf("%w: a zero Ledger; use NewLedger", ErrNoLedgerDialect)
	}
	return l.createLedger()
}

// RecordStatement is the INSERT that records m in the ledger, with every
// value a literal and applied_at from the ledger's clock: the statement Apply
// appends to m's script and Adopt writes. A script that runs m by other means
// and then this statement leaves a database on which Apply skips m.
func (l Ledger) RecordStatement(m Migration) (ddl.Statement, error) {
	if err := validate([]Migration{m}); err != nil {
		return ddl.Statement{}, err
	}
	if l.dialect == nil {
		return ddl.Statement{}, fmt.Errorf("%w: a zero Ledger; use NewLedger", ErrNoLedgerDialect)
	}
	return l.record(m, Checksum(m.Script), l.now())
}

// Script is what Apply would run on a database with an empty ledger, as
// statements rather than run: TableScript, then each migration's statements
// followed by its RecordStatement, in the order given. It is for a team that
// applies schema changes by hand or through its own tooling; after the script
// has run, Apply with the same migrations applies nothing and reports no
// mismatch.
//
// It reads no database, so it records every migration given: pass only the
// ones the target database lacks. It takes no lock, and RefuseUnknown does
// not apply, since there is no ledger to read; whoever runs the script keeps
// it from running beside a deploy. Each statement keeps its flags, so a
// statement that must run outside a transaction, is a check, or has a SkipIf
// guard needs a runner that honours them, as Execute does; joined as plain
// text those flags are lost, and a MySQL CREATE INDEX IF NOT EXISTS, whose
// guard is its SkipIf query, runs unguarded.
func (l Ledger) Script(ms ...Migration) (ddl.Script, error) {
	if err := validate(ms); err != nil {
		return nil, err
	}
	out, err := l.TableScript()
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		s, err := l.withRecord(m, Checksum(m.Script), l.now())
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}
