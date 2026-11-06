# Issue #10 Prometheus Connector Implementation Plan

**Goal**: Execute only registered Prometheus metric templates with validated selectors and bounded time/series/sample budgets, returning structured, traceable results that later Evidence storage can consume.

**Architecture**: `datasource.ValidateQuery` validates service, environment, query type, time, step, series and sample limits. `internal/connector/prometheus` maps the registered template ID to a fixed PromQL expression, uses the existing outbound policy and SecretRef resolver, requests `/api/v1/query_range` or `/api/v1/query`, and parses bounded matrix or vector responses. This PR does not persist Evidence or start investigations; it provides the connector contract for subsequent orchestration work.

**Requirements**: Issue #10, Proposal #1, ADR 0003/0004/0005 and repository `AGENTS.md`.

## Constraints

- Do not accept PromQL from users, Agent output, mapping settings, or query parameters.
- Do not treat an empty or stale result as a healthy signal.
- Reject invalid query budgets before network access; never retry without a finite bound.
- Keep credentials out of URLs, results, errors, and logs; use outbound allowlist/DNS pinning and block redirects.
- Limit response bytes, series, per-series points, and total points.

## Task 1: Validate Prometheus Query Budgets

**Files**: `internal/datasource/registry.go`, `internal/datasource/registry_test.go`, `internal/api/mapping_handler.go`, `internal/api/mapping_handler_test.go`.

- Add environment, query_type, at, step and max_points to Prometheus-only template parameters.
- Require a valid environment selector; normalize a bounded duration step and points budget.
- Default to range queries; instant queries require only one RFC3339 `at` timestamp and normalize to one sample per returned series.
- Reject when the requested range cannot fit the requested point budget or the total series × point budget exceeds the global sample ceiling.
- Return these normalized values from the existing planning endpoint; keep other sources' accepted parameters unchanged.

## Task 2: Execute Fixed Templates

**Files**: new `internal/connector/prometheus/client.go` and `client_test.go`.

- Register fixed expressions for latency P95, error ratio, request rate, container CPU, container memory, and database query latency. Selectors use validated service/environment values quoted as PromQL string literals.
- Build a finite-timeout, allowlist-constrained client; inject optional Bearer credentials from `SecretResolver`.
- POST form-encoded read queries to `/api/v1/query_range` or `/api/v1/query`; never interpolate caller-provided expressions.
- Parse only the expected Prometheus `matrix` or `vector` response; enforce byte/series/point bounds and return valid series when individual series are malformed or Prometheus returns warnings, marking the result partial.
- Return query/template metadata, source time range, unit, duration, response SHA-256, warnings, missing/stale/partial status and normalized samples. Empty results remain missing evidence.

## Validation

- Run focused tests first, including zero-network assertions for invalid templates/budgets.
- Test all template expressions, selector escaping, auth secrecy, range and instant paths/forms/responses, timeout, HTTP/API errors, invalid JSON, body/series/point limits, empty results, warning/partial results, malformed individual series and stale samples using `httptest`.
- Run full `make check` with the isolated local PostgreSQL test URL and Compose placeholder secret paths, `gitleaks detect --source . --no-git --redact`, and `git diff --check`.
- Review the stacked diff against `feat/9-grafana-webhook`, commit and open a PR based on that branch.

## Risk and rollback

Risk is standard: all requests are read-only and bounded, and no migration is needed. Rollback is a code revert. No live Compose Prometheus is required by the unit tests; the fixture metrics and fixed expression contracts are aligned with `deploy/demo/prometheus.yaml`.
