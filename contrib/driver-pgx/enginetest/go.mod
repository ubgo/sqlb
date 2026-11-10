module github.com/ubgo/sqlb/contrib/driver-pgx/enginetest

go 1.24.0

require (
	github.com/jackc/pgx/v5 v5.7.5
	github.com/ubgo/sqlb v0.1.0
	github.com/ubgo/sqlb/contrib/driver-pgx v0.0.0-00010101000000-000000000000
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

// Test-only and never released, so the replace never reaches a consumer:
// with GOWORK=off the tests still run against this checkout.
replace github.com/ubgo/sqlb/contrib/driver-pgx => ../
