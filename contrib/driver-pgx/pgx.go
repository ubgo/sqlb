// Package driverpgx opens Postgres databases for github.com/ubgo/sqlb.
//
// It is a separate Go module inside the sqlb repository for the reason every
// contrib module is: the core depends on nothing but the standard library, so
// a library that embeds it inherits no driver. A driver is a dependency, so it
// lives out here, and an application pays for it only by importing this
// module.
//
// The module's real work is its tests. The pg package holds five hundred
// exported constructs and, until this module existed, not one of them had run
// against a Postgres server: rendered text was checked and nothing else. A
// comparison with another Go builder showed exactly what that hides — a table
// declared without an
// alias rendered a quoted empty identifier, which SQLite accepts and Postgres
// rejects, so every test stayed green while the SQL was invalid on the engine
// the package is named after. This module runs the shared conformance suite
// and executes the pg constructs against a real server, which is the only
// thing that turns an asserted construct into a proven one.
//
// The driver is github.com/jackc/pgx/v5 through its database/sql adapter, so
// exec.DBTX is satisfied by *sql.DB and *sql.Tx exactly as it is for SQLite.
package driverpgx

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	// The adapter registers itself under DriverName.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// DriverName is the database/sql name the pgx stdlib adapter registers.
const DriverName = "pgx"

// EnvDSN is the environment variable the tests read the connection string
// from. Absence is a loud skip, never a silent pass: a test that needs a
// server and has none says so in its output rather than reporting success
// for work it did not do.
const EnvDSN = "SQLB_PG_DSN"

// Defaults applied to every pool Open creates. They are the settings a
// process whose libraries own tables wants, and each is overridable.
const (
	// DefaultMaxOpenConns caps the pool. Postgres connections are processes
	// on the server, so an uncapped pool under load exhausts the server's
	// max_connections before it exhausts anything on the client.
	DefaultMaxOpenConns = 8
	// DefaultConnMaxLifetime recycles connections so a server restart or a
	// pooler in front of it does not leave the pool holding dead sockets.
	DefaultConnMaxLifetime = 30 * time.Minute
)

// Option adjusts what Open does.
type Option func(*config)

type config struct {
	maxOpen  int
	lifetime time.Duration
}

// WithMaxOpenConns caps the pool at n connections.
func WithMaxOpenConns(n int) Option { return func(c *config) { c.maxOpen = n } }

// WithConnMaxLifetime sets how long a connection may be reused.
func WithConnMaxLifetime(d time.Duration) Option { return func(c *config) { c.lifetime = d } }

// Open opens a Postgres database at dsn with the module's defaults and pings
// it, so a bad DSN fails here rather than at the first query.
func Open(dsn string, opts ...Option) (*sql.DB, error) {
	cfg := config{maxOpen: DefaultMaxOpenConns, lifetime: DefaultConnMaxLifetime}
	for _, o := range opts {
		o(&cfg)
	}
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("driverpgx: open: %w", err)
	}
	db.SetMaxOpenConns(cfg.maxOpen)
	db.SetConnMaxLifetime(cfg.lifetime)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("driverpgx: ping: %w", err)
	}
	return db, nil
}

// DSNFromEnv returns the connection string the tests use, and false when it is
// not set. It is exported so a consumer's own integration tests can share the
// convention.
func DSNFromEnv() (string, bool) {
	dsn := os.Getenv(EnvDSN)
	return dsn, dsn != ""
}
