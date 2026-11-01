# Host composition contract

The module exports `NewActivity(client, ActivityOptions)` and
`RegisterActivity(registry, activity)`. Construct Client from the host's existing
Goals, History, Control, Story, Input, Submitter, authorization and target resolver.
Options supply trusted provider/model names and execution authorization. No second
remote connection, configuration file or runtime is created by the activity.

The recipe names operation `cortex_assistant`; c2j expands its `run` step to task
route `cortex_assistant:run`. RegisterActivity installs the host executable in its
ActivityRegistry and a stateless fail-closed Definition in the compiler's process
metadata registry. The latter contains no Client or authorization/model state.
Registries built after metadata registration initially contain that fail-closed
definition; RegisterActivity replaces only that exact definition locally. Foreign
or already installed host activities conflict. No unrelated operation is replaced.
Registration is a startup operation before WorkSet construction, not concurrent
with execution. NewActivity itself has no registration side effects.

Inputs use typed ActivityInput with closed decoding and action checks. TaskResult
is canonically encoded to the existing JSON object output shape because upstream
facade request structs lack the schema tags required by the activity registrar.
Existing policy validation bounds each result; this is not a second transcript.
The adapter validates CurrentJobContext coordinates, action/path, the immutable
recipe/protocol descriptor and the injected session authority before Worker.
Documents use AddOutputArtifact and publication failures retain their cause and
return no completed response output.

The execution authorizer must return the principal authorized to execute this
session, not simply trust a tenant, cell catalog or browser actor. Agent authority
cannot produce a human confirmation receipt. HTTP request identity must come from
the deployment authentication boundary; Cortex's email cookie is not sufficient.
Provider/model and existing secret bindings are deployment-owned, never model or
browser overrides. Missing authority/capability fails explicitly.

Cortex adoption is separate consumer work at cortexcli.serve, shared by production
and development. Application composition owns the shared resources, registers the
actual activity before building the ordinary WorkSet and installs exactly one
executable recipe worker supporting replay. Resolve NewServer's read-only replay
registration explicitly; preserve history-only construction. Do not rely on
registration order or put host-bound Clients in a process-global replay registry.

Use executionruntime with consistent allocation, StageExecution and
WrapTaskWorker on all live tasks, including extensions/model. Keep tenant,
source/workspace/secret and allocation constraints. Start one engine runner and
HTTP only after successful composition. Coordinate failures, bound HTTP drain,
cancel/join execution and then close owned resources. Clean up partial startup;
externally owned resources remain the caller's responsibility. A handler starts
no runner and mutates no registry.

Legacy v1 release descriptors approve exact immutable selectors and protocols.
History is read using its own helper selector; v1 extension output is never
interpreted as v2 activity output. Reopen defaults unavailable for legacy routes;
enable it only when the original executable route is actually supported. New
submissions always use v2. No pending task or committed action is migrated.

Candidate tests must name the exact tree and use the actual registered activity
without helper interception or CORTEX_ASSISTANT_CONFIG. Model fixtures support
negative tests; actual provider/usage/provenance and artifact hashes remain a
separate requirement. After authorized publication, verify the resulting immutable
selector through that same route and record its relationship to the candidate.
Local suite success is not published release or Cortex integration acceptance.

The opt-in `TestWorkerActualProvider` runs the same guarded registered activity
and the pinned pydantic extension without helper or model interception. Supply
`ASSISTANT_REAL_MODEL`, `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and an absolute
`ASSISTANT_PROVIDER_EVIDENCE` output path from trusted host bindings. The ordinary
suite skips this external check unless a model is explicitly selected. Do not put
credential values in recipe inputs or checked-in files. The evidence records the
validated response, usage, local recipe binding, replay provenance and hashes of
bytes downloaded through the facade. It does not establish published-selector
or Cortex consumer acceptance.
