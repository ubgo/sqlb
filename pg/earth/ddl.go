package earth

import "github.com/ubgo/sqlb/pg"

// Extension is the earthdistance extension this package's constructs need, for
// pg.CreateExtension(Extension), so a library installs what it uses without
// spelling its name.
var Extension = pg.Extension{Name: "earthdistance"}

// Cube is the extension earthdistance depends on. CREATE EXTENSION does not
// install it unless CASCADE is written, which this module leaves out, so it is
// named here to be installed first.
var Cube = pg.Extension{Name: "cube"}
