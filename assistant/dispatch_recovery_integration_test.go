package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/contextual"
	"github.com/colony-2/c2j/pkg/execution"
	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	"github.com/colony-2/c2j/pkg/jobref"
	coreops "github.com/colony-2/c2j/pkg/ops"
	recipeops "github.com/colony-2/c2j/pkg/ops/recipe"
	"github.com/colony-2/c2j/pkg/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/starter"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerexport "github.com/colony-2/c2j/pkg/worker/export"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	workerworkflow "github.com/colony-2/c2j/pkg/worker/workflow"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
	"github.com/colony-2/jobdb/pkg/jobdb/runtime/remote"
	sqliteruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/sqlite"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

// The server commits through the real runtime before discarding one response.
// Only transport is faulted; no helper, operation, identity or receipt is mocked.
type submissionResponseLoss struct {
	handler  http.Handler
	mu       sync.Mutex
	match    func(*http.Request) bool
	bodies   [][]byte
	replies  [][]byte
	paths    []string
	accepted chan struct{}
}

func (f *submissionResponseLoss) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.match(r) {
		f.handler.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", 500)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	recorded := httptest.NewRecorder()
	f.handler.ServeHTTP(recorded, r)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.replies = append(f.replies, bytes.Clone(recorded.Body.Bytes()))
	f.paths = append(f.paths, r.URL.Path)
	lose := len(f.bodies) == 1 && recorded.Code >= 200 && recorded.Code < 300
	f.mu.Unlock()
	if lose {
		if f.accepted != nil {
			close(f.accepted)
			<-r.Context().Done()
			return
		}
		http.Error(w, "fixture: response lost after acceptance", 503)
		return
	}
	for k, vs := range recorded.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(recorded.Code)
	_, _ = w.Write(recorded.Body.Bytes())
}
func dispatchEvidence(t *testing.T, name string, value any) {
	t.Helper()
	if dir := os.Getenv("ASSISTANT_DISPATCH_EVIDENCE"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		b, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".json"), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostFreshDispatchLostResponse(t *testing.T) {
	ctx := celltest.Context(context.Background())
	dbPath := filepath.Join(t.TempDir(), "dispatch.db")
	db, err := sqliteruntime.NewFromConfig(ctx, sqliteruntime.Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(ctx) }()
	if _, err = db.CreateCell(ctx, jobdb.CreateCellRequest{TenantID: "tenant", Name: "root", Repo: "github.com/example/root"}); err != nil {
		t.Fatal(err)
	}
	if err = jobdbschema.Register(ctx, db, "tenant"); err != nil {
		t.Fatal(err)
	}
	fault := &submissionResponseLoss{handler: celltest.Handler(remote.NewServer(db)), match: func(r *http.Request) bool {
		return r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/jobs/frozen-backend")
	}}
	server := httptest.NewServer(fault)
	defer server.Close()
	newHost := func() jobdbschema.WorkflowEngine {
		sdk, e := remote.New(server.URL, server.Client())
		if e != nil {
			t.Fatal(e)
		}
		engine, e := jobworkflow.NewEngineBuilder().WithRuntime(sdk).BuildEngine()
		if e != nil {
			t.Fatal(e)
		}
		return jobdbschema.WorkflowEngine{Engine: engine, Registry: sdk}
	}
	start := workflowctl.StartJob{TenantId: "tenant", JobID: "frozen-backend", Cell: "root", CellRepo: "github.com/example/root", RecipeName: "build", Inputs: map[string]any{"prompt": "exact confirmed prompt"}, Artifacts: []jobdb.Artifact{jobdb.NewArtifactFromBytes("confirmed.txt", []byte("retained attachment"))}}
	prepared, err := starter.PrepareRecipeJob(ctx, start, starter.StartRecipeJobOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := starter.NewDescriptor(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	saved := mustJSON(frozen)
	if _, err = frozen.Submit(ctx, newHost()); err == nil {
		t.Fatal("expected lost accepted response")
	}
	if err = db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	db, err = sqliteruntime.NewFromConfig(ctx, sqliteruntime.Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	fault.handler = celltest.Handler(remote.NewServer(db))
	var restored starter.Descriptor
	if err = json.Unmarshal(saved, &restored); err != nil {
		t.Fatal(err)
	}
	ref, err := restored.Submit(ctx, newHost())
	if err != nil {
		t.Fatal(err)
	}
	if ref.JobID != "frozen-backend" || ref.GoalID == "" {
		t.Fatal("missing receipt", ref)
	}
	rows, err := db.ListJobs(ctx, jobdb.ListJobsRequest{TenantIds: []string{"tenant"}, Stores: []jobdb.JobStore{jobdb.JobStoreActive, jobdb.JobStoreArchived}})
	if err != nil || len(rows.Jobs) != 1 || rows.Jobs[0].GoalID != ref.GoalID {
		t.Fatal("duplicated or changed destination", rows, err)
	}
	fault.mu.Lock()
	equal := len(fault.bodies) == 2 && sameSubmission(fault.bodies[0], fault.bodies[1]) && bytes.Equal(fault.replies[0], fault.replies[1])
	fault.mu.Unlock()
	if !equal {
		t.Fatalf("retry changed full wire operation or accepted receipt: bodies=%s replies=%s", fault.bodies, fault.replies)
	}
	start.Inputs["prompt"] = "changed prompt"
	changed, err := starter.PrepareRecipeJob(ctx, start, starter.StartRecipeJobOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = newHost().SubmitJobRef(ctx, changed); !errors.Is(err, jobdb.ErrExistingJobMismatch) {
		t.Fatal("changed content accepted", err)
	}
	dispatchEvidence(t, "host-fresh-lost-response", map[string]any{"descriptor": restored, "receipt": ref, "job_count": len(rows.Jobs), "identical_semantic_submission": equal, "changed_content_rejected": true})
}

func TestOrdinaryRecipeDispatchLostResponse(t *testing.T)      { runOrdinaryDispatchRecovery(t, false) }
func TestOrdinaryRecipeDispatchWorkerReplacement(t *testing.T) { runOrdinaryDispatchRecovery(t, true) }

func runOrdinaryDispatchRecovery(t *testing.T, replace bool) {
	ctx, cancel := context.WithTimeout(celltest.Context(context.Background()), 90*time.Second)
	defer cancel()
	coreops.Replace(append(workerexport.GetAll(), recipeops.GetOps()...)...)
	repo := registeredFixtureRepository(t)
	if err := os.MkdirAll(filepath.Join(repo, ".c2/recipes"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".c2/recipes/build.yaml"), []byte("id: build\nschema_version: 2\ninput_schema:\n  prompt: {type: string}\nsequence: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "backend fixture"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatal(e, string(b))
		}
	}
	db, err := sqliteruntime.NewFromConfig(ctx, sqliteruntime.Config{DBPath: filepath.Join(t.TempDir(), "ordinary-dispatch.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(celltest.Context(context.Background()))
	if _, err = db.CreateCell(ctx, jobdb.CreateCellRequest{TenantID: "tenant", Name: "root", Repo: "github.com/example/root"}); err != nil {
		t.Fatal(err)
	}
	configureFixtureGit(t, db, repo)
	fault := &submissionResponseLoss{handler: celltest.Handler(remote.NewServer(db)), match: func(r *http.Request) bool {
		return r.Method == "PUT" && strings.Contains(r.URL.Path, "/leases/") && strings.Contains(r.URL.Path, "/jobs/")
	}}
	server := httptest.NewServer(fault)
	defer server.Close()
	httpClient := server.Client()
	if replace {
		fault.accepted = make(chan struct{})
	} else {
		httpClient.Transport = replayLostPUT{base: httpClient.Transport}
	}
	sdk, err := remote.New(server.URL, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	scoped := executionruntime.New(sdk, execution.Allocation{SchemaVersion: 1})
	engine, err := jobworkflow.NewEngineBuilder().WithRuntime(scoped).WithWorkerTenantId("tenant").BuildEngine()
	if err != nil {
		t.Fatal(err)
	}
	wrapped := jobdbschema.WorkflowEngine{Engine: engine, Registry: sdk}
	ctl := &workerworkflow.SWFWorkflowControl{Engine: wrapped}
	registry, err := workerops.NewActivityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	workers, err := compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(ctl).Build(), registry, compiler.RecipeJobWorkerOptions{RootSourceResolver: compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{}), Allocation: scoped.Allocation, WrapTaskWorker: scoped.WrapTaskWorker, StageExecution: scoped.Stage})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	parent, err := recipe.LoadRecipeFromString([]byte(`id: dispatch-recovery
schema_version: 2
sequence:
  - id: dispatch
    op: recipes.run
    inputs:
      recipes:
        - name: build
          cell_name: root
          cell_repo: github.com/example/root
          inputs: {prompt: exact confirmed prompt}
outputs:
  child: '${{ sequence.dispatch.outputs }}'
`))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := starter.StartRecipeJob(ctx, workflowctl.StartJob{TenantId: "tenant", JobID: "dispatch-parent", Cell: "root", CellRepo: "github.com/example/root", RecipeName: "dispatch-recovery", GitRef: "main", JobContext: contextual.JobContext{Workflow: contextual.WorkflowContext{CellName: "root"}, GitBase: contextual.GitBaseContext{BaseRepo: "https://github.com/example/root.git", BaseRef: "main"}}}, wrapped, *parent)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	runCtx, stop := context.WithCancel(ctx)
	go func() { defer close(done); engine.Run(runCtx) }()
	if replace {
		select {
		case <-fault.accepted:
		case <-ctx.Done():
			stop()
			<-done
			t.Fatal("no accepted child", ctx.Err())
		}
		stop()
		<-done
		engine, err = jobworkflow.NewEngineBuilder().WithRuntime(scoped).WithWorkerTenantId("tenant").BuildEngine()
		if err != nil {
			t.Fatal(err)
		}
		wrapped = jobdbschema.WorkflowEngine{Engine: engine, Registry: sdk}
		ctl = &workerworkflow.SWFWorkflowControl{Engine: wrapped}
		workers, err = compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(ctl).Build(), registry, compiler.RecipeJobWorkerOptions{RootSourceResolver: compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{}), Allocation: scoped.Allocation, WrapTaskWorker: scoped.WrapTaskWorker, StageExecution: scoped.Stage})
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.RegisterWorkers(workers); err != nil {
			t.Fatal(err)
		}
		done = make(chan struct{})
		runCtx, stop = context.WithCancel(ctx)
		go func() { defer close(done); engine.Run(runCtx) }()
	}
	defer func() { stop(); <-done }()
	if err = jobworkflow.WaitForJobToComplete(ctx, 80*time.Second, ref.Key(), engine); err != nil {
		t.Fatal(err)
	}
	children, err := db.ListJobs(ctx, jobdb.ListJobsRequest{TenantIds: []string{"tenant"}, ParentJobIDs: []string{ref.JobID}, Stores: []jobdb.JobStore{jobdb.JobStoreActive, jobdb.JobStoreArchived}})
	if err != nil || len(children.Jobs) != 1 {
		t.Fatal("expected one child", children, err)
	}
	// Read-only replay reconstructs the committed ordinary result without a new submit.
	replayed, err := engine.ReplayJobRun(ctx, jobworkflow.ReplayRunRequest{JobKey: ref.Key()})
	if err != nil {
		t.Fatal("replay", err)
	}
	raw, err := replayed.GetData()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(children.Jobs[0].JobKey.JobId)) {
		t.Fatal("retained result omitted accepted identity", string(raw))
	}
	if err = jobworkflow.WaitForJobToComplete(ctx, 10*time.Second, children.Jobs[0].JobKey, engine); err != nil {
		t.Fatal("child fixture", err)
	}
	fault.mu.Lock()
	defer fault.mu.Unlock()
	if len(fault.bodies) != 2 {
		t.Fatalf("expected one retry, got %d requests: %s", len(fault.bodies), fault.replies)
	}
	var first, second any
	_ = json.Unmarshal(fault.bodies[0], &first)
	_ = json.Unmarshal(fault.bodies[1], &second)
	if !sameSubmission(fault.bodies[0], fault.bodies[1]) || !bytes.Equal(fault.replies[0], fault.replies[1]) {
		t.Fatalf("ordinary retry changed operation or receipt\n%s\n%s\n%s", fault.bodies[0], fault.bodies[1], fault.replies)
	}
	evidenceName := "ordinary-recipe-lost-response"
	if replace {
		evidenceName = "ordinary-recipe-worker-replacement"
	}
	dispatchEvidence(t, evidenceName, map[string]any{"parent": ref, "child": children.Jobs[0].JobKey, "paths": fault.paths, "requests": []any{first, second}, "receipts": []json.RawMessage{fault.replies[0], fault.replies[1]}, "job_count": 1, "worker_replacement": replace, "replayed_output": json.RawMessage(raw)})
}

// requestTime is the SDK's transport clock, outside the semantic Job request.
// Compare every submitted job field, input byte, artifact and metadata value.
func sameSubmission(a, b []byte) bool {
	var x, y map[string]json.RawMessage
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	delete(x, "requestTime")
	delete(y, "requestTime")
	return reflect.DeepEqual(x, y)
}

// Model an authorized retry of the same HTTP request in the existing task.
// A recipe-level retry is a new invocation and is deliberately not used here.
type replayLostPUT struct{ base http.RoundTripper }

func (r replayLostPUT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil || resp.StatusCode != 503 || req.Method != "PUT" || !strings.Contains(req.URL.Path, "/leases/") {
		return resp, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	again := req.Clone(req.Context())
	if req.GetBody == nil {
		return nil, errors.New("fixture request cannot replay")
	}
	again.Body, err = req.GetBody()
	if err != nil {
		return nil, err
	}
	return r.base.RoundTrip(again)
}

func exerciseOrdinaryConfirmedDispatch(t *testing.T, ctx context.Context, facade *Client, caller Caller, key jobref.Ref, db *sqliteruntime.Runtime) {
	t.Helper()
	session, err := facade.Get(ctx, caller, key.GoalID)
	if err != nil || session.Current == nil {
		t.Fatal("proposal prompt", err)
	}
	var req PromptRequest
	if session.Current.Form.Request == nil || decode(*session.Current.Form.Request, &req) != nil || req.Proposal == nil {
		t.Fatalf("proposal missing: %+v", session.Current)
	}
	p := req.Proposal
	in := input.StructuredSubmission{RequestID: *session.Current.Form.RequestId, SubmissionID: "confirmed-backend", Response: map[string]any{"command": "confirm", "proposal_id": p.ID, "proposal_hash": p.Hash}}
	forged := in
	forged.Response = map[string]any{"command": "confirm", "proposal_id": p.ID, "proposal_hash": "forged"}
	if _, err = facade.Respond(ctx, caller, key.GoalID, forged); !errors.Is(err, ErrStale) {
		t.Fatal("forged confirmation", err)
	}
	resolver := facade.host.ResolveCell
	facade.host.ResolveCell = func(ctx context.Context, w Caller, name string) (recipejob.ResolvedTarget, error) {
		target, e := resolver(ctx, w, name)
		target.CellID = "changed-identity"
		return target, e
	}
	if _, err = facade.Respond(ctx, caller, key.GoalID, in); !errors.Is(err, ErrStale) {
		t.Fatal("changed target", err)
	}
	facade.host.ResolveCell = resolver
	children, err := db.ListJobs(ctx, jobdb.ListJobsRequest{TenantIds: []string{caller.TenantID}, ParentJobIDs: []string{key.JobID}})
	if err != nil || len(children.Jobs) != 0 {
		t.Fatal("negative confirmation dispatched", err)
	}
	first, err := facade.Respond(ctx, caller, key.GoalID, in)
	if err != nil {
		t.Fatal("confirm", err)
	}
	var destination string
	for ctx.Err() == nil {
		session, err = facade.Get(ctx, caller, key.GoalID)
		if err == nil && session.Current != nil && *session.Current.Form.RequestId != in.RequestID {
			for _, entry := range session.Replay.Entries {
				if entry.Role == "dispatch" {
					destination = entry.Destination
					if len(entry.Origin.Path) < 4 || entry.Origin.Path[3] != "dispatch" || entry.Origin.Invocation == "" {
						t.Fatal("dispatch lost actual runtime origin", entry.Origin)
					}
				}
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if destination == "" {
		t.Fatal("dispatch did not return to prompt", err, ctx.Err())
	}
	retry, err := facade.Respond(ctx, caller, key.GoalID, in)
	if err != nil || !reflect.DeepEqual(first.Receipt, retry.Receipt) {
		t.Fatal("confirmation retry", err)
	}
	children, err = db.ListJobs(ctx, jobdb.ListJobsRequest{TenantIds: []string{caller.TenantID}, ParentJobIDs: []string{key.JobID}, Stores: []jobdb.JobStore{jobdb.JobStoreActive, jobdb.JobStoreArchived}})
	if err != nil || len(children.Jobs) != 1 || children.Jobs[0].JobKey.JobId != destination || children.Jobs[0].GoalID == key.GoalID {
		t.Fatal("backend identity", children, err)
	}
	dispatchEvidence(t, "confirmed-dispatch", map[string]any{"proposal": p, "confirmation": first, "retried_confirmation": retry, "destination": children.Jobs[0].JobKey, "goal": children.Jobs[0].GoalID, "history": session.Replay, "forged_rejected": true, "changed_target_rejected": true})
	for {
		closed, e := facade.Close(ctx, caller, key.JobID)
		if e != nil {
			t.Fatal(e)
		}
		if closed.Terminal {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx = jobdb.WithPrimaryGoalSelectionAuthorizer(ctx, func(context.Context, jobdb.GoalKey, jobdb.ResolvedCell) error { return nil })
	frozen, e := facade.PrepareReopen(ctx, caller, key.JobID, "after-dispatch", time.Unix(800, 0))
	if e != nil {
		t.Fatal(e)
	}
	next, e := facade.SubmitFrozenStart(ctx, caller, frozen)
	if e != nil {
		t.Fatal(e)
	}
	for ctx.Err() == nil {
		session, e = facade.Get(ctx, caller, key.GoalID)
		if e == nil && session.Current != nil && session.JobID == next.JobID {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	var reopened PromptRequest
	if decode(*session.Current.Form.Request, &reopened) != nil || reopened.Proposal != nil || !strings.Contains(reopened.Assistant.Text, destination) {
		t.Fatal("reopen offered consumed proposal", reopened)
	}
	newConfirmation := in
	newConfirmation.RequestID = *session.Current.Form.RequestId
	newConfirmation.SubmissionID = "stale-proposal-after-reopen"
	if _, e = facade.Respond(ctx, caller, key.GoalID, newConfirmation); !errors.Is(e, ErrStale) {
		t.Fatal("consumed proposal accepted", e)
	}
	dispatchEvidence(t, "confirmed-dispatch-reopen", map[string]any{"original_job": key.JobID, "next_job": next.JobID, "goal": next.GoalID, "form": reopened, "history": session.Replay})
	if _, e = facade.Close(ctx, caller, next.JobID); e != nil {
		t.Fatal(e)
	}
}

func exerciseTerminalDispatchDiscovery(t *testing.T, ctx context.Context, facade *Client, caller Caller, key jobref.Ref, db *sqliteruntime.Runtime, loss *submissionResponseLoss) {
	t.Helper()
	session, err := facade.Get(ctx, caller, key.GoalID)
	if err != nil || session.Current == nil {
		t.Fatal("initial prompt", err)
	}
	var req PromptRequest
	if decode(*session.Current.Form.Request, &req) != nil || req.Proposal == nil {
		t.Fatal("missing proposal")
	}
	in := input.StructuredSubmission{RequestID: *session.Current.Form.RequestId, SubmissionID: "terminal-confirm", Response: map[string]any{"command": "confirm", "proposal_id": req.Proposal.ID, "proposal_hash": req.Proposal.Hash}}
	first, err := facade.Respond(ctx, caller, key.GoalID, in)
	if err != nil {
		t.Fatal(err)
	}
	var recovered Entry
	for ctx.Err() == nil {
		session, err = facade.Get(ctx, caller, key.GoalID)
		if err == nil {
			for _, e := range session.Replay.Entries {
				if e.Role == "dispatch" && e.Status == "failed" {
					recovered = e
				}
			}
		}
		if recovered.Recovery == "accepted" {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}
	if recovered.Destination == "" || recovered.DestinationGoal == "" || recovered.Status != "failed" || !strings.Contains(recovered.Error, "503") {
		t.Fatalf("failed dispatch not reconciled: %+v %v %v", recovered, err, ctx.Err())
	}
	exerciseDispatchDiscoveryNegatives(t, ctx, facade, caller, recovered.Origin, *req.Proposal)
	// A new host and repeated read observe the same accepted identity. They do
	// not restart the failed task, turn its error into success or submit again.
	cfg := facade.cfg
	bindings := *cfg.Bindings
	bindings.BaseURL = "https://fixture.invalid/exact/root"
	cfg.Bindings = &bindings
	replacement, err := New(facade.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	replacement.cfg.Bindings.BaseURL = facade.cfg.Bindings.BaseURL
	session, err = replacement.Get(ctx, caller, key.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := replacement.Respond(ctx, caller, key.GoalID, in)
	if err != nil || !reflect.DeepEqual(first.Receipt, retry.Receipt) {
		t.Fatal("confirmation receipt", err)
	}
	ctx = jobdb.WithPrimaryGoalSelectionAuthorizer(ctx, func(context.Context, jobdb.GoalKey, jobdb.ResolvedCell) error { return nil })
	for {
		closed, e := replacement.Close(ctx, caller, key.JobID)
		if e != nil {
			t.Fatal(e)
		}
		if closed.Terminal {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}
	operation, err := replacement.PrepareReopen(ctx, caller, key.JobID, "after-terminal-dispatch", time.Unix(900, 0))
	if err != nil {
		t.Fatal(err)
	}
	next, err := replacement.SubmitFrozenStart(ctx, caller, operation)
	if err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		session, err = replacement.Get(ctx, caller, key.GoalID)
		if err == nil && session.JobID == next.JobID && session.Current != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("reopen", ctx.Err(), err)
	}
	var form PromptRequest
	if decode(*session.Current.Form.Request, &form) != nil || form.Proposal != nil {
		t.Fatal("consumed proposal restored", form)
	}
	found := false
	for _, e := range session.Replay.Entries {
		if e.Role == "dispatch" && reflect.DeepEqual(e, recovered) {
			found = true
		}
	}
	if !found {
		t.Fatal("reopen lost original failure and recovered destination", session.Replay)
	}
	children, err := db.ListJobs(ctx, jobdb.ListJobsRequest{TenantIds: []string{caller.TenantID}, ParentJobIDs: []string{key.JobID}, Stores: []jobdb.JobStore{jobdb.JobStoreActive, jobdb.JobStoreArchived}})
	if err != nil || len(children.Jobs) != 1 || children.Jobs[0].JobKey.JobId != recovered.Destination {
		t.Fatal("duplicate/mismatched child", children, err)
	}
	loss.mu.Lock()
	submissions := len(loss.bodies)
	loss.mu.Unlock()
	if submissions != 1 {
		t.Fatal("read/reopen resubmitted", submissions)
	}
	dispatchEvidence(t, "terminal-dispatch-discovery", map[string]any{"original": key, "reopened": next, "confirmation": first.Receipt, "retried_receipt": retry.Receipt, "recovered_failure": recovered, "history": session.Replay, "child_count": len(children.Jobs), "submission_requests": submissions})
	if _, err = replacement.Close(ctx, caller, next.JobID); err != nil {
		t.Fatal(err)
	}
}
