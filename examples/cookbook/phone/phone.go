// Package phone is the plugin shape: it borrows a core table under its own
// alias and contributes a join plus bindings to a composite user.
package phone

import (
	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/examples/cookbook/authlite"
)

// TableAliasIdentity is private to this plugin. The core uses "i"; the plugin
// must not, or a composite that joins auth_identity twice is ambiguous.
const TableAliasIdentity = "phi"

// TableIdentity is the core's auth_identity under the plugin's alias.
var TableIdentity = sqlb.Table{Name: authlite.TableNameIdentity, Alias: TableAliasIdentity}

// The core's identity columns under this plugin's alias, so the plugin can
// join to them without the core knowing the plugin exists.
var (
	IdentityUserID = sqlb.Col[string](TableIdentity, authlite.ColIdentUserID)
	IdentityKind   = sqlb.Col[authlite.IdentifierKind](TableIdentity, authlite.ColIdentKind)
	IdentityValue  = sqlb.Col[string](TableIdentity, authlite.ColIdentValue)
)

// Data is what the plugin adds to a composite user.
type Data struct {
	Number string
}

// Exists reports whether the LEFT join found a row. A miss scans the
// COALESCE fallback, so a zero Data is indistinguishable from an empty
// number without this.
func (d *Data) Exists() bool { return d.Number != "" }

// UserJoin is a LEFT join. INNER would drop every user without a phone.
func UserJoin() sqlb.Join {
	return sqlb.Join{
		Type:  sqlb.JoinLeft,
		Table: TableIdentity,
		On: []sqlb.Predicate{
			sqlb.EqExpr(IdentityUserID, authlite.UserID),
			sqlb.Eq(IdentityKind, authlite.KindPhone),
		},
	}
}

// UserBindings binds the plugin's columns to dst.
func UserBindings(dst *Data) []sqlb.Binding {
	empty := sqlb.Literal[string]("''")
	return []sqlb.Binding{
		sqlb.Bind(sqlb.Coalesce(IdentityValue, empty), &dst.Number),
	}
}
