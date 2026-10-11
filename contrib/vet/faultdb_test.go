package vet_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
)

// errUnexpected is a query no fakeQuery matched. A fault test requires its
// error not to be this one, so a case that failed only because the test was
// wrong does not pass as one that failed where it meant to.
var errUnexpected = errors.New("faultdb: unexpected query")

// faultDB is a database/sql driver that serves canned rows and fails where a
// test says, so the catalogue readers' parsing and every error branch run
// without a server. A query is matched by the first entry whose match is a
// substring of its text; an unmatched query is an error, so a reader asking
// something the test did not expect fails loudly rather than reading nothing.
type faultDB struct {
	queries    []fakeQuery
	connectErr error
}

// fakeQuery is one canned answer. err fails the query itself; failAt, when
// non-zero, fails the failAt'th row (1-based) with rowErr, the mid-read
// failure a network drop gives; closeErr fails Rows.Close.
type fakeQuery struct {
	match    string
	cols     []string
	rows     [][]driver.Value
	err      error
	failAt   int
	rowErr   error
	closeErr error
}

func (f *faultDB) open() *sql.DB { return sql.OpenDB(fakeConnector{f}) }

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
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	for _, q := range c.db.queries {
		if strings.Contains(query, q.match) {
			if q.err != nil {
				return nil, q.err
			}
			return &fakeRows{q: q}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errUnexpected, query)
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeRows struct {
	q fakeQuery
	i int
}

func (r *fakeRows) Columns() []string { return r.q.cols }
func (r *fakeRows) Close() error      { return r.q.closeErr }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.q.failAt != 0 && r.i+1 == r.q.failAt {
		return r.q.rowErr
	}
	if r.i >= len(r.q.rows) {
		return io.EOF
	}
	copy(dest, r.q.rows[r.i])
	r.i++
	return nil
}
