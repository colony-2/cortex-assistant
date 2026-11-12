package assistant_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/execution"
	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	"github.com/colony-2/c2j/pkg/jobref"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/secrets"
	"github.com/colony-2/c2j/pkg/starter"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/template/colonycel"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	workerworkflow "github.com/colony-2/c2j/pkg/worker/workflow"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
	sqliteruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/sqlite"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

type unavailableSubmission struct{ calls int }

func (s *unavailableSubmission) SubmitJobRef(context.Context, jobdb.SubmitJob) (jobref.Ref, error) {
	s.calls++
	return jobref.Ref{}, assistant.ErrUnavailable
}

// This is an external consumer of only public APIs. It proves ordinary durable
// submission/read composition and resource ownership, not release resolution.
// Full helper/provider execution is exercised by the separate ordinary suite.
func TestConsumerOrdinaryRuntimeAndReadOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(celltest.Context(context.Background()), 30*time.Second)
	defer cancel()
	rt, err := sqliteruntime.NewFromConfig(ctx, sqliteruntime.Config{DBPath: filepath.Join(t.TempDir(), "consumer.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(celltest.Context(context.Background()))
	if _, err = rt.CreateCell(ctx, jobdb.CreateCellRequest{TenantID: "tenant", Name: "root", Repo: "github.com/example/root"}); err != nil {
		t.Fatal(err)
	}
	scoped := executionruntime.New(rt, execution.Allocation{SchemaVersion: 1})
	engine, err := jobworkflow.NewEngineBuilder().WithRuntime(scoped).WithWorkerTenantId("tenant").BuildEngine()
	if err != nil {
		t.Fatal(err)
	}
	wrapped := jobdbschema.WorkflowEngine{Engine: engine, Registry: rt}
	ctl := &workerworkflow.SWFWorkflowControl{Engine: wrapped}
	inputs, err := input.NewRuntime(ctl, nil)
	if err != nil {
		t.Fatal(err)
	}
	stories, err := story.New(story.ServiceConfig{Engine: wrapped, SchemaRegistry: rt, RootSourceResolver: compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{}), CELOptionsProvider: colonycel.NewBuilder(colonycel.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	root := recipejob.ResolvedTarget{CellName: "root", CellRepo: "github.com/example/root", RepositorySource: "https://github.com/example/root.git", DefaultRef: "main"}
	host := assistant.Host{Goals: rt, History: wrapped, Control: ctl, Input: inputs, Story: stories, Submitter: wrapped, Authorize: func(context.Context, assistant.Caller, string) error { return nil }, ResolveCell: func(context.Context, assistant.Caller, string) (recipejob.ResolvedTarget, error) { return root, nil }}
	commit := strings.Repeat("a", 40) // Queued descriptor fixture; deliberately never resolved.
	cfg := assistant.Config{Root: root, Protocol: assistant.GoalProtocol, Recipe: "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit, RecipeCommit: commit, CursorKey: []byte(strings.Repeat("key!", 8)), Bindings: &assistant.OrdinaryBindings{BaseURL: "https://provider.example.invalid/root", APIKey: secrets.Reference{Name: "consumer_key"}, Provider: "openai", Model: "approved"}}
	registry, err := workerops.NewActivityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	workers, err := compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(ctl).Build(), registry, compiler.RecipeJobWorkerOptions{Allocation: scoped.Allocation, WrapTaskWorker: scoped.WrapTaskWorker, StageExecution: scoped.Stage})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	registrySnapshot := func() []coreops.RegisterableOp {
		items := coreops.List()
		sort.Slice(items, func(i, j int) bool { return items[i].GetName() < items[j].GetName() })
		return items
	}
	registered := registrySnapshot()
	client, err := assistant.New(host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	caller := assistant.Caller{TenantID: "tenant", Actor: input.Actor{Kind: "human", ID: "consumer"}}
	at := time.Unix(100, 0).UTC()
	snapshot := assistant.FrozenSnapshot{TenantID: caller.TenantID, Source: assistant.Source{Kind: "global"}, At: at, Documents: []assistant.SnapshotDocument{}}
	selected := assistant.SnapshotSelection{TenantID: caller.TenantID, Source: snapshot.Source, At: at, Documents: map[string]assistant.SnapshotDocument{}}
	in := assistant.StartInput{RequestID: "consumer-start", SubmittedAt: at, Prompt: "queued discussion", Source: snapshot.Source}
	frozen, err := client.PrepareStartFrozen(ctx, caller, in, snapshot, selected)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.SubmitFrozenStart(ctx, caller, frozen)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := &unavailableSubmission{}
	host.Submitter = unavailable
	reader, err := assistant.New(host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, facade := range []*assistant.Client{client, reader} {
		got, err := facade.Get(ctx, caller, session.ID)
		if err != nil || got.JobID != session.JobID || got.ID != session.ID || got.Current != nil || len(got.Replay.Entries) != 1 || got.Replay.Entries[0].Text != in.Prompt {
			t.Fatalf("retained queued read: %+v %v", got, err)
		}
	}
	if unavailable.calls != 0 {
		t.Fatal("read attempted submission")
	}
	if _, err = reader.SubmitFrozenStart(ctx, caller, frozen); !errors.Is(err, assistant.ErrUnavailable) || unavailable.calls != 1 {
		t.Fatal("unavailable executor", err, unavailable.calls)
	}
	if !reflect.DeepEqual(registered, registrySnapshot()) {
		t.Fatal("facade mutated global operations")
	}
	// A read-only host must additionally deny mutation at its HTTP/control/input
	// authorization boundary. A missing submitter alone is not a read permission.
	closed, err := client.Close(ctx, caller, session.JobID)
	if err != nil || !closed.Terminal {
		t.Fatal("queued close", closed, err)
	}
	if _, err = reader.Get(ctx, caller, session.ID); err != nil {
		t.Fatal("closed retained read", err)
	}
	// The caller explicitly installs and owns the existing ordinary worker. No
	// assistant activity is registered, and an unrelated inline recipe still runs.
	unrelated, err := recipe.LoadRecipeFromString([]byte("id: unrelated\nschema_version: 2\nsequence: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := starter.StartRecipeJob(ctx, workflowctl.StartJob{TenantId: "tenant", Cell: root.CellName, CellRepo: root.CellRepo, RecipeName: "unrelated"}, wrapped, *unrelated)
	if err != nil {
		t.Fatal(err)
	}
	runctx, stop := context.WithCancel(ctx)
	joined := make(chan struct{})
	go func() { defer close(joined); engine.Run(runctx) }()
	defer func() { stop(); <-joined }()
	for {
		result, err := ctl.InspectJob(ctx, ref.Key())
		if err != nil {
			t.Fatal(err)
		}
		if result.Terminal {
			run, err := wrapped.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: ref.Key()})
			if err != nil || run.Job.Status != jobdb.JobStatusCompleted {
				t.Fatal("unrelated recipe", run.Job.Status, err)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Log("public consumer: durable queued submission/read; unavailable submit adapter; unchanged global operation registry; unrelated ordinary recipe completed; caller cancels and joins engine before closing owned SQLite")
}
