#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${ROOT_DIR}/deploy/compose/compose.yaml"

: "${RELYTHRA_POSTGRES_PASSWORD:?Set RELYTHRA_POSTGRES_PASSWORD before running smoke tests}"
export RELYTHRA_POSTGRES_PASSWORD
export RELYTHRA_API_PORT="${RELYTHRA_API_PORT:-18080}"
export RELYTHRA_WORKER_PORT="${RELYTHRA_WORKER_PORT:-18081}"
export RELYTHRA_WEB_PORT="${RELYTHRA_WEB_PORT:-13000}"

COMPOSE=(docker compose --project-name relythra-smoke --file "${COMPOSE_FILE}")

cleanup() {
  "${COMPOSE[@]}" down --volumes --remove-orphans
}
trap cleanup EXIT

"${COMPOSE[@]}" config --quiet
"${COMPOSE[@]}" up --build --wait --wait-timeout 180

# A second run must be a successful no-op after the empty-database migration.
"${COMPOSE[@]}" run --rm --no-deps migrate up

curl --fail --silent --show-error "http://localhost:${RELYTHRA_API_PORT}/health/live" \
  | grep --fixed-strings '"status":"ok"'
curl --fail --silent --show-error "http://localhost:${RELYTHRA_API_PORT}/health/ready" \
  | grep --fixed-strings '"status":"ready"'
curl --fail --silent --show-error "http://localhost:${RELYTHRA_WORKER_PORT}/metrics" \
  | grep --fixed-strings 'relythra_worker_heartbeat_timestamp_seconds'
curl --fail --silent --show-error "http://localhost:${RELYTHRA_WEB_PORT}/" \
  | grep --fixed-strings '<title>Relythra</title>'

printf 'Smoke checks passed: migrations, API, worker, and web are reachable.\n'
