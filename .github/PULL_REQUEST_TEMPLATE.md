<!-- Thanks for contributing to sqlb! -->

## What & why

<!-- What does this change and why? Link the issue it closes. -->
Closes #

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor / internal
- [ ] Docs
- [ ] Other:

## Checklist

- [ ] `task check` is green. That is gofmt, vet and tests in every module, then the negative type-check build, the zero-dependency gate and the docs check.
- [ ] New logic has a test that would fail without it. A rendering change has an example with its expected SQL; an execution change has a case in `sqlbtest`.
- [ ] Nothing new in the core that all four of Postgres, MySQL 8, SQLite and ClickHouse do not share with the same syntax and meaning. Anything else is in an engine package. MySQL is a reference engine only: this repository has no MySQL dialect and cannot render for it, so that quarter of the rule is checked by reading MySQL's documentation rather than by building a statement.
- [ ] Every new engine construct is guarded and appears in its package's guard test, so it fails at `Build` under another dialect.
- [ ] Every new closed-set value is a named constant; every new export has a doc comment saying why it exists and what stays true.
- [ ] No suppressed diagnostics. No `//nolint`, no skipped test, no loosened assertion.
- [ ] Docs, README and CHANGELOG updated for anything user-facing.
- [ ] No unrelated changes or formatting churn.
