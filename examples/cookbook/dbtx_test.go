package cookbook_test

import (
	"context"
	"database/sql"
	"errors"
)

// sqlResult satisfies sql.Result for the recording transaction.
type sqlResult struct{}

func (sqlResult) LastInsertId() (int64, error) { return 0, nil }
func (sqlResult) RowsAffected() (int64, error) { return 1, nil }

// errNoQueryInRecipe: the recording transaction executes only; a recipe that
// queries through it is a recipe that needs a real database.
var errNoQueryInRecipe = errors.New("cookbook: recordingTx does not run queries")

func (r *recordingTx) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errNoQueryInRecipe
}
