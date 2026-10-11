# Contributing to sqlb

## The gate

```sh
task check
```

That walks every module with `gofmt`, `go vet` and `go test`, then runs the two gates the repository cannot ship without: the negative type-check build, and the zero-dependency check on the core. Everything must be green before a change is proposed.

The Postgres and MySQL proofs skip without a server, so a change touching SQL is also run on all three engines with `task test:engines`, which starts and removes throwaway servers itself. [COMMANDS.md](./COMMANDS.md) is every command this repository uses, with the exact lines each task runs.

CI exists but does not run on push. This repository is private, where GitHub Actions minutes are metered, so the workflows are `workflow_dispatch` only and `task check` is the real gate. The exact trigger lines to restore automatic runs are in a comment at the top of each workflow.

## Where things stand

The maintainers keep a measured snapshot of engines, packages, what each gate proves, and every known gap. A change that closes a gap should say so in the pull request, and a change that opens one should name it, so the snapshot stays honest. A gap nobody wrote down is a gap nobody fixes.

## The rules a change is reviewed against

**Zero dependencies in the core.** The root module imports nothing outside the standard library. Anything that needs a third-party package is a contrib module. `task zerodep` is the check.

**Only universal SQL in the core.** A construct belongs in the core only when Postgres, MySQL 8, SQLite and ClickHouse all accept it with the same syntax and the same meaning. Anything else goes in the engine package, even when two engines agree. Before adding a name to `constants.go` or a constructor to the core, check all four. Postgres, SQLite and MySQL are checked by execution, through the shared conformance suite in `sqlbtest`, which a construct claimed universal gets a case in; ClickHouse is the quarter still checked by reading its documentation.

**Every engine construct fails closed.** It records its own name through the guarded constructors, and its package's guard test builds it under `SQLiteDialect{}` and asserts `ErrUnsupportedDialect`. A construct without that test is not finished.

**Every closed-set value is a named constant.** Operators, keywords, join types, sort directions, function names, setting names. `constants.go` in the core, and per-package constants in the engine packages.

**Every exported symbol has a doc comment that says why and what it guarantees.** Not what the code does, which is visible. Why it exists, and what stays true. A comment above a group documents the first symbol only, so each symbol gets its own.

**Scope is driven by a consumer, never by symmetry.** A write builder ships when a library needs to write. A dialect ships when a library targets the engine. A helper ships when a catalogue or a consumer shows the construct. "The other three have it" is not a reason.

**It is not an ORM.** No relation graph, no code generation, no derived DDL, no lazy loading. A change that needs one of those belongs in the consumer.

## Adding a helper to an engine package

Write it over the guarded constructors, give it the construct name that should appear in the error, add it to the package's guard test, and add a row to the package README's SQL-to-helper table.

## Adding a Postgres extension

Do not edit `pg`. Copy `pg/postgis` and follow the four steps in [docs/extensions.md](./docs/extensions.md).

## Adding a contrib module

One Go module per external dependency, with its own `go.mod`, resolved through the committed workspace during development rather than a `replace` in a published `go.mod`. A driver module is finished when it passes the conformance suite, not when it compiles.

## Tests

A change to rendering needs an example whose expected SQL is in the test. A change to execution needs a conformance case. A new exported function needs a test that pins its behaviour; new logic without one does not ship.

Never suppress a diagnostic to make a gate pass. No `//nolint`, no loosened assertion, no skipped test. Fix the cause, or say why the tool is wrong and leave it failing for review.

## Commits

Describe what changed and why. The repository history is read by people deciding whether a behaviour was deliberate.
