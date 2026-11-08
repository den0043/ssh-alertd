# Local security and reliability patches (upstream v0.2.0)

This vendored source has local standard-library-only changes. The v0.2.0
configuration schema and notifier/server options are unchanged. The local fork also changes the deployment unit restart policy and config
ownership as described below; release automation is a separate work stage.

## Authentication event parsing

- A successful event must match a complete, anchored OpenSSH `Accepted METHOD
  for USER from IP port N ssh2...` message. Failed, invalid-user and disconnect
  diagnostics cannot become successes by embedding `Accepted` in a username.
- IPv4/IPv6 addresses are checked with `net.ParseIP`; client ports must be
  decimal integers in 1..65535. Control characters and malformed records are
  rejected.
- File input requires a structural RFC3164-style or RFC3339 timestamp, host,
  and `sshd[PID]:` / `sshd-session[PID]:` prefix. Arbitrary leading text is not
  searched for a successful-login substring.

## Journal provenance

- The follower reads JSON (`journalctl -f -n 0 -o json -t sshd -t
  sshd-session`), not unauthenticated `-o cat` messages.
- Records must contain a string `MESSAGE`, trusted `_UID` equal to `"0"`, and
  an absolute trusted `_EXE` whose basename is `sshd` or `sshd-session`.
  The client-supplied `SYSLOG_IDENTIFIER` alone is never authentication.
- Missing fields, unexpected field types/arrays, malformed JSON, non-root
  senders and logger/systemd-cat executables are ignored. There is no required
  systemd unit or working directory; standalone root sshd is supported.
- Rejected journal records are never logged as raw JSON.

## Telegram credentials and HTTP behavior

- Transport/TLS/request-construction errors never include or wrap request URLs
  or arbitrary transport error strings. Timeout/cancellation remain identifiable
  through standard context errors.
- HTTP error responses report only the numeric status: neither response text,
  status text nor redirect locations can echo a bot token into error logs.
- HTTP redirects are not followed, including 307/308 body-preserving redirects.
- The main logger and standard logger use a synchronized redacting writer for
  configured Telegram tokens (and SMTP password), raw and percent-encoded
  forms, plus recognizable bot-token patterns. It operates on complete log
  records, as emitted by `log.Logger`, not secrets split across separate writes.
- Existing HTML escaping is retained and covered by regression tests.

## Follower failures and remaining delivery limitations

- Unexpected EOF, a nonzero follower exit, startup/read/scanner failures return
  errors from `Monitor.Run`; main exits nonzero. Cancellation returns nil.
- The follower child is terminated/reaped on read failure or EOF, including a
  child that closes stdout but keeps running. Raw input, command stderr and
  scanner error strings are not printed as diagnostics.
- **No persistent queue, journal cursor, retry/replay or exactly-once delivery
  is implemented.** Followers start at new records (`-n 0`), so events during
  downtime/restarts can be lost. Failed notifications are not retried. A send
  timeout can have an uncertain delivery outcome; manually replaying events
  can duplicate alerts. A service restart policy restores monitoring only,
  not missed events or notification state.
- File-source prefixes provide syntax validation, not journal-grade origin
  authentication. File mode cannot distinguish a fully forged syslog entry
  from sshd output. Only use an appropriately trusted logging pipeline; prefer
  the journal source with trusted metadata where local sender spoofing matters.
- Root compromise or a malicious executable run as root under the allowed
  basename is outside this provenance filter's threat model.

## Versioned fork releases

- `.go-version` pins the build toolchain; `VERSION` is the only release version.
- The builder embeds version, source commit and clean/dirty state; `-version`
  returns JSON before reading configuration or starting any log follower.
- CI and tag workflows run formatting, vet, race tests, release-policy tests,
  cross-build verification and native runtime tests before artifacts are used.
- Only the isolated draft job has contents:write. Actions use full commit SHAs;
  event data is not interpolated into shell source. Manual dispatch cannot publish.
- The publisher requires the exact tag/commit, creates a draft prerelease,
  reads it back, downloads and byte-compares assets; it never auto-publishes,
  creates a missing tag or overwrites an existing release. API tests are mocks.
- Legacy Debian/Arch packaging is not a fork release artifact; the old Debian
  builder is disabled pending a separate installation audit.
- See docs/RELEASING.md. Local checks do not prove a successful GitHub run or
  published release. SHA256 is not a signature; immutable settings/review are
  separate repository controls.

## Regression checks

The tests use only local fixtures, controlled child test binaries, and
loopback/`httptest` servers. They never write journal records, invoke real
journalctl/logger/systemd-cat, change SSH/services, or contact Telegram.
They cover negative authentication/provenance cases, IPv6/syslog compatibility,
real connection refusal/TLS/client-timeout failures, response-token echoes,
redirect denial, redaction, subprocess EOF/scanner/cancellation, and main's
nonzero exit after follower EOF.

Run with a compatible Go toolchain:

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

## Fork deployment defaults

- `deploy/ssh-alertd.service` uses `Restart=always` (manual stop still stops it).
- tmpfiles keeps `/etc/ssh-alertd` root:ssh-alertd 0750 and `config.json`
  root:ssh-alertd 0640: the service can read but cannot rewrite its credentials.
- A previous installation may have local `/etc/tmpfiles.d/ssh-alertd.conf`
  overrides; inspect them rather than claiming the packaged defaults took effect.
- These files were tested without installing or changing real system services.
