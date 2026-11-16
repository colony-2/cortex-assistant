package assistanttest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/cellref"
	"github.com/colony-2/c2j/pkg/configuration"
	"github.com/colony-2/c2j/pkg/contextual"
	"github.com/colony-2/c2j/pkg/execution"
	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/gitprofile"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/c2j/pkg/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/secrets"
	"github.com/colony-2/c2j/pkg/starter"
	coretask "github.com/colony-2/c2j/pkg/task"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
	sqliteruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/sqlite"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

func TestContextualTaskSnapshotAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(celltest.Context(context.Background()), 180*time.Second)
	defer cancel()
	source, c := executionSource(t)
	f, err := PrepareFixture(ctx, FixtureOptions{SourceCommit: c, SourceDirectory: source, Scenario: Scenario{Replies: []Reply{{User: "finite", Response: `{"text":"finite"}`}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	provider := &secretProvider{}
	rt, err := sqliteruntime.NewFromConfig(ctx, sqliteruntime.Config{DBPath: filepath.Join(t.TempDir(), "snapshot.db"), SecretProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(context.WithoutCancel(ctx))
	if _, err = rt.CreateCell(ctx, jobdb.CreateCellRequest{TenantID: "tenant", Name: "root", Repo: "github.com/colony-2/cortex-assistant"}); err != nil {
		t.Fatal(err)
	}
	set := func(name string, value any) {
		b, _ := json.Marshal(value)
		if _, err := rt.SetTenantConfigurationValue(ctx, jobdb.SetTenantConfigurationValueRequest{TenantID: "tenant", Name: name, Value: b}); err != nil {
			t.Fatal(err)
		}
	}
	set(gitprofile.ReadOnlyKey, "fixture")
	set(gitprofile.ProfilePrefix+"fixture", f.Profile)
	registry, err := workerops.NewActivityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := recipejob.ResolvedTarget{CellName: "root", CellRepo: "github.com/colony-2/cortex-assistant", RepositorySource: "https://github.com/colony-2/cortex-assistant.git", DefaultRef: "main"}
	h, err := Compose(ctx, Options{Scoped: executionruntime.New(rt, execution.Allocation{SchemaVersion: 1}), Schemas: rt, Goals: rt, Tenant: "tenant", Registry: registry, Config: f.Config(root, []byte(strings.Repeat("k", 32))), Authorize: func(context.Context, assistant.Caller, string) error { return nil }, ResolveCell: func(context.Context, assistant.Caller, string) (recipejob.ResolvedTarget, error) { return root, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.WithoutCancel(ctx))
	raw, err := os.ReadFile(filepath.Join(f.Directory, "inference.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := recipe.LoadRecipeFromString(raw)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := compiler.ResolveInlineRecipes(ctx, *r, compiler.InlineResolutionOptions{ProjectID: "tenant", RootFile: filepath.Join(f.Directory, "inference.yaml"), WorkingDir: f.Directory})
	if err != nil {
		t.Fatal(err)
	}
	start := workflowctl.StartJob{TenantId: "tenant", JobID: "finite-snapshot", Cell: "root", CellRepo: root.CellRepo, GitRef: "main", RecipeName: "cortex-assistant-inference", JobContext: contextual.JobContext{Workflow: contextual.WorkflowContext{CellName: "root"}, GitBase: contextual.GitBaseContext{BaseRepo: root.RepositorySource, BaseRef: "main", GitAuthor: "Assistant Fixture <fixture@example.invalid>"}, CellResolution: &cellref.Context{SelfCell: "root", SelfRepo: root.CellRepo}}, Inputs: map[string]any{"base_url": configuration.Reference{Kind: "config", Name: "codex_api_base_url", ExpectedType: "string"}, "model": configuration.Reference{Kind: "config", Name: "codex_model", ExpectedType: "string"}, "provider": "openai", "api_key": secrets.Reference{Name: "codex_api_key"}, "prompt": "USER \"finite\"\n", "response_schema": map[string]any{"type": "object"}}}
	prepared, err := starter.PrepareRecipeJob(ctx, start, starter.StartRecipeJobOptions{}, expanded.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := starter.NewDescriptor(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	set("codex_api_base_url", "https://snapshot.invalid/exact/%2f/")
	set("codex_model", "snapshot-model")
	ref, err := descriptor.Submit(ctx, h.Host.Submitter)
	if err != nil {
		t.Fatal(err)
	}
	for {
		state, e := h.Control.InspectJob(ctx, ref.Key())
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
	output, err := h.Control.JobResult(ctx, ref.Key())
	if err != nil {
		t.Fatal(err)
	}
	want, err := output.GetData()
	if err != nil {
		t.Fatal(err)
	}
	api := &jobinspect.Service{Runtime: rt, Tenant: "tenant"}
	page, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: ref.JobID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	policies := 0
	for _, task := range page.Tasks {
		if task.Path == "cortex-assistant-inference/policy" && task.Type == "extension_execution:extension_execution" {
			field := ""
			value, e := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: ref.JobID, Task: task.ID, Attempt: task.Attempt, Field: &field})
			if e != nil {
				t.Fatal(e)
			}
			var accepted map[string]string
			if json.Unmarshal(value.Value, &accepted) != nil || accepted["base_url"] != "https://snapshot.invalid/exact/%2f/" || accepted["model"] != "snapshot-model" {
				t.Fatal("policy changed snapshot", string(value.Value))
			}
			policies++
		}
	}
	if policies != 1 {
		t.Fatal("missing consuming policy task")
	}
	run, err := h.Engine.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: ref.Key(), IncludeInputs: true, IncludeOutputs: true, IncludeAttemptInputs: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshots := 0
	for _, entry := range run.Execution {
		record := entry.Record
		if record.Kind != "result" || record.Output == nil {
			continue
		}
		var env coretask.OutputEnvelope
		if json.Unmarshal(record.Output.Data, &env) != nil {
			continue
		}
		var out workerops.ActivityInvocationOutput
		if json.Unmarshal(env.Payload, &out) != nil || out.Configuration == nil {
			continue
		}
		if string(out.Configuration.Values["codex_model"]) == `"snapshot-model"` && string(out.Configuration.Values["codex_api_base_url"]) == `"https://snapshot.invalid/exact/%2f/"` {
			snapshots++
		}
	}
	leaves := 0
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
			t.Fatal("missing actual leaf inputs")
		}
		api, ok := inputs["api"].(map[string]any)
		if !ok || api["base_url"] != "https://snapshot.invalid/exact/%2f/" || inputs["default_model"] != "snapshot-model" || inputs["default_provider"] != "openai" {
			t.Fatal("leaf did not receive exact policy outputs")
		}
		leaves++
	}
	if leaves != 1 {
		t.Fatalf("actual leaf input count %d", leaves)
	}
	if snapshots != 1 {
		t.Fatalf("actual policy configuration snapshot count %d", snapshots)
	}
	// Remove settings and disable the secret provider. Completed execution replay
	// must reuse outcomes without any live materialization or provider lookup.
	for _, name := range []string{"codex_api_base_url", "codex_model"} {
		if _, err = rt.RemoveTenantConfigurationValue(ctx, jobdb.RemoveTenantConfigurationValueRequest{TenantID: "tenant", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	before := provider.calls.Load()
	provider.disabled.Store(true)
	replay, err := h.Engine.ReplayJobRun(ctx, jobworkflow.ReplayRunRequest{JobKey: ref.Key(), JobWorker: compiler.NewRecipeJobWorker(compiler.RecipeJobWorkerOptions{ReadOnlyReplay: true})})
	if err != nil {
		t.Fatal(err)
	}
	got, err := replay.GetData()
	if err != nil || string(got) != string(want) || provider.calls.Load() != before {
		t.Fatal("replay reread settings/secrets or changed outcome", err)
	}
}
