module github.com/ubgo/sqlb/contrib/driver-mysql/enginetest

go 1.24.0

require (
	github.com/ubgo/sqlb v0.1.0
	github.com/ubgo/sqlb/contrib/driver-mysql v0.0.0-00010101000000-000000000000
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/go-sql-driver/mysql v1.10.1 // indirect
)

// Test-only and never released, so the replace never reaches a consumer:
// with GOWORK=off the tests still run against this checkout.
replace github.com/ubgo/sqlb/contrib/driver-mysql => ../
