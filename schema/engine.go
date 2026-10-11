package schema

import "sort"

// engineTypes is each engine's spelling of every portable type. It is
// description, not action: the DDL renderer in contrib/migrate reads it to
// write CREATE TABLE and sqlb vet reads it to check a Col[T] against a live
// catalogue, and holding it here is what makes those two agree on what a
// bigint is. The keys are the dialect names the render layer reports.
var engineTypes = map[string]map[Type]string{
	"postgres": {
		TypeText: "TEXT", TypeInt: "INTEGER", TypeBigInt: "BIGINT",
		TypeBool: "BOOLEAN", TypeFloat: "DOUBLE PRECISION", TypeBytes: "BYTEA",
		TypeTimestamp: "TIMESTAMPTZ", TypeDate: "DATE", TypeUUID: "UUID",
		TypeJSON: "JSONB", TypeDecimal: "NUMERIC",
	},
	"sqlite": {
		TypeText: "TEXT", TypeInt: "INTEGER", TypeBigInt: "INTEGER",
		TypeBool: "INTEGER", TypeFloat: "REAL", TypeBytes: "BLOB",
		TypeTimestamp: "TEXT", TypeDate: "TEXT", TypeUUID: "TEXT",
		TypeJSON: "TEXT", TypeDecimal: "NUMERIC",
	},
	// MySQL's text is VARCHAR(255) rather than TEXT because a TEXT column cannot
	// be a key or UNIQUE without a prefix length, and a library's email or
	// token column usually is one; a long text column says so with RawType.
	// A timestamp is DATETIME(6), which stores what it is given, written and
	// read in UTC; TIMESTAMP converts through the session's time zone.
	"mysql": {
		TypeText: "VARCHAR(255)", TypeInt: "INT", TypeBigInt: "BIGINT",
		TypeBool: "BOOLEAN", TypeFloat: "DOUBLE", TypeBytes: "LONGBLOB",
		TypeTimestamp: "DATETIME(6)", TypeDate: "DATE", TypeUUID: "CHAR(36)",
		TypeJSON: "JSON", TypeDecimal: "DECIMAL(38, 10)",
	},
	"clickhouse": {
		TypeText: "String", TypeInt: "Int32", TypeBigInt: "Int64",
		TypeBool: "Bool", TypeFloat: "Float64", TypeBytes: "String",
		TypeTimestamp: "DateTime64(3)", TypeDate: "Date", TypeUUID: "UUID",
		TypeJSON: "String", TypeDecimal: "Decimal(38, 10)",
	},
}

// EngineNames reports which engines have a type table, in a stable order, so
// a caller can check before it starts rather than after.
func EngineNames() []string {
	out := make([]string, 0, len(engineTypes))
	for k := range engineTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EngineType is an engine's spelling of a portable type. The second result
// is false when the engine has no table or the type is not portable; a
// caller that can express the type another way, such as Column.Raw, decides
// what that means.
func EngineType(engine string, t Type) (string, bool) {
	m, ok := engineTypes[engine]
	if !ok {
		return "", false
	}
	s, ok := m[t]
	return s, ok
}

// GoTypes is the Go types a portable type accepts, spelled as reflect prints
// them, so []byte is "[]uint8". More than one is allowed where the choice is
// genuinely the caller's, such as whether a timestamp arrives as time.Time or
// as a string. It is the same table Column.Get checks against, exported so a
// tool reading declarations from source applies the one rule.
func GoTypes(t Type) []string {
	return append([]string(nil), goTypes[t]...)
}
