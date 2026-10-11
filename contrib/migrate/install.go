package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/schema"
)

// Table is a library's CREATE TABLE, as the ddl package and an engine package
// build it: pg.CreateTable(t)... satisfies it. It is what Install takes, so a
// table is declared once, in the DDL a library already writes, and its
// description for ordering is derived from that statement rather than
// written beside it.
type Table interface {
	Build() (ddl.Script, error)
	Describe() (schema.Table, error)
}

// Contribution is one package's tables, under the owner name a duplicate is
// reported against.
type Contribution struct {
	Owner  string
	Tables []Table
}

// Install orders every contributed CREATE TABLE so each table is created after
// the tables its foreign keys reference, across contributors that never
// imported each other, and returns the statements as one Script.
//
// The ordering is schema.Set.Sorted over each statement's Describe, so it is
// the same deterministic, cycle-checked order Plan computes from a declared
// Set. A table two contributors both create is refused with
// schema.ErrDuplicateTable naming both owners, and a foreign key to a table
// no contribution creates with schema.ErrMissingTable: Install is for a whole
// database, installed together. A nil table is ErrNilTable rather than a
// panic, since a Tables slice built conditionally is where one comes from.
// No contributions give an empty Script.
func Install(contribs ...Contribution) (ddl.Script, error) {
	byKey := map[string]Table{}
	set, err := schema.New("")
	if err != nil {
		return nil, err
	}
	for _, c := range contribs {
		for i, t := range c.Tables {
			if isNil(t) {
				return nil, fmt.Errorf("%w: %s, table %d", ErrNilTable, c.Owner, i)
			}
			desc, err := t.Describe()
			if err != nil {
				return nil, fmt.Errorf("migrate: %s: %w", c.Owner, err)
			}
			if err := set.Add(c.Owner, desc); err != nil {
				return nil, err
			}
			byKey[ddl.RefTableName(desc.Schema, desc.Name)] = t
		}
	}
	sorted, err := set.Sorted()
	if err != nil {
		return nil, err
	}
	var out ddl.Script
	for _, desc := range sorted {
		s, err := byKey[ddl.RefTableName(desc.Schema, desc.Name)].Build()
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}

// ErrNilTable is returned by Install for a nil entry in a Contribution's
// Tables, whether the interface itself is nil or holds a nil pointer: a
// (*pg.CreateTableReady)(nil) is not == nil as a Table, and calling Describe
// on it panicked.
var ErrNilTable = errors.New("migrate: a contribution holds a nil table")

// isNil reports whether t is nil or wraps a nil pointer, map, slice, func or
// channel, the kinds whose methods can be called on a nil receiver and then
// dereference it.
func isNil(t Table) bool {
	if t == nil {
		return true
	}
	v := reflect.ValueOf(t)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// ErrCheckFailed is returned by Execute when a MustReturnNoRows statement
// returned a row: the check found a problem, SQLite's foreign_key_check a
// broken reference.
var ErrCheckFailed = errors.New("migrate: a check statement returned rows")

// Execute runs a Script on one connection, honouring the three flags a
// Statement carries, and is the reference runner for them:
//
//   - OutsideTransaction statements run on their own, before any transaction
//     is opened and after the one before them commits, because SQLite's
//     PRAGMA foreign_keys is a silent no-op inside one;
//   - statements that are not Transactional, Postgres's CREATE INDEX
//     CONCURRENTLY among them, run alone and outside a transaction;
//   - consecutive transactional statements run in one transaction, committed
//     before the next statement that must stand alone, and rolled back on any
//     failure. On Postgres and SQLite that undoes every statement of the
//     transaction. MySQL commits each DDL statement implicitly, so there a
//     failure leaves the statements before it applied. Running the script
//     again recovers only if each statement can run twice: a CREATE TABLE
//     written with IfNotExists can, a MySQL CREATE INDEX cannot, since MySQL
//     has no IF NOT EXISTS for it (docs/ddl/not-built.md has the check);
//   - a MustReturnNoRows statement is queried, and a row fails the run with
//     ErrCheckFailed, because executing it and ignoring the result reports
//     success whatever it found;
//   - a statement with a SkipIf query is skipped when that query returns a
//     row, which is how MySQL's CREATE INDEX IF NOT EXISTS is written.
//
// It takes a *sql.Conn rather than a pool on purpose. A connection-scoped
// setting such as PRAGMA foreign_keys applies to one connection, and a pool
// is free to run the next statement on another, so a rebuild through a
// *sql.DB could turn enforcement off on one connection and copy the table on a
// different one that still enforces it. ExecuteDB pins a connection for a
// caller holding a pool.
//
// It holds no ledger and takes no lock: which scripts run, once, and in what
// order across deploys is the application's decision, per ADR-0003.
func Execute(ctx context.Context, conn *sql.Conn, s ddl.Script) error {
	return execute(ctx, conn, s, beginDeferred)
}

// txn is the transaction execute runs consecutive transactional statements
// in. *sql.Tx is one; the ledger's SQLite transaction, begun IMMEDIATE on the
// pinned connection because database/sql cannot say IMMEDIATE, is the other.
type txn interface {
	execQuerier
	Commit() error
	Rollback() error
}

// beginner opens the transaction execute uses, which is the one thing the
// ledger changes about how a script runs.
type beginner func(ctx context.Context, conn *sql.Conn) (txn, error)

// beginDeferred is database/sql's own transaction, what Execute has always
// opened. A failed begin returns a nil txn rather than a nil *sql.Tx inside
// one, which would compare as non-nil.
func beginDeferred(ctx context.Context, conn *sql.Conn) (txn, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// execute is Execute with the transaction's opening chosen by the caller.
func execute(ctx context.Context, conn *sql.Conn, s ddl.Script, begin beginner) error {
	var tx txn
	commit := func() error {
		if tx == nil {
			return nil
		}
		err := tx.Commit()
		tx = nil
		return err
	}
	fail := func(err error) error {
		if tx != nil {
			_ = tx.Rollback()
		}
		return err
	}
	for _, st := range s {
		if st.OutsideTransaction || !st.Transactional {
			if err := commit(); err != nil {
				return err
			}
			if err := run(ctx, conn, st); err != nil {
				return err
			}
			continue
		}
		if tx == nil {
			var err error
			if tx, err = begin(ctx, conn); err != nil {
				return err
			}
		}
		if err := run(ctx, tx, st); err != nil {
			return fail(err)
		}
	}
	return commit()
}

// execQuerier is what a statement runs through: a connection or a transaction.
type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func run(ctx context.Context, q execQuerier, st ddl.Statement) error {
	if st.SkipIf != "" {
		skip, err := returnsRow(ctx, q, st.SkipIf)
		if err != nil {
			return fmt.Errorf("migrate: %s: %w", st.SkipIf, err)
		}
		if skip {
			return nil
		}
	}
	if !st.MustReturnNoRows {
		if _, err := q.ExecContext(ctx, st.SQL); err != nil {
			return fmt.Errorf("migrate: %s: %w", st.SQL, err)
		}
		return nil
	}
	rows, err := q.QueryContext(ctx, st.SQL)
	if err != nil {
		return fmt.Errorf("migrate: %s: %w", st.SQL, err)
	}
	found := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if found {
		return fmt.Errorf("%w: %s", ErrCheckFailed, st.SQL)
	}
	return nil
}

// returnsRow reports whether query returns at least one row.
func returnsRow(ctx context.Context, q execQuerier, query string) (bool, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return false, err
	}
	found := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	return found, nil
}

// ExecuteDB pins one connection from db for the whole script and runs Execute
// on it.
func ExecuteDB(ctx context.Context, db *sql.DB, s ddl.Script) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return Execute(ctx, conn, s)
}
