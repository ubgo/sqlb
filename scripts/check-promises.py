#!/usr/bin/env python3
"""Run the promise gate over all public Markdown, including newly added files."""
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent


# ds:def id=promise-gate-a57aksdv
def main():
    result = subprocess.run(
        ['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard', '--', '*.md'],
        cwd=ROOT, check=True, capture_output=True,
    )
    # Tool-generated instructions/data are not the public software specification.
    excluded = {'.agents', '.claude', '.ds', '.lore', 'docsi', 'testdata'}
    docs = sorted({p for p in result.stdout.decode().split('\0')
                   if p and not excluded.intersection(Path(p).parts)})
    if not docs:
        raise SystemExit('promise gate: no public Markdown found')
    command = [sys.executable, str(ROOT / 'scripts/spec-promises'), 'check',
               '--list', 'testdata/promises.txt', '--tests', '**/*_test.go',
               '--tests', 'scripts/check-typecheck.py']
    for doc in docs:
        command.extend(['--spec', doc])
    # Scan the executable's help too: flag strings are part of the public
    # specification, and a saved help snapshot would silently become stale.
    help_run = subprocess.run(
        ['go', 'run', './contrib/vet/cmd/sqlb', 'vet', '-help'],
        cwd=ROOT, check=True, capture_output=True, text=True,
    )
    with tempfile.TemporaryDirectory(prefix='sqlb-promises-') as tmp:
        help_file = Path(tmp) / 'sqlb-vet-help.txt'
        help_file.write_text(help_run.stdout + help_run.stderr)
        command.extend(['--spec', str(help_file)])
        return subprocess.run(command, cwd=ROOT).returncode


if __name__ == '__main__':
    sys.exit(main())
