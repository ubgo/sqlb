package driverpgx_test

import (
	"database/sql"
	"testing"

	"github.com/ubgo/sqlb"
	driverpgx "github.com/ubgo/sqlb/contrib/driver-pgx"
	"github.com/ubgo/sqlb/sqlbtest"
)

// openOrSkip opens the database SQLB_PG_DSN names, or skips loudly.
//
// A skip rather than a pass: a suite that needs a server and has none must
// say so in its output, or a green run means nothing. The gate runs this
// package like every other; whether the proof actually ran is visible in the
// skip count, and the docs say which it was.
func openOrSkip(t *testing.T) *sql.DB {
	t.Helper()
	dsn, ok := driverpgx.DSNFromEnv()
	if !ok {
		t.Skipf("%s not set: the Postgres proof did not run", driverpgx.EnvDSN)
	}
	db, err := driverpgx.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setupDDL is sqlbtest.Schema in Postgres's spelling. sqlb generates no DDL, so
// every integration writes this once, and the conformance suite binds to the
// names it declares.
var setupDDL = []string{
	`DROP TABLE IF EXISTS sqlb_conformance_post`,
	`DROP TABLE IF EXISTS sqlb_conformance_user`,
	`CREATE TABLE sqlb_conformance_user (
		id     BIGINT  PRIMARY KEY,
		email  TEXT    NOT NULL,
		name   TEXT,
		age    BIGINT  NOT NULL,
		active BOOLEAN NOT NULL
	)`,
	`CREATE TABLE sqlb_conformance_post (
		id      BIGINT PRIMARY KEY,
		user_id BIGINT NOT NULL REFERENCES sqlb_conformance_user(id),
		title   TEXT   NOT NULL,
		views   BIGINT NOT NULL
	)`,
}

var teardownDDL = []string{
	`DROP TABLE IF EXISTS sqlb_conformance_post`,
	`DROP TABLE IF EXISTS sqlb_conformance_user`,
}

// TestConformance runs the shared suite against a real Postgres, which is
// the second engine the builder's rendering is proven on rather than asserted.
func TestConformance(t *testing.T) {
	db := openOrSkip(t)
	cfg := sqlbtest.Config{
		DB:       db,
		Dialect:  sqlb.PostgresDialect{},
		Setup:    setupDDL,
		Teardown: teardownDDL,
	}
	sqlbtest.Run(t, cfg)
	sqlbtest.RunReturning(t, cfg, sqlb.PostgresDialect{})
}
