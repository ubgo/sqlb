// Package sqlb is a typed SQL layer for Go libraries that own database tables
// and cannot depend on an ORM.
//
// The problem it solves is a specific one. A library such as an auth package
// ships its own tables, and the application that embeds it has tables of its
// own. The library must let the application join across that boundary, filter
// on its own columns and scan into its own structs, without the library
// knowing any of it, and without dragging in a code generator or an ORM that
// the application may already have a different copy of.
//
// One package declares tables with typed columns; another joins them, and the
// compiler checks every column, join condition and scan target:
//
//	var Users = sqlb.Table{Name: "app_user", Alias: "u"}
//	var (
//		UserID    = sqlb.Col[string](Users, "id")
//		UserEmail = sqlb.Col[string](Users, "email")
//	)
//
//	var id, email string
//	q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
//		Columns(sqlb.Bind(UserID, &id), sqlb.Bind(UserEmail, &email)).
//		From(Users).
//		Where(sqlb.Eq(UserEmail, "a@example.com")).
//		Build()
//	err = sqlb.One(ctx, db, q)
//
// Nothing is passed to One for the destination, because the query already
// carries it. Bind captured the pointer, Build collected the pointers in
// SELECT order, and Scan writes straight through them. The column list and
// the scan targets come from one slice, so they cannot drift.
//
// The condition that follows from it: a [Query] is not safe for concurrent
// use. Executing one from two goroutines is a data race, because those
// pointers are the caller's variables. Build the
// query where its destination lives, per call. One and Each refuse an
// overlapping execution with [ErrQueryInUse] rather than interleaving rows,
// which is detection and not prevention.
//
// [github.com/ubgo/sqlb/scan] is the other strategy and does not have that
// hazard: it returns values rather than writing through pointers, so one
// Reader built with fresh per-row destinations and concurrency-safe mapping
// functions can be shared between executions. Neither
// is deprecated. Binding is shorter to write and fits most queries; scanning
// is what a query shared between goroutines, cached, or held in a struct
// field needs.
//
// # This package is a facade
//
// Everything here is an alias or a forwarder. The implementation lives in one
// package per layer, and a caller who wants only part of it can import that
// part directly:
//
//	render   placeholders, identifier quoting, bound values; standard library only
//	dialect  one package per engine: dialect/postgres, dialect/sqlite, dialect/mysql, dialect/clickhouse
//	expr     tables, typed columns, predicates, aggregates, windows
//	stmt     SELECT, INSERT, UPDATE, DELETE, clause slots, Query
//	exec     running a statement and iterating its rows
//	bind     results written through caller pointers, re-exported here
//	scan     results returned as values, imported directly
//	schema   what tables exist, for generators and migrations, imported directly
//
// Every layer compiles and is useful without anything above it and never
// imports upward, which is what lets a package outside this module replace any
// one of them. scan and schema are absent from this facade because their names
// would collide with ones already here, not because they are lesser.
//
// # What belongs here
//
// The core holds only what Postgres, MySQL 8, SQLite and ClickHouse all
// accept with the same syntax and the same meaning. Anything one engine
// spells differently lives in that engine's package, [github.com/ubgo/sqlb/pg],
// [github.com/ubgo/sqlb/sqlite], [github.com/ubgo/sqlb/mysql] or
// [github.com/ubgo/sqlb/ch], even when two
// engines agree.
//
// All four engines have dialect and construct packages. Engine constructs
// record unsupported use through Args.Unsupported, so a wrong-dialect render
// fails at Build with ErrUnsupportedDialect. Server versions and installed
// extensions remain runtime concerns.
//
// # What it is not
//
// It is not an ORM: there is no relation graph, lazy loading or model generator.
// The ddl package builds explicitly authored schema statements. schema describes
// tables and their dependencies; contrib/migrate can plan creation, install
// contributed DDL and execute checksum-tracked migrations. These are opt-in
// layers, so a caller building queries need not use schema management.
//
// # Zero dependencies
//
// This module imports nothing outside the standard library, and a CI gate
// fails the build if that changes. A driver is a dependency, so driver
// integrations are separate modules under contrib/, and each one proves
// itself by running the shared conformance suite in
// [github.com/ubgo/sqlb/sqlbtest] against a real database.
package sqlb
