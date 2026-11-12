# Host composition contract

## Current ordinary v4 route

Use `assistant.GoalProtocol` with an immutable published `discussion-goal.yaml`
selector and matching `RecipeCommit`. `Config.Root` identifies the registered
canonical `CellName` and `CellRepo`; `RepositorySource` supplies its Git execution
source. The facade uses existing Goals, History, Control, Story, Input and
Submitter collaborators. `Authorize` must enforce actual tenant/actor/job access
before reading bytes or acting. `ResolveCell` supplies current registered target
identity. These are trusted application bindings, never browser/model fields.

No assistant activity registration, custom runner or second remote connection is
needed. Construct the host's ordinary c2j worker with consistent allocation,
`StageExecution` and `WrapTaskWorker`, then start its single owned engine runner.
Preserve tenant, source/workspace/secret and allocation constraints. Drain HTTP,
cancel/join execution, then close owned resources. Clean up partial startup;
externally owned resources remain the caller's responsibility. Constructing a
Client and calling handlers starts no runner and mutates no operation registry.
The external-package consumer test exercises queued ordinary submission/read,
an unavailable submit adapter, unchanged global operations and a completed
unrelated ordinary recipe on the caller-owned SQLite runtime/engine.

Read-only replay still requires the public Host collaborators. Supply existing
read services, fail-closed mutation adapters and route authorization. An
unavailable `Submitter` can return `ErrUnavailable` without blocking retained
reads. It alone does not deny Close or Respond: the host must also enforce its
read-only policy at HTTP/control/input boundaries. Never infer execution authority
from the existence of a read client.

## Trusted capture and bindings

The request boundary supplies a `FrozenSnapshot` and independent
`SnapshotSelection`. Bind tenant, exact story/review request, capture time,
selected document IDs, original producer job/task, stored references and captured
byte hashes. Never derive the trusted selection from the browser/model payload
being checked. `DecodeFrozenSnapshot` performs closed validation without source
lookup. Global snapshots contain no page documents. Replaced review identities,
forged producers/references and unselected documents fail validation.

Snapshots allow at most 32 documents, 32 KiB of UTF-8 text per source document and
64 KiB encoded JSON overall, including escaping. Each document records capture
provenance and included, truncated, denied, unavailable, unsupported or
budget-omitted outcome. Nonread outcomes carry no captured text/hash.
`SnapshotPrompt` includes whole documents in stable ID order within the encoded
budget. No URL-only reference proves bytes were read. Cortex owns actual capture
at its request boundary and later HTTP/browser integration.

`OrdinaryBindings` supplies endpoint, named secret reference, provider and model.
The includable `inference.yaml` forwards them through declared inputs to immutable
c2ops `6396e1fe6462084e85ea44d86d372bdf3b133db0#pydantic`. Existing caller authority
for the secret is required; Cortex must not fetch or submit plaintext. Missing,
empty or invalid bindings fail before provider calls. Browser, prompt, evidence
and model output cannot supply routing, selectors, credentials or limits.
The leaf allows a 30-second overall inference deadline inside a 45-second task
deadline; package realization and startup require additional runtime headroom.
The effective modules are c2j `v0.0.125-0.20261111181458-c2193c57d890` and JobDB
`v0.0.81-0.20261110034917-7f6133791ec9`.

The runtime owns resolution of secret references. Source tests audit retained
inputs, attempts, results and readable IO artifact references for synthetic
plaintext, and scan exported diagnostics. These are bounded audits, not a claim
about every external service/process log. Treat runtime storage, configuration,
transport and access as privileged even when exported results contain no key.

## Frozen operations and sequential lifecycle

`PrepareStartFrozen` returns complete authenticated `FrozenStart` bytes. Retain
that operation with the logical request before calling `SubmitFrozenStart`.
Retry it unchanged after uncertain transport, including host replacement. Keep
`CursorKey` stable so the caller and integrity can be verified. Named secret
references do not retain future authority. `StartFrozen` is a convenience for
callers without a durable prepublication step. The authoritative receipt's goal
ID identifies the v4 session; retain its execution job ID too.

Close targets the captured execution job ID, including delayed requests. Inspect
its result until terminal before preparing sequential reopen. Cancellation does
not roll back accepted external work or prove a provider request stopped.
`PrepareReopen` accepts that job, logical request ID and frozen timestamp; retain
and submit its returned operation unchanged. Same-goal selection uses existing
host authority, with no fallback goal. Succeeded or explicitly failed goals cannot
reopen. Competing simultaneous reopen clients are excluded.

The fresh v4 job reads retained predecessors and waits for new input without
recapturing a page or replaying opening inference, committed replies or dispatch.
Original task/attempt/receipt/artifact origins remain inspectable. Missing records
return unavailable. There is no second transcript, coordinator or session store.
V4 `finish` returns to waiting; explicit close is captured-job cancellation.
`Reopen` is the historical restart API; use prepare/submit for v4.

Freeze complete request/submission IDs and command/attachment bytes for Respond.
Retries reconcile the authoritative occurrence and actor. Changed contents
conflict; new submissions to an old prompt are stale. V4 accepts up to four bound
attachments, at most 16 KiB each and 64 KiB combined with generated documents.
UTF-8 plain text, Markdown and valid JSON may enter the bounded prompt. Other
media is explicitly unsupported for model reading and remains downloadable.
OpenArtifact authorizes the original producer; close every returned reader.
Missing bytes remain unavailable, while retained metadata stays inspectable.

`OrdinaryLimits` bounds human text at 16 KiB, assistant text32KiB, snapshot64KiB,
raw model field128KiB, model prompt96KiB and encoded operation input/upload/page
at192KiB. V4 human text also fits the JSON string-content bound. Runtime chapter
headroom is64KiB below the default256KiB. Paging is per-page, not a total-history
cap. Discovery exhausts backend pages and does not promise constant memory or
bounded whole-history cost. Stable-watermark cursors bind authorized tenant,
session, execution and retained lineage; refresh after continuation.

## Exact confirmed backend dispatch

The v4 helper resolves the proposed existing cell through its task-scoped broker.
Root's accepted policy treats registered canonical resolution as eligibility;
submission authority is still enforced by the broker. The current form carries
canonical name, immutable CellID, repository and full prompt. Human confirmation
must match the exact current ID/hash. Host ResolveCell rechecks current identity;
the helper checks again before the standard `recipes.run` task submits a separate
backend goal. Repository strings alone do not establish eligibility.

HTTP confirmation retries reuse request/submission IDs and return the original
input receipt. c2j owns the dispatch task's retained inputs, logical invocation,
destination and task recovery. Do not add a recipe-level retry: a new invocation
can allocate another child. A restart creates a new parent and can likewise
allocate a new destination. Reopening the discussion consumes the confirmed
proposal even when submission outcome is unavailable and never resubmits it.

After terminal failure, replay performs read-only typed child listing by original
tenant, parent job and SHA-256 of the exact logical invocation. It follows active
and archived pages, verifies a unique canonical child and persisted StartJob
against the recorded operation and confirmation, and authorizes child access.
The original failed status/error stays; `recovery: accepted`, `destination` and
`destination_goal` establish submission acceptance, not backend completion.
Missing, denied, ambiguous or mismatched records yield `recovery: unavailable`
without submitting another job.

Retained task input is a reconstruction contract, not an unconditional serialized
backend request snapshot. Live lookup/configuration, Git profile/credentials and
authorization may change. Mutable Git refs do not prove immutable backend source
bytes. Literal unchanged dispatch inputs and complete host FrozenStart descriptors
are separately tested. No new c2j capability or permission is introduced.

## Compatibility and release evidence

Explicit v1/v2/v3 `LegacyReleases` preserve their own immutable selectors,
protocols, pending tasks and identities. V3 `discussion.yaml` remains read-only
for reopen and rejects attachments as before. Frozen v2 uses `session.yaml` and
its original `cortex_assistant:run` host activity. For that historical route only,
construct `NewActivity` with trusted provider/model and execution authorization,
then `RegisterActivity` before constructing the ordinary WorkSet. Registration
installs only its exact fail-closed metadata definition; unrelated activities are
preserved. See the README's frozen v2 reference for its larger historical limits
and original model selector. The pre-existing v1 executable deprecation remains;
no new migration or deprecation is authorized.

The mandatory build verification hook resolves the exact managed candidate commit C
and its full tree through the ordinary worker, helper, input runtime and durable
SQLite storage. Canonical assistant selectors end in C. A supported tenant Git
profile maps their transport to a read-only local HTTP mirror of that same commit;
no substitute assistant commit is created. Full-tree blob/mode hashes and resolved
recipe artifacts identify the tested source, including helper manifests and modules.

The unchanged pinned c2ops package executes against deterministic HTTPS structured
provider responses. Its existing `NIX_SSL_CERT_FILE` trust mechanism selects an
ephemeral fixture CA only in the test subprocess; certificate and hostname checking
remain enabled. The hook uses no `--fixture-loopback`, manifest replacement or
inference-result interception. Each required case exports actual request bodies,
validated results, task/attempt/receipt origins and download hashes in a separate
directory. Missing evidence, required skips and identity/integrity failures fail the
hook. Historical source-only tests outside this hook retain their documented HTTP
fixture-manifest boundary and do not certify candidate acceptance.

This proves candidate behavior and byte provenance before assessment. It does not
prove public fetchability, the eventual squash/merge hash, live-provider behavior
or deployment. Public assistant publication is no longer a separate pre-completion
gate. Cortex browser/deployment acceptance belongs to its unchanged later assignment.
