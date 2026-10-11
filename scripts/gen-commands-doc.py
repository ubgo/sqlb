#!/usr/bin/env python3
"""Generate the task reference in COMMANDS.md from Taskfile.yml.

Every task, internal ones included, is written with its description, the
variables and environment it sets, and the exact commands it runs, between
marker comments, so running this twice is not two tables and the hand-written
part above the markers is left alone.

With --check it writes nothing and exits 1 when COMMANDS.md is not what the
Taskfile would generate, which is how `task docs:commands` keeps the page from
describing commands that no longer exist or missing ones that do.
"""

import os
import re
import sys

import yaml

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PAGE = os.path.join(REPO, "COMMANDS.md")
BEGIN = "<!-- BEGIN GENERATED TASKS -->"
END = "<!-- END GENERATED TASKS -->"


DEFAULT = re.compile(r'^\{\{\.(\w+) \| default "(.*)"\}\}$')
FROM = re.compile(r'^\{\{\.(\w+)\}\}$')


def describe(v):
    """A task variable in words: its default, or the variable it is set from."""
    v = str(v)
    m = DEFAULT.match(v)
    if m:
        return "default `" + m.group(2) + "`"
    m = FROM.match(v)
    if m:
        return "set from `" + m.group(1) + "`"
    return "`" + v + "`"


def render_cmd(c):
    """One command as the shell or task call it is."""
    if isinstance(c, str):
        return c.rstrip("\n")
    if "task" in c:
        return "task " + c["task"]
    if "defer" in c:
        d = c["defer"]
        inner = d["task"] if isinstance(d, dict) and "task" in d else str(d)
        return "defer: task " + inner + "   # runs when the task ends, even if a step failed"
    if "for" in c:
        return "for each MODULE: task " + c.get("task", "") + " DIR=<module>"
    return str(c)


def generate(tf):
    tasks = tf["tasks"]
    out = [BEGIN, "", "Generated from `Taskfile.yml` by `python3 scripts/gen-commands-doc.py`; `task docs:commands` fails when it is stale. " + str(len(tasks)) + " tasks.", ""]
    mods = tf.get("vars", {}).get("MODULES", "")
    out += ["`MODULES` is `" + mods + "`; a task that says *for each MODULE* runs its step in each of those directories.", ""]
    for name in sorted(tasks):
        t = tasks[name] or {}
        internal = t.get("internal", False)
        out.append("### `task " + name + "`" + (" (internal)" if internal else ""))
        out.append("")
        if t.get("desc"):
            out.append(t["desc"])
            out.append("")
        if internal:
            out.append("Internal: called by other tasks, not run directly.")
            out.append("")
        if t.get("dir"):
            out.append("Runs in `" + str(t["dir"]) + "`.")
            out.append("")
        if t.get("vars"):
            out.append("Variables, each overridable on the command line as `task " + name + " NAME=value`:")
            out.append("")
            for k, v in t["vars"].items():
                out.append("- `" + k + "`, " + describe(v))
            out.append("")
        if t.get("env"):
            out.append("Environment it sets for its commands:")
            out.append("")
            for k, v in t["env"].items():
                out.append("- `" + k + "`, " + describe(v))
            out.append("")
        cmds = t.get("cmds") or []
        if cmds:
            out.append("```sh")
            for c in cmds:
                out.append(render_cmd(c))
            out.append("```")
            out.append("")
    out.append(END)
    return "\n".join(out)


def main():
    with open(os.path.join(REPO, "Taskfile.yml"), encoding="utf-8") as fh:
        tf = yaml.safe_load(fh)
    with open(PAGE, encoding="utf-8") as fh:
        text = fh.read()
    if BEGIN not in text or END not in text:
        print("COMMANDS.md has no generated-task markers", file=sys.stderr)
        return 1
    head, rest = text.split(BEGIN, 1)
    _, tail = rest.split(END, 1)
    new = head + generate(tf) + tail
    if "--check" in sys.argv:
        if new != text:
            print("COMMANDS.md is stale: run python3 scripts/gen-commands-doc.py", file=sys.stderr)
            return 1
        print("ok: COMMANDS.md describes every task in Taskfile.yml")
        return 0
    with open(PAGE, "w", encoding="utf-8") as fh:
        fh.write(new)
    print("COMMANDS.md: " + str(len(tf["tasks"])) + " tasks")
    return 0


if __name__ == "__main__":
    sys.exit(main())
