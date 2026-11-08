#!/usr/bin/env python3
"""Same offline checks in local work and GitHub CI. No installation or API calls."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
ENV = dict(os.environ, GOTOOLCHAIN='local', GOENV='off', GOWORK='off',
           GOPROXY='off', GOSUMDB='off', GOFLAGS='', PYTHONDONTWRITEBYTECODE='1')


def run(args, capture=False):
    result = subprocess.run(args, cwd=ROOT, env=ENV, check=False, text=True,
                            stdout=subprocess.PIPE if capture else None, timeout=300)
    if result.returncode:
        raise RuntimeError('check failed: ' + args[0])
    return result.stdout or ''


def main():
    expected = 'go' + (ROOT / '.go-version').read_text().removesuffix('\n')
    if run(['go', 'env', 'GOVERSION'], capture=True).strip() != expected:
        raise RuntimeError('toolchain must exactly match .go-version')
    files = run(['gofmt', '-l', '.'], capture=True)
    if files:
        raise RuntimeError('gofmt required: ' + files)
    run(['go', 'vet', '-p', '2', './...'])
    with tempfile.TemporaryDirectory(prefix='ssh-alertd-check-', dir=ENV['TMPDIR']) as temporary:
        log = Path(temporary) / 'go-tests.jsonl'
        with log.open('w') as stream:
            result = subprocess.run(['go', 'test', '-race', '-p', '2', '-count=1', '-json', './...'],
                                    cwd=ROOT, env=ENV, stdout=stream, stderr=subprocess.STDOUT, timeout=300)
        events = []
        for line in log.read_text().splitlines():
            try:
                events.append(json.loads(line))
            except ValueError:
                pass
        if result.returncode or any(event.get('Action') == 'fail' for event in events):
            print(log.read_text(), file=sys.stderr)
            raise RuntimeError('Go race tests failed')
        passed = [event for event in events if event.get('Action') == 'pass' and event.get('Test')]
        if not passed:
            raise RuntimeError('Go tests produced no passing test records')
        print('Go tests passed: %d top-level, %d with subtests' %
              (sum('/' not in event['Test'] for event in passed), len(passed)), flush=True)
    run([sys.executable, '-I', '-B', '-m', 'unittest', 'discover', '-s', 'tests', '-p', 'test_*.py', '-v'])
    print('Formatting, Go vet/race and Python release-policy tests: PASS', flush=True)
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (RuntimeError, OSError, subprocess.SubprocessError) as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
