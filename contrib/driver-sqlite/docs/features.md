# driver-sqlite — features

Every exported symbol, what it is for, and the invariant it keeps.

## Open

```go
func Open(dsn string, opts ...Option) (*sql.DB, error)
```

Opens a SQLite database with [`DefaultPragmas`](#defaultpragmas) applied to every connection in the pool.

`dsn` is a `modernc.org/sqlite` DSN: a file path, or a `file:` URI. `Open` appends its pragmas as query parameters, so a DSN that already carries `_pragma` keeps it and the appended ones are applied after.

`Open` does not ping. A SQLite file is created on first use, so a failure to open is a permissions or path problem, and the first real statement reports it with more context than a ping would.

```go
db, err := driversqlite.Open("app.db")
db, err := driversqlite.Open("file:app.db?mode=rwc")
db, err := driversqlite.Open("app.db", driversqlite.WithMaxOpenConns(8))
```

**Invariant:** each pragma name appears once in the resulting DSN, carrying the last value given for it. Emitting a name twice would leave the result to whichever order the driver applies parameters in, which is how an override silently fails to override.

## Memory

```go
func Memory(opts ...Option) (*sql.DB, error)
```

Opens a private in-memory database for tests.

It pins the pool to one connection. An in-memory SQLite database belongs to the connection that created it, so a pool of two serves the second query from a second, empty database and the schema appears to vanish halfway through a test. One connection is the only setting where that cannot happen, and a test is never the place to debug it.

The database exists until the returned `*sql.DB` is closed.

```go
db, err := driversqlite.Memory()
t.Cleanup(func() { _ = db.Close() })
```

## Option

```go
type Option func(*config)
```

The one escape hatch. Anything the defaults get wrong for a caller is reachable by name, so a disagreement about one pragma never costs the other three.

## WithPragma

```go
func WithPragma(name, value string) Option
```

Adds a pragma, or overrides a default of the same name. The last value given for a name wins, and the pragma keeps the position where the name first appeared.

```go
// WAL off, the other three defaults untouched.
db, err := driversqlite.Open("app.db", driversqlite.WithPragma("journal_mode", "DELETE"))
```

## WithoutDefaultPragmas

```go
func WithoutDefaultPragmas() Option
```

Drops `DefaultPragmas`, leaving only what `WithPragma` adds. For a caller with their own considered set who wants nothing applied behind it.

## WithMaxOpenConns

```go
func WithMaxOpenConns(n int) Option
```

Caps the pool. SQLite takes one writer at a time, so a large pool buys nothing for writes and costs contention. It is worth raising for a read-heavy load under WAL.

## Pragma

```go
type Pragma struct {
	Name  string
	Value string
}
```

One PRAGMA applied to every connection in the pool.

**Per-connection is the invariant that matters.** SQLite scopes most pragmas to a connection, so setting one with a bare `Exec` configures whichever pooled connection served that call and silently leaves the others alone. Passing them in the DSN is what makes them hold for every connection the pool opens, including the ones it opens later.

## DefaultPragmas

```go
var DefaultPragmas = []Pragma{
	{Name: "journal_mode", Value: "WAL"},
	{Name: "busy_timeout", Value: "5000"},
	{Name: "foreign_keys", Value: "ON"},
	{Name: "synchronous", Value: "NORMAL"},
}
```

Each one turns off a default that is wrong for a library-owned schema.

- **`journal_mode=WAL`** lets readers run while a writer commits. Without it a single write blocks every read in the process.
- **`busy_timeout=5000`** makes a contended write wait up to five seconds instead of returning `SQLITE_BUSY` at once. Two refusals escape it, measured: a deferred transaction that reads first cannot upgrade to a write while another connection writes, so begin a writing transaction with `BEGIN IMMEDIATE`; and `journal_mode=WAL` on a file's first open fails as busy while another connection is in the file.
- **`foreign_keys=ON`** enforces the constraints the schema declares. SQLite parses them and ignores them by default, so a delete that should fail quietly orphans rows. `TestForeignKeys_AreEnforced` proves this one by observing a rejected insert, not by reading the setting back.
- **`synchronous=NORMAL`** is the documented companion of WAL: durable against a process crash, and only a power loss can lose the last commits.

## DriverName

```go
const DriverName = "sqlite"
```

The `database/sql` name `modernc.org/sqlite` registers. Exported so a caller who wants `sql.Open` directly, without the pragmas, can name the same driver rather than guessing at the string.
