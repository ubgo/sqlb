package migrate_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// errUnexpected is a statement the fake was not told about. A fault test
// requires its error not to be this one, so a case that failed only because
// the test was wrong does not pass as one that failed where it meant to.
var errUnexpected = errors.New("faultdb: unexpected statement")

// faultDB is a database/sql driver that fails where a test says: a
// connection, a BEGIN, a COMMIT, an Exec or a query matched by substring, a
// row mid-read. Execute's error branches cannot be reached with a real
// database without breaking it, and this reaches every one of them. It
// records what ran, in order, so a test can check what a failure left behind.
type faultDB struct {
	connectErr, beginErr, commitErr error
	exec                            map[string]error // substring → error; nil value is success
	query                           map[string]fakeQuery
	mu                              sync.Mutex
	log                             []string
}

type fakeQuery struct {
	err, rowErr, closeErr error
	rows                  int // how many rows to return
	failAt                int // 1-based row that fails with rowErr
}

func (f *faultDB) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, s)
}

func (f *faultDB) open() *sql.DB {
	db := sql.OpenDB(fakeConnector{f})
	db.SetMaxOpenConns(1)
	return db
}

type fakeConnector struct{ db *faultDB }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	if c.db.connectErr != nil {
		return nil, c.db.connectErr
	}
	return &fakeConn{db: c.db}, nil
}

func (c fakeConnector) Driver() driver.Driver { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("faultdb: use OpenDB") }

type fakeConn struct{ db *faultDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("faultdb: no Prepare")
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.db.beginErr != nil {
		return nil, c.db.beginErr
	}
	c.db.record("BEGIN")
	return fakeTx{db: c.db}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	for match, err := range c.db.exec {
		if strings.Contains(q, match) {
			if err != nil {
				return nil, err
			}
			c.db.record(q)
			return driver.RowsAffected(0), nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errUnexpected, q)
}

func (c *fakeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	for match, fq := range c.db.query {
		if strings.Contains(q, match) {
			if fq.err != nil {
				return nil, fq.err
			}
			c.db.record(q)
			return &fakeRows{q: fq}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errUnexpected, q)
}

type fakeTx struct{ db *faultDB }

func (t fakeTx) Commit() error {
	if t.db.commitErr != nil {
		return t.db.commitErr
	}
	t.db.record("COMMIT")
	return nil
}

func (t fakeTx) Rollback() error { t.db.record("ROLLBACK"); return nil }

type fakeRows struct {
	q fakeQuery
	i int
}

func (r *fakeRows) Columns() []string { return []string{"x"} }
func (r *fakeRows) Close() error      { return r.q.closeErr }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.q.failAt != 0 && r.i+1 == r.q.failAt {
		return r.q.rowErr
	}
	if r.i >= r.q.rows {
		return io.EOF
	}
	dest[0] = int64(1)
	r.i++
	return nil
}
