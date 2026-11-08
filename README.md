# ssh-alertd — security-hardening fork

**[Русская инструкция](docs/README.ru.md)** · [Security changes and limitations](SECURITY_PATCHES.md)

This fork is based on Amekiri-Studio/ssh-alertd. The source includes local security
fixes; it is not an unmodified upstream release. No hardened release is published
yet. CI and draft-release automation are prepared and locally tested, but have
not run on GitHub yet. See [release guide](docs/RELEASING.md). The other
translated READMEs are upstream documentation, not verified fork instructions.

[![CI](https://github.com/den0043/ssh-alertd/actions/workflows/ci.yml/badge.svg)](https://github.com/den0043/ssh-alertd/actions/workflows/ci.yml)

**English** · [简体中文](docs/README.zh-CN.md) · [繁體中文](docs/README.zh-TW.md) · [日本語](docs/README.ja.md)

A small, modular SSH Alert Daemon written in Go. It watches `sshd` logs and, on
every **successful** SSH login, sends an alert containing:

- Login **IP** (the client's source IP)
- Login **username**
- **Time**
- **Client port** (the client's source port, taken from the sshd log line
  `from <IP> port <port>` — not the server's listening port 22)

Telegram and SMTP are implemented today. The notifier layer is an interface, so
WhatsApp, WeCom, DingTalk and Feishu can be added as self-contained files
without touching the rest of the system.

## Architecture

```
main.go                       wiring: config -> source -> monitor -> dispatcher
internal/
  event/event.go              LoginEvent domain model
  config/config.go            JSON config loading + validation
  monitor/
    source.go                 Source interface; journald & file followers
    monitor.go                sshd line parser -> LoginEvent
  notifier/
    notifier.go               Notifier interface + concurrent Dispatcher
    telegram.go               Telegram Bot backend (one file per backend)
    smtp.go                   SMTP (email) backend
```

Data flow: `Source` (journald/file) → `Monitor` parses "Accepted ..." lines →
`Dispatcher` fans each `LoginEvent` out to all enabled `Notifier`s concurrently.

## Build and test this fork

Use the exact toolchain in `.go-version` (Go 1.27.3) and Python 3.9+.
Set `TMPDIR` to a private scratch directory. From a clean, committed checkout:

```sh
python3 -I -B scripts/check.py
python3 -I -B scripts/release.py build --output dist
IFS= read -r version < VERSION
SSH_ALERTD_BINARY="$PWD/dist/ssh-alertd-${version}-linux-amd64" python3 -I -B tests/runtime-test.py
```

The builder creates static amd64/arm64 executables, tarballs, `BUILDINFO.json`,
`LICENSES.txt` and `SHA256SUMS`. It validates both ELF architectures, archive
contents and hashes; the native binary also reports its embedded version,
commit, source state and Go toolchain using `-version` without reading a config.
Dirty sources are refused by default. `--allow-dirty` is for local experiments;
such files cannot pass ordinary release verification.

A matching `v<VERSION>` tag runs tests and builds, then creates a **draft
prerelease**, not a public/latest release. Manual dispatch only builds artifacts.
The draft job downloads its assets and compares their bytes to the verified
local build. Publishing is a separate human action; see
[RELEASING.md](docs/RELEASING.md). No hardening release is published yet.

Legacy Debian/Arch packaging is excluded from the fork workflow; the old Debian
builder is disabled pending a separate package-installation audit. Do not use
upstream packages expecting them to contain fork fixes. Go is needed to build,
not to run the static executable. SHA256 is integrity checking, not a signature.

## Configure

Copy `config.example.json` to `/etc/ssh-alertd/config.json` and fill in your
Telegram bot token and chat ID.

- `log_source.type`: `journald` (default, reads sshd via `journalctl -f`) or
  `file` (tails `log_source.path` via `tail -F`, e.g. `/var/log/auth.log` on
  Debian/Ubuntu or `/var/log/secure` on RHEL).
- `hostname`: optional override; defaults to the OS hostname.

### Telegram Bot setup

Three steps: **create the bot to get a token → get the chat_id → fill in
`config.json`**.

**1. Create the bot and get `bot_token`**

1. In Telegram, search for the official **@BotFather** and start a chat.
2. Send `/newbot` and provide a display name and a username (the username must
   end in `bot`, e.g. `my_ssh_alert_bot`).
3. BotFather returns a token like `123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx` —
   that is your `bot_token`.

**2. Get `chat_id`**

A bot cannot message you first; you must message it before it can learn the
chat ID.

- To a private chat: search for your bot → tap **Start** → send any message.
- To a group (for multiple recipients): add the bot to the group and send a
  message there.

Use an already known chat ID or obtain it through a trusted tool that reads the
token interactively. Do not put a real bot token into a browser URL, command-line
argument, shell history, screenshot or repository. These instructions never need
your real token in chat.

**3. Fill in `config.json`**

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx",
  "chat_id": "-1001234567890",
  "api_base": "https://api.telegram.org"
}
```

- `enabled` must be `true`, otherwise the backend is not registered (startup
  exits with "no notifiers enabled").
- `bot_token` / `chat_id` are both **strings** — mind the quotes; an empty value
  for either fails startup.
- `api_base` is usually left at the default; change it only when you need a
  reverse proxy (networks where `api.telegram.org` is not reachable directly).

**4. Verify**

Use the saved root-owned config for a test. The Russian guide shows a Python
POST that does not put the token in argv or expose network exception URLs.
A successful API response is not proof of the whole SSH-to-Telegram pipeline:
open a genuinely new SSH authentication from your client and check the alert.
Do not reuse a multiplexed connection for that verification.

`/etc/ssh-alertd` must be root:ssh-alertd 0750 and `config.json` must be
root:ssh-alertd 0640 for the dedicated service: readable, not writable, by it.
Never commit a real config or share unredacted logs.

#### Custom message (optional)

By default the message uses a built-in HTML format. To customize the text, set
`message_template` (a [Go template](https://pkg.go.dev/text/template) with the
same event fields as SMTP: `.Username` `.IP` `.Port` `.Method` `.Hostname`
`.Time`):

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExampleBotTokenReplaceMe",
  "chat_id": "-1001234567890",
  "message_template": "🔐 <b>{{.Username}}</b> logged in from <code>{{.IP}}</code> on {{.Hostname}}",
  "parse_mode": "HTML"
}
```

- `message_template`: empty (default) uses the built-in HTML format.
- `message_template_file`: a path read as the message template; it takes
  precedence over `message_template` and is handy for multi-line messages.
- `parse_mode`: `HTML` (default), `MarkdownV2`, `Markdown`, or `none` (plain
  text). With `HTML`, event fields are auto-escaped (via `html/template`) so a
  value like a username can't break the markup; with the other modes you are
  responsible for any escaping the format requires.

> Telegram's "HTML" supports only a small set of inline tags (`<b> <i> <code>
> <pre> <a>` …) — there is no layout or color, so keep templates to formatted
> text.

Ready-to-use examples live in [`examples/telegram/`](examples/telegram/).

### SMTP (email) setup

Add an `smtp` block under `notifiers` to receive alerts by email. This is handy
on networks where Telegram is unreachable, since it talks to your own mail
server directly.

```json
"smtp": {
  "enabled": true,
  "host": "smtp.example.com",
  "port": 587,
  "username": "alert@example.com",
  "password": "your-smtp-password",
  "from": "alert@example.com",
  "to": ["admin@example.com"],
  "encryption": "starttls"
}
```

- `encryption`: `starttls` (default, typically port 587), `tls` (implicit
  TLS / SMTPS, typically port 465), or `none` (port 25, no transport security).
- `port`: optional — defaults to `465` for `tls`, otherwise `587`.
- `username` / `password`: optional; omit `username` to skip authentication
  (e.g. an internal relay). When set, `PLAIN` auth is used (so always over TLS).
- `to`: one or more recipients; at least one is required.
- Multiple notifiers can be enabled at once — each enabled backend gets every
  alert independently.

Telegram and SMTP are independent: enabling one does not require the other.

#### Custom email templates

The subject and body can be customized with [Go templates](https://pkg.go.dev/text/template).
Templates receive the login event with these fields:

| Field | Example |
| --- | --- |
| `.Username` | `alice` |
| `.IP` | `203.0.113.5` |
| `.Port` | `50568` (client source port) |
| `.Method` | `publickey` / `password` |
| `.Hostname` | `web-01` |
| `.Time` | a `time.Time`; format with `{{.Time.Format "2006-01-02 15:04:05"}}` |

```json
"smtp": {
  "enabled": true,
  "host": "smtp.example.com",
  "from": "alert@example.com",
  "to": ["admin@example.com"],
  "subject_template": "[ALERT] SSH login {{.Username}}@{{.Hostname}}",
  "body_template": "{{.Username}} logged in from {{.IP}}:{{.Port}} via {{.Method}} at {{.Time.Format \"2006-01-02 15:04:05\"}}",
  "html": false
}
```

- `subject_template` / `body_template`: inline Go templates. Empty values use the
  built-in subject and body.
- `body_template_file`: a path read as the body template; it takes precedence
  over `body_template` and is handy for multi-line or HTML bodies.
- `html: true` renders the body as `text/html` (using `html/template`, which
  auto-escapes the event fields); pair it with a `body_template`.

Templates are compiled at startup, so a malformed template fails fast with a
clear error rather than silently dropping alerts.

Ready-to-use HTML and plain-text examples live in
[`examples/email/`](examples/email/).

## Run

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

### As a systemd service

```sh
sudo install -m0755 ssh-alertd /usr/local/bin/ssh-alertd
sudo install -d /etc/ssh-alertd
sudo install -m0640 config.example.json /etc/ssh-alertd/config.json   # then edit
sudo install -m0644 deploy/ssh-alertd.service /etc/systemd/system/ssh-alertd.service

# Create the dedicated user; keep config owned by root, readable by the group.
sudo install -Dm644 deploy/ssh-alertd.sysusers /usr/lib/sysusers.d/ssh-alertd.conf
sudo install -Dm644 deploy/ssh-alertd.tmpfiles /usr/lib/tmpfiles.d/ssh-alertd.conf
sudo systemd-sysusers
sudo systemd-tmpfiles --create

sudo systemctl daemon-reload
sudo systemctl enable --now ssh-alertd
```

The service runs as the dedicated `ssh-alertd` user. The config belongs to
root:ssh-alertd and is not writable by the service. It has supplementary
`systemd-journal` membership for the journald source. For the `file` source,
also grant it read access to the auth log (see Privileges below).

## Privileges

ssh-alertd itself does **not require full root**: it does not listen on a port
or write system files — it only **reads sshd login logs** and makes HTTPS
requests to Telegram. The privileges needed depend entirely on how the log is
read, and reading SSH authentication logs is a protected operation on Linux.

| Log source (`log_source.type`) | Reads | Privileges required |
| --- | --- | --- |
| `journald` (default) | systemd journal | root, **or** membership in the `systemd-journal` group |
| `file` | `/var/log/auth.log` (Debian/Ubuntu), `/var/log/secure` (RHEL) | root, **or** a group with read access to the file (usually `adm` on Debian/Ubuntu) |

Confirm access with the matching command first — if you can see output, the
daemon can read it too:

```sh
journalctl -t sshd -t sshd-session -n5    # journald source
tail -n5 /var/log/auth.log                # file source
```

### systemd deployment

The unit runs as the dedicated system user **`ssh-alertd`** (created from
`deploy/ssh-alertd.sysusers` by systemd-sysusers) and is granted read access to
the journal via `SupplementaryGroups=systemd-journal` — so **the journald source
needs no extra authorization when started via systemd**. The fork tmpfiles rules
keep `/etc/ssh-alertd` root:ssh-alertd 0750 and `config.json` root:ssh-alertd 0640:
the service can read the token but cannot rewrite the config. Inspect any local
`/etc/tmpfiles.d/ssh-alertd.conf` overrides on an existing installation.

> The Arch / Debian packages install these two files and trigger
> sysusers/tmpfiles automatically. For a manual systemd deployment, run once:
>
> ```sh
> sudo install -Dm644 deploy/ssh-alertd.sysusers /usr/lib/sysusers.d/ssh-alertd.conf
> sudo install -Dm644 deploy/ssh-alertd.tmpfiles /usr/lib/tmpfiles.d/ssh-alertd.conf
> sudo systemd-sysusers && sudo systemd-tmpfiles --create
> ```

For the **file** source, the `ssh-alertd` user cannot read `auth.log` by
default; add the log file's group to the unit, e.g. on Debian/Ubuntu:

```ini
SupplementaryGroups=systemd-journal adm
```

### Manual debugging

The simplest approach is to run as root directly:

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

Or add your current user to the relevant group to avoid sudo (re-login for the
group to take effect):

```sh
sudo usermod -aG systemd-journal $USER   # journald source
sudo usermod -aG adm $USER               # file source (Debian/Ubuntu)
```

## Troubleshooting

ssh-alertd writes diagnostics to **stderr** (view with
`journalctl -u ssh-alertd -f` under systemd). A healthy start shows
`enabled notifier: telegram` and `monitor started on source ...`; every
successful login produces `detected SSH login: ...` and
`notifier telegram delivered alert ...`.

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Exits at startup with `no notifiers enabled` | `telegram.enabled` is not `true` | Set it to `true` |
| Startup error `telegram enabled but bot_token/chat_id is empty` | Missing credentials | Fill in `bot_token` / `chat_id` (strings) |
| Startup error `parse config ...` | JSON syntax error or unknown field | Config uses `DisallowUnknownFields`; remove typos/extra keys, compare against `config.example.json` |
| **No** `detected` log after a login | Log source can't read sshd | See "No log lines" below |
| `detected` present but `notifier telegram failed` | Telegram delivery failed | See "No Telegram message" below |

**No log lines (no `detected`)**

1. Confirm access first — if you can see output, the daemon can read it (see
   Privileges):
   ```sh
   journalctl -t sshd -t sshd-session -n5    # journald source
   tail -n5 /var/log/auth.log                # file source
   ```
2. Open a genuinely new authentication from your normal SSH client (not a reused
   multiplexed connection) and check for `Accepted ... ssh2` in the journal. Do
   not install extra keys or enable password login merely to test localhost.
3. Journald records also need trusted `_UID=0` and an absolute `_EXE` ending in
   `sshd` or `sshd-session`. A matching syslog identifier alone is not trusted.
   For the `file` source, check that `log_source.path` is correct
   (`/var/log/auth.log` on Debian/Ubuntu, `/var/log/secure` on RHEL/CentOS).
4. On a few distros sshd's syslog identifier is neither `sshd` nor
   `sshd-session`, so the journald source filters it out; switch to the `file`
   source.

**No Telegram message (`notifier telegram failed` in the log)**

HTTP errors deliberately report only a numeric status. Full URLs, response
bodies and arbitrary transport errors are not logged because they may contain
the bot token. Redirects are not followed. Common status codes are 401/404 for
credentials or endpoint issues, 400 for a bad request/chat, and 403 for denied
access. Check the chat and credentials locally without printing them.

See the Russian guide for a safe saved-config test. Network/TLS/timeouts are
reported without raw exception text. The daemon has no persistent queue,
journal cursor, replay or Telegram retry: downtime and network failures can lose
alerts. Restarting monitoring does not recover missed events.

## Adding a new notifier

1. Create `internal/notifier/<backend>.go` implementing the `Notifier`
   interface (`Name()` and `Send(ctx, event.LoginEvent) error`).
2. Add its config struct to `internal/config/config.go`.
3. Register it in `buildNotifiers` in `main.go`.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the
full text.
