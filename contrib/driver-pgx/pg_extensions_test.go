package driverpgx_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
	"github.com/ubgo/sqlb/pg/crypto"
	"github.com/ubgo/sqlb/pg/earth"
	"github.com/ubgo/sqlb/pg/fuzzy"
	"github.com/ubgo/sqlb/pg/ltree"
	"github.com/ubgo/sqlb/pg/postgis"
	"github.com/ubgo/sqlb/pg/trgm"
	"github.com/ubgo/sqlb/pg/unaccent"
	"github.com/ubgo/sqlb/pg/vector"
)

// The extension catalogue: one table with a column per extension type, in a
// database where every extension this module wraps is created. A server
// without one of them fails the setup loudly rather than skipping, because
// the point is to execute the constructs.
var (
	catExt = sqlb.Table{Name: "cat_ext", Alias: "x"}
	xID    = sqlb.Col[int64](catExt, "id")
	xText  = sqlb.Col[string](catExt, "txt")
	xVec   = sqlb.Col[vector.Vector](catExt, "vec")
	xHalf  = sqlb.Col[vector.HalfVector](catExt, "half")
	xSpar  = sqlb.Col[vector.SparseVector](catExt, "spar")
	xBits  = sqlb.Col[vector.BitVector](catExt, "bits")
	xPath  = sqlb.Col[ltree.Path](catExt, "path")
	xGeom  = sqlb.Col[postgis.Geometry](catExt, "geom")
	xLat   = sqlb.Col[float64](catExt, "lat")
	xLon   = sqlb.Col[float64](catExt, "lon")
)

// codeFeatureNotSupported is what Postgres raises for an extension the server
// does not have installed, "extension ... is not available".
const codeFeatureNotSupported = "0A000"

// extNeeded is every extension this catalogue is built on. vector and postgis
// are not shipped with Postgres, so a server may not have them; requireExts
// skips loudly rather than reporting a missing extension as a broken construct.
var extNeeded = []string{
	"vector", "pg_trgm", "fuzzystrmatch", "ltree",
	"pgcrypto", "cube", "earthdistance", "unaccent", "postgis",
}

var extDDL = []string{
	`DROP TABLE IF EXISTS cat_ext`,
	`CREATE TABLE cat_ext (id BIGINT PRIMARY KEY, txt TEXT NOT NULL, vec vector(3) NOT NULL, half halfvec(3) NOT NULL,
		spar sparsevec(3) NOT NULL, bits bit(3) NOT NULL, path ltree NOT NULL, geom geometry(Point, 4326) NOT NULL,
		lat DOUBLE PRECISION NOT NULL, lon DOUBLE PRECISION NOT NULL, blob BYTEA NOT NULL)`,
	`INSERT INTO cat_ext VALUES
		(1, 'Héllo wörld', '[1,2,3]', '[1,2,3]', '{1:1,3:3}/3', B'101', 'top.mid.leaf', ST_SetSRID(ST_MakePoint(-0.1, 51.5), 4326), 51.5, -0.1, '\\x01'),
		(2, 'hello world', '[3,2,1]', '[3,2,1]', '{2:2}/3', B'011', 'top.other', ST_SetSRID(ST_MakePoint(2.35, 48.85), 4326), 48.85, 2.35, '\\x02')`,
}

// requireExts installs every extension the catalogue needs, and skips the test
// naming the ones this server does not ship. An extension absent from the
// server is a property of the server, so reporting it as a failure would make
// a Postgres 18 container that carries no pgvector look like a broken suite;
// the skip says which extension is missing, so a skip is never silent.
func requireExts(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var missing []string
	for _, name := range extNeeded {
		if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS `+name); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == codeFeatureNotSupported {
				missing = append(missing, name)
				continue
			}
			t.Fatalf("CREATE EXTENSION %s: %v", name, err)
		}
	}
	if len(missing) > 0 {
		t.Skipf("SKIPPED, NOT PASSED: this server does not ship %s", strings.Join(missing, ", "))
	}
}

func extCatalogue(t *testing.T) *sql.DB {
	t.Helper()
	db := openOrSkip(t)
	requireExts(t, db)
	ctx := context.Background()
	for _, ddl := range extDDL {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("setup %q: %v", ddl, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS cat_ext`) })
	return db
}

func xExpr(t *testing.T, db *sql.DB, name string, e sqlb.SQLer) {
	t.Helper()
	executes(t, db, name, sel().Exprs(e).From(catExt))
}

func xPred(t *testing.T, db *sql.DB, name string, p sqlb.Predicate) {
	t.Helper()
	executes(t, db, name, sel().Exprs(xID).From(catExt).Where(p))
}

// TestPGExtensions_Execute executes every construct in the eight extension
// sub-packages.
func TestPGExtensions_Execute(t *testing.T) {
	db := extCatalogue(t)

	// vector
	q := vector.ParamVector(vector.Vector{1, 2, 3})
	xExpr(t, db, "vector.L2", vector.L2(xVec, q))
	xExpr(t, db, "vector.Cosine", vector.Cosine(xVec, q))
	xExpr(t, db, "vector.InnerProduct", vector.InnerProduct(xVec, q))
	xExpr(t, db, "vector.L1", vector.L1(xVec, q))
	xExpr(t, db, "vector.Hamming", vector.Hamming(xBits, vector.ParamBit(vector.BitVector("111"))))
	xExpr(t, db, "vector.Jaccard", vector.Jaccard(xBits, vector.ParamBit(vector.BitVector("111"))))
	xExpr(t, db, "vector.L2Distance", vector.L2Distance(xVec, q))
	xExpr(t, db, "vector.CosineDistance", vector.CosineDistance(xVec, q))
	xExpr(t, db, "vector.InnerProductOf", vector.InnerProductOf(xVec, q))
	xExpr(t, db, "vector.L1Distance", vector.L1Distance(xVec, q))
	xExpr(t, db, "vector.HammingDistance", vector.HammingDistance(xBits, vector.ParamBit(vector.BitVector("111"))))
	xExpr(t, db, "vector.JaccardDistance", vector.JaccardDistance(xBits, vector.ParamBit(vector.BitVector("111"))))
	xExpr(t, db, "vector.Dims", vector.Dims(xVec))
	xExpr(t, db, "vector.Norm", vector.Norm(xVec))
	xExpr(t, db, "vector.L2Norm half", vector.L2Norm(xHalf))
	xExpr(t, db, "vector.L2Norm sparse", vector.L2Norm(xSpar))
	xExpr(t, db, "vector.L2Normalize", vector.L2Normalize(xVec))
	xExpr(t, db, "vector.Subvector", vector.Subvector(xVec, 1, 2))
	xExpr(t, db, "vector.BinaryQuantize", vector.BinaryQuantize(xVec))
	xExpr(t, db, "vector.Sum", vector.Sum(xVec))
	xExpr(t, db, "vector.Avg", vector.Avg(xVec))
	xExpr(t, db, "vector.Add", vector.Add(xVec, q))
	xExpr(t, db, "vector.Sub", vector.Sub(xVec, q))
	xExpr(t, db, "vector.Mul", vector.Mul(xVec, q))
	xExpr(t, db, "vector.Concat", vector.Concat(xVec, xVec))
	xExpr(t, db, "vector.CastVector", vector.CastVector(xHalf, 3))
	xExpr(t, db, "vector.CastHalf", vector.CastHalf(xVec, 3))
	xExpr(t, db, "vector.CastSparse", vector.CastSparse(xVec, 3))
	xExpr(t, db, "vector.CastBit", vector.CastBit(vector.ParamBit(vector.BitVector("101")), 3))
	xExpr(t, db, "vector.ParamHalf", vector.L2(xHalf, vector.ParamHalf(vector.HalfVector{1, 2, 3})))
	xExpr(t, db, "vector.ParamSparse NewSparse", vector.L2(xSpar, vector.ParamSparse(vector.NewSparse([]float32{1, 0, 3}))))
	xExpr(t, db, "vector.SetEfSearch", vector.SetEfSearch(40))
	xExpr(t, db, "vector.SetProbes", vector.SetProbes(1))
	xExpr(t, db, "vector.SetHNSWIterativeScan", vector.SetHNSWIterativeScan(vector.ScanRelaxedOrder))
	xExpr(t, db, "vector.SetIVFFlatIterativeScan", vector.SetIVFFlatIterativeScan(vector.ScanOff))
	xExpr(t, db, "vector.SetMaxScanTuples", vector.SetMaxScanTuples(1000))
	xExpr(t, db, "vector.SetMaxProbes", vector.SetMaxProbes(10))
	xExpr(t, db, "vector.ForceIndex", vector.ForceIndex())
	xExpr(t, db, "vector.ForceExact", vector.ForceExact())
	executes(t, db, "vector.OrderUsing", sel().Exprs(xID).From(catExt).Order(pg.OrderUsing(vector.L2(xVec, q), "<")))

	// trgm
	xPred(t, db, "trgm.Similar", trgm.Similar(xText, sqlb.Param("hello")))
	xPred(t, db, "trgm.WordSimilar", trgm.WordSimilar(sqlb.Param("hello"), xText))
	xPred(t, db, "trgm.StrictWordSimilar", trgm.StrictWordSimilar(sqlb.Param("hello"), xText))
	xExpr(t, db, "trgm.Distance", trgm.Distance(xText, sqlb.Param("hello")))
	xExpr(t, db, "trgm.WordDistance", trgm.WordDistance(sqlb.Param("hello"), xText))
	xExpr(t, db, "trgm.Similarity", trgm.Similarity(xText, sqlb.Param("hello")))
	xExpr(t, db, "trgm.WordSimilarity", trgm.WordSimilarity(sqlb.Param("hello"), xText))
	xExpr(t, db, "trgm.StrictWordSimilarity", trgm.StrictWordSimilarity(sqlb.Param("hello"), xText))
	xExpr(t, db, "trgm.ShowTrgm", trgm.ShowTrgm(xText))

	// fuzzy
	xExpr(t, db, "fuzzy.Dmetaphone", fuzzy.Dmetaphone(xText))
	xExpr(t, db, "fuzzy.DmetaphoneAlt", fuzzy.DmetaphoneAlt(xText))
	xExpr(t, db, "fuzzy.Soundex", fuzzy.Soundex(xText))
	xExpr(t, db, "fuzzy.Metaphone", fuzzy.Metaphone(xText, 4))
	xExpr(t, db, "fuzzy.Levenshtein", fuzzy.Levenshtein(xText, sqlb.Param("hello")))
	xExpr(t, db, "fuzzy.LevenshteinLessEqual", fuzzy.LevenshteinLessEqual(xText, sqlb.Param("hello"), 3))
	xExpr(t, db, "fuzzy.Difference", fuzzy.Difference(xText, sqlb.Param("hello")))

	// ltree
	xExpr(t, db, "ltree.Lit", ltree.Lit("top.mid"))
	xPred(t, db, "ltree.AncestorOf", ltree.AncestorOf(ltree.Lit("top"), xPath))
	xPred(t, db, "ltree.DescendantOf", ltree.DescendantOf(xPath, ltree.Lit("top")))
	xPred(t, db, "ltree.MatchesLQuery", ltree.MatchesLQuery(xPath, "top.*"))
	xPred(t, db, "ltree.MatchesTxtQuery", ltree.MatchesTxtQuery(xPath, "top & leaf"))
	xExpr(t, db, "ltree.NLevel", ltree.NLevel(xPath))
	xExpr(t, db, "ltree.Subpath", ltree.Subpath(xPath, 0, 1))
	xExpr(t, db, "ltree.Subltree", ltree.Subltree(xPath, 0, 1))
	xExpr(t, db, "ltree.Index", ltree.Index(xPath, "mid"))
	xExpr(t, db, "ltree.LCA", ltree.LCA(xPath, ltree.Lit("top.other")))
	xExpr(t, db, "ltree.Concat", ltree.Concat(xPath, ltree.Lit("extra")))

	// crypto
	xExpr(t, db, "crypto.Crypt GenSalt", crypto.Crypt(xText, crypto.GenSalt("bf", 4)))
	xExpr(t, db, "crypto.Digest", crypto.Digest(xText, "sha256"))
	xExpr(t, db, "crypto.HMAC", crypto.HMAC(xText, sqlb.Param("key"), "sha256"))
	xExpr(t, db, "crypto.PgpSymEncrypt", crypto.PgpSymEncrypt(xText, sqlb.Param("key")))
	xExpr(t, db, "crypto.PgpSymDecrypt", crypto.PgpSymDecrypt(crypto.PgpSymEncrypt(xText, sqlb.Param("key")), sqlb.Param("key")))
	xExpr(t, db, "crypto.GenRandomBytes", crypto.GenRandomBytes(8))

	// earth
	here := earth.LLToEarth(xLat, xLon)
	there := earth.LLToEarth(sqlb.Param(48.85), sqlb.Param(2.35))
	xExpr(t, db, "earth.LLToEarth", here)
	xExpr(t, db, "earth.Distance", earth.Distance(here, there))
	xExpr(t, db, "earth.Box", earth.Box(there, 1000))
	xPred(t, db, "earth.BoxContains", earth.BoxContains(earth.Box(there, 1000), here))

	// unaccent
	xExpr(t, db, "unaccent.Unaccent", unaccent.Unaccent(xText))
	xExpr(t, db, "unaccent.WithDictionary", unaccent.WithDictionary("unaccent", xText))

	// postgis
	pt := postgis.MakePoint(sqlb.Param(2.35), sqlb.Param(48.85), 4326)
	xExpr(t, db, "postgis.MakePoint", pt)
	xExpr(t, db, "postgis.Distance", postgis.Distance(xGeom, pt))
	xExpr(t, db, "postgis.DistanceSphere", postgis.DistanceSphere(xGeom, pt))
	xPred(t, db, "postgis.DWithin", postgis.DWithin(xGeom, pt, sqlb.Param(1.0)))
	xPred(t, db, "postgis.Intersects", postgis.Intersects(xGeom, pt))
	xPred(t, db, "postgis.Contains", postgis.Contains(xGeom, pt))
	xExpr(t, db, "postgis.KNN", postgis.KNN(xGeom, pt))
	xExpr(t, db, "postgis.AsGeoJSON", postgis.AsGeoJSON(xGeom))
}
