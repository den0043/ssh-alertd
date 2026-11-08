"""Static security policy checks; actionlint separately checks actual YAML/schema."""
from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]


class WorkflowPolicy(unittest.TestCase):
    def setUp(self):
        self.ci = (ROOT / '.github/workflows/ci.yml').read_text()
        self.release = (ROOT / '.github/workflows/release.yml').read_text()

    def test_all_external_actions_use_full_commit_hashes(self):
        for source in [self.ci, self.release]:
            uses = re.findall(r'(?m)^\s*(?:-\s*)?uses:\s*(\S+)', source)
            self.assertTrue(uses)
            for action in uses:
                self.assertRegex(action, r'^actions/[a-z-]+@[a-f0-9]{40}$')

    def test_only_draft_job_can_write_contents(self):
        self.assertNotIn('contents: write', self.ci)
        before, draft = self.release.split('\n  draft:\n', 1)
        self.assertNotIn('contents: write', before)
        self.assertEqual(draft.count('contents: write'), 1)
        self.assertIn('needs: check-and-build', draft)
        self.assertIn("if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')", draft)

    def test_no_privileged_pr_event_or_arbitrary_version_input(self):
        for text in [self.ci, self.release]:
            self.assertNotIn('pull_request_target', text)
            self.assertNotIn('workflow_run:', text)
            self.assertNotIn('inputs.version', text)
        self.assertIn('workflow_dispatch:', self.release)

    def test_no_github_expressions_interpolated_into_shell(self):
        for text in [self.ci, self.release]:
            run_indent = None
            for line in text.splitlines():
                indent = len(line) - len(line.lstrip())
                if run_indent is not None and line.strip() and indent <= run_indent:
                    run_indent = None
                if run_indent is not None:
                    self.assertNotIn('${{', line)
                if line.lstrip().startswith('run:'):
                    self.assertNotIn('${{', line)
                    if line.strip().endswith('|'):
                        run_indent = indent

    def test_build_and_tests_gate_upload(self):
        for text in [self.ci, self.release]:
            check = text.index('scripts/check.py')
            build = text.index('scripts/release.py build')
            runtime = text.index('tests/runtime-test.py')
            upload = text.index('uses: actions/upload-artifact@')
            self.assertLess(check, build)
            self.assertLess(build, runtime)
            self.assertLess(runtime, upload)
            self.assertIn('go-version-file: .go-version', text)
            self.assertIn('persist-credentials: false', text)
            self.assertIn('cache: false', text)

    def test_download_fails_on_digest_mismatch(self):
        self.assertIn('digest-mismatch: error', self.release)
        self.assertIn('scripts/publish_draft.py', self.release)
        self.assertNotIn('gh release edit', self.release)
        self.assertNotIn('--clobber', self.release)

    def test_legacy_debian_builder_fails_closed(self):
        result = subprocess.run(['bash', str(ROOT / 'packaging/debian/build.sh')],
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 2)
        self.assertIn('disabled', result.stderr)

    def test_no_debian_packages_in_release_pipeline(self):
        self.assertNotIn('packaging/debian', self.release)
        self.assertNotIn('*.deb', self.release)


if __name__ == '__main__':
    unittest.main()
