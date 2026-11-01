"""Break the vendored gate through its CLI; each failure must name its cause."""
from pathlib import Path
import os
import subprocess
import sys
import tempfile
import unittest

CHECKER = Path(__file__).resolve().with_name('spec-promises')


class PromiseGateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'SPEC.md').write_text('Values never reach SQL text.\n')
        (self.root / 'promises.txt').write_text(
            '# spec: SPEC.md\nparameter-binding\ttest\tValues never reach SQL text.\n')
        (self.root / 'values_test.go').write_text('// promise:parameter-binding\n')

    def check(self, status, message=''):
        run = subprocess.run([sys.executable, str(CHECKER), 'check', '--list', 'promises.txt'],
                             cwd=self.root, capture_output=True, text=True)
        self.assertEqual(run.returncode, status, run.stdout + run.stderr)
        self.assertIn(message, run.stdout + run.stderr)

    def test_valid_inventory(self):
        self.check(0, '1 tested')

    def test_unlisted_promise(self):
        with (self.root / 'SPEC.md').open('a') as f:
            f.write('Rendering always panics.\n')
        self.check(1, 'promise not in')

    def test_reworded_quote(self):
        (self.root / 'SPEC.md').write_text('Values never become SQL text.\n')
        self.check(1, 'quote no longer in the spec')

    def test_removed_only_tag(self):
        (self.root / 'values_test.go').write_text('// no tag\n')
        self.check(1, 'no test names')

    def test_typo_in_tag(self):
        (self.root / 'values_test.go').write_text('// promise:parameter-bindng\n')
        self.check(1, 'which promises.txt does not list')

    def test_missing_test_file(self):
        (self.root / 'values_test.go').unlink()
        self.check(1, 'no test names')

    def test_empty_inventory(self):
        (self.root / 'promises.txt').write_text('# spec: SPEC.md\n')
        self.check(2, 'lists no promises')

    def test_missing_spec(self):
        (self.root / 'SPEC.md').unlink()
        self.check(2, 'SPEC.md')

    def test_prose_requires_reason(self):
        (self.root / 'promises.txt').write_text(
            '# spec: SPEC.md\nadvice\tprose: \tValues never reach SQL text.\n')
        self.check(2, 'neither')

    def test_dependency_tags_do_not_count(self):
        (self.root / 'vendor').mkdir()
        (self.root / 'values_test.go').rename(self.root / 'vendor/values_test.go')
        self.check(1, 'no test names')

    def test_new_document_is_scanned_by_repository_gate(self):
        subprocess.run(['git', 'init', '-q'], cwd=self.root, check=True)
        (self.root / 'testdata').mkdir()
        (self.root / 'promises.txt').rename(self.root / 'testdata/promises.txt')
        scripts = self.root / 'scripts'
        scripts.mkdir()
        (scripts / 'spec-promises').write_bytes(CHECKER.read_bytes())
        wrapper = CHECKER.with_name('check-promises.py')
        (scripts / wrapper.name).write_bytes(wrapper.read_bytes())
        # This fixture has no Go module. The command fixture supplies help;
        # the repository check itself exercises the real built CLI.
        fake_go = self.root / 'go'
        fake_go.write_text('#!/bin/sh\nprintf "Usage: fixture\\n"\n')
        fake_go.chmod(0o755)
        (self.root / 'NEW.md').write_text('Fresh docs always need review.\n')
        run = subprocess.run([sys.executable, str(scripts / wrapper.name)], cwd=self.root,
                             capture_output=True, text=True,
                             env=dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ['PATH']))
        self.assertEqual(run.returncode, 1, run.stdout + run.stderr)
        self.assertIn('NEW.md', run.stdout)
        self.assertIn('promise not in', run.stdout)


if __name__ == '__main__':
    unittest.main()
