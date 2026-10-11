# Contributing to driver-sqlite

This is a contrib module inside the `ubgo/sqlb` repository. The repository-wide rules are in [the root CONTRIBUTING.md](../../CONTRIBUTING.md), and they apply here too.

## The gate

```sh
task check      # in this directory
task check      # at the repository root, which walks this module as well
```

## What belongs in this module

Only what needs the driver dependency. A helper that works without it belongs upstream, in the core or in an engine package, where every user gets it rather than only the users of SQLite.

## What finishing means

A driver module is finished when the conformance suite passes, not when it compiles. A change to `Open` or to the pragmas needs a test that would fail without it: `TestForeignKeys_AreEnforced` is the model, because it proves the pragma by observing the constraint it enables rather than by reading the setting back.

## Local resolution

`go.mod` carries a `replace` pointing at the repository root, so the module builds against the working copy. A `replace` in a dependency is ignored by consumers, so the `require` line must resolve on its own once the parent is tagged.
