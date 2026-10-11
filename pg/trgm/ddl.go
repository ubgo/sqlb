package trgm

import "github.com/ubgo/sqlb/pg"

// Extension is the pg_trgm extension this package's constructs need, for
// pg.CreateExtension(Extension), so a library installs what it uses without
// spelling its name.
var Extension = pg.Extension{Name: "pg_trgm"}

// GinOps and GistOps are the trigram operator classes. GIN is the usual
// choice for similarity and LIKE search; GiST also serves ordering by
// distance. A nullable column takes pg.NullableOps(GinOps).
var (
	GinOps  = pg.NewOpClass[string]("gin_trgm_ops")
	GistOps = pg.NewOpClass[string]("gist_trgm_ops")
)
