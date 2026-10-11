# `mysql` — MySQL statements and constructs

Everything MySQL in one import: the statements start here, `mysql.Select`, `mysql.Insert`, `mysql.Update`, `mysql.Delete` and the DDL constructors, and the constructs MySQL spells its own way are beside them: the upsert with its row alias, `INSERT IGNORE`, `UPDATE` and `DELETE` with `LIMIT`, locking reads, `WITH ROLLUP`, full-text `MATCH`, the JSON and text functions, and MySQL's keys, indexes, views and triggers.

**Every construct is executed, not only rendered.** `contrib/driver-mysql` runs each against a real MySQL 8.4, and the first runs found three that rendered cleanly and failed there.

The DDL detector in `ddl_coverage_test.go` classifies every statement the server's own `HELP 'Data Definition'` lists. Since 2026-09-24 it has no `partial` and no `left-out` entry: the table options, the remaining `ALTER TABLE` actions, the partition maintenance actions, the procedural trigger steps, `ALTER VIEW`, the `EVENT` and the stored routines all build, and what it still does not build is the seventeen statements it marks `operator` — server, storage and account administration, which a library's schema does not own.

## Full documentation

The page for this package is [`docs/mysql/README.md`](../docs/mysql/README.md), ending in a reference table generated from the source. The documentation index is [`../docs/README.md`](../docs/README.md).
