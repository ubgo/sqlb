// Package drivermysql opens MySQL databases for github.com/ubgo/sqlb.
//
// It is a separate Go module for the reason every contrib module is: the core
// depends on nothing but the standard library, so a library that embeds it
// inherits no driver.
//
// The module's real work is its tests. MySQL was, until this module, the one
// engine the core's universality rule named and nothing here had ever run
// against, so a quarter of every "universal" claim was checked by reading
// MySQL's documentation. The first run found the core's LIKE escape clause,
// ESCAPE '\', was an unterminated string on MySQL's default sql_mode.
//
// The driver is github.com/go-sql-driver/mysql, so exec.DBTX is satisfied by
// *sql.DB and *sql.Tx exactly as for the other engines.
package drivermysql

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
)

// DriverName is the database/sql name go-sql-driver/mysql registers.
const DriverName = "mysql"

// EnvDSN is the environment variable the tests read the connection string
// from. Absence is a loud skip, never a silent pass.
const EnvDSN = "SQLB_MYSQL_DSN"

// Defaults applied to every pool Open creates.
const (
	// DefaultMaxOpenConns caps the pool; a MySQL connection is a server
	// thread, and max_connections is the limit an uncapped pool reaches.
	DefaultMaxOpenConns = 8
	// DefaultConnMaxLifetime recycles connections before MySQL's wait_timeout
	// closes them from the server side.
	DefaultConnMaxLifetime = 5 * time.Minute
)

// Option adjusts what Open does.
type Option func(*config)

type config struct {
	maxOpen     int
	lifetime    time.Duration
	changedRows bool
}

// WithMaxOpenConns caps the pool at n connections.
func WithMaxOpenConns(n int) Option { return func(c *config) { c.maxOpen = n } }

// WithConnMaxLifetime sets how long a connection may be reused.
func WithConnMaxLifetime(d time.Duration) Option { return func(c *config) { c.lifetime = d } }

// WithChangedRows restores MySQL's own rows-affected count, which counts only
// the rows an UPDATE changed, instead of the rows it matched. It is the one
// way out of the clientFoundRows default Open sets, for a caller that wants
// "did anything change" and knows it is on MySQL; a library that runs on more
// than one engine should not use it, because no other engine can answer that
// question the same way.
func WithChangedRows() Option { return func(c *config) { c.changedRows = true } }

// Open opens a MySQL database at dsn and pings it. It forces three settings
// whatever the DSN says, because each is a silent wrong answer without it:
// parseTime, so a DATETIME scans into a time.Time rather than bytes; a UTC
// location, because the dialect writes times in UTC and a DATETIME read back
// in another zone would shift by the offset; and clientFoundRows, so an UPDATE
// reports the rows it matched, as Postgres and SQLite do. Without it an UPDATE
// that sets a row to the value it already has reports 0 affected on MySQL and
// 1 elsewhere, and code that reads 0 as "not found" says so about a row that
// exists; measured on 8.4.11 by sqlbtest's crud_transcript case.
// WithChangedRows restores MySQL's count.
func Open(dsn string, opts ...Option) (*sql.DB, error) {
	cfg := config{maxOpen: DefaultMaxOpenConns, lifetime: DefaultConnMaxLifetime}
	for _, o := range opts {
		o(&cfg)
	}
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("drivermysql: parse dsn: %w", err)
	}
	mc.ParseTime = true
	mc.Loc = time.UTC
	mc.ClientFoundRows = !cfg.changedRows
	connector, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, fmt.Errorf("drivermysql: connector: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(cfg.maxOpen)
	db.SetConnMaxLifetime(cfg.lifetime)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("drivermysql: ping: %w", err)
	}
	return db, nil
}

// DSNFromEnv reads EnvDSN, reporting whether it was set.
func DSNFromEnv() (string, bool) {
	dsn := os.Getenv(EnvDSN)
	return dsn, dsn != ""
}
