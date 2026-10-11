package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/mysql"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/render"
	"github.com/ubgo/sqlb/sqlite"
)

// A migration ledger records which migrations have run, so a deploy applies
// each one once, and a lock stops two processes deploying at once from both
// applying it. Both are process rather than rendering, which is why they are
// here and not in the root module; which migrations exist and when a deploy
// runs them stays the application's decision.

// Migration is one named change, owned by a package: its Script runs once,
// and the ledger remembers it by Owner and Name. Name is the migration's
// identity for ever: renaming one makes it new, and editing its Script after
// it ran fails Apply with ErrChecksumMismatch rather than being silently
// skipped or silently re-run.
type Migration struct {
	Owner  string
	Name   string
	Script ddl.Script
}

// The ledger's errors.
var (
	// ErrChecksumMismatch is a migration whose script changed after the
	// ledger recorded it: the database holds what the old script did, and
	// the code now describes something else.
	ErrChecksumMismatch = errors.New("migrate: a migration changed after it was applied")
	// ErrLocked is a lock another process holds past the context's deadline:
	// the migration lock, or on SQLite the database's own write lock.
	ErrLocked = errors.New("migrate: another process holds the migration lock")
	// ErrBadMigration is a migration with no owner, no name, no statements,
	// or a name used twice in one Apply.
	ErrBadMigration = errors.New("migrate: a migration needs an owner, a name and a script, and a name used once")
	// ErrNoLedgerDialect is a Ledger for a dialect it has no table for.
	ErrNoLedgerDialect = errors.New("migrate: the ledger supports postgres, mysql and sqlite")
	// ErrUnknownMigration is a ledger row Apply was not given, for an owner it
	// was given, from a Ledger made with RefuseUnknown: the database has been
	// migrated by newer code than the code now running, typically after a
	// rollback, and nothing is applied to a schema this code never knew.
	ErrUnknownMigration = errors.New("migrate: the database has a migration this code does not know")
)

// Locker serializes migration runs across processes. Lock blocks until the
// lock is held or ctx ends, and Unlock releases it; both run on the pinned
// connection Apply is given, since a session lock belongs to one connection.
type Locker interface {
	Lock(ctx context.Context, conn *sql.Conn) error
	Unlock(ctx context.Context, conn *sql.Conn) error
}

// Ledger applies migrations once each, recording them in a table of its own.
type Ledger struct {
	dialect render.Dialect
	table   sqlb.Table
	lock    Locker
	// refuseUnknown is RefuseUnknown's switch; false, the zero value, keeps
	// Apply's original behaviour of ignoring rows it was not given.
	refuseUnknown bool
}

// The ledger table's columns. Owner and name are its key, which is why both
// are text and not a single string: two packages may name a migration the
// same thing.
const (
	colOwner     = "owner"
	colName      = "name"
	colChecksum  = "checksum"
	colAppliedAt = "applied_at"
)

// DefaultLedgerTable is the ledger's table unless NewLedger is told another.
const DefaultLedgerTable = "sqlb_migrations"

// NewLedger makes a ledger for d, keeping its records in table (empty means
// DefaultLedgerTable), serialized by lock. EngineLock(d) is the usual lock:
// Postgres's advisory lock, MySQL's named lock, a lock row on SQLite.
func NewLedger(d render.Dialect, table string, lock Locker) (Ledger, error) {
	switch render.DialectName(d) {
	case render.DialectPostgres, render.DialectMySQL, render.DialectSQLite:
	default:
		return Ledger{}, fmt.Errorf("%w: %q", ErrNoLedgerDialect, render.DialectName(d))
	}
	if table == "" {
		table = DefaultLedgerTable
	}
	if lock == nil {
		lock = noLock{}
	}
	return Ledger{dialect: d, table: sqlb.Table{Name: table}, lock: lock}, nil
}

// RefuseUnknown returns a copy of l whose Apply fails with
// ErrUnknownMigration, before applying anything, when the ledger records a
// migration it was not given under an owner it was given. It is how a
// library refuses to run against a database a newer release of itself
// migrated: after a rollback the ledger may hold 003 while the code knows
// only 001 and 002, and plain Apply would skip what it recognises and start
// the old code on a schema it was never written for. Owners Apply was not
// given are not judged, because one ledger table can serve several packages,
// each knowing only its own migrations. l itself is unchanged.
func (l Ledger) RefuseUnknown() Ledger {
	l.refuseUnknown = true
	return l
}

// isSQLite reports whether the ledger runs on SQLite, whose file-wide write
// lock is why its transactions and connections are handled differently.
func (l Ledger) isSQLite() bool {
	return render.DialectName(l.dialect) == render.DialectSQLite
}

type noLock struct{}

func (noLock) Lock(context.Context, *sql.Conn) error   { return nil }
func (noLock) Unlock(context.Context, *sql.Conn) error { return nil }

// Checksum is a script's identity for the ledger: SHA-256 over each
// statement's text, flags and SkipIf guard, so a change to any statement, or
// to whether it runs in a transaction, is a different migration.
func Checksum(s ddl.Script) string {
	h := sha256.New()
	for _, st := range s {
		// A hash.Hash's Write never returns an error, per its documentation,
		// so there is none to handle.
		_, _ = fmt.Fprintf(h, "%s\x00%t%t%t\x00%s\x00", st.SQL, st.Transactional, st.OutsideTransaction, st.MustReturnNoRows, st.SkipIf)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// createLedger is the ledger table's CREATE TABLE IF NOT EXISTS for the
// dialect, written by the dialect's own builders.
func (l Ledger) createLedger() (ddl.Script, error) {
	owner := sqlb.Col[string](l.table, colOwner)
	name := sqlb.Col[string](l.table, colName)
	sum := sqlb.Col[string](l.table, colChecksum)
	at := sqlb.Col[string](l.table, colAppliedAt)
	cols := []ddl.ColumnSpec{ddl.Column(owner), ddl.Column(name), ddl.Column(sum), ddl.Column(at)}
	switch render.DialectName(l.dialect) {
	case render.DialectPostgres:
		return pg.CreateTable(l.table).IfNotExists().Column(cols[0], cols[1:]...).Clause(pg.PrimaryKey(owner, name)).Build()
	case render.DialectMySQL:
		return mysql.CreateTable(l.table).IfNotExists().Column(cols[0], cols[1:]...).Clause(mysql.PrimaryKey(owner, name)).Build()
	default:
		return sqlite.CreateTable(l.table).IfNotExists().Column(cols[0], cols[1:]...).Clause(sqlite.PrimaryKey(owner, name)).Build()
	}
}

// record is the ledger row for m as a statement of its own, every value a
// literal, so it joins the migration's script and commits in the same
// transaction as its last statements where the engine has transactional DDL.
func (l Ledger) record(m Migration, sum string, at time.Time) (ddl.Statement, error) {
	a := sqlb.NewInlineArgs(l.dialect)
	d := l.dialect
	sqlText := "INSERT INTO " + l.table.Qualified(d) + " (" +
		strings.Join([]string{d.QuoteIdent(colOwner), d.QuoteIdent(colName), d.QuoteIdent(colChecksum), d.QuoteIdent(colAppliedAt)}, ", ") +
		") VALUES (" + strings.Join([]string{a.Literal(m.Owner), a.Literal(m.Name), a.Literal(sum), a.Literal(at.UTC().Format(appliedAtLayout))}, ", ") + ")"
	if err := a.Err(); err != nil {
		return ddl.Statement{}, fmt.Errorf("migrate: %s/%s: %w", m.Owner, m.Name, err)
	}
	return ddl.Statement{SQL: sqlText, Transactional: true}, nil
}

// AppliedMigration is one row of the ledger: which migration ran, the
// checksum of the script that ran, and when.
type AppliedMigration struct {
	Owner, Name, Checksum string
	// AppliedAt is when Apply recorded the migration, in UTC, to the
	// nanosecond where the clock had it.
	AppliedAt time.Time
}

// appliedAtLayout is how the ledger writes applied_at: text, in UTC, the
// same on every engine, so the column has one type and reading it back is a
// parse rather than a per-driver conversion. It is not sortable as text,
// since RFC 3339 drops trailing zeros of the fraction, so Applied orders in
// Go after parsing.
const appliedAtLayout = time.RFC3339Nano

// Applied returns every migration the ledger records, oldest first; two
// recorded at the same instant come in owner, then name, order, so the result
// is deterministic. A database with no ledger table has applied none, and
// that is an empty list rather than an error, because a database nobody has
// migrated yet is an ordinary thing to ask about. The table's existence is
// read from the engine's catalogue rather than inferred from an error, so a
// failure that is not a missing table is still returned.
//
// It takes no lock: a row commits with its migration, so a concurrent Apply
// shows each migration as either applied or not, never half.
func (l Ledger) Applied(ctx context.Context, conn *sql.Conn) ([]AppliedMigration, error) {
	if l.dialect == nil {
		return nil, fmt.Errorf("%w: a zero Ledger; use NewLedger", ErrNoLedgerDialect)
	}
	ok, err := l.exists(ctx, conn)
	if err != nil || !ok {
		return []AppliedMigration{}, err
	}
	return l.rows(ctx, conn)
}

// AppliedDB pins one connection from db and runs Applied on it.
func (l Ledger) AppliedDB(ctx context.Context, db *sql.DB) ([]AppliedMigration, error) {
	conn, err := l.pinConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return l.Applied(ctx, conn)
}

// exists reports whether the ledger table is there, asked of each engine's
// catalogue. Postgres resolves the quoted name through the search path, as
// every other ledger statement does; MySQL looks in the current database;
// SQLite in the main schema.
func (l Ledger) exists(ctx context.Context, conn *sql.Conn) (bool, error) {
	var found bool
	var err error
	switch render.DialectName(l.dialect) {
	case render.DialectPostgres:
		err = conn.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", l.table.Qualified(l.dialect)).Scan(&found)
	case render.DialectMySQL:
		err = conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?)", l.table.Name).Scan(&found)
	default:
		err = conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", l.table.Name).Scan(&found)
	}
	if err != nil {
		return false, fmt.Errorf("migrate: does the ledger %s exist: %w", l.table.Name, err)
	}
	return found, nil
}

// rows reads every ledger row, oldest first.
func (l Ledger) rows(ctx context.Context, conn *sql.Conn) ([]AppliedMigration, error) {
	owner := sqlb.Col[string](l.table, colOwner)
	name := sqlb.Col[string](l.table, colName)
	sum := sqlb.Col[string](l.table, colChecksum)
	at := sqlb.Col[string](l.table, colAppliedAt)
	var o, n, s, a string
	q, err := sqlb.NewSelect(l.dialect).Columns(sqlb.Bind(owner, &o), sqlb.Bind(name, &n), sqlb.Bind(sum, &s), sqlb.Bind(at, &a)).From(l.table).Build()
	if err != nil {
		return nil, err
	}
	out := []AppliedMigration{}
	err = sqlb.Each(ctx, conn, q, func() error {
		when, err := time.Parse(appliedAtLayout, a)
		if err != nil {
			return fmt.Errorf("migrate: ledger row %s/%s: applied_at %q: %w", o, n, a, err)
		}
		out = append(out, AppliedMigration{Owner: o, Name: n, Checksum: s, AppliedAt: when.UTC()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out, func(x, y AppliedMigration) int {
		if c := x.AppliedAt.Compare(y.AppliedAt); c != 0 {
			return c
		}
		if c := strings.Compare(x.Owner, y.Owner); c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
	return out, nil
}

// unknown names, as "owner/name", every recorded migration under an owner of
// ms that ms does not contain, oldest first.
func unknown(recorded []AppliedMigration, ms []Migration) []string {
	owners := map[string]bool{}
	given := map[[2]string]bool{}
	for _, m := range ms {
		owners[m.Owner] = true
		given[[2]string{m.Owner, m.Name}] = true
	}
	var out []string
	for _, r := range recorded {
		if owners[r.Owner] && !given[[2]string{r.Owner, r.Name}] {
			out = append(out, r.Owner+"/"+r.Name)
		}
	}
	return out
}

// Apply runs each migration the ledger has not recorded, in the order given,
// and records it; it returns the names it applied as "owner/name". It takes
// the lock first and holds it throughout, so a second process running Apply
// at the same time waits and then finds the work done.
//
// A migration already recorded with the same checksum is skipped; one
// recorded with another fails with ErrChecksumMismatch and nothing after it
// runs. The ledger row is the last statement of the migration's script, so on
// Postgres and SQLite, where DDL is transactional, the row and the change
// commit together when the script's last statements are transactional; on
// MySQL, which commits every DDL statement implicitly, a failure part-way
// leaves the change without its row, and the next Apply runs the migration
// again, which is why a MySQL migration should be safe to repeat.
//
// From a Ledger made with RefuseUnknown, a recorded migration it was not
// given, under an owner it was given, fails with ErrUnknownMigration before
// anything runs.
//
// On SQLite every transaction begins IMMEDIATE, so two deploys on one file
// wait for each other instead of one failing with SQLITE_BUSY; see
// ledger_sqlite.go for the two refusals that used to escape.
func (l Ledger) Apply(ctx context.Context, conn *sql.Conn, ms ...Migration) (applied []string, err error) {
	seen := map[[2]string]bool{}
	for _, m := range ms {
		k := [2]string{m.Owner, m.Name}
		if m.Owner == "" || m.Name == "" || len(m.Script) == 0 || seen[k] {
			return nil, fmt.Errorf("%w: %q/%q", ErrBadMigration, m.Owner, m.Name)
		}
		seen[k] = true
	}
	if l.dialect == nil {
		return nil, fmt.Errorf("%w: a zero Ledger; use NewLedger", ErrNoLedgerDialect)
	}
	create, err := l.createLedger()
	if err != nil {
		return nil, err
	}
	if err := l.lock.Lock(ctx, conn); err != nil {
		return nil, err
	}
	defer func() {
		// The unlock runs on a fresh context: one that ended is why an
		// error is being returned, and the lock must still be released.
		if uerr := l.lock.Unlock(context.WithoutCancel(ctx), conn); uerr != nil && err == nil {
			err = uerr
		}
	}()
	begin := l.beginner()
	if err := execute(ctx, conn, create, begin); err != nil {
		return nil, err
	}
	recorded, err := l.rows(ctx, conn)
	if err != nil {
		return nil, err
	}
	if l.refuseUnknown {
		if names := unknown(recorded, ms); len(names) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrUnknownMigration, strings.Join(names, ", "))
		}
	}
	done := make(map[[2]string]string, len(recorded))
	for _, r := range recorded {
		done[[2]string{r.Owner, r.Name}] = r.Checksum
	}
	for _, m := range ms {
		sum := Checksum(m.Script)
		if prev, ok := done[[2]string{m.Owner, m.Name}]; ok {
			if prev != sum {
				return applied, fmt.Errorf("%w: %s/%s", ErrChecksumMismatch, m.Owner, m.Name)
			}
			continue
		}
		row, err := l.record(m, sum, time.Now())
		if err != nil {
			return applied, err
		}
		script := append(append(ddl.Script{}, m.Script...), row)
		if err := execute(ctx, conn, script, begin); err != nil {
			return applied, fmt.Errorf("migrate: %s/%s: %w", m.Owner, m.Name, err)
		}
		applied = append(applied, m.Owner+"/"+m.Name)
	}
	return applied, nil
}

// ApplyDB pins one connection from db and runs Apply on it. On SQLite an
// open refused because another connection is converting the file to WAL is
// waited out, as Apply waits out the write lock.
func (l Ledger) ApplyDB(ctx context.Context, db *sql.DB, ms ...Migration) ([]string, error) {
	conn, err := l.pinConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return l.Apply(ctx, conn, ms...)
}

// EngineLock is the lock each engine has: a session advisory lock on
// Postgres, a named lock on MySQL, and TableLock on SQLite, which has
// neither. key names the lock; two ledgers with the same key serialize, and
// ones with different keys do not.
func EngineLock(d render.Dialect, key string) Locker {
	switch render.DialectName(d) {
	case render.DialectPostgres:
		return PostgresAdvisoryLock{Key: advisoryKey(key)}
	case render.DialectMySQL:
		return MySQLNamedLock{Name: key}
	default:
		return TableLock{Dialect: d, Table: key + "_lock", StaleAfter: DefaultStaleAfter}
	}
}

// advisoryKey turns a lock name into Postgres's bigint advisory key, the
// first eight bytes of its SHA-256, so the same name is the same lock in
// every process.
func advisoryKey(name string) int64 {
	sum := sha256.Sum256([]byte(name))
	var k int64
	for _, b := range sum[:8] {
		k = k<<8 | int64(b)
	}
	return k
}

// PostgresAdvisoryLock is pg_advisory_lock: a session lock the server
// releases if the connection drops, so a crashed deploy never leaves it held.
type PostgresAdvisoryLock struct{ Key int64 }

// Lock waits for the lock; a cancelled ctx cancels the wait.
func (p PostgresAdvisoryLock) Lock(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", p.Key)
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: %w", ErrLocked, ctx.Err())
	}
	return err
}

// Unlock releases it.
func (p PostgresAdvisoryLock) Unlock(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", p.Key)
	return err
}

// MySQLNamedLock is GET_LOCK: a named lock the server releases if the
// session ends. MySQL takes its wait in whole seconds, so the wait is ctx's
// remaining time rounded up, or DefaultLockWait without a deadline.
type MySQLNamedLock struct{ Name string }

// DefaultLockWait is how long a lock waits when ctx has no deadline.
const DefaultLockWait = 60 * time.Second

// Lock waits for the lock and fails with ErrLocked when the wait runs out.
func (m MySQLNamedLock) Lock(ctx context.Context, conn *sql.Conn) error {
	wait := DefaultLockWait
	if dl, ok := ctx.Deadline(); ok {
		wait = time.Until(dl)
	}
	secs := int64((wait + time.Second - 1) / time.Second)
	secs = max(secs, 0)
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", m.Name, secs).Scan(&got); err != nil {
		return err
	}
	if !got.Valid || got.Int64 != 1 {
		return fmt.Errorf("%w: %s", ErrLocked, m.Name)
	}
	return nil
}

// Unlock releases it.
func (m MySQLNamedLock) Unlock(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", m.Name)
	return err
}

// TableLock is a lock held as the one row of a table, for an engine with no
// session lock, SQLite. While another connection holds SQLite's write lock the
// row's INSERT is refused outright, SQLITE_BUSY rather than a duplicate key,
// and that is treated as the lock being held: the wait continues until ctx
// ends or, with no deadline, until StaleAfter has passed. A row older than StaleAfter is taken to be a crashed
// holder's and is replaced, which a session lock never needs, so pick
// StaleAfter longer than any migration takes. It works on any engine; the
// session locks are better where they exist.
type TableLock struct {
	Dialect    render.Dialect
	Table      string
	StaleAfter time.Duration
	// Holder names this process in the row; empty uses a random value.
	Holder string
}

// DefaultStaleAfter is how old a TableLock row must be to be taken over.
const DefaultStaleAfter = 15 * time.Minute

// lockPoll is how often a waiting TableLock tries again.
const lockPoll = 50 * time.Millisecond

func (t TableLock) holder() string {
	if t.Holder != "" {
		return t.Holder
	}
	return "sqlb-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// Lock inserts the lock row, waiting while another holder's fresh row is
// there, and fails with ErrLocked when ctx ends first.
func (t TableLock) Lock(ctx context.Context, conn *sql.Conn) error {
	d := t.Dialect
	tbl := d.QuoteIdent(t.Table)
	create := "CREATE TABLE IF NOT EXISTS " + tbl + " (id INTEGER NOT NULL PRIMARY KEY, holder TEXT NOT NULL, taken_at BIGINT NOT NULL)"
	me := t.holder()
	stale := t.StaleAfter
	if stale <= 0 {
		stale = DefaultStaleAfter
	}
	start := time.Now()
	created := false
	wait := func() error {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrLocked, ctx.Err())
		case <-time.After(lockPoll):
			return nil
		}
	}
	for {
		// Creating the table is a write too, refused like the INSERT while
		// another connection holds SQLite's write lock.
		if !created {
			if _, err := conn.ExecContext(ctx, create); err != nil {
				if time.Since(start) > stale {
					return fmt.Errorf("migrate: lock %s: create: %w", t.Table, err)
				}
				if werr := wait(); werr != nil {
					return werr
				}
				continue
			}
			created = true
		}
		now := time.Now().UnixNano()
		_, err := conn.ExecContext(ctx, "INSERT INTO "+tbl+" (id, holder, taken_at) VALUES (1, "+d.Placeholder(1)+", "+d.Placeholder(2)+")", me, now)
		if err == nil {
			return nil
		}
		// The row exists: take it over if it is stale.
		res, derr := conn.ExecContext(ctx, "DELETE FROM "+tbl+" WHERE id = 1 AND taken_at < "+d.Placeholder(1), now-stale.Nanoseconds())
		switch {
		case derr != nil:
			// The database refused both statements, which on SQLite is
			// another connection holding the write lock while it migrates:
			// wait for it. An error that lasts longer than a lock may be
			// held is not contention, and is returned rather than retried
			// for ever.
			if time.Since(start) > stale {
				return fmt.Errorf("migrate: lock %s: %w", t.Table, derr)
			}
		default:
			if n, _ := res.RowsAffected(); n > 0 {
				continue
			}
		}
		if werr := wait(); werr != nil {
			return werr
		}
	}
}

// Unlock deletes the lock row. A waiting TableLock writes to the same table
// while it polls, and SQLite refuses a write while another connection's is in
// progress, so the delete is retried through that contention, for as long as
// StaleAfter at most; a failed first attempt was what the race detector
// showed, leaving the lock held until it went stale.
func (t TableLock) Unlock(ctx context.Context, conn *sql.Conn) error {
	stale := t.StaleAfter
	if stale <= 0 {
		stale = DefaultStaleAfter
	}
	start := time.Now()
	for {
		_, err := conn.ExecContext(ctx, "DELETE FROM "+t.Dialect.QuoteIdent(t.Table)+" WHERE id = 1")
		if err == nil {
			return nil
		}
		if time.Since(start) > stale {
			return fmt.Errorf("migrate: unlock %s: %w", t.Table, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("migrate: unlock %s: %w", t.Table, err)
		case <-time.After(lockPoll):
		}
	}
}
