// Package enginetest proves contrib/vet catalogue and drift checks on a real database.
//
// It is its own module, never released, so what these tests need to open a
// database stays out of the module under test: a consumer of that module
// requires only what its library code imports.
package enginetest
