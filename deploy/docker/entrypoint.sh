#!/bin/sh
set -eu

source_database_secret=/run/secrets/postgres_password
runtime_database_secret=/run/relythra-secrets/postgres_password
source_management_secret=/run/secrets/management_token
runtime_management_secret=/run/relythra-secrets/management_token

if [ "${RELYTHRA_DATABASE_PASSWORD_REF:-}" = "file:${source_database_secret}" ] ||
   [ "${RELYTHRA_ADMIN_TOKEN_REF:-}" = "file:${source_management_secret}" ]; then
  mkdir -p /run/relythra-secrets
  chown root:relythra /run/relythra-secrets
  chmod 0710 /run/relythra-secrets
fi

if [ "${RELYTHRA_DATABASE_PASSWORD_REF:-}" = "file:${source_database_secret}" ]; then
  cp "${source_database_secret}" "${runtime_database_secret}"
  chown relythra:relythra "${runtime_database_secret}"
  chmod 0440 "${runtime_database_secret}"
  export RELYTHRA_DATABASE_PASSWORD_REF="file:${runtime_database_secret}"
fi

if [ "${RELYTHRA_ADMIN_TOKEN_REF:-}" = "file:${source_management_secret}" ]; then
  cp "${source_management_secret}" "${runtime_management_secret}"
  chown relythra:relythra "${runtime_management_secret}"
  chmod 0440 "${runtime_management_secret}"
  export RELYTHRA_ADMIN_TOKEN_REF="file:${runtime_management_secret}"
fi

exec su-exec relythra /usr/local/bin/relythra "$@"
