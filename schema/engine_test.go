package schema_test

import (
	"testing"

	"github.com/ubgo/sqlb/schema"
)

// TestEngineType_CoversEveryPortableTypeOnEveryEngine is the completeness
// check: a portable type an engine cannot spell would make CreateTable fail
// for a declaration Validate accepted.
func TestEngineType_CoversEveryPortableTypeOnEveryEngine(t *testing.T) {
	engines := schema.EngineNames()
	if len(engines) != 4 {
		t.Fatalf("engines = %v", engines)
	}
	for _, e := range engines {
		for _, typ := range schema.TypeValues {
			if _, ok := schema.EngineType(e, typ); !ok {
				t.Errorf("%s has no spelling for %s", e, typ)
			}
		}
	}
	if _, ok := schema.EngineType("oracle", schema.TypeText); ok {
		t.Error("unknown engine reported a spelling")
	}
	if _, ok := schema.EngineType("postgres", schema.Type("money")); ok {
		t.Error("non-portable type reported a spelling")
	}
}

// TestGoTypes_ReturnsACopy pins that a caller cannot edit the table through
// the slice it is handed.
func TestGoTypes_ReturnsACopy(t *testing.T) {
	got := schema.GoTypes(schema.TypeBytes)
	if len(got) != 1 || got[0] != "[]uint8" {
		t.Fatalf("GoTypes(bytes) = %v", got)
	}
	got[0] = "string"
	if again := schema.GoTypes(schema.TypeBytes); again[0] != "[]uint8" {
		t.Error("the table was edited through the returned slice")
	}
	if schema.GoTypes(schema.Type("money")) != nil {
		t.Error("unknown type should report nil")
	}
}
