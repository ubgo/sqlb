// Package widgets is a small library that owns two tables and lets the
// application choose their names at run time with a prefix. It is the
// worked example for docs/declare-once.md: every table and column is written
// once, as typed columns built by NewTables; the same values write the
// CREATE TABLE (the declaration), the numbered migrations and every query, so
// a column cannot be created under one name or type and read under another.
//
// It speaks Postgres and SQLite. A statement typed to one engine cannot take
// the other's clause, so the few places the engines differ (the primary key
// clause, ON CONFLICT, FOR UPDATE) are a switch on the engine, and everything
// else is written once.
package widgets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/ddl"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/schema"
	"github.com/ubgo/sqlb/sqlite"
)

// Engine is the database the library talks to.
type Engine int

// The engines this library supports.
const (
	Postgres Engine = iota + 1
	SQLite
)

// Owner is the library's name in the migration ledger.
const Owner = "acme/widgets"

// ErrBadPrefix is a prefix that is not letters, digits and underscores.
// The prefix comes from the application's configuration, so it is checked.
var ErrBadPrefix = errors.New("widgets: a table prefix must be letters, digits and underscores")

// ErrEngine is an Engine this library does not support.
var ErrEngine = errors.New("widgets: unsupported engine")

var prefixPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$|^$`)

// Tables is every table and column the library owns, under one prefix.
// It is built at run time because the real table names depend on the
// prefix; a library without a prefix could make one package-level value.
type Tables struct {
	Accounts         sqlb.Table
	AccountID        sqlb.Expr[int64]           // bigint
	AccountEmail     sqlb.Expr[string]          // text, unique
	AccountActive    sqlb.Expr[bool]            // boolean, default true
	AccountCreatedAt sqlb.Expr[time.Time]       // timestamptz
	AccountBirthday  sqlb.Expr[*time.Time]      // date, added by migration 2
	AccountProfile   sqlb.Expr[json.RawMessage] // jsonb, added by migration 2
	AccountAvatar    sqlb.Expr[[]byte]          // bytea, added by migration 2

	Members         sqlb.Table
	MemberAccountID sqlb.Expr[int64]  // part of the composite key
	MemberTeam      sqlb.Expr[string] // part of the composite key, indexed
	MemberRole      sqlb.Expr[string]

	// The column definitions, the one place a column's SQL type, default
	// or nullability is said. The declaration and the migrations both use
	// them, so ADD COLUMN creates exactly what CREATE TABLE describes.
	defID, defEmail, defActive, defCreatedAt ddl.ColumnSpec
	defBirthday, defProfile, defAvatar       ddl.ColumnSpec
	defMemberAccount, defTeam, defRole       ddl.ColumnSpec
}

// NewTables names every table prefix + its name. The column names, Go types
// and SQL types are written here and nowhere else.
func NewTables(prefix string) (Tables, error) {
	if !prefixPattern.MatchString(prefix) {
		return Tables{}, fmt.Errorf("%w: %q", ErrBadPrefix, prefix)
	}
	var t Tables
	t.Accounts = sqlb.Table{Name: prefix + "accounts", Alias: "wa"}
	t.AccountID = sqlb.Col[int64](t.Accounts, "id")
	t.AccountEmail = sqlb.Col[string](t.Accounts, "email")
	t.AccountActive = sqlb.Col[bool](t.Accounts, "active")
	t.AccountCreatedAt = sqlb.Col[time.Time](t.Accounts, "created_at")
	t.AccountBirthday = sqlb.Col[*time.Time](t.Accounts, "birthday")
	t.AccountProfile = sqlb.Col[json.RawMessage](t.Accounts, "profile")
	t.AccountAvatar = sqlb.Col[[]byte](t.Accounts, "avatar")

	t.Members = sqlb.Table{Name: prefix + "members", Alias: "wm"}
	t.MemberAccountID = sqlb.Col[int64](t.Members, "account_id")
	t.MemberTeam = sqlb.Col[string](t.Members, "team")
	t.MemberRole = sqlb.Col[string](t.Members, "role")

	t.defID = ddl.Column(t.AccountID)
	t.defEmail = ddl.Column(t.AccountEmail)
	t.defActive = ddl.Column(t.AccountActive).Default(true)
	t.defCreatedAt = ddl.Column(t.AccountCreatedAt)
	t.defBirthday = ddl.Column(t.AccountBirthday).As(ddl.Portable[*time.Time](schema.TypeDate))
	// A column added to a table that has rows is nullable or has a default.
	t.defProfile = ddl.Column(t.AccountProfile).As(ddl.Portable[json.RawMessage](schema.TypeJSON)).Default(json.RawMessage(`{}`))
	t.defAvatar = ddl.Column(t.AccountAvatar).Nullable()
	t.defMemberAccount = ddl.Column(t.MemberAccountID)
	t.defTeam = ddl.Column(t.MemberTeam)
	t.defRole = ddl.Column(t.MemberRole).Default("member")
	return t, nil
}

// Index names, derived from the real table names so the prefix reaches them.
func (t Tables) emailKey() string   { return t.Accounts.Name + "_email_key" }
func (t Tables) teamIndex() string  { return t.Members.Name + "_team_idx" }
func (t Tables) ownerIndex() string { return t.Members.Name + "_owner_idx" }

// indexStmt is a CREATE INDEX and the schema.Index it creates. Each index is
// written once, below, and used both by the migration that creates it and by
// the declaration's description, so the two cannot disagree about its
// columns, its order or its predicate.
type indexStmt interface {
	Build() (ddl.Script, error)
	Describe() (schema.Index, error)
}

// emailIndex is the unique index ON CONFLICT names.
func (t Tables) emailIndex(e Engine) indexStmt {
	if e == SQLite {
		return sqlite.CreateIndex(sqlite.Index{Name: t.emailKey()}, t.Accounts).Unique().IfNotExists().On(sqlite.IndexKey(t.AccountEmail))
	}
	return pg.CreateIndex(pg.Index{Name: t.emailKey()}, t.Accounts).Unique().IfNotExists().On(pg.IndexKey(t.AccountEmail))
}

// teamIndexStmt lists a team's members.
func (t Tables) teamIndexStmt(e Engine) indexStmt {
	if e == SQLite {
		return sqlite.CreateIndex(sqlite.Index{Name: t.teamIndex()}, t.Members).IfNotExists().On(sqlite.IndexKey(t.MemberTeam))
	}
	return pg.CreateIndex(pg.Index{Name: t.teamIndex()}, t.Members).IfNotExists().On(pg.IndexKey(t.MemberTeam))
}

// ownerIndexStmt is a partial index: a team's owners, a handful of rows among
// many members, so only those rows are indexed.
func (t Tables) ownerIndexStmt(e Engine) indexStmt {
	if e == SQLite {
		return sqlite.CreateIndex(sqlite.Index{Name: t.ownerIndex()}, t.Members).IfNotExists().
			On(sqlite.IndexKey(t.MemberTeam)).Where(sqlb.Eq(t.MemberRole, "owner"))
	}
	return pg.CreateIndex(pg.Index{Name: t.ownerIndex()}, t.Members).IfNotExists().
		On(pg.IndexKey(t.MemberTeam)).Where(sqlb.Eq(t.MemberRole, "owner"))
}

// Declaration is the library's tables as they are now: one CREATE TABLE per
// table and the indexes beside it, rendered for engine. Describe on these is
// the schema.Table the live database is checked against (see Describe); a
// fresh database is never created from them directly, but from Migrations,
// and the tests prove the two end in the same schema.
func (t Tables) Declaration(e Engine) ([]Describer, error) {
	accountIndexes := []indexStmt{t.emailIndex(e)}
	memberIndexes := []indexStmt{t.teamIndexStmt(e), t.ownerIndexStmt(e)}
	switch e {
	case Postgres:
		return []Describer{
			indexed{pg.CreateTable(t.Accounts).
				Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt, t.defBirthday, t.defProfile, t.defAvatar).
				Clause(pg.PrimaryKey(t.AccountID)), accountIndexes},
			indexed{pg.CreateTable(t.Members).
				Column(t.defMemberAccount, t.defTeam, t.defRole).
				Clause(pg.PrimaryKey(t.MemberAccountID, t.MemberTeam), pg.References(t.MemberAccountID, t.AccountID).OnDelete(pg.RefCascade)),
				memberIndexes},
		}, nil
	case SQLite:
		return []Describer{
			indexed{sqlite.CreateTable(t.Accounts).
				Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt, t.defBirthday, t.defProfile, t.defAvatar).
				Clause(sqlite.PrimaryKey(t.AccountID)), accountIndexes},
			indexed{sqlite.CreateTable(t.Members).
				Column(t.defMemberAccount, t.defTeam, t.defRole).
				Clause(sqlite.PrimaryKey(t.MemberAccountID, t.MemberTeam), sqlite.References(t.MemberAccountID, t.AccountID).OnDelete(sqlite.RefCascade)),
				memberIndexes},
		}, nil
	}
	return nil, ErrEngine
}

// Describer is a CREATE TABLE with its description: what contrib/migrate's
// Install takes and what Describe reads.
type Describer interface {
	Build() (ddl.Script, error)
	Describe() (schema.Table, error)
}

// indexed adds the table's separately created indexes to its description,
// because CREATE TABLE describes only the keys written inside it. Each index
// describes itself, predicate and key order included.
type indexed struct {
	create  Describer
	indexes []indexStmt
}

func (x indexed) Build() (ddl.Script, error) { return x.create.Build() }

func (x indexed) Describe() (schema.Table, error) {
	d, err := x.create.Describe()
	if err != nil {
		return schema.Table{}, err
	}
	for _, ix := range x.indexes {
		desc, err := ix.Describe()
		if err != nil {
			return schema.Table{}, err
		}
		d.Indexes = append(d.Indexes, desc)
	}
	return d, nil
}

// Describe is every table as schema describes it, derived from the
// declaration: the input to sqlb vet's check against a live database.
func (t Tables) Describe(e Engine) ([]schema.Table, error) {
	decl, err := t.Declaration(e)
	if err != nil {
		return nil, err
	}
	out := make([]schema.Table, 0, len(decl))
	for _, d := range decl {
		desc, err := d.Describe()
		if err != nil {
			return nil, err
		}
		out = append(out, desc)
	}
	return out, nil
}

// Migration is one numbered migration: a name the ledger keys it by and the
// statements it runs. It mirrors contrib/migrate's Migration without
// importing it, so this example stays in the zero-dependency module; a real
// library returns migrate.Migration directly.
type Migration struct {
	Name   string
	Script ddl.Script
}

// Migrations is the library's numbered migrations, written by hand with the
// ddl builders over the same columns. Never edit a released one: the ledger
// checksums each, and a changed script fails the next deploy. A new release
// appends a new number.
//
// Migration 0001 writes IF NOT EXISTS everywhere. It is what an earlier
// release created with hand-written CREATE TABLE IF NOT EXISTS strings, so on
// a database that release already created it changes nothing and is only
// recorded; on a fresh database it creates the tables.
func (t Tables) Migrations(e Engine) ([]Migration, error) {
	var m1, m2 []func() (ddl.Script, error)
	switch e {
	case Postgres:
		m1 = []func() (ddl.Script, error){
			pg.CreateTable(t.Accounts).IfNotExists().Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt).Clause(pg.PrimaryKey(t.AccountID)).Build,
			t.emailIndex(e).Build,
			pg.CreateTable(t.Members).IfNotExists().Column(t.defMemberAccount, t.defTeam, t.defRole).
				Clause(pg.PrimaryKey(t.MemberAccountID, t.MemberTeam), pg.References(t.MemberAccountID, t.AccountID).OnDelete(pg.RefCascade)).Build,
		}
		m2 = []func() (ddl.Script, error){
			pg.AlterTable(t.Accounts).Action(ddl.AddColumn(t.defBirthday), ddl.AddColumn(t.defProfile), ddl.AddColumn(t.defAvatar)).Build,
			t.teamIndexStmt(e).Build,
		}
	case SQLite:
		m1 = []func() (ddl.Script, error){
			sqlite.CreateTable(t.Accounts).IfNotExists().Column(t.defID, t.defEmail, t.defActive, t.defCreatedAt).Clause(sqlite.PrimaryKey(t.AccountID)).Build,
			t.emailIndex(e).Build,
			sqlite.CreateTable(t.Members).IfNotExists().Column(t.defMemberAccount, t.defTeam, t.defRole).
				Clause(sqlite.PrimaryKey(t.MemberAccountID, t.MemberTeam), sqlite.References(t.MemberAccountID, t.AccountID).OnDelete(sqlite.RefCascade)).Build,
		}
		m2 = []func() (ddl.Script, error){
			sqlite.AlterTable(t.Accounts).Action(ddl.AddColumn(t.defBirthday), ddl.AddColumn(t.defProfile), ddl.AddColumn(t.defAvatar)).Build,
			t.teamIndexStmt(e).Build,
		}
	default:
		return nil, ErrEngine
	}
	// Release 3 added the partial owner index, the same value the
	// declaration describes.
	m3 := []func() (ddl.Script, error){t.ownerIndexStmt(e).Build}
	out := make([]Migration, 0, 3)
	for i, steps := range [][]func() (ddl.Script, error){m1, m2, m3} {
		var s ddl.Script
		for _, build := range steps {
			part, err := build()
			if err != nil {
				return nil, fmt.Errorf("widgets: migration %04d: %w", i+1, err)
			}
			s = append(s, part...)
		}
		out = append(out, Migration{Name: fmt.Sprintf("%04d", i+1), Script: s})
	}
	return out, nil
}

// Account is one account row.
type Account struct {
	ID        int64
	Email     string
	Active    bool
	CreatedAt time.Time
	Birthday  *time.Time
	Profile   json.RawMessage
	Avatar    []byte
}

// bind is the account's column list and scan targets as one list, so the
// two cannot drift.
func (t Tables) bind(a *Account) []sqlb.Binding {
	return []sqlb.Binding{
		sqlb.Bind(t.AccountID, &a.ID),
		sqlb.Bind(t.AccountEmail, &a.Email),
		sqlb.Bind(t.AccountActive, &a.Active),
		sqlb.Bind(t.AccountCreatedAt, &a.CreatedAt),
		sqlb.Bind(t.AccountBirthday, &a.Birthday),
		sqlb.Bind(t.AccountProfile, &a.Profile),
		sqlb.Bind(t.AccountAvatar, &a.Avatar),
	}
}

// Store runs the library's queries.
type Store struct {
	T      Tables
	Engine Engine
	DB     sqlb.DBTX
}

// Upsert inserts the account, or updates the existing account with the same
// email (ON CONFLICT on the unique email index), and reads the row back with
// RETURNING into out.
func (s Store) Upsert(ctx context.Context, a Account, out *Account) error {
	t := s.T
	row := []sqlb.Assignment{
		sqlb.Set(t.AccountID, a.ID), sqlb.Set(t.AccountEmail, a.Email), sqlb.Set(t.AccountActive, a.Active),
		sqlb.Set(t.AccountCreatedAt, a.CreatedAt), sqlb.Set(t.AccountBirthday, a.Birthday),
		sqlb.Set(t.AccountProfile, a.Profile), sqlb.Set(t.AccountAvatar, a.Avatar),
	}
	var q sqlb.Query
	var err error
	switch s.Engine {
	case Postgres:
		ins := pg.Insert(t.Accounts).Row(row...).Clause(pg.OnConflict(t.AccountEmail).
			DoUpdate(sqlb.SetExpr(t.AccountActive, pg.Excluded(t.AccountActive)), sqlb.SetExpr(t.AccountProfile, pg.Excluded(t.AccountProfile))).Clause())
		q, err = pg.InsertReturning(ins, t.bind(out)...).Build()
	case SQLite:
		ins := sqlite.Insert(t.Accounts).Row(row...).Clause(sqlite.OnConflict(t.AccountEmail).
			DoUpdate(sqlb.SetExpr(t.AccountActive, sqlite.Excluded(t.AccountActive)), sqlb.SetExpr(t.AccountProfile, sqlite.Excluded(t.AccountProfile))).Clause())
		q, err = sqlite.InsertReturning(ins, t.bind(out)...).Build()
	default:
		return ErrEngine
	}
	if err != nil {
		return err
	}
	return sqlb.One(ctx, s.DB, q)
}

// Join adds the account to a team, doing nothing when it is already in it.
func (s Store) Join(ctx context.Context, accountID int64, team string) error {
	t := s.T
	row := []sqlb.Assignment{sqlb.Set(t.MemberAccountID, accountID), sqlb.Set(t.MemberTeam, team)}
	var q sqlb.Query
	var err error
	switch s.Engine {
	case Postgres:
		q, err = pg.Insert(t.Members).Row(row...).Clause(pg.OnConflict(t.MemberAccountID, t.MemberTeam).DoNothing()).Build()
	case SQLite:
		q, err = sqlite.Insert(t.Members).Row(row...).Clause(sqlite.OnConflict(t.MemberAccountID, t.MemberTeam).DoNothing()).Build()
	default:
		return ErrEngine
	}
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, q.SQL, q.Args...)
	return err
}

// Lock reads the account inside the caller's transaction and, on Postgres,
// locks its row with FOR UPDATE until the transaction ends. SQLite has no
// row locks: its write transaction locks the whole database, so the plain
// read is the same guarantee once the caller's transaction has written.
func (s Store) Lock(ctx context.Context, tx *sql.Tx, id int64, out *Account) error {
	t := s.T
	var q sqlb.Query
	var err error
	switch s.Engine {
	case Postgres:
		q, err = pg.Select().Columns(t.bind(out)...).From(t.Accounts).Where(sqlb.Eq(t.AccountID, id)).Clause(pg.ForUpdate()).Build()
	case SQLite:
		q, err = sqlite.Select().Columns(t.bind(out)...).From(t.Accounts).Where(sqlb.Eq(t.AccountID, id)).Build()
	default:
		return ErrEngine
	}
	if err != nil {
		return err
	}
	return sqlb.One(ctx, tx, q)
}

// Teams lists the teams an account is in, ordered by name.
func (s Store) Teams(ctx context.Context, accountID int64) ([]string, error) {
	t := s.T
	var team string
	q, err := sqlb.NewSelect(dialect(s.Engine)).Columns(sqlb.Bind(t.MemberTeam, &team)).From(t.Members).
		Where(sqlb.Eq(t.MemberAccountID, accountID)).Order(sqlb.OrderBy(t.MemberTeam, sqlb.SortAsc)).Build()
	if err != nil {
		return nil, err
	}
	var out []string
	err = sqlb.Each(ctx, s.DB, q, func() error { out = append(out, team); return nil })
	return out, err
}

func dialect(e Engine) sqlb.Dialect {
	if e == Postgres {
		return pg.Dialect{}
	}
	return sqlite.Dialect{}
}
