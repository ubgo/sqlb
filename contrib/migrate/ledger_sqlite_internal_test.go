package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

// codedErr is a driver error exposing SQLite's result code the way
// modernc.org/sqlite's does.
type codedErr int

func (e codedErr) Error() string { return fmt.Sprintf("sqlite error %d", int(e)) }
func (e codedErr) Code() int     { return int(e) }

// TestIsSQLiteBusy pins how busy is recognised without importing a driver:
// by the primary code in the low byte when the error has one, extended codes
// included, and by SQLite's message otherwise. A coded error that is not busy
// is not busy even if its message mentions a lock.
func TestIsSQLiteBusy(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		busy bool
	}{
		"nil":                  {nil, false},
		"SQLITE_BUSY":          {codedErr(5), true},
		"SQLITE_BUSY_RECOVERY": {codedErr(261), true},
		"SQLITE_BUSY_SNAPSHOT": {codedErr(517), true},
		"wrapped":              {fmt.Errorf("open: %w", codedErr(5)), true},
		"SQLITE_ERROR":         {codedErr(1), false},
		"SQLITE_LOCKED":        {codedErr(6), false},
		"message only":         {errors.New("database is locked"), true},
		"another message":      {errors.New("no such table: x"), false},
	} {
		if got := isSQLiteBusy(c.err); got != c.busy {
			t.Errorf("%s: busy %v, want %v", name, got, c.busy)
		}
	}
}

// TestRetryBusy pins the three ways the retry ends: success after busy, a
// different error at once, and a deadline that passes while still busy,
// which is ErrLocked carrying the busy error.
func TestRetryBusy(t *testing.T) {
	ctx := context.Background()
	calls := 0
	if err := retryBusy(ctx, func() error {
		calls++
		if calls < 3 {
			return codedErr(5)
		}
		return nil
	}); err != nil || calls != 3 {
		t.Errorf("busy twice then success: %v after %d calls", err, calls)
	}
	calls = 0
	other := errors.New("no such table")
	if err := retryBusy(ctx, func() error { calls++; return other }); !errors.Is(err, other) || calls != 1 {
		t.Errorf("another error must return at once: %v after %d calls", err, calls)
	}
	short, cancel := context.WithTimeout(ctx, 3*lockPoll)
	defer cancel()
	start := time.Now()
	err := retryBusy(short, func() error { return codedErr(5) })
	if !errors.Is(err, ErrLocked) || !errors.Is(err, context.DeadlineExceeded) || !isSQLiteBusy(err) {
		t.Errorf("busy past the deadline: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the retry outlived its deadline: %v", time.Since(start))
	}
}

// fakeConn is a txConn whose ExecContext answers each statement from a
// script of errors, recording what it was asked, and whose Raw records the
// error its callback returned.
type fakeConn struct {
	fail    map[string][]error // per statement, the errors to return in turn; nil once exhausted
	ran     []string
	rawErrs []error
}

func (f *fakeConn) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	f.ran = append(f.ran, query)
	if errs := f.fail[query]; len(errs) > 0 {
		f.fail[query] = errs[1:]
		return nil, errs[0]
	}
	return nil, nil
}

func (f *fakeConn) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("fakeConn: no queries")
}

func (f *fakeConn) Raw(fn func(any) error) error {
	err := fn(nil)
	f.rawErrs = append(f.rawErrs, err)
	return err
}

// TestImmediateTx_CommitAndRollback pins the three endings of the ledger's
// SQLite transaction: a COMMIT refused as busy is committed again, since
// SQLite leaves the transaction open; a COMMIT that fails otherwise is rolled
// back, so the connection is not left inside a transaction; and a ROLLBACK
// that fails discards the connection through Raw with driver.ErrBadConn
// rather than returning it to the pool still holding the write lock.
func TestImmediateTx_CommitAndRollback(t *testing.T) {
	ctx := context.Background()

	busy := &fakeConn{fail: map[string][]error{sqliteCommit: {codedErr(5), codedErr(5)}}}
	if err := (immediateTx{ctx: ctx, conn: busy}).Commit(); err != nil {
		t.Fatalf("a busy COMMIT must be retried: %v", err)
	}
	if want := []string{sqliteCommit, sqliteCommit, sqliteCommit}; !slices.Equal(busy.ran, want) {
		t.Errorf("ran %v, want %v", busy.ran, want)
	}

	full := errors.New("disk full")
	failed := &fakeConn{fail: map[string][]error{sqliteCommit: {full}}}
	if err := (immediateTx{ctx: ctx, conn: failed}).Commit(); !errors.Is(err, full) {
		t.Fatalf("a failed COMMIT: %v", err)
	}
	if want := []string{sqliteCommit, sqliteRollback}; !slices.Equal(failed.ran, want) {
		t.Errorf("a failed COMMIT ran %v, want %v", failed.ran, want)
	}
	if len(failed.rawErrs) != 0 {
		t.Error("a connection whose rollback succeeded was discarded")
	}

	stuck := errors.New("cannot roll back")
	poisoned := &fakeConn{fail: map[string][]error{sqliteRollback: {stuck}}}
	if err := (immediateTx{ctx: ctx, conn: poisoned}).Rollback(); !errors.Is(err, stuck) {
		t.Fatalf("a failed ROLLBACK: %v", err)
	}
	if len(poisoned.rawErrs) != 1 || !errors.Is(poisoned.rawErrs[0], driver.ErrBadConn) {
		t.Errorf("a connection whose rollback failed must be discarded with driver.ErrBadConn: %v", poisoned.rawErrs)
	}
}
