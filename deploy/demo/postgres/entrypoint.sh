#!/bin/sh
set -eu
mkdir -p /run/demo-secrets
chmod 0700 /run/demo-secrets
cp /run/secrets/demo_reader_password /run/demo-secrets/reader_password
chown -R postgres:postgres /run/demo-secrets
chmod 0400 /run/demo-secrets/reader_password
exec /usr/local/bin/docker-entrypoint.sh "$@"
