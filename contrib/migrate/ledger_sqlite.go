package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Two deploys applying migrations to one SQLite file at once must not fail
// each other with SQLITE_BUSY, and a caller must not need a retry loop around
// the ledger for that to hold (issue #2). Two places produced the error, both
// measured with two handles on one file under WAL and a busy timeout:
//
//   - The connection ApplyDB opens. On a database not yet in WAL mode the
//     driver's PRAGMA journal_mode=WAL needs every other connection out of the
//     way and fails at once whatever busy_timeout says, so two processes
//     opening a fresh file together lost one of them about one run in five.
//     ApplyDB and AppliedDB retry the open while the error is SQLite's busy.
//   - A deferred transaction whose first statement reads, a SkipIf guard or a
//     migration that selects before it writes. It holds a read lock, and its
//     first write needs the write lock; while another connection holds that,
//     SQLite refuses the upgrade at once, without consulting busy_timeout,
//     because two readers waiting to upgrade would deadlock. The ledger
//     therefore begins its SQLite transactions IMMEDIATE, taking the write
//     lock before reading anything, so any wait happens at BEGIN where the
//     busy timeout applies.
//
// BEGIN and COMMIT are also retried on busy, so a handle opened with no busy
// timeout still waits rather than failing on the ledger's own lock polling:
// BEGIN changes nothing when refused, and SQLite documents that a COMMIT
// refused with SQLITE_BUSY leaves the transaction open to be committed again.
// Nothing else is retried, because re-running a statement is only safe when a
// refusal provably did nothing.

// The statements that open and close the ledger's SQLite transaction. They
// are written by hand rather than through database/sql's BeginTx because
// BeginTx has no way to ask for IMMEDIATE, and once BEGIN is a statement the
// statements that end it must be too.
const (
	sqliteBeginImmediate = "BEGIN IMMEDIATE"
	sqliteCommit         = "COMMIT"
	sqliteRollback       = "ROLLBACK"
)

// sqliteBusy is SQLite's primary result code SQLITE_BUSY. The extended codes,
// SQLITE_BUSY_RECOVERY and SQLITE_BUSY_SNAPSHOT among them, keep it in the low
// byte, which is why the check masks.
const sqliteBusy = 5

// sqliteBusyText is how a driver that exposes no result code spells
// SQLITE_BUSY: SQLite's own message for it, which every driver passes on.
const sqliteBusyText = "database is locked"

// isSQLiteBusy reports whether err is SQLite refusing for contention. The
// module cannot import a driver, so it recognises the error by shape: a
// Code() int method, which modernc.org/sqlite's error has, and otherwise
// SQLite's message, which mattn/go-sqlite3's carries.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return coded.Code()&0xff == sqliteBusy
	}
	return strings.Contains(err.Error(), sqliteBusyText)
}

// retryBusy runs op until it succeeds, fails with something other than
// SQLite's busy, or the wait runs out: ctx's deadline, or DefaultLockWait when
// ctx has none, so a database that stays locked fails with ErrLocked rather
// than hanging a deploy for ever.
func retryBusy(ctx context.Context, op func() error) error {
	giveUp := time.Now().Add(DefaultLockWait)
	_, hasDeadline := ctx.Deadline()
	for {
		err := op()
		if !isSQLiteBusy(err) {
			return err
		}
		if !hasDeadline && time.Now().After(giveUp) {
			return fmt.Errorf("%w: SQLite stayed busy for %s: %w", ErrLocked, DefaultLockWait, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w: %w", ErrLocked, ctx.Err(), err)
		case <-time.After(lockPoll):
		}
	}
}

// pinConn takes one connection from db for the ledger. On SQLite opening it
// can be refused for contention, see above, and is retried.
func (l Ledger) pinConn(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	if !l.isSQLite() {
		return db.Conn(ctx)
	}
	var conn *sql.Conn
	err := retryBusy(ctx, func() error {
		var err error
		conn, err = db.Conn(ctx)
		return err
	})
	return conn, err
}

// beginner is how the ledger opens its transactions: IMMEDIATE on SQLite,
// database/sql's own elsewhere, where the engine locks rows rather than the
// file and an upgrade is never refused this way.
func (l Ledger) beginner() beginner {
	if l.isSQLite() {
		return beginImmediate
	}
	return beginDeferred
}

// beginImmediate opens a SQLite transaction holding the write lock from its
// first statement, on the pinned connection. Every statement of the script
// then runs on that connection, which is why the transaction is the
// connection itself and why it must end before the connection is reused.
func beginImmediate(ctx context.Context, conn *sql.Conn) (txn, error) {
	err := retryBusy(ctx, func() error {
		_, err := conn.ExecContext(ctx, sqliteBeginImmediate)
		return err
	})
	if err != nil {
		return nil, err
	}
	return immediateTx{ctx: ctx, conn: conn}, nil
}

// txConn is what an immediateTx needs of its connection. *sql.Conn is the
// only implementation in use; the interface exists so the commit retry and
// the discard after a failed rollback can be tested against a connection that
// fails on demand, which a real SQLite file cannot be made to do reliably.
// The ...any and func(any) parameters are database/sql's own signatures,
// copied so *sql.Conn satisfies the interface unchanged.
type txConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	Raw(f func(driverConn any) error) error
}

// immediateTx is a transaction opened with BEGIN IMMEDIATE: statements run on
// the connection, and COMMIT and ROLLBACK are statements too.
type immediateTx struct {
	// ctx is the context the transaction was begun under. It is kept because
	// Commit and Rollback take none, matching *sql.Tx so execute can treat
	// both transactions alike, and the COMMIT retry must still end with it.
	ctx  context.Context
	conn txConn
}

// ExecContext runs a statement inside the transaction, on its connection.
func (t immediateTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(ctx, query, args...)
}

// QueryContext runs a query inside the transaction, on its connection.
func (t immediateTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(ctx, query, args...)
}

// Commit commits, waiting out busy, and rolls back when the commit fails, so
// a failed commit never leaves the transaction open on the connection.
func (t immediateTx) Commit() error {
	err := retryBusy(t.ctx, func() error {
		_, err := t.conn.ExecContext(t.ctx, sqliteCommit)
		return err
	})
	if err != nil {
		return errors.Join(err, t.Rollback())
	}
	return nil
}

// Rollback rolls back on a context that cannot end, since one that ended may
// be why. If even that fails the connection is discarded rather than returned
// to its pool: a pooled connection left inside a transaction would hold
// SQLite's write lock for as long as the pool keeps it, stopping every other
// writer to the file.
func (t immediateTx) Rollback() error {
	_, err := t.conn.ExecContext(context.WithoutCancel(t.ctx), sqliteRollback)
	if err != nil {
		// Raw with driver.ErrBadConn is database/sql's documented way to
		// have a connection closed instead of reused; its own error is that
		// sentinel, which says nothing the rollback error does not. The
		// callback's parameter is the driver's connection, typed any by
		// database/sql, and is not used.
		_ = t.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return err
}
