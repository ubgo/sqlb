package ltree

import "github.com/ubgo/sqlb/pg"

// Extension is the ltree extension this package's constructs need, for
// pg.CreateExtension(Extension), so a library installs what it uses without
// spelling its name.
var Extension = pg.Extension{Name: "ltree"}

// GistOps is ltree's operator class, which serves the ancestor and
// descendant operators; pg.GiST is its method.
var GistOps = pg.NewOpClass[Path]("gist_ltree_ops")
