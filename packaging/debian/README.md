# Debian / Ubuntu package (legacy upstream reference)

> **Not part of the security fork release.** This historical packaging has not
> passed a fork installation audit. Use the static assets built by
> `scripts/release.py`; see `docs/RELEASING.md`.
> The legacy Debian builder is disabled to avoid restoring service-owned config.

A self-contained `.deb` builder for ssh-alertd. It stages the file tree and runs
`dpkg-deb` directly — no debhelper / `dpkg-buildpackage` toolchain required.

## Build

On a Debian/Ubuntu host (or anything with `dpkg-deb` + a Go toolchain):

```sh
cd packaging/debian
./build.sh            # builds ssh-alertd_0.1.0_<arch>.deb
./build.sh 0.2.0      # override the version
```

Requirements: `dpkg-deb` (package `dpkg`) and `go`. The architecture is taken
from `dpkg --print-architecture`.

## Install

```sh
sudo apt install ./ssh-alertd_0.1.0_amd64.deb
# or: sudo dpkg -i ssh-alertd_0.1.0_amd64.deb
```

This installs:

| Path | Content |
| --- | --- |
| `/usr/bin/ssh-alertd` | the daemon binary (statically linked) |
| `/lib/systemd/system/ssh-alertd.service` | systemd unit (ExecStart → `/usr/bin/ssh-alertd`) |
| `/etc/ssh-alertd/config.json` | default config (conffile, mode 0640) |
| `/usr/share/doc/ssh-alertd/` | README, copyright, changelog.gz |

After install:

```sh
sudoedit /etc/ssh-alertd/config.json     # set bot_token & chat_id
sudo systemctl enable --now ssh-alertd
```

## Notes

- **Conffile.** `config.json` is registered in `DEBIAN/conffiles`, so `apt`/`dpkg`
  preserves your edits across upgrades and only removes it on `purge`.
- **Static binary.** Built with `CGO_ENABLED=0`, so the package has no `libc6`
  coupling and works across Debian/Ubuntu releases; `control` depends only on
  `systemd`.
- **Service lifecycle.** Maintainer scripts run `daemon-reload` on install and
  stop/disable the service on removal. The service is **not** auto-started — it
  needs a configured token first.
- **Lint.** If you have `lintian`, run `lintian ssh-alertd_*.deb` for a policy
  check (a few informational tags are expected for a hand-built package).
