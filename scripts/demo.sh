#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
COMPOSE=(docker compose --project-name "${RELYTHRA_DEMO_PROJECT:-relythra-demo}" --env-file "${RELYTHRA_DEMO_ENV_FILE:-.env}" --file deploy/compose/compose.yaml --file deploy/compose/demo.yaml)
case "${1:-up}" in
  up)
    "${COMPOSE[@]}" config --quiet
    if ! "${COMPOSE[@]}" up --build --wait --wait-timeout 240; then
      printf 'Demo startup failed; inspect the specific unhealthy dependency below. No volumes were reset.\n' >&2
      "${COMPOSE[@]}" ps --all
      exit 1
    fi
    printf 'Demo ready. Orders :18090, Grafana :13001; overridden ports are listed by status.\n'
    ;;
  status) "${COMPOSE[@]}" ps --all ;;
  down) "${COMPOSE[@]}" down --remove-orphans ;;
  *) printf 'Usage: %s [up|status|down]\n' "$0" >&2; exit 2 ;;
esac
