"""Offline release validation: synthetic ELF/archive fixtures, no build/API calls."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


r = load('release')
p = load('publish_draft')


def fixture_elf(arch):
    raw = bytearray(120)
    raw[:6] = b'\x7fELF\x02\x01'
    struct.pack_into('<H', raw, 18, r.ARCHES[arch])
    struct.pack_into('<Q', raw, 32, 64)
    struct.pack_into('<HH', raw, 54, 56, 1)
    struct.pack_into('<I', raw, 64, 1)
    return bytes(raw)


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='release-unit-', dir=os.environ['TMPDIR'])
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name) / 'dist'
        self.directory.mkdir()
        self.version = r.version_and_tag()
        self.commit = 'a' * 40
        self.files = {'LICENSES.txt': (ROOT / 'LICENSES.txt').read_bytes()}
        for arch in r.ARCHES:
            name = 'ssh-alertd-%s-linux-%s' % (self.version, arch)
            raw = fixture_elf(arch)
            self.files[name] = raw
            self.files[name + '.tar.gz'] = r.tar_bytes(self.version, arch, raw)
        self.info = {'format': 1, 'version': self.version, 'source_commit': self.commit,
                     'source_state': 'clean', 'go_version': 'go' + r.read_setting('.go-version')}
        self.save()

    def save(self):
        self.info['files'] = {name: r.digest(raw) for name, raw in self.files.items()
                              if name not in ('BUILDINFO.json', 'SHA256SUMS')}
        self.files['BUILDINFO.json'] = json.dumps(self.info).encode()
        self.files['SHA256SUMS'] = ''.join(r.digest(raw) + '  ' + name + '\n'
                                          for name, raw in sorted(self.files.items()) if name != 'SHA256SUMS').encode()
        for name, raw in self.files.items():
            (self.directory / name).write_bytes(raw)

    def verify(self, **kwargs):
        return r.verify(self.directory, self.version, self.commit, **kwargs)

    def test_valid_bundle(self):
        self.assertEqual(self.verify()['source_commit'], self.commit)

    def test_version_and_tag_validation(self):
        for version in ['../x', '1.2', '01.2.3', '1.2.3\n', '1.2.3;touch x', '$(id)', ' 1.2.3', '1.2.3/evil']:
            with self.subTest(version=version), self.assertRaises(r.ReleaseError):
                r.validate_version(version)
        with self.assertRaises(r.ReleaseError):
            r.version_and_tag('v999.0.0')
        self.assertEqual(r.version_and_tag('v' + self.version), self.version)

    def test_tar_is_reproducible(self):
        self.assertEqual(r.tar_bytes(self.version, 'amd64', fixture_elf('amd64')),
                         r.tar_bytes(self.version, 'amd64', fixture_elf('amd64')))

    def test_extra_asset_rejected(self):
        (self.directory / 'unexpected').write_text('fixture')
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_missing_asset_rejected(self):
        (self.directory / 'BUILDINFO.json').unlink()
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_symlink_asset_rejected(self):
        path = self.directory / 'LICENSES.txt'
        path.unlink()
        path.symlink_to(ROOT / 'LICENSES.txt')
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_symlink_directory_rejected(self):
        link = Path(self.tmp.name) / 'link'
        link.symlink_to(self.directory, target_is_directory=True)
        with self.assertRaises(r.ReleaseError):
            r.verify(link, self.version, self.commit)

    def test_checksum_tampering_rejected(self):
        (self.directory / 'LICENSES.txt').write_text('tampered fixture')
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_checksum_path_injection_rejected(self):
        (self.directory / 'SHA256SUMS').write_text('a' * 64 + '  ../../victim\n')
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_build_identity_mismatches_rejected(self):
        for key, value in [('version', '999.0.0'), ('source_commit', 'b' * 40),
                           ('go_version', 'go0.0.0'), ('format', True), ('source_state', 'unknown')]:
            with self.subTest(key=key):
                old = self.info[key]
                self.info[key] = value
                self.save()
                with self.assertRaises(r.ReleaseError):
                    self.verify()
                self.info[key] = old
        self.save()

    def test_dirty_not_release(self):
        self.info['source_state'] = 'dirty'
        self.save()
        with self.assertRaises(r.ReleaseError):
            self.verify()
        self.assertEqual(self.verify(allow_dirty=True)['source_state'], 'dirty')

    def test_bad_architecture_rejected(self):
        with self.assertRaises(r.ReleaseError):
            r.elf_check(fixture_elf('arm64'), 'amd64')

    def test_dynamic_binary_rejected(self):
        raw = bytearray(fixture_elf('amd64'))
        struct.pack_into('<I', raw, 64, 3)
        with self.assertRaises(r.ReleaseError):
            r.elf_check(raw, 'amd64')

    def test_short_elf_rejected(self):
        with self.assertRaises(r.ReleaseError):
            r.elf_check(b'not an executable', 'amd64')

    def test_archive_malformed_even_with_updated_checksums(self):
        name = 'ssh-alertd-%s-linux-amd64.tar.gz' % self.version
        self.files[name] = b'not gzip'
        self.save()
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_wrong_archive_payload_even_with_updated_checksums(self):
        name = 'ssh-alertd-%s-linux-amd64.tar.gz' % self.version
        self.files[name] = r.tar_bytes(self.version, 'arm64', fixture_elf('arm64'))
        self.save()
        with self.assertRaises(r.ReleaseError):
            self.verify()

    def test_duplicate_manifest_keys_rejected(self):
        with self.assertRaises(r.ReleaseError):
            r.strict_json(b'{"version":1,"version":2}')

    def test_nonempty_output_refused_before_compiler(self):
        with patch.object(r, 'run', side_effect=[self.commit, '']) as commands:
            with self.assertRaises(r.ReleaseError):
                r.build(self.directory)
            self.assertEqual(commands.call_count, 2)

    def test_dirty_tree_refused_before_compiler(self):
        with patch.object(r, 'run', side_effect=[self.commit, ' M fixture.go']) as commands:
            with self.assertRaises(r.ReleaseError):
                r.build(Path(self.tmp.name) / 'new-output')
            self.assertEqual(commands.call_count, 2)

    def test_decompressed_archive_limit(self):
        with patch.object(r, 'MAX_TAR', 1):
            with self.assertRaises(r.ReleaseError):
                self.verify()

    def test_tag_cannot_allow_dirty_before_commands(self):
        with patch.object(r, 'run', side_effect=AssertionError('must fail before git/build')):
            with self.assertRaises(r.ReleaseError):
                r.build(self.directory, tag='v' + self.version, allow_dirty=True)


class DraftTests(unittest.TestCase):
    def setUp(self):
        self.tag = 'v' + r.version_and_tag()
        self.commit = 'a' * 40
        self.repo = 'fixture-owner/fixture-repository'
        self.environment = {'GITHUB_EVENT_NAME': 'push', 'GITHUB_REF': 'refs/tags/' + self.tag,
                            'GITHUB_REPOSITORY': self.repo, 'GITHUB_SHA': self.commit,
                            'GH_TOKEN': 'offline-fixture-not-a-real-token', 'TMPDIR': os.environ['TMPDIR']}

    def test_dispatch_cannot_publish(self):
        with patch.dict(os.environ, dict(self.environment, GITHUB_EVENT_NAME='workflow_dispatch'), clear=True):
            with self.assertRaises(p.release.ReleaseError):
                p.check_context(self.tag, self.repo, self.commit)

    def test_tag_and_commit_context_required(self):
        for key in ['GITHUB_REF', 'GITHUB_REPOSITORY', 'GITHUB_SHA', 'GH_TOKEN']:
            with self.subTest(key=key), patch.dict(os.environ, dict(self.environment, **{key: ''}), clear=True):
                with self.assertRaises(p.release.ReleaseError):
                    p.check_context(self.tag, self.repo, self.commit)

    def test_remote_tag_mismatch_stops_before_create(self):
        with patch.dict(os.environ, self.environment, clear=True), \
                patch.object(p.release, 'verify'), patch.object(p, 'gh', return_value='{"sha":"wrong"}') as cli:
            with self.assertRaises(p.release.ReleaseError):
                p.publish('/fixture', self.tag, self.repo, self.commit)
            self.assertEqual(cli.call_count, 1)

    def test_published_release_not_accepted_as_draft(self):
        with patch.object(p, 'gh', return_value=json.dumps({'tagName': self.tag, 'isDraft': False, 'assets': []})):
            with self.assertRaises(p.release.ReleaseError):
                p.inspect_draft(self.tag, self.repo)

    def test_draft_assets_readback(self):
        data = {'tagName': self.tag, 'isDraft': True, 'isPrerelease': True,
                'assets': [{'name': name} for name in r.asset_names(self.tag[1:])]}
        with patch.object(p, 'gh', return_value=json.dumps(data)):
            self.assertTrue(p.inspect_draft(self.tag, self.repo)['isDraft'])
        data['assets'].append(data['assets'][0])
        with patch.object(p, 'gh', return_value=json.dumps(data)):
            with self.assertRaises(p.release.ReleaseError):
                p.inspect_draft(self.tag, self.repo)

    def test_create_is_draft_and_readback_bytes_are_compared(self):
        def cli(args):
            if args[0] == 'api':
                return json.dumps({'sha': self.commit})
            return ''
        with patch.dict(os.environ, self.environment, clear=True), \
                patch.object(p.release, 'verify') as verify, \
                patch.object(p.release, 'read_asset', return_value=b'fixture-bytes'), \
                patch.object(p, 'inspect_draft') as inspect, patch.object(p, 'gh', side_effect=cli) as calls, \
                contextlib.redirect_stdout(io.StringIO()):
            p.publish('/fixture', self.tag, self.repo, self.commit)
            create = [c.args[0] for c in calls.call_args_list if c.args[0][:2] == ['release', 'create']][0]
            self.assertIn('--draft', create)
            self.assertIn('--prerelease', create)
            self.assertIn('--latest=false', create)
            self.assertIn('--verify-tag', create)
            self.assertNotIn('--clobber', create)
            self.assertEqual(verify.call_count, 2)
            self.assertEqual(inspect.call_count, 2)

    def test_changed_download_fails(self):
        def cli(args):
            return json.dumps({'sha': self.commit}) if args[0] == 'api' else ''
        with patch.dict(os.environ, self.environment, clear=True), \
                patch.object(p.release, 'verify'), patch.object(p, 'inspect_draft'), \
                patch.object(p, 'gh', side_effect=cli), \
                patch.object(p.release, 'read_asset', side_effect=[b'downloaded', b'local']):
            with self.assertRaises(p.release.ReleaseError):
                p.publish('/fixture', self.tag, self.repo, self.commit)


if __name__ == '__main__':
    unittest.main()
