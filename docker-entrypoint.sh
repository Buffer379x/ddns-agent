#!/bin/sh
set -eu
umask 077
# A dedicated data volume from 1.0.x may be root-owned. Migrate ownership before
# dropping privileges. With --user, the caller manages volume permissions.
if [ "$(id -u)" = 0 ]; then
  data=${DDNS_DATA_DIR:-/data}
  uid=${DDNS_UID:-65532}
  gid=${DDNS_GID:-65532}
  case "$data" in ''|/|.) echo 'DDNS_DATA_DIR must be a dedicated absolute directory' >&2; exit 1;; /*) ;; *) exit 1;; esac
  case "$uid:$gid" in *[!0-9:]*|0:*|:*) echo 'DDNS_UID and DDNS_GID must be numeric; UID must not be root' >&2; exit 1;; esac
  mkdir -p "$data"
  chown -Rh "$uid:$gid" "$data"
  chmod 700 "$data"
  exec su-exec "$uid:$gid" /ddns-agent "$@"
fi
exec /ddns-agent "$@"
