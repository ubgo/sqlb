---
name: Feature request
about: A construct sqlb cannot express, or an ergonomic gap
title: "[feature] "
labels: enhancement
---

**The SQL you want to write**

Paste it. A concrete statement is worth more than a description of one.

**What you tried**

The Go you wrote, and where it stopped. If you reached for the typed door, say so: `sqlb.Fn` and the engine packages' escape hatches exist precisely so a missing helper never blocks you, and knowing you needed one tells us which helper to add.

**Which engines need it**

A construct goes in the core only when Postgres, MySQL 8, SQLite and ClickHouse all accept it with the same syntax and meaning. Anything else belongs in an engine package. Saying which engines you need narrows where it lands. MySQL is a reference engine only: this repository has no MySQL dialect and cannot render for it, so that quarter of the rule is checked by reading MySQL's documentation rather than by building a statement.

**Anything else**

Links to the engine's documentation for the construct, or a query that shows why the workaround is worse.
