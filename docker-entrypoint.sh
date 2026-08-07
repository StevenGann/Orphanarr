#!/bin/sh
# Dual-mode entrypoint: supports both container idioms and forces neither.
#
# The *arr ecosystem uses PUID/PGID; the security-minded use
# `--user 1000:1000`. Supporting only one produces a whole category of
# permission tickets, so this supports both (DESIGN §11.1, dissent D3).
set -eu

umask "${UMASK:-002}"

if [ "$(id -u)" != "0" ]; then
    # Already unprivileged: `docker run --user 1000:1000` or `user:` in
    # compose. Nothing to drop, nothing to chown. This is the path the
    # design records as first-class.
    exec "$@"
fi

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if ! getent group "$PGID" >/dev/null 2>&1; then
    addgroup -g "$PGID" orphanarr 2>/dev/null || true
fi
if ! getent passwd "$PUID" >/dev/null 2>&1; then
    adduser -D -u "$PUID" -G "$(getent group "$PGID" | cut -d: -f1)" orphanarr 2>/dev/null || true
fi

# chown /config ONLY.
#
# Never a media root: that would be a 40 TB chown on every start, and on a
# hardlinked library it would reach through to the seeding torrent's inode,
# violating I1 on startup before the program has done anything.
mkdir -p "${ORPHANARR__CONFIG_DIR:-/config}"
chown -R "$PUID:$PGID" "${ORPHANARR__CONFIG_DIR:-/config}" 2>/dev/null || true

exec su-exec "$PUID:$PGID" "$@"
