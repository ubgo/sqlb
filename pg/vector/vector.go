// Package vector is the pgvector extension: the four vector types, every
// distance operator and function, the utility functions, aggregates,
// arithmetic, casts, and the query-time settings. Needs CREATE EXTENSION
// vector on the server. Wire forms are pgvector's text I/O, so any
// database/sql driver carries them without a codec.
package vector

import (
	"database/sql/driver"
	"errors"
	"strconv"
	"strings"

	"github.com/ubgo/sqlb"
	"github.com/ubgo/sqlb/pg"
)

// Construct names, as they appear in ErrUnsupportedDialect.
const (
	construct = "vector"

	opL2      = "<->"
	opCosine  = "<=>"
	opInner   = "<#>"
	opL1      = "<+>"
	opHamming = "<~>"
	opJaccard = "<%>"

	fnL2Distance      = "l2_distance"
	fnCosineDistance  = "cosine_distance"
	fnInnerProduct    = "inner_product"
	fnL1Distance      = "l1_distance"
	fnHammingDistance = "hamming_distance"
	fnJaccardDistance = "jaccard_distance"
	fnVectorDims      = "vector_dims"
	fnVectorNorm      = "vector_norm"
	fnL2Norm          = "l2_norm"
	fnL2Normalize     = "l2_normalize"
	fnSubvector       = "subvector"
	fnBinaryQuantize  = "binary_quantize"
	fnSum             = "sum"
	fnAvg             = "avg"

	typeVector = "vector"
	typeHalf   = "halfvec"
	typeSparse = "sparsevec"
	typeBit    = "bit"

	// Settings pgvector reads at query time. SetEfSearch and friends bind
	// them for the transaction through set_config(..., true).
	SettingHNSWEfSearch          = "hnsw.ef_search"
	SettingHNSWIterativeScan     = "hnsw.iterative_scan"
	SettingHNSWMaxScanTuples     = "hnsw.max_scan_tuples"
	SettingHNSWScanMemMultiplier = "hnsw.scan_mem_multiplier"
	SettingIVFFlatProbes         = "ivfflat.probes"
	SettingIVFFlatIterativeScan  = "ivfflat.iterative_scan"
	SettingIVFFlatMaxProbes      = "ivfflat.max_probes"
	SettingEnableSeqscan         = "enable_seqscan"
	SettingEnableIndexscan       = "enable_indexscan"
)

// IterativeScan is a value for hnsw.iterative_scan or ivfflat.iterative_scan.
type IterativeScan string

// The iterative-scan modes. Off is the default and can return fewer rows
// than LIMIT once a filter rejects candidates; the other two keep scanning,
// and relaxed order trades exact ordering for speed.
const (
	ScanOff          IterativeScan = "off"
	ScanStrictOrder  IterativeScan = "strict_order"  // hnsw only
	ScanRelaxedOrder IterativeScan = "relaxed_order" // hnsw and ivfflat
)

// ErrFormat is returned by Scan for a value not in the type's text form.
var ErrFormat = errors.New("vector: value is not in pgvector text form")

// Vector is a vector(n): up to 2000 float32 elements, text form "[1,2,3]".
type Vector []float32

// HalfVector is a halfvec(n): up to 4000 half-precision elements. Go has no
// float16, so it carries float32 and the server rounds. Text form "[1,2,3]".
type HalfVector []float32

// SparseVector is a sparsevec(n): only the non-zero elements, text form
// "{1:1.5,3:2}/5" with 1-based indices. Indices must be strictly increasing.
type SparseVector struct {
	Dimensions int32
	Indices    []int32 // 0-based, as pgvector-go keeps them
	Values     []float32
}

// BitVector is a bit(n) used as a binary vector, text form "0101".
type BitVector string

// Dense is a vector type with element-wise distance operators.
type Dense interface {
	Vector | HalfVector | SparseVector
}

// Elementwise is a vector type with arithmetic and concatenation.
type Elementwise interface{ Vector | HalfVector }

func parseList(s string, into *[]float32) error {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return ErrFormat
	}
	body := s[1 : len(s)-1]
	if body == "" {
		*into = []float32{}
		return nil
	}
	parts := strings.Split(body, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return errors.Join(ErrFormat, err)
		}
		out = append(out, float32(f))
	}
	*into = out
	return nil
}

func formatList(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func text(src any) (string, bool, error) {
	switch t := src.(type) {
	case []byte:
		return string(t), true, nil
	case string:
		return t, true, nil
	case nil:
		return "", false, nil
	default:
		return "", false, ErrFormat
	}
}

// Scan parses "[1,2,3]".
func (v *Vector) Scan(src any) error {
	s, ok, err := text(src)
	if err != nil || !ok {
		*v = nil
		return err
	}
	var out []float32
	if err := parseList(s, &out); err != nil {
		return err
	}
	*v = out
	return nil
}

// Value renders "[1,2,3]".
func (v Vector) Value() (driver.Value, error) { return formatList(v), nil }

// Scan parses "[1,2,3]" into float32. The server has already rounded to half
// precision, so the value read back is not the value written.
func (v *HalfVector) Scan(src any) error {
	s, ok, err := text(src)
	if err != nil || !ok {
		*v = nil
		return err
	}
	var out []float32
	if err := parseList(s, &out); err != nil {
		return err
	}
	*v = out
	return nil
}

// Value renders "[1,2,3]". The server rounds each element to half precision.
func (v HalfVector) Value() (driver.Value, error) { return formatList(v), nil }

// NewSparse builds a SparseVector from a dense slice, keeping the non-zero
// elements, the same as pgvector-go's NewSparseVector.
func NewSparse(dense []float32) SparseVector {
	sv := SparseVector{Dimensions: int32(len(dense))}
	for i, f := range dense {
		if f != 0 {
			sv.Indices = append(sv.Indices, int32(i))
			sv.Values = append(sv.Values, f)
		}
	}
	return sv
}

// Scan parses "{1:1.5,3:2}/5".
func (v *SparseVector) Scan(src any) error {
	s, ok, err := text(src)
	if err != nil || !ok {
		*v = SparseVector{}
		return err
	}
	s = strings.TrimSpace(s)
	slash := strings.LastIndexByte(s, '/')
	if len(s) < 3 || s[0] != '{' || slash < 0 || s[slash-1] != '}' {
		return ErrFormat
	}
	dims, err := strconv.ParseInt(s[slash+1:], 10, 32)
	if err != nil {
		return errors.Join(ErrFormat, err)
	}
	out := SparseVector{Dimensions: int32(dims)}
	body := s[1 : slash-1]
	if body == "" {
		*v = out
		return nil
	}
	for _, pair := range strings.Split(body, ",") {
		k, val, found := strings.Cut(pair, ":")
		if !found {
			return ErrFormat
		}
		idx, err := strconv.ParseInt(strings.TrimSpace(k), 10, 32)
		if err != nil {
			return errors.Join(ErrFormat, err)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(val), 32)
		if err != nil {
			return errors.Join(ErrFormat, err)
		}
		out.Indices = append(out.Indices, int32(idx-1))
		out.Values = append(out.Values, float32(f))
	}
	*v = out
	return nil
}

// String is the text form, so a logged parameter reads as pgvector sees it.
func (v SparseVector) String() string {
	s, err := v.Value()
	if err != nil {
		return err.Error()
	}
	return s.(string)
}

// Value renders "{1:1.5,3:2}/5".
func (v SparseVector) Value() (driver.Value, error) {
	if len(v.Indices) != len(v.Values) {
		return nil, ErrFormat
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := range v.Indices {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(int64(v.Indices[i])+1, 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatFloat(float64(v.Values[i]), 'g', -1, 32))
	}
	b.WriteString("}/")
	b.WriteString(strconv.FormatInt(int64(v.Dimensions), 10))
	return b.String(), nil
}

// Scan accepts "0101". Value renders it unchanged.
func (v *BitVector) Scan(src any) error {
	s, ok, err := text(src)
	if err != nil || !ok {
		*v = ""
		return err
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '0' && s[i] != '1' {
			return ErrFormat
		}
	}
	*v = BitVector(s)
	return nil
}

// Value sends the bit string unchanged.
func (v BitVector) Value() (driver.Value, error) { return string(v), nil }

// ---------------------------------------------------------------------------
// Distance operators. Each is an Expr[float64], so one expression is a
// SELECT item, a WHERE operand and an ORDER BY term, and an ORDER BY on it
// with LIMIT is what an HNSW or IVFFlat index serves.
// ---------------------------------------------------------------------------

// L2 renders a <-> b, the Euclidean distance.
func L2[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".L2", opL2, a, b)
}

// Cosine renders a <=> b, the cosine distance.
func Cosine[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".Cosine", opCosine, a, b)
}

// InnerProduct renders a <#> b, the NEGATIVE inner product, because
// Postgres indexes support ascending order only. InnerProductOf is the
// positive function form.
func InnerProduct[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".InnerProduct", opInner, a, b)
}

// L1 renders a <+> b, the taxicab distance.
func L1[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".L1", opL1, a, b)
}

// Hamming renders a <~> b on binary vectors.
func Hamming(a, b sqlb.Expr[BitVector]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".Hamming", opHamming, a, b)
}

// Jaccard renders a <%> b on binary vectors.
func Jaccard(a, b sqlb.Expr[BitVector]) sqlb.Expr[float64] {
	return pg.BinOp[float64](construct+".Jaccard", opJaccard, a, b)
}

// ---------------------------------------------------------------------------
// Distance functions. The same numbers as the operators, for the places an
// operator cannot go: an argument, a CASE branch, a check constraint.
// ---------------------------------------------------------------------------

// L2Distance renders l2_distance(a, b), the same number as the <-> operator.
// The function form goes where an operator cannot: a function argument, a
// CASE branch, a check constraint. Only the operator uses an index.
func L2Distance[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".L2Distance", fnL2Distance, a, b)
}

// CosineDistance renders cosine_distance(a, b), the function form of <=>.
func CosineDistance[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".CosineDistance", fnCosineDistance, a, b)
}

// InnerProductOf renders inner_product(a, b), the positive value.
func InnerProductOf[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".InnerProductOf", fnInnerProduct, a, b)
}

// L1Distance renders l1_distance(a, b), the function form of <+>.
func L1Distance[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".L1Distance", fnL1Distance, a, b)
}

// HammingDistance renders hamming_distance(a, b), the function form of <~>.
func HammingDistance(a, b sqlb.Expr[BitVector]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".HammingDistance", fnHammingDistance, a, b)
}

// JaccardDistance renders jaccard_distance(a, b), the function form of <%>.
func JaccardDistance(a, b sqlb.Expr[BitVector]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".JaccardDistance", fnJaccardDistance, a, b)
}

// ---------------------------------------------------------------------------
// Utility functions.
// ---------------------------------------------------------------------------

// Dims renders vector_dims(v), the element count. It is for vector only.
func Dims(v sqlb.Expr[Vector]) sqlb.Expr[int32] {
	return pg.Func[int32](construct+".Dims", fnVectorDims, v)
}

// Norm renders vector_norm(v), the Euclidean length. It is for vector only;
// L2Norm is the halfvec and sparsevec spelling of the same thing.
func Norm(v sqlb.Expr[Vector]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".Norm", fnVectorNorm, v)
}

// L2Norm renders l2_norm(v), the Euclidean length of a halfvec or sparsevec.
// Postgres names this function differently per type, hence the two helpers.
func L2Norm[V HalfVector | SparseVector](v sqlb.Expr[V]) sqlb.Expr[float64] {
	return pg.Func[float64](construct+".L2Norm", fnL2Norm, v)
}

// L2Normalize renders l2_normalize(v): the unit-length vector.
func L2Normalize[V Dense](v sqlb.Expr[V]) sqlb.Expr[V] {
	return pg.Func[V](construct+".L2Normalize", fnL2Normalize, v)
}

// Subvector renders subvector(v, start, count), 1-based, the expression an
// index on the first dimensions of a Matryoshka embedding is built on.
func Subvector[V Elementwise](v sqlb.Expr[V], start, count int) sqlb.Expr[V] {
	return pg.Func[V](construct+".Subvector", fnSubvector, v, sqlb.Param(start), sqlb.Param(count))
}

// BinaryQuantize renders binary_quantize(v): one bit per element, positive
// is 1. Cast it with CastBit to compare with a bit(n) column.
func BinaryQuantize[V Elementwise](v sqlb.Expr[V]) sqlb.Expr[BitVector] {
	return pg.Func[BitVector](construct+".BinaryQuantize", fnBinaryQuantize, v)
}

// ---------------------------------------------------------------------------
// Aggregates, element-wise. The core Sum and Avg are for numbers.
// ---------------------------------------------------------------------------

// Sum renders sum(v), the element-wise total. The core Sum is for numbers;
// this one stays typed as the vector, so the result feeds another operator.
func Sum[V Dense](v sqlb.Expr[V], opts ...sqlb.AggOption) sqlb.Expr[V] {
	return pg.Expr[V](construct+".Sum", sqlb.Aggregate[V](fnSum, []sqlb.SQLer{v}, opts...).SQL)
}

// Avg renders avg(v), the element-wise mean: the centroid of a group. There
// is no avg(sparsevec) in pgvector, so this takes Elementwise only.
func Avg[V Elementwise](v sqlb.Expr[V], opts ...sqlb.AggOption) sqlb.Expr[V] {
	return pg.Expr[V](construct+".Avg", sqlb.Aggregate[V](fnAvg, []sqlb.SQLer{v}, opts...).SQL)
}

// ---------------------------------------------------------------------------
// Arithmetic, element-wise, and concatenation.
// ---------------------------------------------------------------------------

// Add renders a + b, element-wise. The two operands must have equal
// dimensions or the server raises an error.
func Add[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[V] {
	return pg.BinOp[V](construct+".Add", sqlb.OpAdd, a, b)
}

// Sub renders a - b, element-wise.
func Sub[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[V] {
	return pg.BinOp[V](construct+".Sub", sqlb.OpSub, a, b)
}

// Mul renders a * b, element-wise, not a dot product. InnerProductOf is the
// dot product.
func Mul[V Dense](a, b sqlb.Expr[V]) sqlb.Expr[V] {
	return pg.BinOp[V](construct+".Mul", sqlb.OpMul, a, b)
}

// Concat renders a || b: the two vectors end to end, so the result has the
// sum of their dimensions.
func Concat[V Elementwise](a, b sqlb.Expr[V]) sqlb.Expr[V] {
	return pg.BinOp[V](construct+".Concat", sqlb.OpConcat, a, b)
}

// ---------------------------------------------------------------------------
// Casts and typed parameters. A bare $n has no type until Postgres sees the
// other operand, so a parameter that is a function argument needs the cast.
// dims 0 renders the type without a dimension.
// ---------------------------------------------------------------------------

func typeName(base string, dims int) string {
	if dims <= 0 {
		return base
	}
	return base + "(" + strconv.Itoa(dims) + ")"
}

func cast[R any](name string, e sqlb.SQLer, typ string, dims int) sqlb.Expr[R] {
	return pg.Expr[R](construct+"."+name, func(a *sqlb.Args) string { return e.SQL(a) + "::" + typeName(typ, dims) })
}

// CastVector renders e::vector(dims). e may be a float array, text, or
// another vector type.
func CastVector[T any](e sqlb.Expr[T], dims int) sqlb.Expr[Vector] {
	return cast[Vector]("CastVector", e, typeVector, dims)
}

// CastHalf renders e::halfvec(dims). An expression index on this cast is how
// a vector column gets a half-precision index.
func CastHalf[T any](e sqlb.Expr[T], dims int) sqlb.Expr[HalfVector] {
	return cast[HalfVector]("CastHalf", e, typeHalf, dims)
}

// CastSparse renders e::sparsevec(dims).
func CastSparse[T any](e sqlb.Expr[T], dims int) sqlb.Expr[SparseVector] {
	return cast[SparseVector]("CastSparse", e, typeSparse, dims)
}

// CastBit renders e::bit(dims); dims is required, a bare bit is bit(1).
func CastBit[T any](e sqlb.Expr[T], dims int) sqlb.Expr[BitVector] {
	return cast[BitVector]("CastBit", e, typeBit, dims)
}

// ParamVector binds v and casts it: $n::vector(len). The dimension comes
// from the value, so it cannot disagree with it.
func ParamVector(v Vector) sqlb.Expr[Vector] {
	return cast[Vector]("ParamVector", sqlb.Param(v), typeVector, len(v))
}

// ParamHalf binds v as $n::halfvec(len).
func ParamHalf(v HalfVector) sqlb.Expr[HalfVector] {
	return cast[HalfVector]("ParamHalf", sqlb.Param(v), typeHalf, len(v))
}

// ParamSparse binds v as $n::sparsevec(dims), the dimension count taken from
// the value, not from the number of non-zero elements.
func ParamSparse(v SparseVector) sqlb.Expr[SparseVector] {
	return cast[SparseVector]("ParamSparse", sqlb.Param(v), typeSparse, int(v.Dimensions))
}

// ParamBit binds v as $n::bit(len), the length taken from the string.
func ParamBit(v BitVector) sqlb.Expr[BitVector] {
	return cast[BitVector]("ParamBit", sqlb.Param(v), typeBit, len(v))
}

// ---------------------------------------------------------------------------
// Query-time settings, bound for the transaction through set_config. Run
// the returned expression in a SELECT inside the transaction that runs the
// search; SET cannot take a bind parameter, set_config can.
// ---------------------------------------------------------------------------

// SetEfSearch binds hnsw.ef_search: candidates per HNSW scan, default 40.
func SetEfSearch(n int) sqlb.Expr[string] { return pg.SetLocal(SettingHNSWEfSearch, strconv.Itoa(n)) }

// SetProbes binds ivfflat.probes: lists per IVFFlat scan, default 1.
func SetProbes(n int) sqlb.Expr[string] { return pg.SetLocal(SettingIVFFlatProbes, strconv.Itoa(n)) }

// SetHNSWIterativeScan binds hnsw.iterative_scan, so a filtered search keeps
// scanning until LIMIT rows pass the WHERE clause instead of returning fewer.
// ScanRelaxedOrder is faster and may return rows slightly out of order; wrap
// the scan in a MATERIALIZED CTE and sort again to fix that.
func SetHNSWIterativeScan(mode IterativeScan) sqlb.Expr[string] {
	return pg.SetLocal(SettingHNSWIterativeScan, string(mode))
}

// SetIVFFlatIterativeScan binds ivfflat.iterative_scan. IVFFlat supports
// ScanRelaxedOrder only, never ScanStrictOrder.
func SetIVFFlatIterativeScan(mode IterativeScan) sqlb.Expr[string] {
	return pg.SetLocal(SettingIVFFlatIterativeScan, string(mode))
}

// SetMaxScanTuples binds hnsw.max_scan_tuples, the ceiling on how far an
// iterative scan goes before it gives up and returns what it has.
func SetMaxScanTuples(n int) sqlb.Expr[string] {
	return pg.SetLocal(SettingHNSWMaxScanTuples, strconv.Itoa(n))
}

// SetMaxProbes binds ivfflat.max_probes, the IVFFlat ceiling matching
// SetMaxScanTuples.
func SetMaxProbes(n int) sqlb.Expr[string] {
	return pg.SetLocal(SettingIVFFlatMaxProbes, strconv.Itoa(n))
}

// ForceIndex binds enable_seqscan = off, so the planner takes the vector
// index. It is for proving an index is used, never for production traffic.
func ForceIndex() sqlb.Expr[string] { return pg.SetLocal(SettingEnableSeqscan, "off") }

// ForceExact binds enable_indexscan = off, so the search is exact and slow.
// It is how a recall measurement gets its ground truth.
func ForceExact() sqlb.Expr[string] { return pg.SetLocal(SettingEnableIndexscan, "off") }
