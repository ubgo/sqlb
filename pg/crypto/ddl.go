package crypto

import "github.com/ubgo/sqlb/pg"

// Extension is the pgcrypto extension this package's constructs need, for
// pg.CreateExtension(Extension), so a library installs what it uses without
// spelling its name.
var Extension = pg.Extension{Name: "pgcrypto"}
