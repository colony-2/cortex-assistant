# Assistant source acceptance

Each expectation contains at most 30 words. IDs are stable. Filenames are relative to the repository and may extend existing suites. All statements are required; P0 identifies critical behavior, P1 supporting compatibility/documentation. Dependencies: R = embedded c2j/input/JobDB with durable storage and ordinary worker; M = verified immutable c2ops explicit-binding package; F = deterministic structured HTTPS provider fixtures with subprocess-scoped test CA. Deployment and Cortex HTTP/browser acceptance are outside this assignment.

## DAS-001

The candidate recipe should execute capture, inference, independent validation and prompt collection through ordinary resolved operations without custom registration or a Cortex runner.

Annotations: `assistant/worker_integration_test.go`, `session.yaml`; P0; integration; dependencies: R, M, F.

## DAS-002

Declared endpoint, secret, provider and model inputs should propagate through supported includes with documented caller authority; missing, empty or invalid bindings should cause zero provider requests.

Annotations: `assistant/provider_integration_test.go`, `session.yaml`; P0; integration; dependencies: R, M, F.

## DAS-003

Browser, prompt, evidence and model output should never override routing, credentials, selectors or limits; ambient keys, endpoints and proxies should not supply missing bindings.

Annotations: `assistant/provider_integration_test.go`; P0; integration; dependencies: DAS-002, F.

## DAS-004

Resolved inference should preserve exact API roots, reject redirects, isolate concurrent clients and leave environment unchanged while enforcing an overall deadline across retries and repair.

Annotations: `assistant/provider_integration_test.go`; P0; integration; dependencies: M, F, c2ops package evidence.

## DAS-005

Actual structured responses should satisfy independent closed assistant-schema and citation validation; malformed output, unknown citations and executable tool requests should produce bounded safe errors.

Annotations: `assistant/respond_errors_test.go`, `assistant/provider_integration_test.go`; P0; integration; dependencies: R, M, F.

## DAS-006

Synthetic credentials should remain absent from published results and diagnostics across success, failure and retry; audits should identify runtime-managed secret retention and its consequences.

Annotations: `assistant/provider_integration_test.go`; P0; integration; dependencies: DAS-002, retained histories/logs, synthetic secrets.

## DAS-007

Global sessions should capture no page evidence; opted-in story and exact-review snapshots should preserve tenant, request, document and original child producer identities.

Annotations: `assistant/assistant_test.go`, `assistant/worker_integration_test.go`; P0; integration; dependencies: R, actual review-form fixtures.

## DAS-008

Replaced review requests and forged snapshot references should fail without substituting context; unselected sources, arbitrary URLs and recursive children should never enter model input.

Annotations: `assistant/assistant_test.go`; P0; unit; dependencies: trusted capture fixtures, DAS-007.

## DAS-009

Every document should expose capture time, provenance and inclusion outcome, distinguishing denied, unavailable, unsupported, truncated and budget-omitted material from bytes actually read.

Annotations: `assistant/assistant_test.go`, `assistant/worker_integration_test.go`; P0; integration; dependencies: R, multi-document/expired/missing-history fixtures.

## DAS-010

Actual model requests should contain bounded included UTF-8/JSON text beyond 1,000 characters, preserve source identifiers and exclude URL-only evidence from read coverage.

Annotations: `assistant/worker_integration_test.go`; P0; integration; dependencies: R, M, F, multibyte and budget fixtures.

## DAS-011

Materialized documents and supported downloads should preserve recorded references and byte hashes after replacement; missing bytes should report unavailable without unsupported media-understanding claims.

Annotations: `assistant/worker_integration_test.go`, `assistant/uploads.go`; P0; integration; dependencies: R, stored text/image/video/audio fixtures.

## DAS-012

Frozen starts and sends should deduplicate retries; conflicting, stale and simultaneous submissions should preserve authoritative receipts and one recorded reply per accepted prompt occurrence.

Annotations: `assistant/worker_integration_test.go`; P0; integration; dependencies: R, M, F, concurrency fixtures.

## DAS-013

Replay should reconstruct actual task origins, ordered receipts and inspectable failures after reload or worker replacement without a second transcript, context artifact or session database.

Annotations: `assistant/worker_integration_test.go`, `assistant/runtime_restoration_test.go`; P0; integration; dependencies: R, failure/retry fixtures.

## DAS-014

Stable-watermark paging should visit retained entries once across appends; missing history should remain unavailable, while foreign cursors and divergent lineage should conflict.

Annotations: `assistant/assistant_test.go`, `assistant/worker_integration_test.go`; P0; integration; dependencies: R, large-history and lineage fixtures.

## DAS-015

Close should cancel the captured job; sequential reopen should create a fresh same-goal prompt-ready job with retained context, without replaying committed inference or dispatch.

Annotations: `assistant/worker_integration_test.go`; P0; integration; dependencies: R, sequential close/reopen fixtures, DAS-013.

## DAS-016

Explicit legacy descriptors should preserve supported reads and honest reopen availability; new releases should neither migrate pending tasks nor reinterpret frozen execution identities.

Annotations: `assistant/assistant_test.go`, `assistant/releases.go`; P0; unit; dependencies: v1/v2 descriptor fixtures.

## DAS-017

Backend goal dispatch should require current confirmation of the exact canonical eligible target and prompt; stale or forged confirmations should dispatch nothing and retries should reconcile identity.

Annotations: `assistant/guard_integration_test.go`, `assistant/worker_integration_test.go`; P0; integration; dependencies: R, destination submission fixtures.

## DAS-018

Effective encoded limits should respect runtime envelopes with headroom; exact boundaries should pass, excess should fail safely, and large-history input should remain usable through finish/reopen.

Annotations: `assistant/runtime_restoration_test.go`, `assistant/worker_integration_test.go`; P0; integration; dependencies: R, escaping/multibyte/offload fixtures.

## DAS-019

Consumer examples should compile against effective dependencies, use ordinary submission and declared bindings, preserve unrelated operations, and document read-only replay, unavailable states and resource ownership.

Annotations: `README.md`, `docs/hosting.md`, `assistant/consumer_integration_test.go`; P1; integration; dependencies: R, effective Go module graph.

## DAS-020

Candidate-pinned recipe/helper selectors and immutable inference should resolve identical tested bytes through ordinary operations, recording candidate/tree hashes, dependency/package identities, task bindings, transport and provider-fixture boundaries, and results.

Annotations: `assistant/provider_integration_test.go`, `assistant/worker_integration_test.go`; P0; integration; dependencies: R, M, F, immutable release access.

## Required checks

Run `go test ./...` and relevant native recipe cases through the pinned runtime; inspect `tests/test_verify.py` and `tests/verify-wrapper.yaml` to run applicable wrapper checks. Record exact native commands after verifying the installed CLI contract. Do not skip retained-worker, large-history or negative binding checks. Use real runtime/input/storage and resolved helper/model operations; deterministic HTTP responses may replace only the provider. Record actual request bodies, task/attempt/receipt/release bindings and downloaded-byte hashes. Keep synthetic authorization-header fixture data private and distinguish it from published output scans.

R requires compatible installed Go/runtime/storage; M requires the delivered immutable package and its resolver/store/cache-or-realization support. Missing optional environment metadata is not a failure. Missing essential package bytes require an item-specific dependency report, not a mutable replacement. Source acceptance does not depend on live credentials, Pulse deployment, Cortex browser tests, Docker, new user permissions or an incidental tool patch version.


## Lifecycle cases and interpretation

DAS-012 covers simultaneous sends, not competing reopen clients. DAS-015 requires queued, processing and waiting close, delayed close targeting the captured job, and positive sequential reopen after worker replacement. Reopen must await new input without replaying the opening prompt, committed inference or dispatch. Reconcile uncertain close and submit results using the complete frozen operation and original destination. Missing records require explicit unavailable; this does not satisfy positive recovery cases. Permanently succeeded or explicitly failed goals cannot reopen. DAS-013/014 cover authorized assistant-job membership and actual origins across sequential jobs; unrelated recipes are excluded even within the same goal. Preserve historical descriptor semantics.

Use the c2j fix already selected in go.mod, v0.0.125-0.20261111181458-c2193c57d890. Recheck effective dependencies and declared-secret includes. No new c2j feature is assigned. The supplied incremental patch is already applied; see design.md and evidence/step5. The old absent recovery patches are not implementation inputs.

All DAS IDs remain required. Lifecycle changes are a distinct new contract; supported v1/v2 reads and pending executions retain original semantics. No existing product behavior is removed and no migration or deprecation is authorized.


## Candidate verification amendment

DAS-001 and DAS-020 use the actual build-hook candidate C and its full tree. The mandatory hook mirrors exact C outside the managed checkout, routes canonical assistant selectors through the supported tenant Git profile to a read-only HTTP mirror, and verifies source bytes before and after one full Go suite. No synthetic assistant commit supplies the acceptance identity. The unchanged immutable inference package executes over HTTPS with certificate/hostname verification and child-only `NIX_SSL_CERT_FILE`; no fixture command suffix or manifest replacement is used in this route.

Required case skips, missing artifacts, caught provider errors in positive cases, wrong candidate identity, source/package mutation, subprocess failure and timeout fail verification. Each case exports its own actual request/response, task and behavior evidence. CA-001 requires the historical-policy regressions; CA-002 remains open until integrated required candidate hook evidence passes. The hook does not certify public assistant fetchability, eventual squash identity, live-provider behavior, deployment or Cortex browser integration. The approved correction supersedes the prior publication-first prerequisite and adds no new execution or publication job.
