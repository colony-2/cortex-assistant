# Cortex assistant

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

New sessions use protocol `cortex-assistant/2` and `cortex_assistant:run`.
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
execution. `Config.LegacyReleases` explicitly approves immutable v1 descriptors
for reading. Reopening is denied unless that descriptor's `Reopen` flag attests
that the original route remains installed. No history or pending task is silently
migrated; new starts always use the configured v2 release.

Candidate verification binds the exact tree and recipe content. After authorized
publication, execute the resulting immutable selector through the same registered
activity/model path and record its relationship to that candidate. Synthetic test
selectors are not published releases. Corrected c2j remains pinned at
`v0.0.122` with JobDB `v0.0.74` and Go 1.26; the model operation remains:

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

These checks do not certify a real provider/model, the published assistant release, production credentials/retention, production fault injection and concurrency, or Cortex UI integration. Those approved release gates remain required. The v1 executable route is deprecated for new starts; approved legacy histories remain readable without migration.

Human execution guidance: validate this cell with embedded c2j and JobDB. Cortex consumes the facade; its checkout, browser suite and deployment configuration are not prerequisites for building or testing this module. Consumer integration checks remain consumer-owned.
