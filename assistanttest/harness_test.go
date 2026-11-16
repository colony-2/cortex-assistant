package assistanttest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/execution"
	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/gitprofile"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobinspect"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/ops/extensions"
	recipeops "github.com/colony-2/c2j/pkg/ops/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/swfutil"
	coretask "github.com/colony-2/c2j/pkg/task"
	workerexport "github.com/colony-2/c2j/pkg/worker/export"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
	"github.com/colony-2/jobdb/pkg/jobdb/runtime/remote"
	sqliteruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/sqlite"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

func TestMain(m *testing.M) {
	// Normal application operation initialization, once per test process. Neither
	// Compose nor any scenario changes this registry.
	coreops.Replace(append(append(workerexport.GetAll(), extensions.GetExecutionOp(), input.GetOp()), recipeops.GetOps()...)...)
	os.Exit(m.Run())
}

type secretProvider struct {
	disabled atomic.Bool
	calls    atomic.Int64
}

func (p *secretProvider) ResolveSecrets(_ context.Context, r jobdb.SecretResolveRequest) (jobdb.SecretResolveResult, error) {
	p.calls.Add(1)
	if p.disabled.Load() {
		return jobdb.SecretResolveResult{}, errors.New("secret lookups disabled")
	}
	out := jobdb.SecretResolveResult{Secrets: map[string]jobdb.SecretValue{}}
	for _, n := range r.Names {
		if n == "codex_api_key" {
			out.Secrets[n] = jobdb.SecretValue{Value: "private-assistanttest-key"}
		}
	}
	return out, nil
}
func TestOrdinaryCompositionLocalRemoteAndTurns(t *testing.T) {
	for _, adapter := range []string{"local", "remote"} {
		for _, mutation := range []bool{false, true} {
			t.Run(adapter+map[bool]string{false: "-valid", true: "-forged-final"}[mutation], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(celltest.Context(context.Background()), 180*time.Second)
				defer cancel()
				selectGoal := func(_ context.Context, g jobdb.GoalKey, target jobdb.ResolvedCell) error {
					if g.TenantId != "tenant" || target.Cell != "root" {
						return jobdb.ErrPrimaryGoalSelectionForbidden
					}
					return nil
				}
				ctx = jobdb.WithPrimaryGoalSelectionAuthorizer(ctx, selectGoal)
				source, c := executionSource(t)
				response := `{"text":"fixture validated reply","documents":[],"return_artifact_ids":[],"citations":[]}`
				final := response
				if mutation {
					final = `{"text":"fixture validated reply","documents":[],"return_artifact_ids":[],"citations":["forged-third-turn-source"]}`
				}
				scenario := Scenario{Replies: []Reply{{User: "open", Response: response}, {User: "next", Response: response}, {User: "final", Response: final}}}
				f, err := PrepareFixture(ctx, FixtureOptions{SourceCommit: c, SourceDirectory: source, Scenario: scenario})
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				secrets := &secretProvider{}
				rt, err := sqliteruntime.NewFromConfig(ctx, sqliteruntime.Config{DBPath: filepath.Join(t.TempDir(), "borrowed.db"), BlobDir: t.TempDir(), SecretProvider: secrets})
				if err != nil {
					t.Fatal(err)
				}
				defer rt.Close(context.WithoutCancel(ctx))
				_, err = rt.CreateCell(ctx, jobdb.CreateCellRequest{TenantID: "tenant", Name: "root", Repo: "github.com/colony-2/cortex-assistant"})
				if err != nil {
					t.Fatal(err)
				}
				set := func(name string, value any) {
					t.Helper()
					b, _ := json.Marshal(value)
					if _, err := rt.SetTenantConfigurationValue(ctx, jobdb.SetTenantConfigurationValueRequest{TenantID: "tenant", Name: name, Value: b}); err != nil {
						t.Fatal(err)
					}
				}
				set(gitprofile.ReadOnlyKey, "fixture")
				set(gitprofile.ProfilePrefix+"fixture", f.Profile)
				set("codex_api_base_url", "https://before.invalid/root")
				set("codex_model", "before")
				var runtimeClient jobdb.WorkflowRuntime = rt
				var schemas jobdb.JobSchemaRegistry = rt
				if adapter == "remote" {
					// Keep the server's lease signer for the whole HTTP lifetime.
					// A fresh handler per request cannot validate previously issued leases.
					handler := remote.NewServer(rt)
					server := httptest.NewServer(celltest.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						handler.ServeHTTP(w, r.WithContext(jobdb.WithPrimaryGoalSelectionAuthorizer(r.Context(), selectGoal)))
					})))
					defer server.Close()
					sdk, e := remote.New(server.URL, server.Client())
					if e != nil {
						t.Fatal(e)
					}
					runtimeClient = sdk
					schemas = sdk
				}
				registry, err := workerops.NewActivityRegistry()
				if err != nil {
					t.Fatal(err)
				}
				root := recipejob.ResolvedTarget{CellName: "root", CellRepo: "github.com/colony-2/cortex-assistant", RepositorySource: "https://github.com/colony-2/cortex-assistant.git", DefaultRef: "main"}
				var wrapped atomic.Int64
				var hostCalls atomic.Int64
				var closed atomic.Int64
				opts := Options{Scoped: executionruntime.New(runtimeClient, execution.Allocation{SchemaVersion: 1}), Schemas: schemas, Goals: runtimeClient, Tenant: "tenant", Registry: registry, Config: f.Config(root, []byte(strings.Repeat("k", 32))), Authorize: func(context.Context, assistant.Caller, string) error { return nil }, ResolveCell: func(context.Context, assistant.Caller, string) (recipejob.ResolvedTarget, error) { return root, nil }, Owned: []func(context.Context) error{func(context.Context) error { closed.Add(1); return nil }}}
				opts.AdaptHost = func(host assistant.Host) (assistant.Host, error) {
					original := host.Authorize
					host.Authorize = func(ctx context.Context, c assistant.Caller, target string) error {
						hostCalls.Add(1)
						return original(ctx, c, target)
					}
					return host, nil
				}
				opts.WorkerOptions.WrapTaskWorker = func(w jobworkflow.TaskWorker) jobworkflow.TaskWorker {
					return countedWorker{TaskWorker: w, count: &wrapped}
				}
				h, err := Compose(ctx, opts)
				if err != nil {
					t.Fatal(err)
				}
				defer h.Close(context.WithoutCancel(ctx))
				caller := assistant.Caller{TenantID: "tenant", Actor: input.Actor{Kind: "human", ID: "consumer"}}
				at := time.Unix(100, 0)
				snapshot := assistant.FrozenSnapshot{TenantID: "tenant", Source: assistant.Source{Kind: "global"}, At: at, Documents: []assistant.SnapshotDocument{}}
				frozen, err := h.Client.PrepareStartFrozen(ctx, caller, assistant.StartInput{RequestID: "start", SubmittedAt: at, Source: snapshot.Source, Prompt: "open"}, snapshot, assistant.SnapshotSelection{TenantID: "tenant", Source: snapshot.Source, At: at, Documents: map[string]assistant.SnapshotDocument{}})
				if err != nil {
					t.Fatal(err)
				}
				if secrets.calls.Load() != 0 {
					t.Fatal("construction/preparation resolved secret")
				}
				set("codex_api_base_url", "https://first.invalid/exact/%2f/")
				set("codex_model", "first")
				session, err := h.Client.SubmitFrozenStart(ctx, caller, frozen)
				if err != nil {
					t.Fatal(err)
				}
				ready, err := AwaitPrompt(ctx, h.Host, h.Client, caller, session.ID, session.JobID, "")
				if err != nil {
					rows, e := h.Engine.ListJobs(ctx, jobdb.ListJobsRequest{TenantIds: []string{"tenant"}, CellNames: []string{"root"}, GoalIDs: []string{session.ID}})
					b, _ := json.Marshal(rows)
					t.Fatalf("prompt: %v session=%+v root=%+v jobs=%s error=%v", err, session, h.Config.Root, b, e)
				}
				api := &jobinspect.Service{Runtime: runtimeClient, Tenant: "tenant"}
				opening, err := CheckTurn(ctx, api, h.Client, caller, session.ID, Turn{JobID: session.JobID, Protocol: assistant.GoalProtocol, Opening: true})
				if err != nil {
					t.Fatal(err)
				}
				proofs := []TurnEvidence{opening}
				negativeProof := TurnEvidence{}
				mutationError := ""
				finalBoundary := Turn{}
				var contract *nativeContract
				if adapter == "local" && !mutation {
					n := runNativeContract(t, ctx, source, scenario.Replies[0])
					contract = &n
				}
				openingForm := ready.Current.Form
				for i, user := range []string{"next", "final"} {
					set("codex_api_base_url", "https://later.invalid/unchanged/")
					set("codex_model", "later")
					request := *ready.Current.Form.RequestId
					receipt, e := h.Client.Respond(ctx, caller, session.ID, input.StructuredSubmission{RequestID: request, SubmissionID: user, Response: map[string]any{"command": "send", "prompt": user}})
					if e != nil {
						t.Fatal(e)
					}
					if contract != nil && i == 0 {
						run, e := h.Host.History.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: jobdb.JobKey{TenantId: "tenant", JobId: session.JobID}, IncludeInputs: true, IncludeOutputs: true})
						if e != nil {
							t.Fatal(e)
						}
						tasks, e := swfutil.ExecutionTasks(run)
						if e != nil {
							t.Fatal(e)
						}
						var schema any
						for _, task := range tasks {
							for _, attempt := range task.Attempts {
								if attempt.Output == nil {
									continue
								}
								var envelope coretask.OutputEnvelope
								var output workerops.ActivityInvocationOutput
								if json.Unmarshal(attempt.Output.Data, &envelope) != nil || json.Unmarshal(envelope.Payload, &output) != nil {
									continue
								}
								var assembled struct {
									Kind       string         `json:"kind"`
									ModelInput map[string]any `json:"model_input"`
								}
								if func() error { b, _ := json.Marshal(output.OpOutput); return json.Unmarshal(b, &assembled) }() == nil && assembled.Kind == "assemble" {
									schema = assembled.ModelInput["response_schema"]
									break
								}
							}
						}
						compareNativeConsumer(t, *contract, openingForm, *receipt.Receipt, schema)
					}
					ready, e = AwaitPrompt(ctx, h.Host, h.Client, caller, session.ID, session.JobID, request)
					if e != nil {
						t.Fatal(e)
					}
					finalBoundary = Turn{JobID: session.JobID, Protocol: assistant.GoalProtocol, Receipt: receipt.Receipt}
					proof, e := CheckTurn(ctx, api, h.Client, caller, session.ID, finalBoundary)
					if mutation && i == 1 {
						if !errors.Is(e, ErrMissingValidReply) || e.Error() != "missing valid reply in job "+session.JobID+" after "+request+"/final" {
							t.Fatalf("wrong rejection: %v", e)
						}
						negativeProof = proof
						mutationError = e.Error()
						var prompt assistant.PromptRequest
						if ready.Current.Form.Request == nil {
							t.Fatal("missing safe form")
						}
						b, _ := json.Marshal(*ready.Current.Form.Request)
						if json.Unmarshal(b, &prompt) != nil || prompt.Assistant.Error != "The model response could not be validated." {
							t.Fatal("missing real validation failure")
						}
					} else {
						if e != nil {
							t.Fatal(e)
						}
						proofs = append(proofs, proof)
					}
				}
				if err = CheckScenario(ctx, api, scenario, session.JobID); err != nil {
					t.Fatal(err)
				}
				// Inspect the actual policy outcomes and execution snapshot; earlier results
				// must keep first, later turns later. Secret plaintext must stay out of history.
				run, err := h.Host.History.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: jobdb.JobKey{TenantId: "tenant", JobId: session.JobID}, IncludeInputs: true, IncludeOutputs: true, IncludeArtifacts: true, IncludeAttemptInputs: true})
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(run)
				if strings.Contains(string(raw), "private-assistanttest-key") {
					t.Fatal("secret retained inline")
				}
				if !strings.Contains(string(raw), "https://first.invalid/exact/%2f/") || !strings.Contains(string(raw), "https://later.invalid/unchanged/") {
					t.Fatal("execution snapshots missing")
				}
				// Verify real selected/resolved F, rather than altered production bytes under C.
				page, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: session.JobID, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				resolved := false
				for _, task := range page.Tasks {
					if strings.Contains(task.Type, "source_resolve") {
						field := ""
						v, e := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: session.JobID, Task: task.ID, Attempt: task.Attempt, Field: &field})
						if e != nil {
							t.Fatal(e)
						}
						var value map[string]any
						json.Unmarshal(v.Value, &value)
						if value["submitted_selector"] == f.Selector {
							if value["resolved_commit"] != f.Commit {
								t.Fatal("wrong resolved fixture")
							}
							resolved = true
						}
					}
				}
				if !resolved {
					t.Fatal("missing fixture resolution")
				}

				assertPolicySnapshots(t, run, []string{"first", "later", "later"})
				// Retained reads and sequential Reopen must not resolve the model
				// settings or key. The successor begins at input, without inference.
				for _, name := range []string{"codex_api_base_url", "codex_model"} {
					if _, err = rt.RemoveTenantConfigurationValue(ctx, jobdb.RemoveTenantConfigurationValueRequest{TenantID: "tenant", Name: name}); err != nil {
						t.Fatal(err)
					}
				}
				beforeLookups := secrets.calls.Load()
				secrets.disabled.Store(true)
				if _, err = h.Client.Get(ctx, caller, session.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = h.Client.Replay(ctx, caller, session.ID, assistant.PageRequest{}); err != nil {
					t.Fatal(err)
				}
				if _, err = h.Client.Close(ctx, caller, session.JobID); err != nil {
					t.Fatal(err)
				}
				for {
					state, e := h.Control.InspectJob(ctx, jobdb.JobKey{TenantId: "tenant", JobId: session.JobID})
					if e != nil {
						t.Fatal(e)
					}
					if state.Terminal {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(20 * time.Millisecond):
					}
				}
				reopened, err := h.Client.PrepareReopen(ctx, caller, session.JobID, "reopen", time.Unix(200, 0))
				if err != nil {
					t.Fatal(err)
				}
				successor, err := h.Client.SubmitFrozenStart(ctx, caller, reopened)
				if err != nil {
					t.Fatal(err)
				}
				if successor.ID != session.ID || successor.JobID == session.JobID {
					t.Fatal("wrong reopen lineage")
				}
				if _, err = AwaitPrompt(ctx, h.Host, h.Client, caller, session.ID, successor.JobID, ""); err != nil {
					t.Fatal(err)
				}
				if err = CheckScenario(ctx, api, scenario, session.JobID, successor.JobID); err != nil {
					t.Fatal(err)
				}
				if secrets.calls.Load() != beforeLookups {
					t.Fatal("retained read/reopen resolved secrets")
				}
				if wrapped.Load() == 0 || hostCalls.Load() == 0 {
					t.Fatal("supplied task wrapper was bypassed")
				}
				if err = h.Close(context.WithoutCancel(ctx)); err != nil {
					t.Fatal(err)
				}
				if err = h.Close(context.WithoutCancel(ctx)); err != nil || closed.Load() != 1 {
					t.Fatal("cleanup not idempotent", err)
				}
				if _, err = rt.GetTenantConfiguration(ctx, "tenant"); err != nil {
					t.Fatal("borrowed storage closed", err)
				}
				if err = CheckFixture(f.Directory); err != nil {
					t.Fatal("borrowed transport/source closed", err)
				}
				if evidence := os.Getenv("ASSISTANT_VERIFY_EVIDENCE"); evidence != "" {
					dir := filepath.Join(evidence, "assistanttest", filepath.FromSlash(t.Name()))
					if err = os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					b, _ := json.MarshalIndent(map[string]any{"production_commit": c, "fixture_commit": f.Commit, "fixture_selector": f.Selector, "resolved_fixture": resolved, "adapter": adapter, "mutated": mutation, "turn_proofs": proofs, "final_turn": finalBoundary, "negative_proof": negativeProof, "mutation_error": mutationError, "reopened_job": successor.JobID, "original_job": session.JobID, "worker_wrapper_calls": wrapped.Load(), "host_wrapper_calls": hostCalls.Load(), "resource_close_calls": closed.Load()}, "", "  ")
					if err = os.WriteFile(filepath.Join(dir, "conversation.json"), b, 0600); err != nil {
						t.Fatal(err)
					}
				}
				t.Logf("C=%s F=%s adapter=%s validated-turns=%d", c, f.Commit, adapter, len(proofs))
			})
		}
	}
}
func TestPartialStartupCleanup(t *testing.T) {
	calls := 0
	_, err := Compose(context.Background(), Options{Owned: []func(context.Context) error{func(context.Context) error { calls++; return errors.New("owned cleanup failure") }}})
	if err == nil || !strings.Contains(err.Error(), "owned cleanup failure") || calls != 1 {
		t.Fatal("partial startup cleanup", err, calls)
	}
}

type countedWorker struct {
	jobworkflow.TaskWorker
	count *atomic.Int64
}

func (w countedWorker) Run(ctx jobworkflow.TaskContext, data jobworkflow.TaskData) (jobworkflow.TaskData, error) {
	w.count.Add(1)
	return w.TaskWorker.Run(ctx, data)
}

func assertPolicySnapshots(t *testing.T, run jobdb.GetJobRunResponse, models []string) {
	t.Helper()
	actual := []string{}
	for _, entry := range run.Execution {
		r := entry.Record
		if r.Kind != "result" || r.Output == nil {
			continue
		}
		var envelope coretask.OutputEnvelope
		if json.Unmarshal(r.Output.Data, &envelope) != nil {
			continue
		}
		var output workerops.ActivityInvocationOutput
		if json.Unmarshal(envelope.Payload, &output) != nil || output.Configuration == nil {
			continue
		}
		var model, base string
		if json.Unmarshal(output.Configuration.Values["codex_model"], &model) != nil {
			continue
		}
		if json.Unmarshal(output.Configuration.Values["codex_api_base_url"], &base) != nil {
			t.Fatal("missing endpoint snapshot")
		}
		wantURL := "https://later.invalid/unchanged/"
		if len(actual) == 0 {
			wantURL = "https://first.invalid/exact/%2f/"
		}
		if base != wantURL {
			t.Fatal("changed recorded endpoint", base)
		}
		actual = append(actual, model)
	}
	leafModels := []string{}
	for _, entry := range run.Execution {
		r := entry.Record
		if r.Kind != "result" || r.Input == nil {
			continue
		}
		var request workerops.ActivityInvocationRequest
		if json.Unmarshal(r.Input.Data, &request) != nil || request.Input["selector"] != "./ops/assistanttest-inference" {
			continue
		}
		inputs, ok := request.Input["inputs"].(map[string]any)
		if !ok {
			t.Fatal("missing leaf inputs")
		}
		api, ok := inputs["api"].(map[string]any)
		if !ok {
			t.Fatal("missing leaf API")
		}
		wantURL := "https://later.invalid/unchanged/"
		if len(leafModels) == 0 {
			wantURL = "https://first.invalid/exact/%2f/"
		}
		model, ok := inputs["default_model"].(string)
		if !ok || api["base_url"] != wantURL || inputs["default_provider"] != "openai" {
			t.Fatal("leaf changed policy outputs")
		}
		leafModels = append(leafModels, model)
	}
	if strings.Join(leafModels, "/") != strings.Join(models, "/") {
		t.Fatal("leaf snapshot models differ", leafModels)
	}
	a, _ := json.Marshal(actual)
	w, _ := json.Marshal(models)
	if string(a) != string(w) {
		t.Fatalf("execution snapshots %s want %s", a, w)
	}
}

func TestHarnessCloseJoinsBeforeTransferredResources(t *testing.T) {
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	order := []int{}
	h := &Harness{cancel: cancel, done: done}
	go func() { <-ctx.Done(); close(done) }()
	cleanupErr := errors.New("owned transport cleanup failed")
	for _, i := range []int{1, 2} {
		i := i
		h.owned = append(h.owned, func(context.Context) error {
			select {
			case <-done:
			default:
				t.Error("resource closed before runner joined")
			}
			order = append(order, i)
			if i == 2 {
				return cleanupErr
			}
			return nil
		})
	}
	if err := h.Close(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatal(err)
	}
	if err := h.Close(context.Background()); !errors.Is(err, cleanupErr) || len(order) != 2 || order[0] != 2 || order[1] != 1 {
		t.Fatal("cleanup order/idempotence", order, err)
	}
}
