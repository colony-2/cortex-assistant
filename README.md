# Cortex assistant

The current source contract is `cortex-assistant/4` (`assistant.GoalProtocol`),
using `discussion-goal.yaml` and the ordinary `ops/discussion` helper on the
existing c2j worker. It supports sequential close/reopen in one open goal,
retained conversation and downloads, concurrent sends, and exact confirmed
backend dispatch. No assistant activity registration or Cortex runner is needed.
Immutable assistant publication and resolution must be verified after source
review; local fixture commits are not published releases.

Configure `assistant.New` with `GoalProtocol`, the published immutable
`git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@<40-hex-commit>`
selector, matching `RecipeCommit`, stable deployment `CursorKey`, and trusted
`OrdinaryBindings`. Bindings require an exact HTTPS API root, `openai`, an
unqualified model and `secrets.Reference{Name: "assistant_api_key"}`. They are
server configuration. The caller needs existing authority for the named secret;
the runtime resolves the opaque reference for execution.

Capture the snapshot and independent `SnapshotSelection` at the trusted request
boundary, including tenant, exact review request and original document producers.
`PrepareStartFrozen` returns the complete authenticated operation. Retain it in
the host's existing request record before `SubmitFrozenStart`; retry the saved
operation unchanged after uncertainty or host replacement. The authoritative
receipt's goal ID is the session ID. `Get`/`Replay` reconstruct retained tasks,
input receipts, source coverage, citations, failures and artifact references.
Missing history or bytes report unavailable.

Close targets the job ID captured by the request. Reconcile terminal cancellation
before `PrepareReopen`, then retain and submit its complete returned operation.
Sequential reopen creates a fresh prompt-ready job in the same open goal without
replaying opening inference, committed replies or backend dispatch. Permanently
succeeded or explicitly failed goals cannot reopen. Competing simultaneous
reopen clients are outside this contract. See the compiled public
[consumer examples](assistant/ordinary_example_test.go) and [hosting contract](docs/hosting.md).

At a prompt, freeze request ID, submission ID and complete command/attachment
bytes together. Send retries return the original authoritative receipt; changed
contents conflict, and new submissions to an old prompt are stale. V4 supports
stored or runtime-uploaded attachments and generated documents. UTF-8 text and
valid JSON may be included within budgets; image/video/audio downloads preserve
opaque bytes, without media-understanding claims. Confirmation must match the
current canonical eligible target and prompt. The standard `recipes.run` task
owns backend submission. Replay can discover a single verified accepted child
after terminal failure while preserving that failure; it never submits a new
backend job to resolve uncertainty.

`OrdinaryLimits` reports 16 KiB human text, 32 KiB assistant text, 64 KiB frozen
snapshot, 128 KiB raw model field, 16 KiB per document/file, four files and 64 KiB
aggregate, and 192 KiB encoded upload/replay envelopes. The helper separately
bounds encoded operation input at 192 KiB and model prompts at 96 KiB, leaving
headroom below the runtime's default 256 KiB chapter envelope. UTF-8 byte and
encoded JSON bounds both apply. These are per-operation/page limits, not a total
conversation limit.

Current dependencies are Go 1.26, c2j
`v0.0.125-0.20261111181458-c2193c57d890` and JobDB
`v0.0.81-0.20261110034917-7f6133791ec9`. The ordinary inference selector is:

```
nix:github:colony-2/c2ops/6396e1fe6462084e85ea44d86d372bdf3b133db0#pydantic
```

The worker owns allocation, resolution/cache and execution. The host supplies
existing story/control/input/history/submitter resources and authorization,
starts its engine, and cancels/joins it before closing owned resources. Handlers
start no runner and install no activities. Read-only applications provide their
existing read services and deny mutation at their route/control/input/submission
boundaries. `TestConsumerOrdinaryRuntimeAndReadOnly` exercises public composition
with SQLite, retained queued reads, an unavailable submit adapter and an unrelated
ordinary recipe.

The ordinary integration suite resolves actual helpers from a committed local
source fixture and executes the immutable c2ops payload. A trusted test manifest
adds its `--fixture-loopback` flag for a deterministic HTTP provider; production
inputs cannot enable it. Evidence records model bodies, task/receipt/artifact
origins and downloaded-byte hashes. This establishes source behavior with those
fixture boundaries. Same-route immutable assistant resolution remains a separate
release check. Cortex owns later HTTP/browser integration.

Explicit v1/v2/v3 descriptors preserve historical reads, identities and pending
executions. V3 uses `discussion.yaml`, has no supported reopen and rejects
attachments as before. No migration or new deprecation is introduced. The
following section documents the frozen v2 route; its activity setup and larger
ceilings apply only to that historical contract.

# Frozen v2 compatibility reference

An ordinary c2j recipe runs the conversation. The standalone `assistant` Go package interprets its recorded task data for Cortex; no conversation database, cumulative discussion artifact or provider session exists. The accepted contracts and unchanged release expectations are in [docs/design.md](docs/design.md) and [docs/test-statements.md](docs/test-statements.md).

`session.yaml` captures context once, assembles bounded preceding records, calls the pinned c2ops pydantic operation, validates/materializes the response, then waits for structured human input. Send performs inference; confirm prepares and submits the exact existing-cell build; finish ends successfully. Invalid accepted input returns a visible error and fresh input without inference or dispatch. Model operation failure returns to input through a bounded validation error. Ordinary runtime attempts remain available in generic replay.

## Go facade

```go
client, err := assistant.New(host, config)
session, err := client.Start(ctx, caller, startInput)
page, err := client.List(ctx, caller, pageRequest)
session, err = client.Get(ctx, caller, sessionID)
replay, err := client.Replay(ctx, caller, sessionID, pageRequest)
receipt, err := client.Respond(ctx, caller, sessionID, structuredSubmission)
session, err = client.Reopen(ctx, caller, sessionID)
reader, err := client.OpenArtifact(ctx, caller, sessionID, artifactID)
```

`Host` supplies existing c2j workflow control, story, structured input and normal submitter collaborators, plus standard runtime history/goal readers. `Authorize` must enforce actual tenant/actor/job access before any bytes or submissions. `ResolveCell` returns the current approved catalog target or an error. These callbacks are trusted application bindings, never browser/model fields. No Cortex imports or database setup are needed by this package.

`StartInput` contains stable `RequestID`, frozen `SubmittedAt`, prompt, source (`global`, exact `review` job/request, or `story` job), and named stored attachment refs. Freeze all fields on retries: the deterministic job ID is reused, and the normal backend rejects conflicting submissions. Root and pinned recipe come only from `Config`. The recipe wire adapter carries source as an existing inline JSON-value artifact ref because c2j's recipe `object` type denotes a checkpoint. It does not create a source file. Initial nil attachments become an empty artifact map.

At a prompt, submit `input.StructuredSubmission` with the actual current RequestID and stable SubmissionID. Commands are:

```json
{"command":"send","prompt":"Explain this result","attachments":{}}
{"command":"finish"}
{"command":"confirm","proposal_id":"<current>","proposal_hash":"<current>"}
```

The host supplies the trusted human Actor. Unknown fields, stale identity and mismatched proposals fail. A Go send can use `map[string]any` attachments containing `jobdb.Artifact` values; the facade bounds and freezes bytes, then delegates actual persistence/ref binding to `SubmitStructuredResponse`. Explicit stored refs remain supported. The encoded upload budget includes base64 upload data, independently of raw file budgets. `ArtifactReader` metadata accompanies a caller-owned reader; always close it.

`ReplayPage` is a transient JSON projection containing actual origins, roles, status, text, receipts, proposals and artifact refs. Failed/pending records are exposed through `Session.Progress`. Pages run backward from the newest completed records; follow cursors for older entries. HMAC cursors bind tenant, execution, session and immutable watermark. Appends do not change old pages; refresh to see them. A restart changes execution and requires refreshed cursors. Unavailable history fails explicitly. Logical session IDs resolve a verified descendant using recorded runtime ancestry, never timestamp/text/ordinal equality. Ambiguous divergent successor branches return conflict; direct execution IDs remain inspectable.

Discovery exhausts configured-root backend pages before grouping executions, avoiding duplicates across backend boundaries. This intentionally performs whole-history reads; it does not promise paginated backend replay, constant memory or bounded list cost. No derived session index is stored.

Finish/reopen uses the node's actual `RestartScopeID`, validates restart context and selects the last await-human scope. Successful finish starts a new goal with a stable restart request. Failed dispatch recovery resumes the actual dispatch scope with its recorded frozen preparation. Other failed execution recovery checks the actual goal: open goals continue; closed goals use new-goal mode. Missing scope/goal history errors. Reopen discards the old finish receipt, obtains a fresh request, and never replays committed inference or dispatch. An undispatched historical proposal needs fresh confirmation; dispatched proposals are suppressed.

## Worker configuration and releases

Frozen v2 sessions use protocol `cortex-assistant/2` and `cortex_assistant:run`.
Construct the facade with Cortex's existing host resources, then construct and
register its instance-bound activity before building the normal c2j WorkSet:

```go
activity, err := assistant.NewActivity(client, assistant.ActivityOptions{
    Provider: provider, Model: model,
    AuthorizeExecution: authorizeSessionExecution,
})
// Handle err, then:
err = assistant.RegisterActivity(registry, activity)
// Handle err, construct NewRecipeWorkerWithOptions with the same dependencies,
// executionruntime allocation, StageExecution and WrapTaskWorker, and register
// the WorkSet on the existing engine before its single owner calls engine.Run.
```

`AuthorizeExecution` receives trusted c2j execution coordinates and must bind them
to an authorized session principal. Tenant membership or an email cookie alone is
insufficient. Browser/model input cannot supply identity, provider, catalog or
runtime connection settings. Missing authority/provider/model fails explicitly.
The activity reads no configuration file, creates no remote client and starts no
runner. Generated documents use ordinary `AddOutputArtifact` publication.

Cortex's application composition owner is `cortexcli.serve`, for both entry points.
It shares the runtime/engine and story/control/input/catalog resources, resolves
read-only versus executable replay registration, preserves unrelated operations,
and owns one guarded runner with HTTP drain/cancel/join before owned resources
close. See `docs/hosting.md` for the composition and trust contract. Consumer
adoption is separate work; adding the module does not start workers in NewServer.

The legacy `ops/assistant` / `cmd/assistant-op` executable is deprecated for new
sessions. It remains available only for explicitly supported original v1
execution. `Config.LegacyReleases` explicitly approves immutable v1 and frozen v2
descriptors for reading. Reopening is denied unless that descriptor's `Reopen`
flag attests that the original route remains installed. No history or pending task is silently
migrated; starts through the legacy `Start` method use the configured v2 release.

Candidate verification binds the exact tree and recipe content. After authorized
publication, execute the resulting immutable selector through the same registered
activity/model path and record its relationship to that candidate. Synthetic test
selectors are not published releases. The historical v2 baseline used c2j `v0.0.122` and JobDB `v0.0.74`;
the frozen model identity remains:

```
git+https://github.com/colony-2/c2ops.git//pydantic@f30202d13d197beaed7ba0bd0b120b04443728eb
```

It receives bounded text (including authorized UTF-8 text/* and application/json attachments with stored-ref provenance and explicit budget/format omission markers) plus a closed result schema, no files, an empty tool list and disabled tool/sandbox execution. Returned JSON is independently validated. Text documents support plain text, Markdown and valid JSON. Other existing file/image/video/audio bytes can be returned/downloaded; no media understanding, transcription or generation is claimed. Cortex must sanitize rendering and serve safe MIME/download behavior.

## Limits and verification

Product ceilings: human text32KiB, assistant text64KiB, model prompt128KiB, captured source1MiB, document1MiB, file8MiB, eight files, aggregate16MiB, encoded upload24MiB, encoded replay page4MiB. Text limits count UTF-8 bytes, pages/uploads count encoded JSON, and documents also consume file/count/aggregate budgets. Source truncation and model omissions are explicit. The page cap is not a total discussion cap.

`Config.Limits` must reflect lower actual backend/proxy/provider limits before advertising or accepting uploads. Zero fields use product ceilings; they do not detect deployment limits. In particular the pinned runtime's default task application payload cap is 256KiB: configure lower source/document/reply/input budgets and leave envelope headroom, or configure a sufficient existing runtime cap. Product maxima alone are not deployment certification.

Run:

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/assistant-op
c2j test run --file recipe-tests/session.test.yaml --out-dir /tmp/assistant-recipe-results
```

The Go worker integration test runs actual c2j workers, input receipts, helper policies, replay, documents, finish and reopen against toy, SQLite and the existing remote SDK/server, replacing the worker engine and facade before reopen. Assistant tasks use the exported registered activity; only model responses and root release resolution are fixtures. The SQLite goal test injects loss after normal destination submission, retries the frozen request, and checks one completed destination with no extra inference. Model execution and root release resolution are deterministic fixtures. Native recipe tests separately exercise real input collection/routing with model/helper fixtures. Unit tests cover hostile/malformed inputs, byte/page/file boundaries, role spoofing, provenance, cursors, proposals, uploads and unavailable history.

These checks do not certify a real provider/model, the published assistant release, production credentials/retention, production fault injection and concurrency, or Cortex UI integration. The mandatory build hook now verifies exact candidate source and unchanged inference through ordinary operations over an HTTPS provider fixture; public assistant fetchability is not a separate pre-completion gate. The v1 executable route is deprecated for new starts; approved legacy histories remain readable without migration.

Human execution guidance: validate this cell with embedded c2j and JobDB. Cortex consumes the facade; its checkout, browser suite and deployment configuration are not prerequisites for building or testing this module. Consumer integration checks remain consumer-owned.
