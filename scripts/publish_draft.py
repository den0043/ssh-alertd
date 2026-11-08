#!/usr/bin/env python3
"""Only used by the tag workflow; creates and readback-verifies a DRAFT.

Does not create tags, replace assets, or publish drafts. Requires the existing
GITHUB_TOKEN in GH_TOKEN; no Telegram secrets or custom PAT are required.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('release_tools', ROOT / 'scripts/release.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def gh(argv):
    result = subprocess.run(['gh'] + argv, check=False, text=True, capture_output=True, timeout=180)
    if result.returncode:
        # Do not dump arbitrary authenticated CLI errors or environment values.
        raise release.ReleaseError('GitHub CLI operation failed; draft may need manual inspection')
    return result.stdout


def check_context(tag, repo, commit):
    release.version_and_tag(tag)
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9_.-]*', repo):
        raise release.ReleaseError('invalid repository')
    if not release.COMMIT_RE.fullmatch(commit):
        raise release.ReleaseError('invalid source commit')
    expected = {'GITHUB_EVENT_NAME': 'push', 'GITHUB_REF': 'refs/tags/' + tag,
                'GITHUB_REPOSITORY': repo, 'GITHUB_SHA': commit}
    if any(os.environ.get(key) != value for key, value in expected.items()):
        raise release.ReleaseError('draft creation requires the matching tag push context')
    if not os.environ.get('GH_TOKEN'):
        raise release.ReleaseError('GH_TOKEN is required in the publish job only')


def inspect_draft(tag, repo):
    data = json.loads(gh(['release', 'view', tag, '--repo', repo,
                          '--json', 'tagName,isDraft,isPrerelease,assets']))
    if data.get('tagName') != tag or data.get('isDraft') is not True or data.get('isPrerelease') is not True:
        raise release.ReleaseError('readback: release is not the expected draft')
    names = [asset.get('name') for asset in data.get('assets', [])]
    if len(names) != len(set(names)) or set(names) != release.asset_names(tag[1:]):
        raise release.ReleaseError('readback: release assets differ')
    return data


def publish(directory, tag, repo, commit):
    check_context(tag, repo, commit)
    version = tag[1:]
    release.verify(directory, version, commit)
    remote = json.loads(gh(['api', 'repos/' + repo + '/commits/' + tag]))
    if remote.get('sha') != commit:
        raise release.ReleaseError('remote tag does not point to the tested commit')
    assets = [str(Path(directory) / name) for name in sorted(release.asset_names(version))]
    gh(['release', 'create', tag, '--repo', repo, '--verify-tag', '--draft', '--prerelease',
        '--latest=false', '--title', tag, '--notes-file', str(ROOT / 'docs/RELEASE_NOTES.md')] + assets)
    inspect_draft(tag, repo)
    with tempfile.TemporaryDirectory(prefix='ssh-alertd-draft-', dir=os.environ['TMPDIR']) as temporary:
        gh(['release', 'download', tag, '--repo', repo, '--dir', temporary])
        release.verify(temporary, version, commit)
        # Compare the downloaded files to the exact locally verified artifacts,
        # not merely to hashes supplied by the same remote release.
        for name in release.asset_names(version):
            if release.read_asset(Path(temporary) / name) != release.read_asset(Path(directory) / name):
                raise release.ReleaseError('readback: downloaded asset bytes differ')
    inspect_draft(tag, repo)
    print('Draft and downloaded bytes verified. Publish manually after review: ' + tag)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--repository', required=True)
    parser.add_argument('--commit', required=True)
    args = parser.parse_args()
    try:
        publish(args.directory, args.tag, args.repository, args.commit)
    except (release.ReleaseError, OSError, ValueError, subprocess.SubprocessError):
        print('Draft workflow failed; no automatic publish or overwrite was attempted. '
              'Inspect any existing draft before retrying.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
