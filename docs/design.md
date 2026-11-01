# Assistant conversation as a view of job replay

## Decision and scope

Revise the plan in response to the human request in feedback.json: “use a custom representation of the cortex assistant job replay … the actual task data … not some derivative.” The assistant job's recorded inputs, task outputs, input receipts and attached artifact references are the authoritative discussion. The conversation UI is a custom, read-only representation of that replay. Do not persist a second discussion artifact, cumulative transcript, message ledger, or session database.

This revision preserves the replay-based assistant scope and adds one demonstrated c2j prerequisite. Previous approval is no longer current. Earlier planning passes do not approve this dependency change, and agreement on adverse implementation findings is not acceptance. This turn produces design documents only, without implementation or job submissions.

Keep the feature scope: contextual review/story/global sessions; durable multi-turn discussion; generated text documents; file/image/video/audio downloads; confirmed goal creation for eligible existing cells; finish; reopening at the last prompt; tenant authorization; session discovery. Cortex-assistant owns the recipe, context policy, interpretation of replay, model adaptation, confirmation and restart policy. Cortex owns UI, trusted request binding and simple REST delegation. c2j owns task execution, receipts, replay and restart; existing storage owns retention; c2ops supplies the model operation. Root retains cell eligibility and creation approval. No new cell is proposed.

The current repository contains the standalone Go facade in assistant/, the session.yaml recipe, the cmd/assistant-op worker helper, operation manifest, unit/worker tests and native recipe routing tests. PURPOSE.md already assigns assistant-owned context capture/discovery and replay presentation. No applicable AGENTS.md was found. Preserve its eligibility, authorization and durable-job obligations. Do not implement arbitrary edits, shell execution, repository creation, compute dispatch, a replacement workflow runtime, or a new authentication system.

## Evidence and relevant interfaces

Read inbox/context.json, feedback.json, design-context.md, configuration.md, build-settings.json, the supplied final consultation agreement/design/acceptance, the replacement planning documents and specialist reassessment. Configure and before-design hooks were skipped. Settings supply no additional variables or consultations. The supplied independent findings on simultaneous file caps, fresh confirmation after reopen, and control progress remain relevant; their former cumulative-artifact solution is superseded below.

For this revision, inspected a separate temporary read-only source checkout of c2j v0.0.114, commit 9aff0af49df3c50aed863430f98018e59f4635de:

- pkg/story/workflow.go exposes Service.GetJobRunStory, GetRestartContext, RestartRecipeJob, ListWorkflows and artifact-by-ordinal readers.
- pkg/story/internal/model/job_run_story.go defines JobRunStory, its recorded recipe source/commit, root, restart scopes and attempt structure. Nodes expose Input, Output, TaskOrdinal, InvocationID, Path, ArtifactRefs/ArtifactKeys, PriorAttempts and PastAttempts. TaskOrdinal locates outcomes; it is expressly not the restart target.
- pkg/story/live/recorder.go reconstructs the story from recorded outcomes. Activity invocation outputs expose their OpOutput and attached refs. Recipe root input contains the submitted arguments. Parent/aggregate nodes can repeat child information; only designated task records should become conversational entries.
- pkg/input/types.go and structured.go supply structured requests, response schemas, current-request validation, trusted Actor and accepted Receipt. pkg/recipejob/start.go supports explicit JobID, SubmittedAt, target, artifact refs and normal submissions.
- pkg/story/internal/model/restart_recipe_job.go accepts stable RequestID, source job, actual ScopeID or mutually exclusive chapter boundary, optional context patch, and goal mode.

These inspected structures support the replay representation. Subsequent retained-worker testing demonstrated the pending-input restoration defect documented below; the original inference that no dependency correction was needed is superseded. Source inspection alone does not prove real-worker retention, restart prefix availability or submission equivalence. The source checkout is not a deliverable or runtime mutation. `c2j cells list` requires absent .c2/config.yaml; that failure does not establish an indispensable gap.

The supplied c2ops research inspected pydantic/op.yaml and main.py at Nix source kfhhzsy30rb9yxnb1xnybr03gvgrazp0-source. It records required default_provider/default_model, a JSON-string response, optional response_schema, tool flags, 1,000-character file-text truncation, and metadata-only handling of non-text files. The current recipe and facade pin git+https://github.com/colony-2/c2ops.git//pydantic@f30202d13d197beaed7ba0bd0b120b04443728eb. The research path alone is not a publishable selector. Actual configured operation/provider/model execution and release evidence remain acceptance gates.

## Recipe and actual task data

Publish session.yaml and a small Go module in this cell. Submit with recipejob.SubmitRecipeJob using the configured root target and a separately pinned assistant recipe source. Worker configuration supplies credentials/provider/model. Caller JSON cannot override root, recipe, model, actor, tenant or limits.

Minimal start inputs remain:

```json
{
  "protocol": "cortex-assistant/1",
  "session_id": "host-generated-stable-id",
  "prompt": "What needs attention?",
  "source": {"kind":"review","job_id":"j","request_id":"r"},
  "attachments": {}
}
```

Source is exactly global, story(job_id), or review(job_id,request_id). Attachment bindings use existing stored artifacts or normal submission-bound bytes, never arbitrary URLs or local paths. Freeze session identity and the complete normal submission request, including explicit JobID and SubmittedAt, before submission; equivalent retries reuse it.

Recipe flow: capture → assemble model input → infer → validate/materialize response → await-human → send/confirm/finish. Each operation stores only its actual inputs and result through ordinary task recording. Transition payloads contain bounded source/turn locators, current proposal identity when needed, and artifact refs; never the accumulated discussion. Stable task roles distinguish capture, infer, response, await-human, prepare-action and dispatch. A task-role marker is interpreted only after validating the approved recipe identity/release and node path; arbitrary source/model text cannot forge it.

Authoritative records and their roles:

| Recorded execution data | Meaning in custom replay |
| --- | --- |
| Recipe root arguments | Initial user prompt, source selection and initial attachment bindings |
| Capture task output | Authorized contextual material actually supplied to this session, capture time and source/artifact identities |
| Successful await-human output | Accepted human command, bound attachments and c2j receipt/actor |
| Successful response-task output | Validated assistant text, document/file descriptors, model/usage identity and optional resolved proposal |
| Prepare-action output | Frozen target submission values authorized by the matching current confirmation |
| Dispatch-task output/error | Destination identity, submission result or unresolved/retryable outcome |
| Runtime status/current input/restart scope | Progress, finish state and available next actions |

Capture reads the exact authorized selected review/story once; replaced review identity is an error. Global selection does not dump the repository. Captured bounded text lives in the capture task output, not context.json. Capture output is a real computation result representing what was read, rather than a second persisted discussion. Navigation cannot replace it; later explicitly submitted attachments become additional sources. Context reads and model prompt construction obey existing access checks.

The response helper validates the model result and materializes allowed generated documents through ordinary task artifacts. Its task result contains only this response, not previous messages. Documents/files are the requested outputs, so they remain ordinary artifacts. Artifact publication failures expose incomplete/error task status rather than completed broken links. Raw model inference remains visible in generic job replay; the custom discussion shows only validated response-task results, with explicit error/progress rows when validation/materialization fails.

Model context assembly reads preceding accepted task results from the authorized execution replay. It uses the completed prefix preceding its own task boundary, excludes current/pending work and internal assembled-prompt copies, and never calls inference during replay reads. Ordinary task inputs may record the model prompt sent to that operation; this is actual execution data, not a separately maintained transcript. The model prompt is bounded and records omitted message/source locators explicitly. No mutable provider cache or worker-local files are required for continuity.

## Replay interpretation and presentation

The facade builds a transient view from story.Service.GetJobRunStory and retained restart ancestry. Do not save the view as an artifact or use a saved view as continuation state. A disposable cache may accelerate reads, but deleting it must not change rendered history, authorization, confirmation or restart behavior.

Walk only known leaf/task roles from the pinned recipe schema. Do not turn enclosing state/sequence/operation aggregates, rendered prompt copies, transition payloads or generic logs into extra messages. Use recorded execution order, not timestamps or lexical message IDs. Every displayed entry carries its originating execution/task locator and role. Accepted command identity is tied to the runtime request/receipt; validated response identity is tied to its committed task result. IDs are locators into actual history, not independently assigned transcript identities.

Show completed accepted human sends and validated assistant responses once. Confirm/finish receipts become control/action rows rather than extra user prose. Failed/pending tasks show explicit progress/error state. Retries remain inspectable as attempts, but the main discussion does not repeat a previously committed logical result. Distinct deliberate turns with identical text remain distinct. The source mapping must survive refresh, cache loss, worker replacement and restart lineage.

For restart descendants, use retained copied prefix data where supplied by c2j; resolve retained ancestor data where needed using recorded restart ancestry. Deduplicate only shared prefix origins, not equal text or equal task ordinals across different jobs. Divergent suffixes belong to their respective executions and must not leak into the reopened branch. Generic raw job replay remains available for inspection. Missing/incomplete history returns an explicit unavailable/incomplete response, not a guessed or silently complete discussion. Exact copied-prefix and ancestry mapping is a mandatory real-worker acceptance gate.

Get returns the current session summary and a recent bounded page of discussion entries. Replay returns older pages using opaque cursors bound to tenant/session, execution lineage and a fixed completed-history watermark. New tasks do not reorder existing pages; a refresh obtains a new watermark. Include has_more and an explicit incomplete/retention indication. Every retained entry must be reachable or reported unavailable; there is no silent truncation or total discussion-size cutoff.

The existing GetJobRunStory API returns a complete story, not a paginated task stream. Paging here bounds the public custom representation; it does not claim bounded backend memory, streaming or a new c2j pagination capability. Large-history read cost must be observed on the real worker/backend before release. If retained history cannot be reconstructed correctly with inspected primitives, report a minimal reproduction before requesting external work; do not add a shadow discussion artifact as fallback.

## Conversational protocol and model operation

Use existing c2j structured input with absent kind, request, response_schema and optional presentation. Replace the old conversation artifact reference with a replay locator:

```json
{
  "protocol":"cortex-assistant/1",
  "session_id":"s",
  "replay":{"job_id":"j","through_task":42},
  "assistant":{"text":"Here is the report.","artifact_ids":["recorded-output-artifact-id"]},
  "proposal":null
}
```

The locator is a read boundary, never authorization or a restart target. Latest assistant text is ordinary current-input task data/presentation, not a cumulative discussion copy. c2j owns enclosing request ID and receipts. Complete stored refs use artifacts.Ref with kind=stored and stored.key, validated against the pinned runtime; abbreviated examples are not executable fixtures.

Keep the closed command union:

- send: command, nonempty prompt, attachments by bound name.
- finish: command only.
- confirm: command, proposal_id and proposal_hash; offered only for the current valid pending proposal.

Reject extra fields, empty sends, stale identities, wrong recipe family and forged tenant/actor overrides. Respond uses SubmitStructuredResponse with the trusted host Actor, not legacy SubmitResponse. Source text cannot authorize actions or widen reads.

Keep c2ops pydantic with tools empty, execute_tools=false and enable_tool_execution=false. Assemble permitted UTF-8 text explicitly into the bounded prompt. Parse response JSON once and independently validate text, documents(safe name, text, text MIME), return_artifact_ids and optional create_goal(existing cell locator, exact prompt). Resolve returned IDs only against authorized capture/accepted attachment/response records. Models cannot forge refs or execute paths, shell commands, arbitrary recipe selectors or tenant values. Record model/usage in the actual response result.

Images, video, audio and PDFs remain downloadable within caps; the inspected operation does not establish native media understanding, transcription or new media generation. Generate text/Markdown/JSON documents. Preview safe supported MIME in Cortex, sanitize Markdown and never execute HTML/SVG; unsupported formats download. No range/seek guarantee is added.

## Facade and consumer ownership

Package contract retained: github.com/colony-2/cortex-assistant/assistant.

```go
func New(host Host, cfg Config) (*Client, error)
func (c *Client) Start(ctx context.Context, who Caller, in StartInput) (Session, error)
func (c *Client) List(ctx context.Context, who Caller, page PageRequest) (SessionPage, error)
func (c *Client) Get(ctx context.Context, who Caller, id string) (Session, error)
func (c *Client) Replay(ctx context.Context, who Caller, id string, page PageRequest) (ReplayPage, error)
func (c *Client) Respond(ctx context.Context, who Caller, id string, in input.StructuredSubmission) (input.Output, error)
func (c *Client) Reopen(ctx context.Context, who Caller, id string) (Session, error)
func (c *Client) OpenArtifact(ctx context.Context, who Caller, id, artifactID string) (ArtifactReader, error)
```

Host injects existing authorized workflowctl.WorkflowControl, story.Service, *input.Runtime, recipejob.Submitter and target/catalog/access resolution. Config fixes target, immutable recipe identity and effective caps. Caller contains trusted TenantID and input.Actor under the existing deployment trust model; Cortex's current cookie identity is not upgraded by this design. StartInput carries stable RequestID, source, prompt and attachment bindings. No database handle, new HTTP server or Cortex import belongs in this module.

Session includes logical ID, runtime job/goal refs and lineage, source, status, effective limits, current input and recent ReplayPage. ReplayPage includes entries with actual record locators, artifact descriptors, next cursor and completeness. These are transient response types, not persisted conversation formats. ArtifactReader includes io.ReadCloser, name, MIME and size; caller closes it. Typed errors distinguish forbidden/not found, stale input, conflict, limits, unavailable history/artifacts and not-at-prompt. Finalize compilable types against pinned c2j APIs during implementation.

List scans ordinary retained configured-root job history, validates recipe family and groups session/restart lineage. Include active/closed/failed executions; pending input alone is insufficient discovery. Preserve continuation even when filtering yields an empty page. OpenArtifact authorizes tenant and session membership against actual task artifact refs before reading bytes. Cortex handlers decode, bind trusted Caller, delegate and encode; UI renders the custom representation. Consumer integration is not prerequisite work for this build.

## Confirmation, finish and reopen

Resolve proposals to eligible existing catalog cells; show exact canonical target and full prompt from the successful response-task output, with stable proposal locator/hash. Recheck eligibility before first dispatch. Changed target/prompt requires a new proposal and fresh confirmation. Only a matching accepted current human confirmation receipt authorizes the normal existing-cell build recipe. No unconfirmed prose or model instruction dispatches work.

Persist the frozen normal BuildStartJobRequest as the prepare-action task output before dispatch, including explicit destination JobID derived from tenant/session/proposal identity, SubmittedAt, target and inputs. This is the actual submission operation's input, not a separate action ledger. Equivalent retries reuse it; ambiguous submission reconciles that ID. Conflicting payload fails, never generates a fresh destination. Record/link actual dispatch result. A deliberate later identical proposal has a new originating proposal identity. Confirm returns to input without another inference.

Finish is available at the current prompt, completes successfully with no model call and records only its normal runtime input receipt/terminal state. Tab close/navigation detaches. During infer/action work, show that Close becomes available at the next prompt. Closed presentation suppresses actionable proposals, but preserves their historical task data.

Reopen selects the last actual await-human restart scope from retained story, validates GetRestartContext, uses stable RequestID derived from tenant/session/source execution/scope and original recipe release, and requests goal.mode=new after successful finish. Resume scope entry, discard its former finish receipt and issue fresh current input identity. Inference/actions are outside that scope and must not replay. Concurrent/repeated reopen resolves one successor; a later finish/reopen selects the later execution's last prompt. Active sessions return current execution. Check actual failed-goal state before recovery; missing scope history reports unavailable.

Retain the prior reviewed clarification: an undispatched historical proposal can reappear after reopen, but requires fresh current confirmation and eligibility recheck. Old request/receipt submissions never authorize dispatch. A previously dispatched proposal cannot become a new action merely because history is rendered again. Replay is read-only and cannot invoke model, dispatch or restart operations.

## Limits and progress at boundaries

Effective maxima are the lower of the following product caps and actual configured backend/proxy/provider limits, advertised before uploads:

| Item | Maximum |
| --- | --- |
| Human prompt / assistant text | 32 KiB / 64 KiB UTF-8 |
| Assembled model prompt | 128 KiB, lower provider limit wins |
| Captured source text | 1 MiB, truncation marked |
| One input/returned file | 8 MiB |
| Files per send/reply | 8 |
| Aggregate file bytes per send/reply | 16 MiB |
| Encoded HTTP send | 24 MiB, lower existing route cap wins |
| Generated document | 1 MiB, within count/aggregate caps |
| Encoded custom replay response page | 4 MiB including metadata/escaping |

The former 4-MiB cumulative conversation publication cap is withdrawn because no cumulative publication exists. It becomes a response-page limit; it is not a c2j task-history limit or total-session limit. Preserve retained task history and page older entries; omission from model context must identify omitted locators, never silently summarize confirmations/actions.

Use actual encoded JSON sizes, including escaping and refs. Define bounded per-turn helper/result schemas before implementation. Documents live as file artifacts, with bounded descriptors in response outputs; no cumulative inline copies. Validate prompt/upload/count limits before structured input acceptance where possible; enforce closed schemas on direct input paths too. Acknowledged input receipts remain recorded even if downstream processing rejects invalid/overlarge content: render a bounded validation/limit error and return to a fresh input without inference/dispatch. Do not falsely label an already accepted runtime receipt rejected. Facade rejection before runtime submission has no accepted receipt. Tests distinguish these paths.

Validate model output and response metadata before materialization; overlarge/invalid output becomes a bounded error result and another prompt, without broken links or repeated automatic inference. For dispatch, bound inline outcome data and retain normal full task output/artifacts when needed; never drop or resubmit a committed action to shrink a view. Current prompt/finish does not depend on fitting total historical discussion into one response. A single entry exceeding page envelope limits is an explicit limit/schema error, not silent omission; bounded known record schemas must ensure supported entries fit. Generic runtime history may include larger internal prompts; the custom view selects actual relevant task fields, not every raw field.

No admission reservation for a growing discussion artifact remains. Storage failures use ordinary runtime retry/error semantics. At any read/format limit, preserve current input/finish independently of replay pagination. Reopen does not reset history or bypass per-turn caps. Retention expiry reports unavailable. Numeric memory guarantees, streaming changes and transport expansion remain excluded.

## Alternatives and compatibility

Adopt custom presentation of actual replay because it directly answers human feedback and removes two competing discussion representations. Reject cumulative conversation.json, persisted discussion exports as continuation state, snapshot journals and metadata indexes as authoritative sources. Generated user-requested documents/media are still artifacts; they do not become a conversation database.

Adopt existing c2j story replay rather than a new job-history API or process-capture change. It exposes the needed recorded data and restart scopes. The replay view itself is a derived response, necessarily, but never a separately persisted discussion or execution authority. Context capture and response validation remain real recipe tasks with ordinary outputs.

Keep the simple recipe and inspected pydantic operation instead of a Cortex model loop, new orchestrator or speculative media adapter. Preserve runtime identity/authorization and generic forms. Add Replay to the proposed facade and replace Session's persisted-conversation model with transient replay pages. There is no deployed module or stored assistant schema in this repository, so no data migration or deprecation window is required. Do not import/migrate old draft artifacts implicitly. Unknown recipe/protocol versions fail explicitly. Pins and compiled signatures remain release gates.

Supersede old tests that require conversation.json, context.json, cumulative publication-size rejection, conversation refs in transitions, or pre-receipt rejection of all direct submissions. Preserve their underlying durability, authorization, boundary progress and retry outcomes using actual task data. Supplied specialist findings remain satisfied by valid simultaneous file fixtures, fresh confirmation on reopen, bounded per-turn outputs and explicit finish progress; a new review must assess these changed mechanisms.

## Acceptance outcomes and verification

All outcomes below remain required gates; their enumeration does not claim passes. Existing fixture results cover only parts of this matrix. Record assistant/c2j/c2ops/Cortex identities and actual provider/model. Use configured real workers and retained storage, deterministic fault injection and at least one real-model path.

1. Start global, selected story and exact review sessions. Verify root/recipe/tenant, source/time/text, authorized attachments and no submission from opening composer. Replaced review errors.
2. Complete three turns, reload, remove caches and replace workers. Reconstruct the same discussion directly from recorded inputs/results/receipts; no discussion/context artifact or workspace file is needed.
3. For every visible entry, compare its role/content/order/artifact refs to originating task data. Verify aggregate/child duplication, prompt copies, failed attempts, identical distinct turns and incomplete histories are handled correctly.
4. Generate text/Markdown/JSON documents; return existing file, PNG, MP4 and audio bytes; download after reload/replacement and compare bytes. Test safe previews, sanitization and unsupported downloads.
5. Test exact and excess prompt/text/source/model/document/file/count/aggregate/upload limits, encoded JSON escaping, mismatched declared sizes and retention loss. Eight small files, one 8-MiB file and two 8-MiB files are independent valid fixtures; eight 8-MiB files fail aggregate limits.
6. Exceed one custom replay page while keeping each turn valid. Traverse every retained entry once under a stable watermark; refresh after concurrent append. No synthetic discussion artifact, silent history loss or total-history send cutoff. Finish/reopen remains possible at page boundaries. Distinguish facade rejection without receipt from direct accepted-input validation errors, checking model/dispatch counts.
7. Finish at input with zero model calls. Reopen concurrently/repeatedly: one successor/new goal/fresh input, retained prefix and no repeated committed reply/action. Repeat at a later prompt. Inspect copied-prefix/ancestor mapping and divergent suffixes; missing history never fabricates continuity.
8. Publish proposal, finish, reopen; historical proposal may be offered again, but old confirmation fails. Fresh confirmation requires exact target/prompt/hash and eligible existing cell; source injection, forged/stale input and changed eligibility dispatch nothing.
9. Confirm, interrupt after target submission before result recording, then retry/restart. One destination identity/frozen payload/link survives ambiguity and concurrent confirmation. Conflicting payload errors without a fresh ID. Replay/Get/List/downloads never dispatch.
10. Across two tenants/actors, forge source/session/artifact refs and cursor boundaries. Check all facade operations, current input, replay paging and membership authorization before bytes/submissions.
11. List active/closed/failed sessions across paginated history and lineage, including empty filtered pages. Verify standalone facade compilation and use without Cortex/database imports.
12. Run real-worker Cortex browser start/follow-up/download/reload/finish/reopen and custom replay presentation. REST delegates policy. Preserve generic/review/input/restart regressions in development and embedded UI.

If a required replay/runtime outcome fails, first correct local recipe/helper use. Then report exact versions, minimal reproduction and observed-versus-required behavior before requesting a serving change. Never silently fall back to a discussion artifact or weaken required feature scope.

## External prerequisite requests — revised after demonstrated runtime failure

Agreed prerequisite keys: `["c2j-pending-input-restoration"]`.

Serving owner: c2j, https://github.com/colony-2/c2j.git, main inspected at a501d923f05378adc54d04bd87a689ee291f7afb. The completed consultation agrees this correction; it does not approve or execute a build. Current context has no approved prerequisite or submitted job. Renewed approval is required before submission.

The actual SQLite worker large-history gate fails on its first supported 64-KiB escaped response: the generated request is recorded, but GetDetails loses its identity after invocation input offload. The independent public-API reproduction confirms RestoreInput recovers the same request. The serving owner found no supported standard Runtime configuration or caller sequence that repairs this recorded invocation.

Require pending-input readers to call ActivityInvocationRequest.RestoreInput before ExternalOutput and retain the returned artifact slice. Apply the same rule to the duplicate management reader, preferably through compatible delegation. Discovery inherits the runtime correction. Restoration failure must return an error before decoding or finishing; preserve cancellation and underlying causes. Private transport artifacts must not escape into public bindings/completed output. Preserve unrelated artifacts, restored application references, frozen identity/schema, exact occurrence completion, tenant checks, trusted actors, validation, receipts and conflict semantics. Legacy submissions must continue rejecting structured/review requests.

Serving acceptance requires inline and offloaded identity/request/schema equality, escaped 64-KiB forms, memory and SQLite-reloaded lazy artifacts, discovery/management reads, valid structured submission and resumed send/finish, negative identity/schema/actor/legacy submissions, unavailable/malformed payloads, cancellation and artifact privacy. The requesting assistant must then rerun its retained-worker large-history paging/append/finish/reopen gate. The complete accepted scope is attached as pending-input-agreement.md.

Adopt the existing restoration primitive rather than a new transport or assistant-side WorkflowControl wrapper: it corrects the shared runtime defect and preserves existing public APIs. Reject smaller product limits, truncated forms, bypassed structured submission and capacity-only workarounds because they fail approved behavior. No threshold, JobDB dependency, schema, trusted actor source, assistant policy, session service or process-capture change is proposed.

All preceding assistant feature scope, compatibility requirements and acceptance outcomes remain unchanged. Once the serving build is approved and completed, consume its immutable corrected version and record the pin and regression evidence. No speculative version is selected now. Cortex adoption remains consumer work, not this build's prerequisite. This revision requests renewed approval of the preserved design with this sole additional prerequisite; agreement is not acceptance of the assistant implementation.

## Review evidence, preserved expectations and execution order

The current module pins c2j v0.0.114 and JobDB v0.0.65-0.20261029235121-6d86c38fc234. Inspection confirms session.yaml delegates inference to the pinned pydantic operation and assistant/client.go delegates submission/input/restart to existing c2j interfaces. The serving agreement identifies both runtime and management readers calling ExternalOutput without restoring offloaded input. This is an indispensable runtime correction, not a request for a broader history or transport redesign.

Evidence used in order: current PURPOSE.md and public facade/recipe/module interfaces; supplied replay design and test statements; independent assessment in iter1/consultant/review.md; supplied verification.json, test-results.log and runtime-gap_test.go; completed serving agreement in consultations/c2j-pending-input-restoration/iter1/consultant/agreement.md; current context.json coordination state. design-context.md reports no before-design hook, and build-settings.json adds no variables or specialist consultations. No new runtime tests were executed during this design-only turn.

The independent review confirms the text-attachment inference and false dispatch-success defects were repaired. Preserve their regressions: permitted authorized text bytes must reach inference with bounded omissions, and handled dispatch errors must remain errors without false success. The review's former always-allow fixture observation predates the later denying callback coverage; neither fixture authorizes a claim of production membership certification.

Preserve the supplied test-statements.md expectations. The additional prerequisite acceptance above extends coverage without deleting or weakening existing tests. The file fixtures remain independent valid combinations under simultaneous caps; restored historical proposals still require fresh current confirmation; near-page-boundary finish/reopen remains required. No new deprecation or persisted-data migration is introduced by restoration. Earlier transcript/publication draft expectations remain superseded only as described in Alternatives and compatibility.

Current evidence is incomplete: the full suite fails at TestWorkerLargeHistory before later paging/control assertions; observation-test success reproduces the bug. Prior fixture suite/race successes do not satisfy the enabled gate. Immutable assistant release, real model/usage, deployed story/review capture and media/expiry/limits, production membership matrix, repeated interruption/divergent lineage, and Cortex browser/development/embedded regressions remain open. These require suitable deployment evidence; they are not additional prerequisite builds or waived requirements.

Approval sequence: obtain renewed approval for this design and its single agreed prerequisite; submit its exact key only when approved and only if no job already exists; inspect the serving outcome and immutable corrected dependency; update the local pin during implementation; execute positive restoration regressions, the retained-worker large-history gate and appropriate repository checks; complete outstanding release and consumer evidence before outcome acceptance. Current context has no approved prerequisite, plan key or job. No new cell or repeated consultation is needed for the already agreed scope.

## Human execution clarification

The latest human feedback directs this cell to build and test with embedded c2j and JobDB, with Cortex solely a consumer. Accordingly, this build does not require Cortex checkout/browser runs or a production deployment configuration to execute its local acceptance suite. Earlier consumer-browser matrix entries describe consumer-owned compatibility work, not a gate requiring this cell to use Cortex. Keep facade isolation and the existing runtime compatibility expectations; do not modify the consumer or request its environment as a prerequisite. Deterministic model fixtures remain identified honestly and do not establish real-provider execution.
