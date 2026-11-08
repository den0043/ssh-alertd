#!/usr/bin/env bash
# Legacy upstream packaging is intentionally disabled in the security fork.
# The old postinst changes the config owner; it is not part of a verified release.
printf '%s\n' 'Debian packaging is disabled pending a separate installation audit.' >&2
printf '%s\n' 'Use scripts/release.py for verified static binaries and archives; see docs/RELEASING.md.' >&2
exit 2
