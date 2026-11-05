#!/bin/sh
set -eu
READ_TOKEN="$(cat /run/secrets/observability_token)"
# Limit syntax for safe template rendering. Generate this independent token as hex.
case "$READ_TOKEN" in ''|*[!a-zA-Z0-9_-]*) echo 'Invalid read token format' >&2; exit 1;; esac
[ "${#READ_TOKEN}" -ge 32 ] || { echo 'Read token too short' >&2; exit 1; }
export READ_TOKEN
envsubst '${READ_TOKEN}' < /etc/nginx/demo.conf.template > /etc/nginx/conf.d/default.conf
unset READ_TOKEN
exec nginx -g 'daemon off;'
