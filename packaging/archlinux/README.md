# Arch Linux package (legacy upstream reference)

> **Not part of the security fork release.** This historical packaging has not
> passed a fork installation audit. Do not use it as the fork installer.
> Use the static assets built by `scripts/release.py`; see `docs/RELEASING.md`.

A `makepkg`-based package for ssh-alertd.

## Build & install

```sh
cd packaging/archlinux
makepkg -si        # build, then install with pacman (resolves deps)
```

This produces `ssh-alertd-<ver>-<rel>-<arch>.pkg.tar.zst` and installs:

| Path | Content |
| --- | --- |
| `/usr/bin/ssh-alertd` | the daemon binary |
| `/usr/lib/systemd/system/ssh-alertd.service` | systemd unit (ExecStart → `/usr/bin/ssh-alertd`) |
| `/etc/ssh-alertd/config.json` | default config (mode 0640, in `backup=` so pacman keeps your edits) |
| `/usr/share/doc/ssh-alertd/` | README + example config |
| `/usr/share/licenses/ssh-alertd/LICENSE` | Apache-2.0 |

After install:

```sh
sudoedit /etc/ssh-alertd/config.json     # set bot_token & chat_id
sudo systemctl enable --now ssh-alertd
```

## Notes

- **Source / version.** The `PKGBUILD` builds from the GitHub release tarball
  `v$pkgver`. Before building a real release, create the matching git tag
  (e.g. `git tag v0.1.0 && git push --tags`) and replace `sha256sums=('SKIP')`
  with the real checksum via `updpkgsums`.
- **Build the current checkout instead of a release.** To test the working tree
  without a tag, point `source` at a local tarball or use a `-git` VCS variant
  (`source=("git+file://$PWD/../..")`, `pkgver()` from `git describe`).
- **Go flags.** The build uses Arch's standard hardened Go flags
  (`-buildmode=pie -trimpath -mod=readonly -modcacherw`); `check()` runs
  `go test ./...`.
