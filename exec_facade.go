package sqlb

import (
	"context"

	"github.com/ubgo/sqlb/exec"
)

// DBTX is the subset of database/sql that both *sql.DB and *sql.Tx satisfy.
type DBTX = exec.DBTX

// One executes q and scans exactly one row into the destinations bound when the
func One(ctx context.Context, db DBTX, q Query) error { return exec.One(ctx, db, q) }

// Each executes q and scans every row into the bound destinations, calling fn
func Each(ctx context.Context, db DBTX, q Query, fn func() error) error {
	return exec.Each(ctx, db, q, fn)
}

// Variables re-exported from the execution layer.
var (
	// ErrNoRows is returned by One when the statement matched nothing.
	ErrNoRows = exec.ErrNoRows
	// ErrMultipleRows is returned by One when a statement expected to be
	// unique matched more than one row.
	ErrMultipleRows = exec.ErrMultipleRows
)
