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
"${COMPOSE[@]}" exec -T postgres \
  psql --set=ON_ERROR_STOP=1 --username relythra --dbname relythra \
  < "${ROOT_DIR}/scripts/smoke_domain.sql"
curl --fail --silent --show-error \
  "http://localhost:${RELYTHRA_API_PORT}/api/v1/reports/10000000-0000-4000-8000-000000000011" \
  | grep --fixed-strings '"summary":"Deployment introduced a slow query"'
curl --fail --silent --show-error \
  "http://localhost:${RELYTHRA_API_PORT}/api/v1/evidence/10000000-0000-4000-8000-000000000009" \
  | grep --fixed-strings '"source_type":"PROMETHEUS"'
curl --fail --silent --show-error \
  "http://localhost:${RELYTHRA_API_PORT}/api/v1/deployments/10000000-0000-4000-8000-000000000004" \
  | grep --fixed-strings '"source_time_text":"2026-11-04T13:00:00+08:00"'
curl --fail --silent --show-error "http://localhost:${RELYTHRA_WORKER_PORT}/metrics" \
  | grep --fixed-strings 'relythra_worker_heartbeat_timestamp_seconds'
curl --fail --silent --show-error "http://localhost:${RELYTHRA_WEB_PORT}/" \
  | grep --fixed-strings '<title>Relythra</title>'

# Losing PostgreSQL must fail readiness without taking down the API process.
"${COMPOSE[@]}" stop postgres
readiness_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  "http://localhost:${RELYTHRA_API_PORT}/health/ready")"
test "${readiness_status}" = "503"
curl --silent --show-error "http://localhost:${RELYTHRA_API_PORT}/health/ready" \
  | grep --fixed-strings '"error":"dependency unavailable"'
curl --fail --silent --show-error "http://localhost:${RELYTHRA_API_PORT}/health/live" \
  | grep --fixed-strings '"status":"ok"'
"${COMPOSE[@]}" logs --no-color api | grep --fixed-strings 'readiness check failed'

printf 'Smoke checks passed: migrations, domain constraints, traceable API reads, healthy services, and database-loss behavior.\n'
