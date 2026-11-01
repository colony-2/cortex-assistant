# Pending invocation input restoration agreement

Outcome: agreed. Assessment: revise (current runtime behavior and large-history acceptance remain failing).

I accept the correction scope requested in serving-request.md. No supported existing Runtime configuration or standard caller sequence was found that repairs an already offloaded pending invocation. NewRuntime takes WorkflowControl and SSE only; GetDetails, GetForm, structured submission and paginated occurrence discovery ultimately use Runtime.getOutput. Calling RestoreInput independently recovers a separate decoded request, but cannot update Runtime's subsequent reads. A caller-written TaskHandle/WorkflowControl adapter could normalize data, but would be a custom workaround, not an established pending-input facility. Changing form size, bypassing structured submission, or increasing SQLite capacity does not resolve this recorded invocation through the standard runtime.

Evidence, in order:

1. Bound request serving-request.md reports actual SQLite worker execution stopping at missing request identity, with a generated structured form intact and chapter capacity explicitly configured.
2. Bound runtime-gap_test.go isolates the public invocation/input primitives. I reran `go test -v /tmp/recipe-worktree-4017690583/inbox/runtime-gap_test.go` against this checkout: PASS, reproducing the bug. Inline 1,024-byte text retains identity; 65,536-byte escaped text produces a 393,303-byte form and loses identity; existing RestoreInput recovers it. This PASS certifies observation only.
3. pkg/worker/ops/invocation_artifact.go offloads encoded envelopes above 128 KiB and provides restoration for both memory and stored artifacts. pkg/worker/ops/activity_registry.go ExternalOutput reads Input and Artifacts directly. pkg/input/runtime.go and the duplicate reader in pkg/input/management.go call ExternalOutput without restoration. pkg/input/structured.go and discovery.go share the runtime reader.
4. Supplied test-results.log and verification.json report the real-worker failure and explicitly leave large-history send/paging/finish/reopen acceptance incomplete. I did not rerun that external assistant suite.

## Agreed minimal correction

After reading task data and its artifacts and unmarshalling ActivityInvocationRequest, call request.RestoreInput(ctx, artifacts), retain its returned artifact slice, and only then call ExternalOutput. Apply the same restoration rule to the duplicate management reader, preferably by delegation to the validated runtime reader if compatible with its current callers. That duplicate has no call sites in the inspected input package; HTTP details/submission already delegate to Runtime. No separate discovery mechanism is needed: ListPendingInputsPage inherits the runtime correction.

On restoration failure return an error before decoding, accepting a reply, or finishing a task. Preserve the underlying cause and context cancellation/deadline semantics using existing runtime error conventions. Missing, unreadable or malformed transport payloads must never become successful empty legacy forms. No new error schema or fallback identity is required.

Use the remaining artifacts returned by RestoreInput throughout completion. Its removed private invocation attachment must not become a review document, response artifact, public artifact reference, or completed-output attachment. Preserve unrelated artifacts, restored application artifact references and existing output context. Leave the recorded pending task and its stored payload intact; restoration is a read operation on a local decoded envelope.

Preserve frozen request ID, requested-at, complete request and response schema. Keep existing exact waiting-task completion, tenant checks, stale-request validation, schema validation, trusted caller-supplied Actor, submission identity, receipt construction and finish conflict behavior. Legacy SubmitResponse must continue rejecting structured/review forms once their restored schema/kind is visible. No schema, trusted-actor source, receipt, artifact format, offload threshold, transport, JobDB dependency, assistant policy or structured submission/finish API change is agreed.

## Required acceptance before declaring the fix usable

- Replace the observation assertion with positive regression coverage for inline and offloaded GetDetails/GetForm, checking complete identity, request and schema equality. Include the escaped 64-KiB text case.
- Cover memory artifacts and actual stored/lazy artifacts after SQLite persistence/reload. Exercise ListPendingInputsPage occurrence identity and management-facing reads through the same restoration behavior.
- Submit valid structured responses through Runtime using a trusted actor; inspect the persisted outcome and receipt and resume the worker through send and finish. Check the exact occurrence is consumed and frozen identity retained. Reject wrong request IDs, invalid schema responses, missing actor identity and legacy submission attempts without completing the wait.
- Missing, unreadable and malformed payloads must fail reads/submission without Finish. Verify cancellation propagation, unrelated artifact retention, restored application references, and absence of the private invocation attachment from completion and exposed artifact bindings.
- Rerun the requesting cell's retained-worker large-history acceptance, including paging and finish/reopen. Earlier skipped-suite or observation passes do not satisfy it.

These are acceptance requirements, not claims of completed validation. The consultation agrees the existing requested correction without a material counterproposal. It authorizes no implementation, jobs, dependency changes, commits, merge or publication. A prerequisite must still take the normal design/approval route. Repository source was not changed.
