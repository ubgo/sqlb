// Package authlite is a deliberately small stand-in for a ubgo library that
// owns tables, for the cookbook. It shows the library-author side of every
// extension mechanism: exported tables and columns, a bindings function,
// queries that accept sqlb.Option, a Composite destination, mandatory
// predicates that options cannot remove, and hooks that receive the
// transaction. It renders SQL and executes only through the DBTX it is
// given, so the cookbook runs without a driver.
package authlite

import (
	"context"
	"time"

	"github.com/ubgo/sqlb"
)

// Table and column names. Constants so a rename is one edit and the
// migration file can reference the same names.
const (
	TableNameUser     = "auth_user"
	TableAliasUser    = "u"
	TableNameIdentity = "auth_identity"
	TableAliasIdent   = "i"

	ColID        = "id"
	ColEmail     = "primary_email"
	ColBannedAt  = "banned_at"
	ColCreatedAt = "created_at"

	ColIdentUserID = "user_id"
	ColIdentKind   = "kind"
	ColIdentValue  = "value"
)

// IdentifierKind names the kind of an identity row. A named string type so a
// plugin cannot pass an arbitrary string variable as a kind.
type IdentifierKind string

// The identifier kinds the core knows. A plugin adding a kind adds it here,
// so the closed set stays in one place.
const (
	KindEmail IdentifierKind = "email"
	KindPhone IdentifierKind = "phone"
)

// Tables owned by the core. Package-level because a plugin in another module
// declares its join against them at init time.
var (
	TableUser     = sqlb.Table{Name: TableNameUser, Alias: TableAliasUser}
	TableIdentity = sqlb.Table{Name: TableNameIdentity, Alias: TableAliasIdent}
)

// Typed columns on auth_user. These, with UserBindings, are the public API.
// Adding one is a minor version; renaming or removing one is a major version.
var (
	UserID        = sqlb.Col[string](TableUser, ColID)
	UserEmail     = sqlb.Col[string](TableUser, ColEmail)
	UserBannedAt  = sqlb.Col[*time.Time](TableUser, ColBannedAt)
	UserCreatedAt = sqlb.Col[time.Time](TableUser, ColCreatedAt)
)

// Typed columns on auth_identity.
var (
	IdentityUserID = sqlb.Col[string](TableIdentity, ColIdentUserID)
	IdentityKind   = sqlb.Col[IdentifierKind](TableIdentity, ColIdentKind)
	IdentityValue  = sqlb.Col[string](TableIdentity, ColIdentValue)
)

// User is the core's own columns and nothing reached by a join.
type User struct {
	ID        string
	Email     string
	BannedAt  *time.Time
	CreatedAt time.Time
}

// UserBindings binds every core column to dst. A function, not a slice,
// because each binding holds a pointer into dst.
func UserBindings(dst *User) []sqlb.Binding {
	return []sqlb.Binding{
		sqlb.Bind(UserID, &dst.ID),
		sqlb.Bind(UserEmail, &dst.Email),
		sqlb.Bind(UserBannedAt, &dst.BannedAt),
		sqlb.Bind(UserCreatedAt, &dst.CreatedAt),
	}
}

// NotBanned is the single definition of the rule. Every listing query includes
// it and no Option can remove it, because options only add.
func NotBanned() sqlb.Predicate { return sqlb.IsNull(UserBannedAt) }

// Composite is a destination whose shape the application declares: the core
// user plus whatever plugins and application tables it composes.
type Composite interface {
	Joins() []sqlb.Join
	Bindings() []sqlb.Binding
}

// SignUpEvent is passed to the AfterSignUp hook.
type SignUpEvent struct {
	User *User
}

// Hooks are the application's write points inside the library's transaction.
// Each receives the DBTX the library is writing through, so the application's
// row and the library's row commit or roll back together.
type Hooks struct {
	AfterSignUp func(ctx context.Context, tx sqlb.DBTX, e *SignUpEvent) error
}

// Auth is the library handle: dialect, hooks, and the query methods.
type Auth struct {
	dialect sqlb.Dialect
	hooks   Hooks
}

// Option configures New.
type Option func(*Auth)

// WithDialect sets the engine. Required.
func WithDialect(d sqlb.Dialect) Option { return func(a *Auth) { a.dialect = d } }

// WithHooks installs the application's hooks.
func WithHooks(h Hooks) Option { return func(a *Auth) { a.hooks = h } }

// New wires the handle. In the real library this is where plugin aliases are
// checked for collisions and configuration is validated.
func New(opts ...Option) *Auth {
	a := &Auth{dialect: sqlb.PostgresDialect{}}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Select starts a statement in the configured dialect.
func (a *Auth) Select() *sqlb.AnySelect { return sqlb.NewSelect(a.dialect) }

// singleRow caps lookups that must resolve to at most one row.
const singleRow = 1

// LoadUserQuery returns the core user plus every column the composite adds,
// in one statement.
func (a *Auth) LoadUserQuery(userID string, dst Composite) (sqlb.Query, error) {
	return a.Select().
		Columns(dst.Bindings()...).
		From(TableUser).
		Join(dst.Joins()...).
		Where(sqlb.Eq(UserID, userID)).
		Limit(singleRow).
		Build()
}

// UserByEmailQuery is the login lookup. Both sides go through lower() to
// match the uniqueness index.
func (a *Auth) UserByEmailQuery(email string, dst *User) (sqlb.Query, error) {
	return a.Select().
		Columns(UserBindings(dst)...).
		From(TableUser).
		Where(sqlb.Eq(sqlb.Lower(UserEmail), email), NotBanned()).
		Limit(singleRow).
		Build()
}

// AdminUserListQuery is the paginated listing, open to the application through
// opts. NotBanned is written before Apply and survives every option.
func (a *Auth) AdminUserListQuery(limit, offset int, dst *User, opts ...sqlb.Option) (sqlb.Query, error) {
	return a.Select().
		Columns(UserBindings(dst)...).
		From(TableUser).
		Where(NotBanned()).
		Order(sqlb.OrderBy(UserCreatedAt, sqlb.SortDesc)).
		Limit(limit).
		Offset(offset).
		Apply(opts...).
		Build()
}

// insertUserSQL is the one write the cookbook needs. The real library uses
// the insert builder once it exists; here the point is the hook, not the
// INSERT.
const insertUserSQL = `INSERT INTO auth_user (id, primary_email) VALUES ($1, $2)`

// SignUp writes the user through tx, then runs AfterSignUp through the same
// tx. The caller owns the transaction: it began it and it commits or rolls
// back on the returned error.
func (a *Auth) SignUp(ctx context.Context, tx sqlb.DBTX, id, email string) (*User, error) {
	if _, err := tx.ExecContext(ctx, insertUserSQL, id, email); err != nil {
		return nil, err
	}
	u := &User{ID: id, Email: email}
	if a.hooks.AfterSignUp != nil {
		if err := a.hooks.AfterSignUp(ctx, tx, &SignUpEvent{User: u}); err != nil {
			return nil, err
		}
	}
	return u, nil
}
