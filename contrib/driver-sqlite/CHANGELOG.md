# Changelog

All notable changes to `github.com/ubgo/sqlb/contrib/driver-sqlite` are documented here. The format follows Keep a Changelog, and versioning is SemVer with the pre-GA convention that `v0.x` may break. Released together with the parent `github.com/ubgo/sqlb` module.

## [Unreleased]

### Added

- `Open` and `Memory`, which open a SQLite database with the pragmas a library-owned schema needs: WAL, a busy timeout, foreign key enforcement and `synchronous=NORMAL`.
- `WithPragma`, `WithoutDefaultPragmas` and `WithMaxOpenConns`.
- The conformance run: `sqlbtest` against an in-memory database and against a file-backed one.

[Unreleased]: https://github.com/ubgo/sqlb/commits/main
