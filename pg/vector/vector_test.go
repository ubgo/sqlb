package vector

import (
	"errors"
	"testing"

	"github.com/ubgo/sqlb"
)

var (
	items = sqlb.Table{Name: "items", Alias: "i"}
	emb   = sqlb.Col[Vector](items, "embedding")
	half  = sqlb.Col[HalfVector](items, "half")
	spv   = sqlb.Col[SparseVector](items, "sparse")
	bits  = sqlb.Col[BitVector](items, "bits")
	arr   = sqlb.Col[[]float32](items, "arr")
)

func TestVector_RoundTrip(t *testing.T) {
	in := Vector{0.1, -2, 3.5}
	v, err := in.Value()
	if err != nil || v != "[0.1,-2,3.5]" {
		t.Fatalf("value %v %v", v, err)
	}
	var out Vector
	if err := out.Scan([]byte(v.(string))); err != nil || len(out) != 3 || out[2] != 3.5 {
		t.Fatalf("scan %v %v", out, err)
	}
	if err := out.Scan("[]"); err != nil || len(out) != 0 || out == nil {
		t.Fatalf("empty: %v %v", out, err)
	}
	if err := out.Scan(nil); err != nil || out != nil {
		t.Fatalf("nil: %v %v", out, err)
	}
	for _, bad := range []any{"1,2", "[a]", 42} {
		if err := out.Scan(bad); !errors.Is(err, ErrFormat) {
			t.Fatalf("%v: want ErrFormat, got %v", bad, err)
		}
	}
}

func TestHalfVector_RoundTrip(t *testing.T) {
	v, err := HalfVector{1, 2}.Value()
	if err != nil || v != "[1,2]" {
		t.Fatalf("value %v %v", v, err)
	}
	var out HalfVector
	if err := out.Scan("[1,2]"); err != nil || len(out) != 2 {
		t.Fatalf("scan %v %v", out, err)
	}
}

func TestSparseVector_RoundTrip(t *testing.T) {
	sv := NewSparse([]float32{0, 1.5, 0, 2, 0})
	if sv.Dimensions != 5 || len(sv.Indices) != 2 || sv.Indices[0] != 1 || sv.Indices[1] != 3 {
		t.Fatalf("NewSparse %+v", sv)
	}
	v, err := sv.Value()
	if err != nil || v != "{2:1.5,4:2}/5" {
		t.Fatalf("value %v %v", v, err)
	}
	var out SparseVector
	if err := out.Scan(v.(string)); err != nil || out.Dimensions != 5 || out.Indices[1] != 3 || out.Values[0] != 1.5 {
		t.Fatalf("scan %+v %v", out, err)
	}
	if err := out.Scan("{}/3"); err != nil || out.Dimensions != 3 || len(out.Indices) != 0 {
		t.Fatalf("empty %+v %v", out, err)
	}
	for _, bad := range []string{"[1,2]", "{1:1}", "{a:1}/2", "{1}/2"} {
		if err := out.Scan(bad); !errors.Is(err, ErrFormat) {
			t.Fatalf("%q: want ErrFormat, got %v", bad, err)
		}
	}
	if _, err := (SparseVector{Indices: []int32{1}}).Value(); !errors.Is(err, ErrFormat) {
		t.Fatalf("ragged: want ErrFormat, got %v", err)
	}
}

func TestBitVector_RoundTrip(t *testing.T) {
	var b BitVector
	if err := b.Scan([]byte("0101")); err != nil || b != "0101" {
		t.Fatalf("scan %q %v", b, err)
	}
	if err := b.Scan("01a1"); !errors.Is(err, ErrFormat) {
		t.Fatalf("want ErrFormat, got %v", err)
	}
	if v, err := b.Value(); err != nil || v != "0101" {
		t.Fatalf("value %v %v", v, err)
	}
}

// One render check per helper. The SQL is what pgvector documents.
func TestRender(t *testing.T) {
	q := ParamVector(Vector{1, 2, 3})
	h := ParamHalf(HalfVector{1, 2})
	s := ParamSparse(NewSparse([]float32{0, 1}))
	b := ParamBit("101")
	cases := map[string]struct {
		e    sqlb.SQLer
		want string
	}{
		"L2":               {L2(emb, q), `("i"."embedding" <-> $1::vector(3))`},
		"Cosine":           {Cosine(half, h), `("i"."half" <=> $1::halfvec(2))`},
		"InnerProduct":     {InnerProduct(spv, s), `("i"."sparse" <#> $1::sparsevec(2))`},
		"L1":               {L1(emb, q), `("i"."embedding" <+> $1::vector(3))`},
		"Hamming":          {Hamming(bits, b), `("i"."bits" <~> $1::bit(3))`},
		"Jaccard":          {Jaccard(bits, b), `("i"."bits" <%> $1::bit(3))`},
		"L2Distance":       {L2Distance(emb, q), `l2_distance("i"."embedding", $1::vector(3))`},
		"CosineDistance":   {CosineDistance(emb, q), `cosine_distance("i"."embedding", $1::vector(3))`},
		"InnerProductOf":   {InnerProductOf(emb, q), `inner_product("i"."embedding", $1::vector(3))`},
		"L1Distance":       {L1Distance(emb, q), `l1_distance("i"."embedding", $1::vector(3))`},
		"HammingDistance":  {HammingDistance(bits, b), `hamming_distance("i"."bits", $1::bit(3))`},
		"JaccardDistance":  {JaccardDistance(bits, b), `jaccard_distance("i"."bits", $1::bit(3))`},
		"Dims":             {Dims(emb), `vector_dims("i"."embedding")`},
		"Norm":             {Norm(emb), `vector_norm("i"."embedding")`},
		"L2Norm":           {L2Norm(half), `l2_norm("i"."half")`},
		"L2Normalize":      {L2Normalize(emb), `l2_normalize("i"."embedding")`},
		"Subvector":        {Subvector(emb, 1, 3), `subvector("i"."embedding", $1, $2)`},
		"BinaryQuantize":   {BinaryQuantize(emb), `binary_quantize("i"."embedding")`},
		"Sum":              {Sum(emb), `sum("i"."embedding")`},
		"Avg":              {Avg(half), `avg("i"."half")`},
		"Add":              {Add(emb, q), `("i"."embedding" + $1::vector(3))`},
		"Sub":              {Sub(emb, q), `("i"."embedding" - $1::vector(3))`},
		"Mul":              {Mul(emb, q), `("i"."embedding" * $1::vector(3))`},
		"Concat":           {Concat(emb, q), `("i"."embedding" || $1::vector(3))`},
		"CastVector":       {CastVector(arr, 3), `"i"."arr"::vector(3)`},
		"CastVectorNoDims": {CastVector(arr, 0), `"i"."arr"::vector`},
		"CastHalf":         {CastHalf(emb, 3), `"i"."embedding"::halfvec(3)`},
		"CastSparse":       {CastSparse(emb, 3), `"i"."embedding"::sparsevec(3)`},
		"CastBit":          {CastBit(BinaryQuantize(emb), 3), `binary_quantize("i"."embedding")::bit(3)`},
		"SetEfSearch":      {SetEfSearch(100), `set_config('hnsw.ef_search', $1, true)`},
		"SetProbes":        {SetProbes(10), `set_config('ivfflat.probes', $1, true)`},
		"SetHNSWIterative": {SetHNSWIterativeScan(ScanRelaxedOrder), `set_config('hnsw.iterative_scan', $1, true)`},
		"SetIVFIterative":  {SetIVFFlatIterativeScan(ScanRelaxedOrder), `set_config('ivfflat.iterative_scan', $1, true)`},
		"SetMaxScanTuples": {SetMaxScanTuples(20000), `set_config('hnsw.max_scan_tuples', $1, true)`},
		"SetMaxProbes":     {SetMaxProbes(100), `set_config('ivfflat.max_probes', $1, true)`},
		"ForceIndex":       {ForceIndex(), `set_config('enable_seqscan', $1, true)`},
		"ForceExact":       {ForceExact(), `set_config('enable_indexscan', $1, true)`},
	}
	for k, c := range cases {
		q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(c.e).From(items).Build()
		if err != nil {
			t.Errorf("%s: %v", k, err)
			continue
		}
		want := "SELECT " + c.want + ` FROM "items" AS "i"`
		if q.SQL != want {
			t.Errorf("%s:\n got %s\nwant %s", k, q.SQL, want)
		}
		if _, err := sqlb.NewSelect(sqlb.SQLiteDialect{}).Exprs(c.e).From(items).Build(); !errors.Is(err, sqlb.ErrUnsupportedDialect) {
			t.Errorf("%s under sqlite: want ErrUnsupportedDialect, got %v", k, err)
		}
	}
}

// The parameter values the settings bind are the text pgvector expects.
func TestSettings_Args(t *testing.T) {
	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).Exprs(SetEfSearch(100), SetHNSWIterativeScan(ScanStrictOrder), ForceIndex()).Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Args) != 3 || q.Args[0] != "100" || q.Args[1] != "strict_order" || q.Args[2] != "off" {
		t.Fatalf("args %v", q.Args)
	}
}

// The Scan failure arms of every vector type.
//
// Why they need their own test: Scan is the one place a driver's value
// crosses into the type system, and its failure arms are the least exercised
// code in the package because a healthy database never triggers them. They
// are also the arms whose silent failure is worst: a Scan that swallows a
// malformed value leaves a caller holding a zero vector that looks like a
// real one, and a similarity search over zero vectors returns plausible
// nonsense rather than an error.
func TestScan_FailureArms(t *testing.T) {
	t.Run("HalfVector", func(t *testing.T) {
		var v HalfVector
		if err := v.Scan("[1,2]"); err != nil || len(v) != 2 {
			t.Fatalf("valid: %v %v", v, err)
		}
		// A NULL column clears the value rather than leaving the last row's.
		if err := v.Scan(nil); err != nil || v != nil {
			t.Fatalf("nil: %v %v", v, err)
		}
		for _, bad := range []any{"1,2", "[a]", 42} {
			if err := v.Scan(bad); !errors.Is(err, ErrFormat) {
				t.Errorf("%v: want ErrFormat, got %v", bad, err)
			}
		}
	})

	t.Run("SparseVector", func(t *testing.T) {
		var v SparseVector
		if err := v.Scan(nil); err != nil || v.Dimensions != 0 {
			t.Fatalf("nil: %+v %v", v, err)
		}
		// A non-numeric value on the right of the colon. The index parses,
		// so this reaches a different arm from the bad-index case already
		// covered above.
		if err := v.Scan("{1:x}/3"); !errors.Is(err, ErrFormat) {
			t.Errorf("bad value: want ErrFormat, got %v", err)
		}
		if err := v.Scan(42); !errors.Is(err, ErrFormat) {
			t.Errorf("wrong type: want ErrFormat, got %v", err)
		}
	})

	t.Run("BitVector", func(t *testing.T) {
		var v BitVector
		if err := v.Scan(nil); err != nil || v != "" {
			t.Fatalf("nil: %q %v", v, err)
		}
		if err := v.Scan(42); !errors.Is(err, ErrFormat) {
			t.Errorf("wrong type: want ErrFormat, got %v", err)
		}
	})

	t.Run("Vector", func(t *testing.T) {
		var v Vector
		if err := v.Scan(nil); err != nil || v != nil {
			t.Fatalf("nil: %v %v", v, err)
		}
	})
}

// String is what a logged parameter shows. It must be the wire form, so a log
// line can be pasted into psql; and it must not panic on a malformed value,
// because logging runs on the error path where the value is malformed.
func TestSparseVector_String(t *testing.T) {
	if got := NewSparse([]float32{0, 1.5, 0, 2}).String(); got != "{2:1.5,4:2}/4" {
		t.Errorf("String() = %q", got)
	}
	ragged := SparseVector{Dimensions: 3, Indices: []int32{0}}
	if got := ragged.String(); got == "" {
		t.Error("String() on a malformed value must describe the problem, not return empty")
	}
}

// The index-parses-but-value-does-not arm of SparseVector.Scan, distinct from
// the malformed-index arm above: it reaches the second ParseFloat rather than
// the first ParseInt.
func TestSparseVector_ScanBadValueOnly(t *testing.T) {
	var v SparseVector
	if err := v.Scan("{2:notanumber}/5"); !errors.Is(err, ErrFormat) {
		t.Fatalf("want ErrFormat, got %v", err)
	}
}
