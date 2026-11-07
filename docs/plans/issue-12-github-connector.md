# Issue #12 GitHub Connector Implementation Plan

**Goal**: Read commit and pull request evidence for the exact revision recorded by a verified deployment event, with bounded diff and file content and traceable source metadata.

**Architecture**: Add a read-only `internal/connector/github` client using the existing outbound policy and SecretRef resolver. The connector accepts a deployment-sourced revision, reads commit metadata and changed files, resolves associated pull requests, optionally fetches a bounded unified diff and a small allowlist of dependency/configuration files, and returns redacted `untrusted_data` text with source URLs and response validators. API requests use fixed paths and GitHub REST API version headers; no GraphQL or caller-provided URL/path/query is accepted.

**Requirements**: Issue #12, Proposal #1, ADR 0003/0004 and repository `AGENTS.md`.

## Constraints

- Do not fall back to the repository's default branch when the requested commit is missing or inaccessible.
- Do not accept arbitrary repository names, paths, HTTP methods, headers, GitHub API query text, or caller-provided URLs.
- Validate the GitHub owner/repository and full 40-character commit SHA before network access.
- Require deployment event provenance in the connector request; any no-deployment reference query must be explicitly marked as reference-only by its caller and cannot be reported as deployed evidence.
- Use the outbound allowlist, DNS pinning, timeout and redirect blocking; resolve credentials only from SecretRef and send them in the authorization header.
- Bound file count, total diff bytes, individual patch bytes, aggregate patch bytes (1 MiB), and dependency/config file bytes; allocate patch budget in sorted path order and expose truncation and omission metadata.
- Redact and mark commit messages, patch text and fetched file content as `untrusted_data`; keep credentials and raw API error bodies out of results and logs.
- Treat missing PRs, inaccessible revisions, rate limits and oversized responses as distinct, inspectable outcomes.

## Task 1: Define the bounded result contract

**Files**: new `internal/connector/github/client.go` and `client_test.go`.

- Define deployment revision input, target mapping, file/patch/PR result types, explicit error categories and safe metadata fields.
- Enforce repository, SHA and budget validation before HTTP calls.
- Add failing tests for invalid/unverified revisions, repository formats, budgets, request paths/headers and no-network validation.

## Task 2: Fetch the exact commit and associated PRs

**Files**: `internal/connector/github/client.go`, `client_test.go`.

- Fetch commit metadata and changed-file summaries by immutable SHA; retain API response ETag and source reference.
- Query associated PRs by commit, tolerate no PR as a data gap, and return only bounded PR metadata and source URLs.
- Classify 401/403 permission failures, 404 revision-unavailable, 429/secondary rate limiting with bounded retry metadata, timeout and malformed responses without echoing remote bodies.

## Task 3: Bound and redact patch/dependency evidence

**Files**: `internal/connector/github/client.go`, `client_test.go`.

- Fetch the commit diff with a fixed endpoint and an explicit byte cap; retain a deterministic changed-file inventory even when diff bytes are truncated.
- Fetch only registered dependency/configuration files at the requested SHA, never the default branch, with per-file and total byte caps. Sort changed paths before applying file and patch budgets so selection is stable across API response ordering.
- Apply shared redaction, UTF-8-safe clipping and `untrusted_data` tagging before returning text. Preserve omitted/truncated file metadata and validators.

## Validation

- Add `httptest` coverage for commit-only results, PR associations, missing PR, merge/squash commit behavior, inaccessible SHA, permissions, rate limit/retry metadata, response size limits, file/patch limits, dependency allowlist, redaction canaries, untrusted content, cursor-free fixed requests, outbound rejection, redirects and timeouts.
- Run `go test ./internal/connector/github ./internal/security/outbound ./internal/security/secrets ./internal/security/redact`, race tests for the new connector, `make check`, `gitleaks detect --source . --no-git --redact`, and `git diff --check`.
- Use deterministic mock GitHub API responses; do not depend on public API availability or live credentials.

## Risk and rollback

Risk is standard: the connector is read-only and introduces no migration or write path, but diffs and manifests may contain secrets or prompt-injection text. Rollback is a code revert. No downloaded repository code is executed.
