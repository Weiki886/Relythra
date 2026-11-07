# Issue #11 Loki Connector Implementation Plan

**Goal**: Execute bounded Loki log queries through registered LogQL templates and return redacted, time-normalized, traceable entries with explicit pagination and partial-result semantics.

**Architecture**: Extend the datasource query contract with Loki-only `environment`, `direction`, and `severity` parameters. The Loki connector builds LogQL exclusively from a fixed template catalog, uses the shared outbound policy and SecretRef resolver, reads `/loki/api/v1/query_range` with a bounded response body, and redacts each log line before returning it as `untrusted_data`. Loki has no stable server-side cursor; pagination re-runs the same bounded query and uses a cursor bound to the query and response digests, rejecting changed result sets as expired.

**Requirements**: Issue #11, Proposal #1, ADR 0003/0004/0005 and repository `AGENTS.md`.

## Constraints

- Do not accept caller-provided LogQL, URL paths, HTTP methods, headers, or arbitrary labels.
- Use fixed templates for service errors, slow SQL, container/runtime events, and configuration errors.
- Require exact validated service/environment selectors and constrain time range, direction, severity, page size, and response bytes before network access.
- Keep credentials out of URLs, results, errors, cursors, and logs; use outbound allowlist/DNS pinning and block redirects.
- Preserve the source timestamp text and normalized UTC timestamp; mark excessive timestamp drift.
- Redact each log line and expose it only as `trust=untrusted_data`; fail closed if redaction fails.
- Treat empty, partial, timed-out, malformed, and unauthorized results as distinct outcomes; never turn failure into an empty/healthy result.

## Task 1: Validate Loki Query Plans

**Files**: `internal/datasource/registry.go`, `internal/datasource/registry_test.go`, `internal/api/mapping_handler.go`, `internal/api/mapping_handler_test.go`.

- Define Loki-specific allowed parameters and normalization for `environment`, `direction` (`backward`/`forward`), `severity`, bounded `limit`, and the shared time/cursor fields.
- Keep Prometheus-only defaults and all non-Loki query behavior unchanged.
- Add tests for valid defaults, invalid selector/direction/severity/window/cursor, and rejection of raw LogQL.
- Verify the management query-plan endpoint returns normalized Loki values.

## Task 2: Execute and Normalize Loki Results

**Files**: new `internal/connector/loki/client.go` and `client_test.go`.

- Build fixed selectors for `loki.service.errors.v1`, `loki.service.slow_queries.v1`, `loki.container.restarts.v1`, and `loki.service.config_errors.v1`; use JSON-safe string quoting for validated values and fixed severity filters.
- POST only to `/loki/api/v1/query_range`; include nanosecond `start`/`end`, direction, and a finite maximum entry count.
- Parse only Loki `streams` responses; validate label and entry counts, preserve original timestamps, normalize to UTC, and sort deterministically by timestamp and stable tie-breakers.
- Redact then UTF-8 truncate line content; represent it as `redact.UntrustedData` with original byte count and truncation flag.
- Return template/query metadata, duration, source reference, response digest, warnings, empty/partial state, time-skew marker, entries, and a bounded opaque continuation cursor.
- Cursor schema binds template, selectors, time range, filters, page size, and response digest. Reject malformed cursors, request mismatches, changed result digests, and out-of-range offsets with stable categories.

## Validation

- Run each new datasource/API/connector test before implementation and record the expected behavior-missing failure.
- Use `httptest` to cover fixed query expressions, no raw-query execution, auth secrecy, outbound-policy rejection, redirects, timeout/body read errors, 401/403/429/5xx, invalid JSON/status, byte/entry/label limits, malformed streams, sorting, timestamp drift, empty and partial results, redaction canaries, UTF-8 clipping, prompt-injection content trust marking, and cursor round-trips/mismatch/expiry.
- Run `go test ./internal/datasource ./internal/api ./internal/connector/loki ./internal/security/redact`, then `make check`, `gitleaks detect --source . --no-git --redact`, and `git diff --check`.
- Run the existing `make demo-smoke` only if Docker is available and ensure it uses its isolated project, ports, secrets, and volumes.

## Risk and rollback

Risk is standard: the connector is read-only and has no schema migration, but it handles sensitive and untrusted log content. Rollback is a code revert. This issue does not persist Evidence, start investigations, or add an arbitrary LogQL endpoint.
