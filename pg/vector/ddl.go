package vector

import "github.com/ubgo/sqlb/pg"

// Extension is the pgvector extension this package's constructs need, for
// pg.CreateExtension(Extension), so a library installs what it uses without
// spelling its name.
var Extension = pg.Extension{Name: "vector"}

// HNSW and IVFFlat are pgvector's index methods. HNSW builds slower and
// answers better; IVFFlat needs rows in the table before it is built, because
// it clusters what it finds.
const (
	HNSW    pg.IndexMethod = "hnsw"
	IVFFlat pg.IndexMethod = "ivfflat"
)

// The operator classes, one per distance, typed to the vector kind they index:
// an index is used only by a query ordering by the same distance.
var (
	CosineOps     = pg.NewOpClass[Vector]("vector_cosine_ops")
	L2Ops         = pg.NewOpClass[Vector]("vector_l2_ops")
	IPOps         = pg.NewOpClass[Vector]("vector_ip_ops")
	L1Ops         = pg.NewOpClass[Vector]("vector_l1_ops")
	HalfCosineOps = pg.NewOpClass[HalfVector]("halfvec_cosine_ops")
	HalfL2Ops     = pg.NewOpClass[HalfVector]("halfvec_l2_ops")
	HalfIPOps     = pg.NewOpClass[HalfVector]("halfvec_ip_ops")
)

// M is HNSW's connections per layer, 16 by default: higher answers better and
// builds slower.
func M(n int64) pg.IndexParam { return pg.NewIndexParam("m", n) }

// EfConstruction is HNSW's candidate list while building, 64 by default.
func EfConstruction(n int64) pg.IndexParam { return pg.NewIndexParam("ef_construction", n) }

// Lists is IVFFlat's cluster count; rows/1000 is pgvector's starting point.
func Lists(n int64) pg.IndexParam { return pg.NewIndexParam("lists", n) }
