---
name: Bug report
about: The builder rendered the wrong SQL, or a query failed against a database
title: "[bug] "
labels: bug
---

**What happened**

A clear description of the bug.

**The statement**

The Go that built it, reduced as far as it still reproduces:

```go
q, err := sqlb.NewSelect(sqlb.PostgresDialect{}).
	Columns(...).
	From(...).
	Build()
```

**The SQL**

What `q.SQL` and `q.Args` actually were, and what you expected instead. If the database rejected it, paste its error too.

**Environment**

- Module and version: `github.com/ubgo/sqlb vX.Y.Z`, and any contrib module
- Go version, from `go version`
- Engine and driver: Postgres 16 with pgx, SQLite with modernc, and so on
- OS and architecture

**Does `task check` pass on your clone?**

If it fails, paste the failing output. That usually locates the problem faster than the description does.
