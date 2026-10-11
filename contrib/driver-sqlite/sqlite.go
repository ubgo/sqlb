// Package driversqlite opens SQLite databases for github.com/ubgo/sqlb.
//
// It is a separate Go module inside the sqlb repository. The reason is the
// rule the core lives by: sqlb itself depends on nothing but the standard
// library, so a library that embeds it inherits no driver, no cgo and no
// version conflict. A driver is a dependency, so it lives out here, and an
// application pays for it only by importing this module.
//
// What the module adds over calling sql.Open yourself is the pragma set. Left
// at its defaults, SQLite does not enforce foreign keys, fails a concurrent
// writer immediately instead of waiting, and serialises readers behind the
// writer. None of that is a sensible default for a process whose libraries
// own tables, and all three are invisible until production. Open turns them
// around and says so in one place.
//
// The module's real work is its test: it runs the shared conformance suite in
// github.com/ubgo/sqlb/sqlbtest against a real database, which is the only
// thing that proves the builder's placeholders, argument order and scan
// targets line up outside of rendered text.
package driversqlite

import (
	"database/sql"
	"fmt"
	"strings"

	// The driver registers itself under DriverName. It is pure Go, so this
	// module needs no cgo and cross-compiles.
	_ "modernc.org/sqlite"
)

// DriverName is the database/sql name modernc.org/sqlite registers.
const DriverName = "sqlite"

// pragmaParam is the query parameter modernc.org/sqlite reads pragmas from.
// One parameter per pragma, spelled name(value).
const pragmaParam = "_pragma"

// Pragma is one SQLite PRAGMA applied to every connection in the pool.
//
// Per-connection is the invariant that matters: SQLite scopes most pragmas to
// a connection, so setting one with a bare Exec configures whichever pooled
// connection happened to serve it and silently leaves the others alone.
// Passing them in the DSN is what makes them hold for every connection the
// pool opens, including the ones it opens later.
type Pragma struct {
	Name  string
	Value string
}

// DefaultPragmas is what Open applies unless WithoutDefaultPragmas is passed.
//
// Each one turns off a default that is wrong for a library-owned schema:
//
//   - journal_mode=WAL lets readers run while a writer commits. Without it a
//     single write blocks every read in the process.
//   - busy_timeout=5000 makes a contended write wait up to five seconds
//     instead of returning SQLITE_BUSY at once, which is the error everyone
//     hits first and understands last.
//   - foreign_keys=ON enforces the constraints the schema declares. SQLite
//     parses them and ignores them by default, so a delete that should fail
//     quietly orphans rows.
//   - synchronous=NORMAL is the documented companion of WAL: durable against
//     a process crash, and only a power loss can lose the last commits.
var DefaultPragmas = []Pragma{
	{Name: "journal_mode", Value: "WAL"},
	{Name: "busy_timeout", Value: "5000"},
	{Name: "foreign_keys", Value: "ON"},
	{Name: "synchronous", Value: "NORMAL"},
}

type config struct {
	pragmas     []Pragma
	useDefaults bool
	maxOpen     int
}

// Option configures Open. It is the one escape hatch: anything the defaults
// get wrong for a caller is reachable by name rather than by giving up on the
// helper and calling sql.Open directly.
type Option func(*config)

// WithPragma adds a pragma, or overrides a default of the same name. The last
// one with a given name wins, so WithPragma("journal_mode", "DELETE") turns
// WAL off without turning the other defaults off with it.
func WithPragma(name, value string) Option {
	return func(c *config) { c.pragmas = append(c.pragmas, Pragma{Name: name, Value: value}) }
}

// WithoutDefaultPragmas drops DefaultPragmas, leaving only what WithPragma
// adds. For a caller who has their own considered set and wants nothing
// applied behind it.
func WithoutDefaultPragmas() Option {
	return func(c *config) { c.useDefaults = false }
}

// WithMaxOpenConns caps the pool. SQLite takes one writer at a time, so a
// large pool buys nothing for writes and costs contention; it is worth
// raising only for a read-heavy load under WAL.
func WithMaxOpenConns(n int) Option {
	return func(c *config) { c.maxOpen = n }
}

// Open opens a SQLite database with the pragmas applied to every connection.
//
// dsn is a modernc.org/sqlite DSN: a file path, or a file: URI. Open appends
// its pragmas as query parameters, so a dsn that already carries _pragma
// keeps it and the appended ones are applied after.
//
// Open does not ping. A SQLite file is created on first use, so a failure to
// open is a permissions or path problem the first statement reports with more
// context than a ping would.
func Open(dsn string, opts ...Option) (*sql.DB, error) {
	cfg := config{useDefaults: true, maxOpen: -1}
	for _, o := range opts {
		o(&cfg)
	}
	db, err := sql.Open(DriverName, buildDSN(dsn, cfg))
	if err != nil {
		return nil, fmt.Errorf("driversqlite: open %q: %w", dsn, err)
	}
	if cfg.maxOpen > 0 {
		db.SetMaxOpenConns(cfg.maxOpen)
	}
	return db, nil
}

// Memory opens a private in-memory database for tests.
//
// It pins the pool to one connection. An in-memory SQLite database belongs to
// the connection that created it, so a pool of two serves the second query
// from a second, empty database and the schema appears to vanish. One
// connection is the only setting where that cannot happen, and a test is
// never the place to debug it.
//
// The database exists until the returned *sql.DB is closed.
func Memory(opts ...Option) (*sql.DB, error) {
	all := append([]Option{WithMaxOpenConns(1)}, opts...)
	return Open(":memory:", all...)
}

// buildDSN appends one _pragma parameter per pragma.
//
// A name is emitted once, with the last value given for it, in the position
// where it first appeared. Emitting a name twice makes the result depend on
// the order the driver happens to apply parameters in, which is how an
// override silently fails to override.
func buildDSN(dsn string, cfg config) string {
	pragmas := cfg.pragmas
	if cfg.useDefaults {
		pragmas = append(append([]Pragma{}, DefaultPragmas...), cfg.pragmas...)
	}
	if len(pragmas) == 0 {
		return dsn
	}
	order := make([]string, 0, len(pragmas))
	value := make(map[string]string, len(pragmas))
	for _, p := range pragmas {
		if _, seen := value[p.Name]; !seen {
			order = append(order, p.Name)
		}
		value[p.Name] = p.Value
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	var b strings.Builder
	b.WriteString(dsn)
	for _, name := range order {
		b.WriteString(sep)
		sep = "&"
		b.WriteString(pragmaParam)
		b.WriteString("=")
		b.WriteString(name)
		b.WriteString("(")
		b.WriteString(value[name])
		b.WriteString(")")
	}
	return b.String()
}
