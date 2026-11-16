# Repository verification

Historical selection baseline: cortex-assistant main `d2c4a33352f3f0e888b172946fca69caffd573df` contained only `PURPOSE.md`. The candidate now includes the standalone Go module, registered activity, session recipe and native recipe tests. The coverage mapping has been refreshed for that implementation. The explicit `APPLICATION_FILES` inventory in verify.py identifies reviewed paths; unfamiliar tracked files and incomplete application inventories still fail for coverage review. No hosted CI configuration is present.

Documentation-only disposable fixtures retain their existing checks. A candidate containing the reviewed application inventory additionally runs Go formatting, vet, the full Go test suite, all current native session, contextual goal, policy and secret-include cases through the module's pinned c2j CLI, and the Python verifier suite (including actual shared-wrapper integration). These checks cover the facade/activity and recorded worker/input/restart/tenancy/model-adaptation contracts exercised by those tests; they are not production deployment certification.


## Sequential discussion coverage

The explicit inventory includes the reviewed goal lifecycle and terminal-dispatch files. The full Go suite executes these checks; the inventory remains closed and unfamiliar files still require review.

| Reviewed files | Required coverage |
| --- | --- |
| `assistant/dispatch_discovery.go`, `assistant/dispatch_discovery_test.go`, `assistant/dispatch_recovery_integration_test.go` | DAS-013/017: original invocation and authorized child discovery after terminal failure, rejected mismatches, lost-response and worker-replacement identity recovery. |
| `assistant/ordinary_dispatch.go`, `assistant/ordinary_dispatch_test.go` | DAS-003/005/017/018: independent model bounds, exact canonical proposal and confirmation, consumed proposals and ordinary dispatch. |
| `assistant/lifecycle_race_integration_test.go` | DAS-012/015: send/close uncertainty and cancellation/completion races with authoritative receipts. |
| `assistant/ordinary_boundaries_test.go` | DAS-003/018: authority rejection and exact encoded model, snapshot and page boundaries. |
| `assistant/ordinary_guard_integration_test.go` | DAS-002–006: resolved binding, provider and output rejection matrix and bounded secret audits. |
| `assistant/retention_integration_test.go` | DAS-011/013/014/015: actual SQLite chapter/blob deletion and unavailable replay, downloads and reopen. |
| `assistant/source_capture_integration_test.go` | DAS-007–010: original child text/JSON in an exact review request, frozen snapshot and actual resolved model request. |
| `assistant/consumer_integration_test.go` | DAS-019: public consumer composition, durable submission, read-only replay and caller-owned resources. |

The required hook additionally binds all required ordinary modes and four local/remote fixture conversations to the exact hook candidate C. It creates an external mirror and clean checkout with C and its complete Git tree, completing shallow ancestry in scratch when needed. It compares every tracked Git blob, mode and tree, including recipes, helper manifests/executables, modules and the verifier. Canonical submitted assistant selectors end in C and their actual resolution tasks must return C. A supported tenant Git profile supplies read-only local HTTP source transport; this does not claim public publication or predict the eventual merge hash. No synthetic assistant commit or selected-file snapshot supplies candidate identity.

The immutable c2ops inference selector remains `nix:github:colony-2/c2ops/6396e1fe6462084e85ea44d86d372bdf3b133db0#pydantic`. The actual package manifest, launcher and payload are checked before/after. The required ordinary route uses HTTPS responses with child-only `NIX_SSL_CERT_FILE`, certificate/hostname verification, and an explicit synthetic named secret. No fixture flag, source/manifest rewrite or result interception is used. The untrusted-CA case must fail without reaching the provider application; positive cases require a real request and validated response.

One full Go suite exports unique case directories. The verifier rejects required skips, missing passes/artifacts, caught positive provider errors, wrong identities, changed bytes and timeouts. The full Go event log, candidate manifest, case index, resolved recipes, request bodies, origin/receipt/download evidence and native results remain individual stored artifacts. The hook returns at most five `artifact_refs`: verification evidence, available human/case/candidate indexes, and a JSON value artifact named `artifact-catalog.json` containing every evidence file's stored reference. This keeps the hook below the shared wrapper's 64-property limit without dropping case evidence or archiving it. Runtime-reserved Git state/diff artifacts are excluded from both the catalog and hook outputs. Real shared-wrapper regressions exercise 148 evidence files with reserved names for both passing and failing verification. CA-001 remains conditional on compatibility regressions; CA-002 closes only after the integrated exact-candidate hook passes. See `docs/test-statements.md` for the approved DAS-001/020 amendment.

## Destination grammar (defined before implementation)

Supported inline links are `[label](destination)` and images `![label](destination)`. Labels are nonempty, single-line, without brackets or escapes. Destinations are nonempty bare tokens without whitespace, parentheses, quotes, angle brackets or escapes. Percent-encoded path characters are decoded before filesystem containment checking. Local fragments/queries do not change the target existence check; fragment contents are not validated. URL schemes and protocol-relative URLs are external and never fetched; fragment-only destinations are reported unchecked. Empty, malformed, reference, shortcut, escaped or nested bracket forms and complex destinations fail as unsupported. Markdown HTML links/autolinks, including email autolinks, are unsupported. Escaped bracket/image forms are rejected before supported links are consumed. Fenced code blocks and inline backtick code are literal content, excluded from destination checks. Unclosed code delimiters fail rather than hiding remaining content. This intentionally conservative grammar is not full Markdown conformance.

## Title and prose content

Optional ATX closing hashes are removed before deciding whether a top-level title is nonempty. HTML comments do not supply rendered title or prose content. Comment-only, heading-only, indented-code-only and thematic-break-only bodies cannot satisfy the prose requirement. Remaining HTML blocks fail explicitly as unsupported; unclosed comments fail rather than hiding remaining content. Ordinary prose and nonempty headings with closing hashes remain valid.

## Operation

Git, Python 3, Nix and OpenSSL (for the unrelated-authority negative fixture) are required. The hook declares the existing pinned Nix Go package through execution_needs so Go and gofmt are available without a worker-specific assistant configuration. The pinned c2j CLI is run through `go run` for native recipe cases. Go module/tool downloads and the wrapper integration's read-only recipes checkout require network/cache availability; failures are recorded and fail verification.

The runner uses bounded subprocesses, captures original outcomes, and writes complete logs inside `verification-evidence.json` in operation outbox storage. It checks managed root/target containment, candidate identity, and pre/post HEAD, index bytes, staged entries and tracked content. It never repairs mutation. Test fixtures and native result storage are outside the managed repository. Go formatting reports differences without rewriting files. Missing tools, timeouts, nonzero checks and artifact failures cannot become success. No secrets or environment dumps are logged. Git blob output is represented by object identity, byte count and SHA-256 rather than copying source fixtures into diagnostics.

Application command budgets are 300 seconds for vet, 4200 for the full Go suite, 900 for sequential native cases and 300 for Python tests; the enclosing hook allows 100 minutes including preparation. The native command budget accommodates the controlled cancellation cases' observed lease-invalidation waits; their individual 60-second deadlines remain unchanged. Go tests preserve the existing opt-in external-provider behavior: the ordinary suite does not claim actual-provider acceptance when that test skips. Actual provider evidence remains separately bound to its declared candidate. Public assistant fetchability, Cortex browser integration, hosted runner/cache/platform matrices and publication/deployment are outside this candidate guarantee.

Run `python3 -m unittest discover -s tests -v` for verifier regressions. The suite preserves document grammar, identity/containment, mutation, exit/timeout/artifact checks and actual wrapper integration on disposable fixtures. Added application cases verify inventory completeness, check selection, formatting failures and failure propagation from Go/vet/native/hook stages. Set `VERIFY_TEST_EVIDENCE` to an external directory to retain wrapper integration results. No existing native recipe case or application expectation is removed.

The validated worker package process now supplies `.c2/config.yaml` with the same pinned Go package as this hook. This known tooling configuration is allowed by the coverage inventory; unfamiliar candidate paths still fail. The selected native CLI is c2j `v0.0.127-0.20261113193326-d031eb49c677`, matching the module.

Retained large-history worker validation has a ten-minute context budget. Its earlier five-minute budget expired in the verification worker during replay, although the same assertions completed locally. The hook explicitly gives the Go test process sixty minutes per package inside a seventy-minute subprocess budget. The history size, inline/offloaded restoration, exact stable-watermark page comparisons, append, finish, reopen and inference-count expectations remain unchanged; these budgets are test execution bounds, not product latency requirements.


Automatic-context verification additionally requires execution-time configuration
snapshots, completed-task replay with settings removed and secrets disabled,
fixture byte/schema/dependency drift rejection, external-package examples and
explicit resource ownership. Consumer runs use a distinct committed fixture F,
resolved through their disposable Git profile, while production candidate checks
retain C and the real inference selector. All accepted Send turns have their own
helper/input/form/replay proof. The final citation-only mutation must reach the
real validation failure and its exact missing-valid-reply error; generic errors
cannot satisfy it. Required support tests and each native case are inventoried.
Go packages and native cases execute sequentially to avoid overlapping ordinary-runtime fixtures. All verifier children use `GOMAXPROCS=2` and `GOFLAGS=-p=1`, bounding Go runtime threads and nested helper builds as well as the outer test process. This replaces the native CLI's default four concurrent cases; cancellation, dispatch drain and final-state assertions remain required. A child-process regression checks these bounds without changing the parent's environment.

The contextual suite adds independent turn inspection, a separate final-citation mutation and local/remote consumer runs. The earlier fourteen-minute package budget expired after the large-history test passed in 339 seconds. The larger total suite budget preserves individual case bounds and all behavior assertions.

The assessed candidate exhausted its cumulative twenty-minute assistant package budget while the large-history case was still progressing. The sixty-minute package budget leaves room for all required later cases; individual case contexts and assertions stay intact. V3 turn checks use the session ID returned by Start (v4 still returns the goal ID). Native policy cases also require exact accepted HTTPS endpoints of 4,124 and 20,024 characters, crossing both former extra limits without changing the separate model bound.

The bounded helper builds and sequential native execution trade peak process demand for longer cumulative execution. An exact-source run used more than twenty minutes before final mutation and large-history/later lifecycle checks. The cumulative Go/package/hook bounds are now 70/60/100 minutes so required later checks can finish; the facade runtime fixture alone now has four rather than two minutes after a measured completion timeout; other individual contexts, history sizes and assertions remain unchanged.

The immutable inference deadline fixture supplies `retry-after-ms: 1` on its first real 503 response. This exercises the SDK retry and independent schema-repair request inside the existing two-second invocation deadline without randomized backoff consuming the fixture window. It still requires at least three actual provider requests, exact `pydantic: timeout`, empty stdout and the existing elapsed upper bound. No inference package bytes change.
