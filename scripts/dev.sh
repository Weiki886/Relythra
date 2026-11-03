#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

TMP_ROOT="${TMPDIR:-/tmp}"
RUN_DIR="${TMP_ROOT%/}/relythra-dev-$(id -u)"
LOG_DIR="${RUN_DIR}/logs"
DB_NAME="relythra"
DB_HOST="${PGHOST:-127.0.0.1}"
DB_PORT="${PGPORT:-5432}"
DB_USER="${PGUSER:-$(id -un)}"
DATABASE_URL_WAS_SET="${RELYTHRA_DATABASE_URL:+1}"

export RELYTHRA_DATABASE_URL="${RELYTHRA_DATABASE_URL:-postgres://${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable}"
export RELYTHRA_API_ADDR="127.0.0.1:8080"
export RELYTHRA_WORKER_ADDR="127.0.0.1:8081"

API_URL="http://127.0.0.1:8080"
WORKER_URL="http://127.0.0.1:8081"
WEB_URL="http://127.0.0.1:5173"

api_pid=""
worker_pid=""
web_pid=""

usage() {
  cat <<'EOF'
Usage: ./scripts/dev.sh [--check|--help]

Start the local API, worker, and web development server without Docker.
PostgreSQL must already be running.

Environment:
  RELYTHRA_DATABASE_URL  PostgreSQL connection string.
                         Defaults to postgres://$USER@127.0.0.1:5432/relythra?sslmode=disable
  PGHOST, PGPORT, PGUSER Used to build the default connection string.

Options:
  --check  Validate dependencies, prepare the local database, and print service URLs.
  --help   Show this help.
EOF
}

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command '$1' was not found"
}

preflight() {
  require_command go
  require_command node
  require_command npm
  require_command psql
  require_command curl

  if [ -z "${DATABASE_URL_WAS_SET}" ]; then
    require_command createdb
  fi
}

ensure_database() {
  if psql "${RELYTHRA_DATABASE_URL}" --no-psqlrc --set ON_ERROR_STOP=1 \
    --tuples-only --no-align --command 'SELECT 1' >/dev/null 2>&1; then
    return
  fi

  if [ -n "${DATABASE_URL_WAS_SET}" ]; then
    fail "cannot connect to PostgreSQL using RELYTHRA_DATABASE_URL"
  fi

  psql --no-psqlrc --set ON_ERROR_STOP=1 --tuples-only --no-align \
    --dbname postgres --username "${DB_USER}" --host "${DB_HOST}" --port "${DB_PORT}" \
    --command 'SELECT 1' >/dev/null 2>&1 \
    || fail "cannot connect to PostgreSQL at ${DB_HOST}:${DB_PORT} as ${DB_USER}"

  printf 'Creating local PostgreSQL database %s...\n' "${DB_NAME}"
  createdb --username "${DB_USER}" --host "${DB_HOST}" --port "${DB_PORT}" "${DB_NAME}" \
    || fail "failed to create PostgreSQL database ${DB_NAME}"
}

ensure_web_dependencies() {
  if [ ! -f "${ROOT_DIR}/web/node_modules/vite/bin/vite.js" ]; then
    printf 'Installing web dependencies...\n'
    npm --prefix "${ROOT_DIR}/web" ci --no-audit --no-fund
  fi
}

ensure_port_free() {
  local label="$1"
  local port="$2"

  command -v lsof >/dev/null 2>&1 || return
  if lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
    printf 'Port %s is already in use (%s).\n' "${port}" "${label}" >&2
    lsof -nP -iTCP:"${port}" -sTCP:LISTEN >&2 || true
    exit 1
  fi
}

print_configuration() {
  printf 'API=%s\n' "${API_URL}"
  printf 'WORKER=%s\n' "${WORKER_URL}"
  printf 'WEB=%s\n' "${WEB_URL}"
  printf 'LOGS=%s\n' "${LOG_DIR}"
}

show_logs() {
  local log_file

  for log_file in "${LOG_DIR}"/*.log; do
    [ -f "${log_file}" ] || continue
    printf '\n----- %s -----\n' "$(basename "${log_file}")" >&2
    tail -n 40 "${log_file}" >&2
  done
}

cleanup() {
  local status=$?

  trap - EXIT INT TERM HUP
  for pid in "${api_pid}" "${worker_pid}" "${web_pid}"; do
    if [ -n "${pid}" ] && kill -0 "${pid}" >/dev/null 2>&1; then
      kill "${pid}" >/dev/null 2>&1 || true
    fi
  done
  wait >/dev/null 2>&1 || true
  exit "${status}"
}

wait_for_service() {
  local name="$1"
  local url="$2"
  local pid="$3"
  local attempt=0

  while [ "${attempt}" -lt 240 ]; do
    if ! kill -0 "${pid}" >/dev/null 2>&1; then
      printf '%s exited before becoming healthy.\n' "${name}" >&2
      return 1
    fi
    if curl --fail --silent --show-error "${url}" >/dev/null 2>&1; then
      return
    fi
    attempt=$((attempt + 1))
    sleep 0.25
  done

  printf '%s did not become healthy at %s.\n' "${name}" "${url}" >&2
  return 1
}

monitor_services() {
  local pid
  local status

  while :; do
    for pid in "${api_pid}" "${worker_pid}" "${web_pid}"; do
      if ! kill -0 "${pid}" >/dev/null 2>&1; then
        status=0
        wait "${pid}" || status=$?
        return "${status}"
      fi
    done
    sleep 1
  done
}

mode="${1:-start}"
case "${mode}" in
  --help|-h)
    usage
    exit 0
    ;;
  --check)
    preflight
    ensure_database
    print_configuration
    exit 0
    ;;
  start)
    [ "$#" -eq 0 ] || fail "unexpected arguments; run with --help for usage"
    ;;
  *)
    fail "unknown option '$1'; run with --help for usage"
    ;;
esac

preflight
ensure_database
ensure_web_dependencies
ensure_port_free API 8080
ensure_port_free worker 8081
ensure_port_free web 5173

rm -rf "${RUN_DIR}"
mkdir -p "${LOG_DIR}"
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

printf 'Building Go services...\n'
go build -o "${RUN_DIR}/relythra-api" "${ROOT_DIR}/cmd/relythra-api"
go build -o "${RUN_DIR}/relythra-worker" "${ROOT_DIR}/cmd/relythra-worker"
go build -o "${RUN_DIR}/relythra-migrate" "${ROOT_DIR}/cmd/relythra-migrate"

printf 'Applying PostgreSQL migrations...\n'
"${RUN_DIR}/relythra-migrate" up

"${RUN_DIR}/relythra-api" >"${LOG_DIR}/api.log" 2>&1 &
api_pid=$!
"${RUN_DIR}/relythra-worker" >"${LOG_DIR}/worker.log" 2>&1 &
worker_pid=$!
(
  cd "${ROOT_DIR}/web"
  exec node node_modules/vite/bin/vite.js --host 127.0.0.1 --port 5173
) >"${LOG_DIR}/web.log" 2>&1 &
web_pid=$!

if ! wait_for_service API "${API_URL}/health/ready" "${api_pid}" ||
  ! wait_for_service worker "${WORKER_URL}/metrics" "${worker_pid}" ||
  ! wait_for_service web "${WEB_URL}/" "${web_pid}"; then
  show_logs
  exit 1
fi

printf '\nRelythra is ready.\n'
printf 'Web:            %s\n' "${WEB_URL}"
printf 'API readiness:  %s/health/ready\n' "${API_URL}"
printf 'Worker metrics: %s/metrics\n' "${WORKER_URL}"
printf 'Logs:           %s\n' "${LOG_DIR}"
printf 'Press Ctrl+C to stop all services.\n\n'

monitor_services
show_logs
exit 1
