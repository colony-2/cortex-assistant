# Replay-based assistant test statements

- This replacement follows design.md and human feedback; all tests are planned, filenames proposed, and dependencies denote interfaces/fixtures rather than external prerequisite requests.
- P0 blocks release for safety/continuity; P1 remains required functional coverage. No implementation, runtime tests or submissions occur in this phase.
- Preserve prior acceptance outcomes; replace cumulative discussion/context artifact assertions with actual task-data reconstruction, explicit paging and per-turn validation. No deployed API requires deprecation.
- Prior specialist approval covered the previous mechanism; request renewed review for replay lineage, projection, pagination and direct-input validation semantics.

## Evidence and expectation changes

- Repository now contains the facade, recipe, helper, unit tests and worker tests; preserve existing regressions. No applicable AGENTS.md was found.
- Build settings add no requirements; configure/design hooks were skipped. Existing-cell eligibility, trusted identity, durable discussion, artifacts, confirmation and last-prompt reopening remain required.
- Withdraw cumulative conversation.json/context.json assertions and total-transcript admission reservations; preserve their safety/progress outcomes through recorded tasks, per-turn bounds and replay paging.
- The 4-MiB limit now bounds encoded replay pages, not total retained discussion. No deployed module/schema exists, so migration or deprecation windows are unnecessary.
- Dependency versions, complete runtime refs and compilable facade types remain implementation gates. Only the agreed pending-input restoration correction extends dependency scope; numeric memory certification and speculative infrastructure remain excluded.

## Submission, context and model

- [START1; integration/start_test.go; P0; integration; dependencies: recipejob,worker] Global/story/review sessions submit configured root and pinned assistant recipe with stable JobID, SubmittedAt, session identity and frozen retry inputs.
- [CTX1; integration/context_test.go; P0; integration; dependencies: story,input] Capture exact authorized source/time/text/ref data as actual task output; replaced review errors, navigation preserves context, and global starts avoid repository dumps.
- [CTX2; assistant/context_test.go; P0; unit; dependencies: source-fixtures] Initial root inputs and later accepted attachments supply sources; reject local paths, arbitrary URLs, forged refs and source-directed authorization changes.
- [MODEL1; assistant/model_test.go; P0; unit; dependencies: pydantic-fixtures] Use pinned pydantic with empty tools and disabled execution; validate parsed JSON independently and record actual model/usage in response results.
- [MODEL2; integration/model_test.go; P0; integration; dependencies: real-model,worker] Assemble preceding recorded text within prompt bounds; identify omitted locators, avoid implicit file truncation, and produce one validated real-model response.
- [MODEL3; assistant/model_test.go; P0; unit; dependencies: malformed-results] Invalid/oversized outputs, forged artifact IDs and unsafe document names yield bounded errors and another input without completed broken links.

## Actual replay and continuity

- [REPLAY1; assistant/replay_test.go; P0; unit; dependencies: recorded-story-fixtures] Every discussion entry maps to actual root/task data; exclude aggregate duplicates, prompt copies and transition payloads from conversational messages.
- [REPLAY2; assistant/replay_test.go; P0; unit; dependencies: retry-fixtures] Render committed logical results once, preserve distinct identical turns, expose attempts diagnostically, and display pending/failed records explicitly.
- [REPLAY3; integration/replay_test.go; P0; integration; dependencies: worker,retained-storage] Complete three turns, reload, delete caches and replace workers; reconstruct identical discussion without conversation/context artifacts, journals, provider sessions or workspace files.
- [REPLAY4; integration/replay_test.go; P0; integration; dependencies: task-history] Compare displayed text, ordering, actor/receipt identities, proposals and artifact refs with originating recorded inputs/results; generic raw replay remains inspectable.
- [REPLAY5; integration/replay_test.go; P0; integration; dependencies: incomplete-history] Missing retained tasks or ancestors report incomplete/unavailable history; never silently fabricate a complete discussion or load a transcript fallback.
- [PAGE1; integration/replay_paging_test.go; P0; integration; dependencies: large-history] Exceed 4-MiB response history; stable-watermark paging reaches each retained entry once, while new appends require refresh without reordering prior pages.
- [PAGE2; assistant/replay_paging_test.go; P0; unit; dependencies: encoded-fixtures] Enforce encoded page limits including JSON escaping/metadata; reject invalid or foreign cursors and explicitly report individually unrepresentable entries.
- [PAGE3; integration/replay_paging_test.go; P1; integration; dependencies: retained-backend] Observe large-story read cost without claiming paginated backend reads or numeric memory guarantees; finish and downloads remain independent of presentation paging.
- [READ1; integration/replay_test.go; P0; integration; dependencies: operation-counters] Replay/Get/List/OpenArtifact perform no inference, dispatch or restart; rendering cannot mutate job state or authorize actions.

## Structured input and limits

- [FORM1; integration/input_test.go; P0; integration; dependencies: pinned-input] Use structured input without kind, actual complete stored refs, closed command schemas and SubmitStructuredResponse; reject unknown protocols and unapproved recipe markers.
- [FORM2; integration/input_test.go; P0; integration; dependencies: input.Runtime] Reject stale/conflicting identities; preserve existing duplicate receipt semantics and trusted Actor binding without browser tenant/actor overrides.
- [LIMIT1; assistant/limits_test.go; P0; unit; dependencies: byte-fixtures] Test exact/excess human32-KiB, assistant64-KiB, model128-KiB, source1-MiB and document1-MiB caps; mark truncation/omissions without summarizing action evidence.
- [LIMIT2; assistant/limits_test.go; P0; unit; dependencies: file-fixtures] Accept eight small files, one 8-MiB file and two 8-MiB files; independently reject count, per-file and 16-MiB aggregate excess.
- [LIMIT3; integration/uploads_test.go; P0; integration; dependencies: upload,proxy] Exercise exact/excess 24-MiB encoded sends, lower configured caps and actual byte-count mismatches; advertise effective limits before upload.
- [LIMIT4; integration/input_test.go; P0; integration; dependencies: validation-fixtures] Facade limit rejection has no receipt; direct accepted invalid input retains receipt, returns bounded error/new prompt, and triggers zero inference/dispatch.
- [LIMIT5; integration/limits_test.go; P0; integration; dependencies: large-history,worker] Many valid turns exceed former cumulative cap without rejection; oversized per-turn model output preserves accepted human evidence and permits finish/reopen.

## Artifacts and authorization

- [FILE1; integration/artifacts_test.go; P0; integration; dependencies: worker,storage] Generate text/Markdown/JSON documents; return existing file/PNG/MP4/audio artifacts and compare downloaded bytes after reload and worker replacement.
- [FILE2; integration/artifacts_test.go; P0; integration; dependencies: retention] Expired/unavailable files and failed materialization expose errors rather than completed links; membership derives from actual authorized task refs.
- [AUTH1; integration/authorization_test.go; P0; integration; dependencies: two-tenants,two-actors] Enforce source/session/artifact/cursor authorization across Start/List/Get/Replay/Respond/Reopen/OpenArtifact before reading bytes or submitting work.
- [MEDIA1; integration/cortex_rendering_test.go; P0; integration; dependencies: Cortex,media-fixtures] Preview safe MIME, sanitize Markdown, block HTML/SVG execution, and download unsupported media without advertising understanding, generation, transcription or seeking.

## Goals, finish and restart

- [GOAL1; integration/goal_test.go; P0; integration; dependencies: input,catalog] Only matching current confirmation for exact canonical eligible target/prompt/hash dispatches normal existing-cell build; stale/forged/unconfirmed instructions submit nothing.
- [GOAL2; integration/dispatch_test.go; P0; integration; dependencies: fault-injection,submitter] Interrupt after submission before result recording; retries/concurrent confirmations reconcile one frozen destination identity/link, while conflicting payloads fail without fresh IDs.
- [GOAL3; assistant/proposal_test.go; P0; unit; dependencies: proposal-fixtures] Recheck eligibility before dispatch; changed target/prompt requires renewed confirmation, and deliberately later identical proposals have distinct recorded origins.
- [END1; integration/reopen_test.go; P0; integration; dependencies: restart,input] Finish at prompt records normal receipt/terminal success without inference; reopen concurrently resolves one successor/new goal/fresh current input at actual await-human scope.
- [END2; integration/reopen_test.go; P0; integration; dependencies: restart-lineage] Repeated later finish/reopen preserves correct recorded prefix, excludes divergent suffixes, deduplicates copied ancestry and never replays committed replies/actions.
- [END3; integration/reopen_test.go; P0; integration; dependencies: proposal,input] Proposal/finish/reopen restores historical proposal only for fresh current confirmation; closed presentation suppresses actions and old confirmation identities cannot dispatch.
- [END4; integration/reopen_test.go; P0; integration; dependencies: runtime-state] Active sessions return current execution; failed recovery checks actual goal state, and missing scope history errors without guessed ordinals.

## Facade, consumer and release gates

- [API1; integration/facade_test.go; P0; integration; dependencies: pinned-c2j] Compile standalone facade including Replay with existing collaborators; no Cortex imports, new database or service is required.
- [LIST1; integration/discovery_test.go; P0; integration; dependencies: paginated-history] Discover active/closed/failed sessions by approved recipe family and restart lineage; empty filtered pages retain continuation.
- [WEB1; integration/cortex_browser_test.go; P0; integration; dependencies: Cortex,real-worker] Exercise contextual start/follow-up/download/reload/finish/reopen using custom task replay; opening composer submits nothing, and REST delegates assistant policy.
- [WEB2; integration/cortex_browser_test.go; P1; integration; dependencies: Cortex] Navigation/tab closure detaches; Close becomes available at input and progress explains availability after infer/action work.
- [COMP1; integration/compatibility_test.go; P0; integration; dependencies: Cortex,c2j] Preserve generic/review/input/restart regressions in development and embedded production UI; unrelated generic forms never become assistant sessions.
- [PIN1; integration/releases_test.go; P0; integration; dependencies: published-releases] Resolve immutable recipe/operation releases and record actual assistant/c2j/c2ops/Cortex/provider/model versions; source inspection cannot substitute for real-worker results.
- Consumer filenames denote Cortex-owned boundaries, not prerequisites. The sole agreed prerequisite is c2j-pending-input-restoration; renewed approval remains necessary before submission.
- Required runtime failures need local primitive-use investigation and minimal versioned reproduction before external consultation; never silently drop outcomes or restore a shadow discussion artifact.

## Additional critical boundaries

- [START2; assistant/start_test.go; P0; unit; dependencies: submission-fixtures] Reject conflicting stable start requests; preserve original target, recipe, timestamps and inputs rather than choosing another session/job identity.
- [FORM3; assistant/schema_test.go; P0; unit; dependencies: command-schemas] Accept nonempty send, finish, and current proposal confirmation; reject empty sends, extra fields, unknown commands and confirmation without pending proposals.
- [FORM4; integration/input_test.go; P0; integration; dependencies: bound-uploads] Accepted structured sends retain actual actor, receipt and uploaded stored refs; confirm/finish appear as control rows rather than duplicated user prose.
- [REPLAY6; assistant/replay_test.go; P0; unit; dependencies: spoofed-story-fixtures] Recipe identity and node paths authenticate task roles; model/source markers cannot forge response, input, proposal or dispatch records.
- [REPLAY7; assistant/replay_test.go; P0; unit; dependencies: reordered-timestamps] Recorded execution order overrides timestamps/lexical IDs; identical ordinals across different jobs never establish shared-prefix identity.
- [REPLAY8; integration/context_assembly_test.go; P0; integration; dependencies: worker,replay] Model assembly reads only completed preceding records, excluding itself, pending tasks and prior assembled-prompt copies; retries avoid recursive prompt growth.
- [REPLAY9; integration/replay_test.go; P0; integration; dependencies: injected-failure] Interrupt inference/materialization; preserve accepted human evidence, render incomplete/error state and expose artifacts only after successful publication.
- [PAGE4; assistant/replay_paging_test.go; P0; unit; dependencies: exact-encoding] Accept exactly 4-MiB encoded pages; split excess at entry boundaries, preserve has_more/completeness, and never silently truncate a supported entry.
- [PAGE5; integration/replay_paging_test.go; P0; integration; dependencies: append,restart] Cursors bind tenant/session/lineage/watermark; concurrent appends and reopened branches cannot introduce duplicate entries, skipped retained records or foreign suffixes.
- [LIMIT6; assistant/limits_test.go; P0; unit; dependencies: multibyte-fixtures] Measure UTF-8 bytes rather than characters; JSON escaping counts toward encoded envelopes, while document bytes count toward file limits independently.
- [LIMIT7; integration/limits_test.go; P0; integration; dependencies: near-page-boundary] Facade rejection preserves current input; accepted invalid direct input yields fresh input; both permit finish/reopen and produce no inference/dispatch.
- [LIMIT8; assistant/limits_test.go; P0; unit; dependencies: reply-fixtures] Apply eight-file, 8-MiB individual and 16-MiB aggregate caps to replies and sends; generated documents consume count/aggregate budgets.
- [GOAL4; integration/goal_test.go; P0; integration; dependencies: worker,catalog] Prepare-action records exact frozen BuildStartJobRequest before dispatch; successful confirmation publishes destination outcome then reaches input with zero additional inference calls.
- [GOAL5; integration/goal_test.go; P0; integration; dependencies: decline,finish] Declining proposals through send follows normal inference; finish dispatches nothing, and already dispatched historical proposals cannot become new actions through rendering/reopen.
- [END5; integration/reopen_test.go; P0; integration; dependencies: restart-scopes] Restart selects actual scope identity, original release and new-goal mode; task outcome ordinals alone never authorize or identify restart boundaries.
- [API2; assistant/errors_test.go; P1; unit; dependencies: collaborator-errors] Distinguish forbidden/not-found, stale-input, conflict, limits, unavailable-history/artifacts and not-at-prompt; artifact readers expose metadata and caller-owned closure.
- [MODEL4; assistant/model_test.go; P0; unit; dependencies: adversarial-output] Model output cannot override configured root, recipe, provider, model, limits or tenant, nor execute commands, paths or arbitrary selectors.
- [MODEL5; integration/model_test.go; P0; integration; dependencies: configured-operation] Missing published selector, credentials or provider configuration fails explicitly; no silent operation substitution or unsupported media-understanding claim is permitted.

## Execution evidence required later

- Unit fixtures must cover stable task locators, retry/attempt trees, copied prefixes, divergent branches, encoded boundaries, adversarial refs and unavailable history; implementation-shaped assertion mirrors are insufficient.
- Integration evidence records real worker/backend behavior, release identities, receipts, destination IDs, raw-task comparisons, download hashes and restart boundaries; source inspection and mocks cannot establish acceptance.
- Cortex-owned browser tests require development and embedded production UI; local facade tests remain independently runnable without modifying the consumer or requesting prerequisite work.
- All twelve design acceptance outcomes remain represented; this document claims no executed checks and requests renewed review before implementation.

## Revised prerequisite and evidence rules

- Preserve all existing statement identifiers and expectations; restoration adds coverage without reducing feature scope, product limits, authorization, continuity or compatibility requirements.
- c2j-pending-input-restoration is agreed in context.json and listed in plan_keys; approved prerequisites and jobs remain empty. This document authorizes no implementation or submission.
- Serving filenames below belong to c2j; assistant filenames belong here. New filenames are proposed; existing assistant/assistant_test.go and assistant/worker_integration_test.go regressions remain required.
- No API, schema, receipt, artifact-format or offload-threshold deprecation is proposed. Legacy structured/review rejection remains mandatory; previously superseded transcript expectations remain superseded.
- The observation reproducer currently passes by detecting failure. Replace its observation expectation with positive regressions before claiming restoration acceptance.
- Fixture suites and adverse-finding agreement do not establish release acceptance. Required real-model, authorization, large-history, interruption and Cortex evidence remains mandatory.
- Build settings add no variables or specialist consultations. Read the supplied serving agreement and independent findings; no checks were executed during test planning.

## Pending invocation restoration: serving-owned coverage

- [REST1; pkg/input/runtime_test.go; P0; unit; dependencies: invocation-fixtures] Inline and offloaded GetDetails/GetForm preserve request ID, requested-at, complete request and response schema exactly.
- [REST2; pkg/input/runtime_test.go; P0; unit; dependencies: escaped-fixtures] A 65,536-byte escaped text response retains complete input identity after invocation offload; verify both public readers.
- [REST3; pkg/input/runtime_test.go; P0; unit; dependencies: artifact-fixtures] Restore memory-backed invocation payloads without mutating recorded pending task data or stored payloads.
- [REST4; pkg/input/runtime_integration_test.go; P0; integration; dependencies: SQLite,lazy-artifacts] Persist and reload offloaded invocations; lazy stored-artifact restoration returns identical forms and schemas.
- [REST5; pkg/input/discovery_test.go; P0; integration; dependencies: REST4] ListPendingInputsPage preserves exact occurrence identity across pagination for inline and offloaded requests.
- [REST6; pkg/input/management_test.go; P0; unit; dependencies: invocation-fixtures] Management-facing reads obey identical restoration and error behavior without introducing a separate discovery mechanism.
- [REST7; pkg/input/structured_test.go; P0; integration; dependencies: REST4,trusted-actor] Valid structured submission consumes exactly the intended occurrence and persists frozen identity, submission ID, actor, response and receipt.
- [REST8; pkg/input/runtime_integration_test.go; P0; integration; dependencies: REST7,worker] Resume actual workers through send and finish after restored structured submission; verify persisted outcomes and terminal state.
- [REST9; pkg/input/structured_test.go; P0; unit; dependencies: offloaded-requests] Wrong request IDs, schema-invalid responses and missing trusted actor identity fail without completing the wait.
- [REST10; pkg/input/structured_test.go; P0; integration; dependencies: tenant-fixtures] Cross-tenant submissions cannot consume restored requests; duplicate submissions preserve receipts and conflicting completion retains existing conflict semantics.
- [REST11; pkg/input/runtime_test.go; P0; unit; dependencies: structured-review-fixtures] Legacy SubmitResponse rejects restored structured/review forms while preserving existing supported legacy-form behavior.
- [REST12; pkg/input/runtime_test.go; P0; unit; dependencies: corrupt-artifacts] Missing, unreadable and malformed transport payloads fail reads/submissions without Finish, fallback identity or successful empty forms.
- [REST13; pkg/input/runtime_test.go; P0; unit; dependencies: cancellable-reader] Cancellation and deadlines propagate with underlying causes; restoration failure never acknowledges input or completes pending tasks.
- [REST14; pkg/input/runtime_integration_test.go; P0; integration; dependencies: REST7,artifact-fixtures] Completion retains unrelated artifacts, restored application references and output context; private invocation transport artifacts never enter exposed bindings or completed attachments.

## Assistant regressions and prerequisite consumption

- [FIX1; assistant/assistant_test.go; P0; unit; dependencies: authorized-text-fixtures] Initial, captured and later text attachments reach inference beyond 1,000 characters; denied, invalid and truncated bytes produce explicit bounded omissions.
- [FIX2; assistant/worker_integration_test.go; P0; integration; dependencies: worker,uploads] Accepted permitted text uploads appear in actual inference input after reload; metadata alone cannot satisfy this assertion.
- [FIX3; assistant/assistant_test.go; P0; unit; dependencies: dispatch-fixtures] Handled dispatch errors override destination presence; missing destination or preparation never displays submission success.
- [FIX4; assistant/assistant_test.go; P0; integration; dependencies: Worker,projection,Form] Eligibility rejection and conflicting dispatch remain visible errors through form presentation; only recorded successful destination outcomes show success.
- [REST15; assistant/worker_integration_test.go; P0; integration; dependencies: c2j-pending-input-restoration] TestWorkerLargeHistory passes without skipping: eight valid replies and seven sends exceed one encoded replay page while preserving usable current input.
- [REST16; assistant/worker_integration_test.go; P0; integration; dependencies: REST15,SQLite] Traverse stable-watermark pages exactly once, append and refresh, then finish/reopen with retained prefix, fresh input identity and no repeated inference/actions.
- [REST17; assistant/worker_integration_test.go; P0; integration; dependencies: immutable-serving-outcome] Consume the verified corrected c2j pin; run the full Go suite and existing native recipe cases without excluding large-history coverage.
- [REST18; integration/releases_test.go; P0; integration; dependencies: REST17,release-environment] Record immutable assistant/c2j/c2ops/Cortex identities and actual provider response/usage; fixture-only execution cannot satisfy release gates.

## Human execution clarification

- Validate this module with embedded c2j/JobDB; Cortex remains a consumer. WEB1/WEB2/MEDIA1/COMP1 describe consumer-owned coverage, not a requirement for this build's environment.
- Preserve backend facade compatibility, artifact authorization and safe metadata contracts locally; do not request a Cortex checkout or deployment configuration to run these tests.
