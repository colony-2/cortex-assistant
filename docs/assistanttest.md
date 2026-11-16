# Assistant fixtures and turn checks

Use `github.com/colony-2/cortex-assistant/assistanttest` at the same immutable module
revision as `assistant`. You can keep your existing Host and runner. The fixture
and assertions install no engine, operations or host configuration.

`PrepareFixture(ctx context.Context, opts FixtureOptions) (Fixture, error)` checks
production recipe/helper/module bytes shipped with the module, replaces only the
pinned inference leaf, and commits a separate fixture F. Supply the exact 40-hex
production `SourceCommit` C. For a verified source identity, also supply its clean
`SourceDirectory`; every relevant Git blob must match the shipped bytes. Without
that checkout, C is your supplied identity, not a verified publication claim.
`Fixture.SourceVerified` distinguishes a checked checkout from a supplied identity.
`Fixture.ProductionCommit` records C; `Commit`, `Selector` and `Config(root,
cursorKey)` select F. `ProductionConfig(commit, root, cursorKey)` selects the real
production recipe. Never map C to modified fixture bytes.

`Fixture.Profile` is a versioned Git transport profile mapping the canonical
assistant repository to a disposable loopback Git server. Install it through
existing tenant Git profile configuration in disposable storage; do not change a
real tenant. The caller owns `Fixture.Close() error`, including the directory and
transport. It is idempotent. `ScratchDirectory` optionally controls where the
fixture is created. `CheckFixture(directory string) error` rejects undeclared
production, helper, module, manifest, schema, dependency and stub drift.

`Scenario{Replies: []Reply{{User: "open", Response: responseJSON}}}` declares finite
required replies for distinct exact latest user messages. The stub returns only
the production inference `response`, `model` and `usage` shape. It does not drive
input, create human receipts or implement assistant acceptance. Missing, repeated
or unexpected calls fail. `CheckScenario(ctx, inspector, scenario, jobs...) error`
requires each reply's actual inference outcome once across the supplied jobs.
`NativeMocks(scenario Scenario) []recipetest.OpMock` provides ordered matches for
the real production selector and node. Use normal c2j native execution and only
path-specific passthrough mocks for helpers/policy. Unmatched invocations and
unused required mocks fail; native preparation still needs the real manifest and
dependencies. The fixture avoids production inference preparation in consumer
runs, while retaining helper/Go dependencies.

`AwaitPrompt(ctx, host, client, caller, session, job, afterRequest)` returns
`(assistant.Session, error)` for a new real prompt in the expected job. Pass a
bounded context. Errors and terminal jobs propagate. A safe validation-error
prompt is observable but does not establish a successful reply.

`CheckTurn(ctx, inspector, client, caller, session, turn)` returns
`(TurnEvidence, error)`. Identify either `Turn{JobID: job, Protocol:
assistant.GoalProtocol, Opening: true}` or a specific actual `Receipt`. The check
reads all task/replay pages, requires that receipt's real input and acceptance,
its own nonempty error-free response before the next real form, matching
citations/artifacts and a succeeded replay producer. Evidence retains actual task
occurrence IDs, attempts, receipt, response, form and replay origin. Earlier
replies cannot substitute. `errors.Is(err, assistanttest.ErrMissingValidReply)`
identifies a completed turn reaching its next form without a valid reply. Its
message names job/request/submission. Read errors and unfinished turns remain
distinct. Assertions own no resources.

`Compose(ctx context.Context, opts Options) (*Harness, error)` is optional. Supply
`Scoped`, `Schemas`, `Goals`, `Tenant`, `Registry`, `Config`, `Authorize` and
`ResolveCell` from your normal composition. Initialize ordinary built-in ops once
in your application. Compose never changes the global registry. `WorkerOptions`
retains supplied worker wrappers, source resolver and staging; scoped allocation
and guards remain active. `AdaptHost` preserves supplied host fault wrappers
before client construction. Harness exposes `Host`, `Config`, `Client`, `Engine`,
`Control` and `Input` and runs one owned worker runner.

`Harness.Close(ctx) error` cancels and joins that runner before closing callbacks
explicitly transferred through `Options.Owned`, in reverse order. Partial startup
also cleans up, cleanup errors propagate and repeated closure is safe. Supplied
storage, adapters, HTTP server and fixture transport remain borrowed unless you
explicitly transfer their closure. Drain caller HTTP first, close the harness,
then close caller-owned transports/adapters/storage. Existing trusted goal
selection authority is still required for Reopen, including remote middleware;
ordinary submission authority alone does not grant it.

The [compiled external examples](../assistanttest/example_test.go) show borrowed
assertions and optional composition. The tests execute three-turn valid and
citation-only negative conversations on SQLite and the real JobDB remote adapter,
keep wrappers active, update configuration between turns, inspect recorded
snapshots, remove inference settings before retained reads/Reopen, and check that
Reopen starts at input without inference. Production candidate/provider evidence
and unpublished fixture evidence have separate identities. Neither proves Cortex
browser integration, public publication or live-provider behavior.
