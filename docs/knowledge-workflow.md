---
ds:
  covers: [bound-values-3t7cnq5d, build-failure-pwec46e6, promise-gate-a57aksdv, dialect-failure-zqdgvzs3]
---

# Shared knowledge and checked promises

SQLB uses the same project knowledge in Claude Code and Codex: [AGENTS.md](../AGENTS.md) is the hand-written entrypoint, [CLAUDE.md](../CLAUDE.md) imports it, and Lore renders the shared rules into `.lore/LORE.md` for both agents to read.

## Setup

Install Lore and Docsync on your PATH, alongside Go, Task, Python 3 and the existing task-generation dependency PyYAML (`python3 -m pip install pyyaml`). Lore can be installed with `brew install thesatellite-ai/tap/lore`; Docsync with `go install github.com/ubgo/docsync/cli/cmd/ds@latest`. The initial integration was validated with Lore 0.1.15 and Docsync 0.1.9; the promise checker is the vendored, standard-library-only `spec-promises` 0.1.0, attributed in [NOTICE](../NOTICE).

Run `task knowledge:setup` in a fresh clone to hydrate Lore from `.lore/data`, install its repository hooks and merge driver, render both entrypoints, and scan Docsync references. Load the Lore and Docsync skills before project work; the Docsync skill is shared by the repository's `.claude/skills` and `.agents/skills` directories, and the Lore installer supplies its skill. Codex reads the generated Lore file explicitly because Claude's `@` import syntax is not itself an instruction loader in every agent. The `.mcp.json` registration and Claude session hook are optional client conveniences; both agents can run the same `lore` and `ds` commands directly.

## Daily workflow

Start with `lore search` for the task's subject and `ds map --budget 2000`; for an edited page, use `ds context <page> --budget 2000 --since ack`. Capture verified memories, architecture notes, patterns, hotfixes, rules and decisions with Lore's CLI, then run `task lore:render` and include the resulting shared `.lore/data` records in the change. The SQLite database, generated context and caches stay local; a fresh clone reconstructs them from the shared records.

For new documentation, define the relevant code or test with `ds def`, cite its stable id, and add the id to the page's `ds.covers` frontmatter. Ordinary query-mode parameters keep input in bound arguments, including empty, hostile and Unicode input ([assertion](ds:block?id=bound-values-3t7cnq5d&assert=true)); explicit literal and inline APIs have the boundary described in [SECURITY.md](../SECURITY.md). The empty-select validation returns an error with no partial query ([assertion](ds:block?id=build-failure-pwec46e6&assert=true)); the wrong-dialect case checks the same boundary ([assertion](ds:block?id=dialect-failure-zqdgvzs3&assert=true)). The [architecture guide](architecture.md) and [operating notes](operating-notes.md) extend those references to the project data flow, result lifetimes, dialect differences and operating lessons.

After editing defined code, run `ds check --json`, inspect the flagged sentence and diff, and update the prose where needed. Run `ds impact --staged` before proposing the patch. A person reviews freshness acknowledgments; an agent can record them only with explicit delegation and the configured owner identity. The initial scan is a baseline, not a claim that a person has approved the new documentation.

## Promise gate

[testdata/promises.txt](../testdata/promises.txt) records verbatim public promise sentences and their reviewed classifications. Behavior entries name a real asserting test through a `promise:<id>` comment; advice, historical observations and headings have an explicit prose reason. When a behavior or its wording changes, update the claim, inventory and assertion together.

`task promises:check` scans tracked and untracked public Markdown plus the built CLI help, validates the inventory and tags, and exercises the checker's negative cases ([scope implementation](ds:block?id=promise-gate-a57aksdv)). Tool-generated knowledge, private `docsi` and copied `testdata` documents are excluded from that specification scan. Presence of a tag proves a traceable link, not the strength or execution of an assertion; review the test and run the relevant engine suite. The gate tests cover an unlisted sentence, a reworded quote, a removed tag and a misspelled tag, plus missing files and dependency-directory false positives.

`task check` runs the promise gate and `ds check --frozen` after the existing checks. Engine tests need the scratch DSNs described in [docs/engines.md](./engines.md); a skipped database test is recorded as unverified for that run. The existing GitHub Actions remain manually dispatched, and this integration adds no push or pull-request workflow runs. See the [promise audit](./promise-audit.md) for the initial results and limitations.
