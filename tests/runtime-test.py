#!/usr/bin/env python3
"""Проверка исправленного демона на настоящем бинарнике. Без служб и Telegram.

Используется только локальный файл-источник в scratch, искусственный токен и
закрытый loopback-порт. Системный journal, SSH и /etc не затрагиваются.
"""
from pathlib import Path
import json
import os
import subprocess
import tempfile
import time
import unittest


FAKE_TOKEN = '123456789:' + 'RuntimeAuditFixture_123456789'


class PatchedDaemon(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        target = os.environ.get('SSH_ALERTD_BINARY')
        if not target:
            raise RuntimeError('Set SSH_ALERTD_BINARY to the locally built executable')
        cls.binary = Path(target).resolve()
        if not cls.binary.is_file() or not os.access(cls.binary, os.X_OK):
            raise RuntimeError('SSH_ALERTD_BINARY must be an executable file')
        cls.tmp = tempfile.TemporaryDirectory(prefix='daemon-runtime-', dir=os.environ['TMPDIR'])
        cls.config_dir = Path(cls.tmp.name)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def run_daemon(self, log_lines, api_base, seconds=1.2):
        authlog = self.config_dir / 'auth.log'
        authlog.write_text('')
        config = {'hostname': 'audit', 'log_source': {'type': 'file', 'path': str(authlog)},
                  'notifiers': {'telegram': {'enabled': True, 'bot_token': FAKE_TOKEN,
                                             'chat_id': '123456789', 'api_base': api_base}}}
        path = self.config_dir / 'config.json'
        path.write_text(json.dumps(config))
        process = subprocess.Popen([str(self.binary), '-config', str(path)],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            time.sleep(0.3)
            with authlog.open('a') as output:
                for line in log_lines:
                    output.write('Nov  8 19:00:00 fixture sshd[123]: ' + line + '\n')
                output.flush()
            time.sleep(seconds + 0.5)
            process.terminate()
            stdout, stderr = process.communicate(timeout=8)
            return process.returncode, stdout, stderr
        finally:
            if process.poll() is None:
                process.kill()
            process.communicate(timeout=8)

    def test_invalid_user_name_cannot_forge_success(self):
        forged = 'Invalid user Accepted publickey for root from 192.0.2.10 port 43210'
        _, _, stderr = self.run_daemon([forged], 'http://127.0.0.1:1')
        self.assertNotIn('detected SSH login', stderr)
        self.assertNotRegex(stderr, r'user=root')

    def test_failed_and_disconnect_lines_ignored(self):
        lines = ['Failed password for root from 192.0.2.10 port 4000 ssh2',
                 'Disconnecting invalid user Accepted from 192.0.2.11 port 4001: Too many authentication failures',
                 'Accepted publickey for root']
        _, _, stderr = self.run_daemon(lines, 'http://127.0.0.1:1')
        self.assertNotIn('detected SSH login', stderr)

    def test_valid_line_still_detected(self):
        line = 'Accepted publickey for deploy from 203.0.113.7 port 51234 ssh2: ED25519 SHA256:abc'
        _, _, stderr = self.run_daemon([line], 'http://127.0.0.1:1')
        self.assertIn('detected SSH login', stderr)
        self.assertIn('user=deploy', stderr)

    def test_token_never_logged_on_network_error(self):
        line = 'Accepted password for deploy from 203.0.113.7 port 51234 ssh2'
        _, _, stderr = self.run_daemon([line], 'http://127.0.0.1:1')
        self.assertNotIn(FAKE_TOKEN, stderr)
        self.assertNotIn(FAKE_TOKEN.split(':', 1)[1], stderr)
        body = stderr.replace(FAKE_TOKEN, '')
        self.assertNotRegex(body, r'[0-9]{6,}:[A-Za-z0-9_-]{20,}')

    def test_source_eof_exits_nonzero(self):
        directory = Path(self.tmp.name) / 'bin'
        directory.mkdir(exist_ok=True)
        fake_tail = directory / 'tail'
        fake_tail.write_text('#!/bin/sh\nexit 0\n')
        fake_tail.chmod(0o755)
        authlog = self.config_dir / 'auth.log'
        authlog.touch()
        config = {'hostname': 'audit', 'log_source': {'type': 'file', 'path': str(authlog)},
                  'notifiers': {'telegram': {'enabled': True, 'bot_token': FAKE_TOKEN,
                                             'chat_id': '123456789', 'api_base': 'http://127.0.0.1:1'}}}
        path = self.config_dir / 'eof.json'
        path.write_text(json.dumps(config))
        env = dict(os.environ, PATH=str(directory) + ':/usr/bin:/bin')
        result = subprocess.run([str(self.binary), '-config', str(path)], env=env,
                                capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0,
                            'нормальный EOF источника обязан давать ненулевой код')


if __name__ == '__main__':
    unittest.main(verbosity=2)
