# driver-sqlite — documentation

Per-symbol cookbook for `github.com/ubgo/sqlb/contrib/driver-sqlite`, which opens SQLite databases for [`github.com/ubgo/sqlb`](https://github.com/ubgo/sqlb) with sensible pragmas.

The module is small on purpose. `database/sql` already does the work; what is here is the pragma set, the reason for each one, and the mechanism that makes them hold for every connection in the pool. Each export has a section in [`features.md`](./features.md).

## Index

| Symbol | Kind | What it is |
|---|---|---|
| [`Open`](./features.md#open) | func | Opens a database with the pragmas applied to every connection. |
| [`Memory`](./features.md#memory) | func | Opens a private in-memory database for tests, pinned to one connection. |
| [`Option`](./features.md#option) | type | Functional option for `Open` and `Memory`. |
| [`WithPragma`](./features.md#withpragma) | func | Adds a pragma, or overrides a default of the same name. |
| [`WithoutDefaultPragmas`](./features.md#withoutdefaultpragmas) | func | Drops `DefaultPragmas` entirely. |
| [`WithMaxOpenConns`](./features.md#withmaxopenconns) | func | Caps the connection pool. |
| [`Pragma`](./features.md#pragma) | type | One PRAGMA name and value. |
| [`DefaultPragmas`](./features.md#defaultpragmas) | var | The four pragmas `Open` applies unless told not to. |
| [`DriverName`](./features.md#drivername) | const | The `database/sql` driver name this module registers. |

## The one thing to understand

SQLite scopes most pragmas to a **connection**, and `database/sql` hands you a **pool**. Setting a pragma with `db.Exec("PRAGMA foreign_keys = ON")` configures whichever connection happened to serve that call. The next query may run on a different connection, where the pragma was never set, and nothing reports it.

Putting the pragmas in the DSN is what makes them hold everywhere, because the driver applies them as each connection is opened. That is the whole mechanism, and it is why this module exists rather than a paragraph in the README telling you to run four `Exec` calls.
