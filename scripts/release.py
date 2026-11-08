#!/usr/bin/env python3
"""Build/verify versioned fork assets. Stdlib only; never installs or publishes.

Build requires a clean checkout unless --allow-dirty is explicitly given.
Dirty builds are development artifacts and cannot pass release verification.
"""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
VERSION_RE = re.compile(r'(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[a-z0-9]+(?:[.-][a-z0-9]+)*)?\Z')
COMMIT_RE = re.compile(r'[a-f0-9]{40}\Z')
ARCHES = {'amd64': 62, 'arm64': 183}
MAX_ASSET = 32 * 1024 * 1024
MAX_TAR = 64 * 1024 * 1024


class ReleaseError(Exception):
    pass


def run(argv, **kwargs):
    result = subprocess.run(argv, cwd=ROOT, check=False, text=True,
                            capture_output=True, timeout=240, **kwargs)
    if result.returncode:
        raise ReleaseError('command failed: %s\n%s' % (argv[0], result.stderr[-4000:]))
    return result.stdout.strip()


def read_setting(name):
    text = (ROOT / name).read_text(encoding='utf-8')
    return text[:-1] if text.endswith('\n') else text


def validate_version(version):
    if not isinstance(version, str) or not VERSION_RE.fullmatch(version):
        raise ReleaseError('invalid version (expected X.Y.Z[-suffix], no spaces or shell syntax)')
    return version


def version_and_tag(tag=None):
    version = validate_version(read_setting('VERSION'))
    if tag is not None and tag != 'v' + version:
        raise ReleaseError('tag must exactly equal v' + version)
    return version


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ReleaseError('duplicate JSON key')
            result[key] = value
        return result
    try:
        return json.loads(raw, object_pairs_hook=pairs,
                          parse_constant=lambda value: (_ for _ in ()).throw(ReleaseError('non-finite JSON')))
    except (ValueError, UnicodeError) as exc:
        raise ReleaseError('invalid JSON') from exc


def elf_check(raw, arch):
    if len(raw) < 64 or raw[:6] != b'\x7fELF\x02\x01':
        raise ReleaseError('expected little-endian ELF64')
    if struct.unpack_from('<H', raw, 18)[0] != ARCHES[arch]:
        raise ReleaseError('ELF architecture mismatch')
    offset = struct.unpack_from('<Q', raw, 32)[0]
    size, count = struct.unpack_from('<HH', raw, 54)
    if size < 56 or count == 0 or offset + size * count > len(raw):
        raise ReleaseError('invalid ELF program headers')
    for index in range(count):
        if struct.unpack_from('<I', raw, offset + size * index)[0] == 3:
            raise ReleaseError('ELF has PT_INTERP (not a standalone static binary)')


def archive_sources():
    names = ['README.md', 'LICENSE', 'LICENSES.txt', 'SECURITY_PATCHES.md',
             'config.example.json', 'docs/README.ru.md', 'deploy/ssh-alertd.service',
             'deploy/ssh-alertd.sysusers', 'deploy/ssh-alertd.tmpfiles']
    names += [str(p.relative_to(ROOT)) for p in sorted((ROOT / 'examples').glob('*/*.tmpl'))]
    return names


def asset_names(version):
    names = ['ssh-alertd-%s-linux-%s' % (version, arch) for arch in ARCHES]
    return set(names + [name + '.tar.gz' for name in names] + ['LICENSES.txt', 'BUILDINFO.json', 'SHA256SUMS'])


def tar_bytes(version, arch, binary):
    buffer = io.BytesIO()
    prefix = 'ssh-alertd-%s-linux-%s/' % (version, arch)
    entries = {'ssh-alertd': (binary, 0o755)}
    entries.update({name: ((ROOT / name).read_bytes(), 0o644) for name in archive_sources()})
    with tarfile.open(fileobj=buffer, mode='w', format=tarfile.USTAR_FORMAT) as archive:
        for name, (raw, mode) in sorted(entries.items()):
            info = tarfile.TarInfo(prefix + name)
            info.size, info.mode, info.mtime = len(raw), mode, 0
            info.uid = info.gid = 0
            info.uname = info.gname = 'root'
            archive.addfile(info, io.BytesIO(raw))
    result = io.BytesIO()
    with gzip.GzipFile(filename='', fileobj=result, mode='wb', mtime=0) as stream:
        stream.write(buffer.getvalue())
    return result.getvalue()


def read_asset(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or not 0 < info.st_size <= MAX_ASSET:
        raise ReleaseError('invalid asset file: ' + path.name)
    return path.read_bytes()


def verify(directory, version, commit, allow_dirty=False):
    validate_version(version)
    if not COMMIT_RE.fullmatch(commit):
        raise ReleaseError('invalid expected commit')
    directory = Path(directory)
    if directory.is_symlink() or not directory.is_dir():
        raise ReleaseError('asset directory must be a real directory')
    if {p.name for p in directory.iterdir()} != asset_names(version):
        raise ReleaseError('missing, duplicate or unexpected release assets')
    files = {p.name: read_asset(p) for p in directory.iterdir()}
    sums = files['SHA256SUMS'].decode('ascii')
    expected_sums = ''.join(digest(raw) + '  ' + name + '\n'
                            for name, raw in sorted(files.items()) if name != 'SHA256SUMS')
    if sums != expected_sums:
        raise ReleaseError('SHA256SUMS mismatch or unsafe checksum names')
    info = strict_json(files['BUILDINFO.json'])
    keys = {'format', 'version', 'source_commit', 'source_state', 'go_version', 'files'}
    if not isinstance(info, dict) or set(info) != keys:
        raise ReleaseError('invalid BUILDINFO schema')
    if (type(info['format']) is not int or info['format'] != 1 or info['version'] != version
            or info['source_commit'] != commit or info['go_version'] != 'go' + read_setting('.go-version')
            or info['source_state'] not in ('clean', 'dirty')
            or not allow_dirty and info['source_state'] != 'clean'):
        raise ReleaseError('build identity/version/toolchain/clean-state mismatch')
    expected_hashes = {name: digest(raw) for name, raw in files.items()
                       if name not in ('BUILDINFO.json', 'SHA256SUMS')}
    if info['files'] != expected_hashes or files['LICENSES.txt'] != (ROOT / 'LICENSES.txt').read_bytes():
        raise ReleaseError('BUILDINFO file hashes or licenses mismatch')
    for arch in ARCHES:
        name = 'ssh-alertd-%s-linux-%s' % (version, arch)
        binary = files[name]
        elf_check(binary, arch)
        try:
            with gzip.GzipFile(fileobj=io.BytesIO(files[name + '.tar.gz'])) as stream:
                tar_data = stream.read(MAX_TAR + 1)
            if len(tar_data) > MAX_TAR:
                raise ReleaseError('decompressed archive exceeds limit')
            expected = {name + '/ssh-alertd': binary}
            expected.update({name + '/' + p: (ROOT / p).read_bytes() for p in archive_sources()})
            with tarfile.open(fileobj=io.BytesIO(tar_data), mode='r:') as archive:
                members = archive.getmembers()
                if len(members) != len(expected) or {m.name for m in members} != set(expected):
                    raise ReleaseError('archive layout mismatch')
                for member in members:
                    mode = 0o755 if member.name.endswith('/ssh-alertd') else 0o644
                    if (not member.isfile() or member.linkname or member.pax_headers
                            or member.uid != 0 or member.gid != 0 or member.mode != mode
                            or member.size != len(expected[member.name])
                            or archive.extractfile(member).read() != expected[member.name]):
                        raise ReleaseError('invalid archive member')
        except (OSError, EOFError, tarfile.TarError) as exc:
            raise ReleaseError('invalid release archive') from exc
    return info


def build(output, tag=None, allow_dirty=False):
    if os.geteuid() == 0:
        raise ReleaseError('build as an unprivileged user')
    version = version_and_tag(tag)
    if tag and allow_dirty:
        raise ReleaseError('tag builds cannot allow dirty sources')
    commit = run(['git', 'rev-parse', 'HEAD'])
    if not COMMIT_RE.fullmatch(commit):
        raise ReleaseError('invalid source commit')
    state = 'dirty' if run(['git', 'status', '--porcelain', '--untracked-files=normal']) else 'clean'
    if state != 'clean' and not allow_dirty:
        raise ReleaseError('commit source changes before building release assets')
    output = Path(output).absolute()
    if output.is_symlink() or output.exists() and (not output.is_dir() or any(output.iterdir())):
        raise ReleaseError('output must be absent or an empty real directory; never overwrite releases')
    go = shutil.which('go')
    if not go:
        raise ReleaseError('Go toolchain not found')
    env = dict(os.environ, GOTOOLCHAIN='local', GOENV='off', GOWORK='off', GOPROXY='off',
               GOSUMDB='off', GOFLAGS='', GOEXPERIMENT='', GOAMD64='v1', GOARM64='v8.0',
               CGO_ENABLED='0', GOOS='linux')
    toolchain = run([go, 'env', 'GOVERSION'], env=env)
    if toolchain != 'go' + read_setting('.go-version'):
        raise ReleaseError('Go toolchain must exactly match .go-version')
    host_arch = run([go, 'env', 'GOHOSTARCH'], env=env)
    files = {'LICENSES.txt': (ROOT / 'LICENSES.txt').read_bytes()}
    with tempfile.TemporaryDirectory(prefix='ssh-alertd-release-', dir=os.environ['TMPDIR']) as tmp:
        for arch in ARCHES:
            binary = Path(tmp) / arch
            flags = '-s -w -buildid= -X main.buildVersion=%s -X main.buildCommit=%s -X main.buildSourceState=%s' % (version, commit, state)
            run([go, 'build', '-p', '2', '-trimpath', '-buildvcs=false', '-ldflags', flags,
                 '-o', str(binary), '.'], env=dict(env, GOARCH=arch))
            raw = binary.read_bytes()
            elf_check(raw, arch)
            if arch == host_arch and sys.platform == 'linux':
                identity = strict_json(run([str(binary), '-version']))
                expected_identity = {'version': version, 'commit': commit,
                                     'source_state': state, 'go_version': toolchain}
                if identity != expected_identity:
                    raise ReleaseError('native binary build identity mismatch')
            name = 'ssh-alertd-%s-linux-%s' % (version, arch)
            files[name] = raw
            files[name + '.tar.gz'] = tar_bytes(version, arch, raw)
    info = {'format': 1, 'version': version, 'source_commit': commit, 'source_state': state,
            'go_version': toolchain, 'files': {name: digest(raw) for name, raw in sorted(files.items())}}
    files['BUILDINFO.json'] = (json.dumps(info, sort_keys=True, indent=2) + '\n').encode()
    files['SHA256SUMS'] = ''.join(digest(raw) + '  ' + name + '\n' for name, raw in sorted(files.items())).encode()
    output.mkdir(parents=True, exist_ok=True)
    for name, raw in files.items():
        path = output / name
        with path.open('xb') as stream:
            stream.write(raw)
        path.chmod(0o755 if name.endswith(tuple(ARCHES)) else 0o644)
    verify(output, version, commit, allow_dirty=allow_dirty)
    return info


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    b = sub.add_parser('build')
    b.add_argument('--output', required=True)
    b.add_argument('--tag')
    b.add_argument('--allow-dirty', action='store_true', help='local experiments only; not a release')
    v = sub.add_parser('verify')
    v.add_argument('--directory', required=True)
    v.add_argument('--version', required=True)
    v.add_argument('--commit', required=True)
    v.add_argument('--allow-dirty', action='store_true')
    args = parser.parse_args()
    try:
        if args.command == 'build':
            info = build(args.output, args.tag, args.allow_dirty)
        else:
            info = verify(args.directory, args.version, args.commit, args.allow_dirty)
        print(json.dumps(info, indent=2, sort_keys=True))
    except (ReleaseError, OSError, ValueError, subprocess.SubprocessError) as exc:
        print('Release check failed: ' + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
