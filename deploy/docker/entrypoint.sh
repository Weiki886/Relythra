#!/bin/sh
set -eu

source_secret=/run/secrets/postgres_password
runtime_secret=/run/relythra-secrets/postgres_password

if [ "${RELYTHRA_DATABASE_PASSWORD_REF:-}" = "file:${source_secret}" ]; then
  mkdir -p /run/relythra-secrets
  chown root:relythra /run/relythra-secrets
  chmod 0710 /run/relythra-secrets
  cp "${source_secret}" "${runtime_secret}"
  chown relythra:relythra "${runtime_secret}"
  chmod 0440 "${runtime_secret}"
  export RELYTHRA_DATABASE_PASSWORD_REF="file:${runtime_secret}"
fi

exec su-exec relythra /usr/local/bin/relythra "$@"
