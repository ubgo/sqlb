#!/usr/bin/env python3
"""Verify the repository's Markdown.

Why this exists: the documentation makes claims a reader acts on, and four
kinds of rot are invisible in review but obvious to a reader. A link that
404s, an in-page anchor that scrolls nowhere on GitHub, a horizontal rule
that GitHub renders as a page-wide line the house style forbids, and prose
that was hard-wrapped at some editor's column and now reflows badly in every
other viewer.

Run it with `task docs:check`. Exit status is 0 when clean, 1 when anything
is found, so it can gate a commit.
"""

import os
import re
import sys

# Directories that are never checked. docsi/ is a symlink into a private
# repository rendered by a different tool with its own component grammar;
# testdata/ holds upstream documents copied verbatim, where reflowing the
# prose would destroy the thing under test.
SKIP_DIRS = {".git", "docsi", "testdata", "node_modules"}

# A fenced code block opens and closes with these. Line breaks inside one are
# semantic, so every rule below is suspended until the fence closes.
FENCE = re.compile(r"^\s*(```|~~~)")

# Markdown block starters. A line beginning with one of these is structure,
# not prose, and the one-physical-line rule does not apply to it.
BLOCK_START = re.compile(r"^\s*(#|\||>|[-*+]\s|\d+[.)]\s|<|\[\^|:{3}|\s*$)")

# A badge row: a line built only of images, optionally wrapped in links. They
# stack one per line by convention and render inline, so consecutive badge
# lines are not hard-wrapped prose.
BADGE_LINE = re.compile(r"^\s*(\[?!\[[^\]]*\]\([^)]*\)\]?(\([^)]*\))?\s*)+$")

# A horizontal rule: three or more -, * or _ alone on a line. Flagged because
# the house style uses headings for separation instead.
HRULE = re.compile(r"^\s*([-*_])(\s*\1){2,}\s*$")

# Inline and reference links. The group is the target.
LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")

# Inline code spans, stripped before link extraction. Go generics inside
# backticks look exactly like a Markdown link: Col[string](Users, "email").
CODE_SPAN = re.compile(r"`[^`]*`")

# Characters GitHub strips when it builds a heading anchor.
ANCHOR_STRIP = re.compile(r"[^\w\- ]")


def slug(heading: str) -> str:
    """Return the anchor GitHub generates for a heading.

    GitHub lowercases, drops punctuation other than hyphens and underscores,
    and turns spaces into hyphens. Duplicate slugs get a numeric suffix, which
    collect_anchors below reproduces.
    """
    text = heading.lstrip("#").strip()
    text = re.sub(r"`([^`]*)`", r"\1", text)          # inline code keeps its text
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text)  # links keep their label
    text = ANCHOR_STRIP.sub("", text).strip().lower()
    return text.replace(" ", "-")


def markdown_files(root: str) -> list[str]:
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for name in filenames:
            if name.endswith(".md"):
                out.append(os.path.relpath(os.path.join(dirpath, name), root))
    return sorted(out)


def collect_anchors(lines: list[str]) -> set[str]:
    """Every anchor a reader can jump to in this file."""
    anchors, seen = set(), {}
    in_fence = False
    for line in lines:
        if FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence or not line.startswith("#"):
            continue
        base = slug(line)
        if not base:
            continue
        n = seen.get(base, 0)
        seen[base] = n + 1
        anchors.add(base if n == 0 else f"{base}-{n}")
    return anchors


def check_file(root: str, rel: str, problems: list[str]) -> None:
    path = os.path.join(root, rel)
    lines = open(path, encoding="utf-8").read().split("\n")
    anchors = collect_anchors(lines)
    in_fence = False

    # YAML frontmatter. GitHub issue templates require it, and its --- fences
    # are not horizontal rules and its key: value lines are not prose.
    start = 0
    if lines and lines[0].strip() == "---":
        for j in range(1, len(lines)):
            if lines[j].strip() == "---":
                start = j + 1
                break

    for i, line in enumerate(lines[start:], start=start + 1):
        if FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue

        if HRULE.match(line):
            problems.append(f"{rel}:{i}: horizontal rule; use a heading instead")

        # One physical line per prose paragraph. Two consecutive prose lines
        # is the signature of a hard wrap.
        if not BLOCK_START.match(line) and not BADGE_LINE.match(line) and i < len(lines):
            nxt = lines[i]
            if nxt.strip() and not BLOCK_START.match(nxt) and not FENCE.match(nxt) and not BADGE_LINE.match(nxt):
                problems.append(f"{rel}:{i}: hard-wrapped prose; one physical line per paragraph")

        for target in LINK.findall(CODE_SPAN.sub("", line)):
            target = target.split(" ")[0].strip()
            if target.startswith(("http://", "https://", "mailto:")):
                continue
            file_part, _, anchor = target.partition("#")
            if file_part:
                resolved = os.path.normpath(os.path.join(os.path.dirname(path), file_part))
                if not os.path.exists(resolved):
                    problems.append(f"{rel}:{i}: link target does not exist: {target}")
                    continue
                if anchor and resolved.endswith(".md"):
                    other = open(resolved, encoding="utf-8").read().split("\n")
                    if anchor.lower() not in collect_anchors(other):
                        problems.append(f"{rel}:{i}: anchor not found in {file_part}: #{anchor}")
            elif anchor and anchor.lower() not in anchors:
                problems.append(f"{rel}:{i}: anchor not found in this file: #{anchor}")


def main() -> int:
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    problems: list[str] = []
    files = markdown_files(root)
    for rel in files:
        check_file(root, rel, problems)
    if problems:
        for p in problems:
            print(p)
        print(f"\n{len(problems)} problem(s) in {len(files)} markdown files")
        return 1
    print(f"ok: {len(files)} markdown files, links resolve, anchors match, no rules, no hard wraps")
    return 0


if __name__ == "__main__":
    sys.exit(main())
