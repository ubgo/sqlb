// Package enginetest proves contrib/migrate on real databases: SQLite
// always, Postgres when SQLB_PG_DSN is set and MySQL when SQLB_MYSQL_DSN is
// set (task test:pg and test:mysql).
//
// It is its own module, never released, so the database drivers it opens
// those engines with stay out of the migrate module: a library or app that
// imports migrate requires no database driver, and go mod tidy in it finds
// none to add.
package enginetest
