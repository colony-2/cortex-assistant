package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/execution"
	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/ops/extensions"
	"github.com/colony-2/c2j/pkg/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/template/colonycel"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerexport "github.com/colony-2/c2j/pkg/worker/export"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	workerworkflow "github.com/colony-2/c2j/pkg/worker/workflow"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/runtime/remote"
	sqliteruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/sqlite"
	toyruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/toy"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

// This is a real worker/history/input test with deterministic model execution.
// It does not certify a published assistant release, external model or Cortex UI.
type fixtureResolver struct{ raw []byte }

func (r fixtureResolver) Resolve(_ context.Context, _ string, requested string) (compiler.RecipeSourceResolution, error) {
	if requested == "review-fixture" || strings.Contains(requested, "/review-fixture.yaml@") || requested == "build" || strings.Contains(requested, "/build.yaml@") {
		return compiler.RecipeSourceResolution{SourceKind: compiler.RecipeSourceKindServerRef, SubmittedSelector: requested, ResolvedSelector: requested}, nil
	}
	return compiler.RecipeSourceResolution{SourceKind: compiler.RecipeSourceKindServerRef, SubmittedSelector: selector, ResolvedSelector: selector, ResolvedCommit: commit}, nil
}
func (r fixtureResolver) Load(_ context.Context, _ string, res compiler.RecipeSourceResolution) (recipe.Recipe, error) {
	raw := r.raw
	if res.SubmittedSelector == "review-fixture" || strings.Contains(res.SubmittedSelector, "/review-fixture.yaml@") {
		raw = []byte(reviewFixture)
	}
	if res.SubmittedSelector == "build" || strings.Contains(res.SubmittedSelector, "/build.yaml@") {
		raw = []byte(buildFixture)
	}
	p, e := recipe.LoadRecipeFromString(raw)
	if e != nil {
		return recipe.Recipe{}, e
	}
	return *p, nil
}
func (r fixtureResolver) LoadYAML(_ context.Context, _ string, res compiler.RecipeSourceResolution) ([]byte, error) {
	if res.SubmittedSelector == "review-fixture" || strings.Contains(res.SubmittedSelector, "/review-fixture.yaml@") {
		return []byte(reviewFixture), nil
	}
	if res.SubmittedSelector == "build" || strings.Contains(res.SubmittedSelector, "/build.yaml@") {
		return []byte(buildFixture), nil
	}
	return r.raw, nil
}
func TestWorkerRecordedConversation(t *testing.T) {
	for _, backend := range []string{"toy", "sqlite", "remote"} {
		t.Run(backend, func(t *testing.T) { runWorkerConversation(t, backend, false) })
	}
}
func TestWorkerMembership(t *testing.T) { runWorkerConversation(t, "membership", false) }

func TestWorkerMediaDownloads(t *testing.T) { runWorkerConversation(t, "media", false) }

func TestWorkerContextCapture(t *testing.T) { runWorkerConversation(t, "context", false) }

func TestWorkerLargeHistory(t *testing.T) { runWorkerConversation(t, "large", false) }

func TestWorkerConfirmedGoalAmbiguity(t *testing.T) { runWorkerConversation(t, "sqlite", true) }

const buildFixture = `id: build
input_schema:
  type: {type: string}
  prompt: {type: string}
sequence: []
outputs: {ok: true}
`

const reviewFixture = `id: review-fixture
input_schema: {}
sequence:
  - id: first-review
    op: input
    inputs:
      form:
        kind: review
        title: First contextual review
        fields:
          - {id: decision, type: short_answer, question: "Decision?", required: true}
  - id: second-review
    op: input
    inputs:
      form:
        kind: review
        title: Replacement contextual review
        fields:
          - {id: decision, type: short_answer, question: "Decision?", required: true}
outputs: {}
`

func runWorkerConversation(t *testing.T, backend string, goalCase bool) {
	large := backend == "large"
	actualProvider := backend == "provider"
	provider, model := "fixture", "fixture"
	if actualProvider {
		provider, model = "openai", os.Getenv("ASSISTANT_REAL_MODEL")
	}
	registered := append(workerexport.GetAll(), extensions.GetExecutionOp(), input.GetOp())
	coreops.Replace(registered...)
	raw, e := os.ReadFile("../session.yaml")
	if e != nil {
		t.Fatal(e)
	}
	repo := t.TempDir()
	t.Setenv("CORTEX_ASSISTANT_CONFIG", "")
	// Disposable fixture repository only; the candidate repository is never committed.
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Assistant Test"}, {"config", "user.email", "assistant@example.invalid"}, {"add", "."}, {"commit", "--allow-empty", "-q", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("fixture git: %v %s", e, b)
		}
	}
	var rt jobdb.WorkflowRuntime = toyruntime.New()
	if backend != "toy" {
		runtimeConfig := sqliteruntime.Config{DBPath: filepath.Join(t.TempDir(), "jobs.db")}
		if large {
			// Product UTF-8 maxima can expand sixfold in encoded task data.
			// Use the existing deployment capacity setting, not a runtime change.
			runtimeConfig.MaxChapterApplicationPayloadBytes = 4 << 20
		}
		db, err := sqliteruntime.NewFromConfig(context.Background(), runtimeConfig)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if e := db.Close(context.Background()); e != nil {
				t.Error(e)
			}
		})
		rt = db
	}
	if backend == "remote" {
		server := httptest.NewServer(remote.NewServer(rt))
		t.Cleanup(server.Close)
		sdk, err := remote.New(server.URL, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		rt = sdk
	}
	scoped := executionruntime.New(rt, execution.Allocation{SchemaVersion: 1})
	engine, e := jobworkflow.NewEngineBuilder().WithRuntime(scoped).WithWorkerTenantId(human.TenantID).BuildEngine()
	if e != nil {
		t.Fatal(e)
	}
	wrapped := jobdbschema.WorkflowEngine{Engine: engine, Registry: rt.(jobdb.JobSchemaRegistry)}
	ctl := &workerworkflow.SWFWorkflowControl{Engine: wrapped}
	inputs, e := input.NewRuntime(ctl, nil)
	if e != nil {
		t.Fatal(e)
	}
	resolver := fixtureResolver{raw}
	stories, e := story.New(story.ServiceConfig{Engine: wrapped, SchemaRegistry: rt.(jobdb.JobSchemaRegistry), RootSourceResolver: resolver, CELOptionsProvider: colonycel.NewBuilder(colonycel.Options{})})
	if e != nil {
		t.Fatal(e)
	}
	c, e := New(Host{Goals: rt, History: wrapped, Control: ctl, Input: inputs, Story: stories, Submitter: wrapped, Authorize: func(_ context.Context, who Caller, resource string) error {
		if backend == "membership" {
			peer := Caller{TenantID: human.TenantID, Actor: input.Actor{Kind: "human", ID: "peer"}}
			if who != human && who != peer {
				return ErrForbidden
			}
			if resource == "start" || resource == "list" || who == human && resource == "assistant-"+identity(human.TenantID, "worker") || who == peer && resource == "assistant-"+identity(peer.TenantID, "peer") {
				return nil
			}
			return ErrForbidden
		}
		if who != human {
			return ErrForbidden
		}
		return nil
	}, ResolveCell: func(context.Context, Caller, string) (recipejob.ResolvedTarget, error) {
		if goalCase {
			return recipejob.ResolvedTarget{RepositorySource: repo, DefaultRef: "main"}, nil
		}
		return recipejob.ResolvedTarget{}, ErrForbidden
	}}, Config{Root: recipejob.ResolvedTarget{RepositorySource: repo, DefaultRef: "main"}, Recipe: selector, RecipeCommit: commit, CursorKey: []byte(strings.Repeat("key!", 8))})
	if e != nil {
		t.Fatal(e)
	}
	registry, e := workerops.NewActivityRegistry()
	if e != nil {
		t.Fatal(e)
	}
	var modelCalls atomic.Int32
	var dispatchCalls atomic.Int32
	bindActivity := func(bound *Client) (coreops.RegisterableOp, error) {
		return NewActivity(bound, ActivityOptions{Provider: provider, Model: model, AuthorizeExecution: func(ctx context.Context, current jobcontext.Current) (Caller, error) {
			workerCaller := human
			if backend == "membership" && current.JobID == "assistant-"+identity(human.TenantID, "peer") {
				workerCaller.Actor = input.Actor{Kind: "human", ID: "peer"}
			}
			return workerCaller, nil
		}})
	}
	activity, err := bindActivity(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = RegisterActivity(registry, activity); err != nil {
		t.Fatal(err)
	}
	for name, registration := range registry.GetAll() {
		if actualProvider {
			continue
		} // Preserve the actual registered helper and model extension.
		reg := registration
		real := reg.Step.Invoke
		reg.Step.Invoke = func(deps coreops.OpDependencies, ctx context.Context, in map[string]any) (map[string]any, error) {
			if reg.Metadata.Type == ActivityName {
				out, err := real(deps, ctx, in)
				if err == nil && goalCase && in["action"] == "dispatch" && dispatchCalls.Add(1) == 1 {
					return nil, fmt.Errorf("injected loss after destination submission before result recording")
				}
				return out, err
			}
			if reg.Metadata.Type != "extension_execution" {
				return real(deps, ctx, in)
			}
			selected, _ := in["selector"].(string)
			if selected == ModelOperation {
				call := modelCalls.Add(1)
				if !goalCase && !large && backend != "media" && backend != "membership" && call > 1 && !strings.Contains(string(mustJSON(in)), strings.Repeat("x", 1200)+"uploaded bytes") {
					return nil, fmt.Errorf("accepted text attachment missing from inference input")
				}
				if backend == "media" && call > 1 {
					d, err := c.read(ctx, human, deps.CurrentJobContext().JobID)
					if err != nil {
						return nil, err
					}
					ids := []string{}
					for id, artifact := range c.authorizedArtifacts(d) {
						if artifact.Name == "pixel.png" || artifact.Name == "clip.mp4" || artifact.Name == "sound.wav" {
							ids = append(ids, id)
						}
					}
					result := ModelResult{Text: "download these files", Documents: []Document{{Name: "plain.txt", Text: "plain bytes", MIME: "text/plain"}, {Name: "report.md", Text: "# report", MIME: "text/markdown"}, {Name: "data.json", Text: `{"ok":true}`, MIME: "application/json"}}, ReturnArtifactIDs: ids}
					return map[string]any{"response": string(mustJSON(result)), "model": "media-fixture", "usage": map[string]any{}}, nil
				}
				if large {
					return map[string]any{"response": string(mustJSON(ModelResult{Text: strings.Repeat("<", 64<<10), Documents: []Document{}, ReturnArtifactIDs: []string{}})), "model": "large-deterministic-fixture", "usage": map[string]any{}}, nil
				}
				if goalCase {
					return map[string]any{"response": `{"text":"proposal","documents":[],"return_artifact_ids":[],"create_goal":{"cell":"cell","prompt":"exact build prompt"}}`, "model": "fixture", "usage": map[string]any{}}, nil
				}
				return map[string]any{"response": `{"text":"worker reply","documents":[{"name":"note.md","text":"document bytes","mime":"text/markdown"}],"return_artifact_ids":[]}`, "model": "deterministic-fixture", "usage": map[string]any{}}, nil
			}
			return nil, fmt.Errorf("unexpected extension %s", selected)
		}
		registry.UpdateRegistration(name, reg)
	}
	workers, e := compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(ctl).Build(), registry, compiler.RecipeJobWorkerOptions{RootSourceResolver: resolver, Allocation: scoped.Allocation, WrapTaskWorker: scoped.WrapTaskWorker, StageExecution: scoped.Stage})
	if e != nil {
		t.Fatal(e)
	}
	if e = engine.RegisterWorkers(workers); e != nil {
		t.Fatal(e)
	}
	duration := 2 * time.Minute
	if large {
		duration = 5 * time.Minute
	}
	runctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	prompt := "hello"
	if actualProvider {
		prompt = "Reply briefly acknowledging the source marker candidate-provider-check-20261101. Create exactly one Markdown document named verification.md containing the same marker. Do not propose a goal or return existing artifacts."
	}
	session, e := c.Start(runctx, human, StartInput{RequestID: "worker", SubmittedAt: time.Unix(10, 0), Prompt: prompt, Source: Source{Kind: "global"}})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); engine.Run(runctx) }()
	defer func() { cancel(); <-done }()
	waitInput := func(job string) *input.UserInputDetails {
		for runctx.Err() == nil {
			d, err := inputs.GetDetails(runctx, human.TenantID, job)
			if err == nil && d.Form.RequestId != nil && *d.Form.RequestId != "" {
				return d
			}
			if large && err == nil && d.Form.RequestId == nil {
				trace, traceErr := stories.GetJobRunStory(runctx, story.GetJobRunStoryRequest{ProjectID: human.TenantID, JobID: job})
				if traceErr == nil {
					var walk func(*story.JobRunStoryNode)
					walk = func(n *story.JobRunStoryNode) {
						if n == nil {
							return
						}
						if taskRolePath(n.Path, "form") || taskRolePath(n.Path, "await-human") {
							for label, val := range map[string]any{"in": n.Input, "out": n.Output} {
								var obj map[string]any
								_ = decode(val, &obj)
								for key, item := range obj {
									t.Logf("%s %v %s=%s bytes=%d", label, n.Path, key, fmt.Sprintf("%T", item), len(mustJSON(item)))
								}
							}
						}
						for _, ch := range n.Children {
							walk(ch)
						}
					}
					walk(trace.Root)
				}
				t.Fatal("pending large structured input lost request identity")
			}
			inspection, err := ctl.InspectJob(runctx, jobdb.JobKey{TenantId: human.TenantID, JobId: job})
			if err == nil && inspection.Terminal {
				t.Fatalf("worker terminated before input: %+v", inspection)
			}
			time.Sleep(10 * time.Millisecond)
		}
		inspection, _ := ctl.InspectJob(context.Background(), jobdb.JobKey{TenantId: human.TenantID, JobId: job})
		t.Fatalf("input unavailable: %+v", inspection)
		return nil
	}
	if actualProvider {
		current := waitInput(session.JobID)
		recordActualProviderEvidence(t, runctx, c, session, current, model, prompt)
		return
	}
	if large {
		exerciseLargeWorkerHistory(t, runctx, c, session, waitInput, &modelCalls)
		return
	}
	if backend == "membership" {
		exerciseWorkerMembership(t, runctx, c, session, waitInput, &modelCalls)
		return
	}
	if backend == "media" {
		exerciseWorkerMediaDownloads(t, runctx, c, session, waitInput, &modelCalls)
		return
	}
	if backend == "context" {
		exerciseWorkerContextCapture(t, runctx, c, session, waitInput, &modelCalls)
		return
	}
	if goalCase {
		current := waitInput(session.JobID)
		var request PromptRequest
		if current.Form.Request == nil || decode(*current.Form.Request, &request) != nil || request.Proposal == nil {
			t.Fatal("proposal unavailable")
		}
		originalRequest := *current.Form.RequestId
		if _, err := c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: originalRequest, SubmissionID: "finish-proposal", Response: Command{Command: "finish"}}); err != nil {
			t.Fatal(err)
		}
		for runctx.Err() == nil {
			d, err := c.read(runctx, human, session.JobID)
			if err != nil {
				t.Fatal(err)
			}
			if d.Story.Status == story.WorkflowStatusCompleted {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		reopened, err := c.Reopen(runctx, human, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		session.JobID = reopened.JobID
		current = waitInput(session.JobID)
		if *current.Form.RequestId == originalRequest || current.Form.Request == nil || decode(*current.Form.Request, &request) != nil || request.Proposal == nil {
			t.Fatal("reopened proposal lacks fresh confirmation boundary")
		}
		if _, err = c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: originalRequest, SubmissionID: "old-confirmation", Response: Command{Command: "confirm", ProposalID: request.Proposal.ID, ProposalHash: request.Proposal.Hash}}); !errors.Is(err, ErrStale) {
			t.Fatal("historical input accepted", err)
		}
		if modelCalls.Load() != 1 || dispatchCalls.Load() != 0 {
			t.Fatal("finish/reopen dispatched or inferred")
		}
		// Identical confirmation retries race at the real input receipt boundary.
		confirmations := make(chan error, 4)
		for i := 0; i < 4; i++ {
			go func() {
				_, err := c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *current.Form.RequestId, SubmissionID: "confirmed", Response: Command{Command: "confirm", ProposalID: request.Proposal.ID, ProposalHash: request.Proposal.Hash}})
				confirmations <- err
			}()
		}
		successes := 0
		for i := 0; i < 4; i++ {
			err := <-confirmations
			if err == nil {
				successes++
			} else if !errors.Is(err, ErrStale) && !errors.Is(err, ErrNotAtPrompt) && !errors.Is(err, input.ErrInputNotPending) {
				t.Fatal(err)
			}
		}
		if successes == 0 {
			t.Fatal("no confirmation accepted")
		}
		var next *input.UserInputDetails
		for runctx.Err() == nil {
			next = waitInput(session.JobID)
			if *next.Form.RequestId != *current.Form.RequestId {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if modelCalls.Load() != 1 || dispatchCalls.Load() != 2 {
			t.Fatalf("model=%d dispatch=%d", modelCalls.Load(), dispatchCalls.Load())
		}
		page, err := c.Replay(runctx, human, session.ID, PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		destination := ""
		for _, en := range page.Entries {
			if en.Role == "dispatch" && en.Status == "succeeded" {
				destination = en.Destination
			}
		}
		if destination == "" {
			t.Fatal("destination outcome missing")
		}
		jobs, err := rt.ListJobs(runctx, jobdb.ListJobsRequest{TenantIds: []string{human.TenantID}, PageSize: 100})
		if err != nil {
			t.Fatal(err)
		}
		destinations := 0
		for _, j := range jobs.Jobs {
			if strings.HasPrefix(j.JobKey.JobId, "assistant-goal-") {
				destinations++
				if j.JobKey.JobId != destination {
					t.Fatal("wrong destination")
				}
			}
		}
		if destinations != 1 {
			t.Fatalf("destination jobs %d", destinations)
		}
		for runctx.Err() == nil {
			state, err := ctl.InspectJob(runctx, jobdb.JobKey{TenantId: human.TenantID, JobId: destination})
			if err != nil {
				t.Fatal(err)
			}
			if state.Terminal {
				if state.Status != "completed" {
					t.Fatalf("target build failed: %+v", state)
				}
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, err = c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *next.Form.RequestId, SubmissionID: "finished", Response: Command{Command: "finish"}})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	currentForAuth := waitInput(session.JobID)
	authPage, err := c.Replay(runctx, human, session.ID, PageRequest{})
	if err != nil || len(authPage.Entries) < 2 || len(authPage.Entries[1].Artifacts) != 1 {
		t.Fatal("missing authorization artifact fixture", err)
	}
	protectedArtifact := authPage.Entries[1].Artifacts[0].ID
	for _, denied := range []Caller{{TenantID: "other-tenant", Actor: human.Actor}, {TenantID: human.TenantID, Actor: input.Actor{ID: "other-person", Kind: "human"}}} {
		checks := map[string]func() error{
			"start": func() error {
				_, e := c.Start(runctx, denied, StartInput{RequestID: "denied", SubmittedAt: time.Unix(10, 0), Prompt: "denied", Source: Source{Kind: "global"}})
				return e
			},
			"get":    func() error { _, e := c.Get(runctx, denied, session.ID); return e },
			"list":   func() error { _, e := c.List(runctx, denied, PageRequest{}); return e },
			"replay": func() error { _, e := c.Replay(runctx, denied, session.ID, PageRequest{}); return e },
			"respond": func() error {
				_, e := c.Respond(runctx, denied, session.ID, input.StructuredSubmission{RequestID: *currentForAuth.Form.RequestId, SubmissionID: "denied", Response: Command{Command: "finish"}})
				return e
			},
			"reopen":   func() error { _, e := c.Reopen(runctx, denied, session.ID); return e },
			"artifact": func() error { _, e := c.OpenArtifact(runctx, denied, session.ID, protectedArtifact); return e },
		}
		for name, check := range checks {
			if e := check(); !errors.Is(e, ErrForbidden) {
				t.Fatalf("%s caller=%+v: %v", name, denied, e)
			}
		}
	}
	if modelCalls.Load() != 1 {
		t.Fatal("denied operations inferred")
	}
	stillPending := waitInput(session.JobID)
	if *stillPending.Form.RequestId != *currentForAuth.Form.RequestId {
		t.Fatal("denied operation consumed input")
	}

	for i := 0; i < 3; i++ {
		current := waitInput(session.JobID)
		page, err := c.Replay(runctx, human, session.ID, PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > 1 && len(page.Entries[1].Artifacts) > 0 {
			reader, err := c.OpenArtifact(runctx, human, session.ID, page.Entries[1].Artifacts[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil || string(data) != "document bytes" {
				t.Fatal(err, string(data))
			}
		} else {
			t.Fatal("completed document missing")
		}
		if i > 0 {
			en := page.Entries[2]
			if en.Receipt == nil || en.Receipt.Actor != human.Actor || len(en.Artifacts) != 1 {
				t.Fatalf("accepted upload evidence %+v", en)
			}
			reader, err := c.OpenArtifact(runctx, human, session.ID, en.Artifacts[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil || string(data) != strings.Repeat("x", 1200)+"uploaded bytes" {
				t.Fatal(err, string(data))
			}
		}
		if len(page.Entries) != 2+i*2 {
			t.Fatalf("actual replay entries: %+v", page.Entries)
		}
		command := Command{Command: "send", Prompt: fmt.Sprint("turn ", i), Attachments: map[string]artifacts.Ref{}}
		if i == 2 {
			previous := *current.Form.RequestId
			_, err := inputs.SubmitStructuredResponse(runctx, human.TenantID, session.JobID, input.StructuredSubmission{RequestID: previous, SubmissionID: "invalid-direct", Response: map[string]any{"command": "send", "prompt": strings.Repeat("€", c.cfg.Limits.Human/3+1), "attachments": map[string]any{}}}, human.Actor)
			if err != nil {
				t.Fatal(err)
			}
			for runctx.Err() == nil {
				current = waitInput(session.JobID)
				if *current.Form.RequestId != previous {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if modelCalls.Load() != 3 {
				t.Fatal("invalid accepted input inferred")
			}
			command = Command{Command: "finish"}
		}
		var submitted any = command
		if i == 0 {
			submitted = map[string]any{"command": "send", "prompt": command.Prompt, "attachments": map[string]any{"uploaded.txt": jobdb.NewArtifactFromBytes("uploaded.txt", []byte(strings.Repeat("x", 1200)+"uploaded bytes"))}}
		}
		if _, err = c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *current.Form.RequestId, SubmissionID: fmt.Sprint(i), Response: submitted}); err != nil {
			t.Fatal(err)
		}
	}
	for runctx.Err() == nil {
		d, err := ctl.InspectJob(runctx, jobdb.JobKey{TenantId: human.TenantID, JobId: session.JobID})
		if err == nil && d.Terminal {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if modelCalls.Load() != 3 {
		t.Fatal(modelCalls.Load())
	}
	closed, err := c.Get(runctx, human, session.ID)
	if err != nil || closed.Status != string(story.WorkflowStatusCompleted) {
		t.Fatal(err, closed.Status)
	}
	// Hash actual artifact downloads before replacing the facade and worker, then
	// compare every visible artifact after each restart using the retained store.
	artifactDigests := func() []string {
		t.Helper()
		p, err := c.Replay(runctx, human, session.ID, PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var digests []string
		for _, entry := range p.Entries {
			for _, artifact := range entry.Artifacts {
				r, err := c.OpenArtifact(runctx, human, session.ID, artifact.ID)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(r)
				closeErr := r.Close()
				if readErr != nil || closeErr != nil {
					t.Fatalf("artifact %s read=%v close=%v", artifact.Name, readErr, closeErr)
				}
				if int64(len(body)) != artifact.Size {
					t.Fatalf("artifact %s bytes=%d advertised=%d", artifact.Name, len(body), artifact.Size)
				}
				digests = append(digests, fmt.Sprintf("%s:%s:%x", artifact.Name, artifact.MIME, sha256.Sum256(body)))
			}
		}
		return digests
	}
	originalArtifacts := artifactDigests()
	if len(originalArtifacts) != 4 {
		t.Fatalf("expected three documents and one upload, got %v", originalArtifacts)
	}
	// Replace the worker engine and facade while retaining only runtime storage.
	cancel()
	<-done
	runctx, cancel = context.WithTimeout(context.Background(), duration)
	defer cancel()
	replacement, err := jobworkflow.NewEngineBuilder().WithRuntime(scoped).WithWorkerTenantId(human.TenantID).BuildEngine()
	if err != nil {
		t.Fatal(err)
	}
	c, err = New(c.host, c.cfg)
	if err != nil {
		t.Fatal(err)
	}
	replacementRegistry, err := workerops.NewActivityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	// Retain only stateless infrastructure/model fixtures; rebuild the real
	// host-bound assistant activity rather than retaining its old Client.
	for name, registration := range registry.GetAll() {
		if registration.Metadata.Type != ActivityName {
			replacementRegistry.UpdateRegistration(name, registration)
		}
	}
	replacementActivity, err := bindActivity(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = RegisterActivity(replacementRegistry, replacementActivity); err != nil {
		t.Fatal(err)
	}
	registry = replacementRegistry
	workset, err := compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(ctl).Build(), registry, compiler.RecipeJobWorkerOptions{RootSourceResolver: resolver, Allocation: scoped.Allocation, WrapTaskWorker: scoped.WrapTaskWorker, StageExecution: scoped.Stage})
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.RegisterWorkers(workset); err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	go func() { defer close(done); replacement.Run(runctx) }()
	type reopenResult struct {
		session Session
		err     error
	}
	results := make(chan reopenResult, 4)
	for i := 0; i < 4; i++ {
		go func() { s, e := c.Reopen(runctx, human, session.ID); results <- reopenResult{s, e} }()
	}
	var reopened Session
	for i := 0; i < 4; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if i > 0 && r.session.JobID != reopened.JobID {
			t.Fatal("concurrent reopen forked session")
		}
		reopened = r.session
	}

	fresh := waitInput(reopened.JobID)
	if fresh.Form.RequestId == nil {
		t.Fatal("no new request")
	}
	page, err := c.Replay(runctx, human, session.ID, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 8 || modelCalls.Load() != 3 {
		t.Fatalf("reopen replay=%+v calls=%d", page.Entries, modelCalls.Load())
	}
	if got := artifactDigests(); strings.Join(got, "\n") != strings.Join(originalArtifacts, "\n") {
		t.Fatalf("artifact content/metadata changed after worker replacement and reopen: got %v want %v", got, originalArtifacts)
	}
	if _, err = c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *fresh.Form.RequestId, SubmissionID: "reclosed", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
	for runctx.Err() == nil {
		d, err := c.read(runctx, human, reopened.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Story.Status == story.WorkflowStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	again, err := c.Reopen(runctx, human, session.ID)
	if err != nil || again.JobID == reopened.JobID {
		t.Fatal("later reopen failed", err)
	}
	laterInput := waitInput(again.JobID)
	later, err := c.Replay(runctx, human, session.ID, PageRequest{})
	if err != nil || len(later.Entries) != 8 || modelCalls.Load() != 3 || *laterInput.Form.RequestId == *fresh.Form.RequestId {
		t.Fatal("later restart lost prefix or reused input", err)
	}
	if got := artifactDigests(); strings.Join(got, "\n") != strings.Join(originalArtifacts, "\n") {
		t.Fatalf("artifact content/metadata changed after later reopen: got %v want %v", got, originalArtifacts)
	}
	if _, err = c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *laterInput.Form.RequestId, SubmissionID: "final-close", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
	t.Log("denied tenant/actor across seven facade operations; four concurrent reopens and later finish/reopen retained prefix without inference")

}

// exerciseLargeWorkerHistory uses actual SQLite task history, not synthetic
// story nodes. Model execution/release resolution remain explicit fixtures.
func exerciseLargeWorkerHistory(t *testing.T, runctx context.Context, c *Client, session Session, waitInput func(string) *input.UserInputDetails, calls *atomic.Int32) {
	t.Helper()
	current := waitInput(session.JobID)
	advance := func(command Command, submission string) {
		t.Helper()
		previous := *current.Form.RequestId
		if _, err := c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: previous, SubmissionID: submission, Response: command}); err != nil {
			t.Fatal(err)
		}
		for runctx.Err() == nil {
			current = waitInput(session.JobID)
			if *current.Form.RequestId != previous {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal(runctx.Err())
	}
	for i := 1; i < 8; i++ {
		label := fmt.Sprintf("large turn %d\n", i)
		advance(Command{Command: "send", Prompt: label + strings.Repeat("&", (32<<10)-len(label)), Attachments: map[string]artifacts.Ref{}}, fmt.Sprint(i))
	}
	d, err := c.read(runctx, human, session.JobID)
	if err != nil {
		t.Fatal(err)
	}
	expected := completed(d.Entries)
	if len(expected) != 16 || len(mustJSON(expected)) <= 4<<20 {
		t.Fatalf("history entries=%d bytes=%d", len(expected), len(mustJSON(expected)))
	}
	first, err := c.Replay(runctx, human, session.ID, PageRequest{Limit: 100})
	if err != nil || !first.HasMore || first.Cursor == "" {
		t.Fatal(err, first.HasMore)
	}
	if len(mustJSON(first)) > c.cfg.Limits.Page {
		t.Fatal("page exceeded encoded cap")
	}
	// Append after taking the first page: the saved cursor must retain its watermark.
	advance(Command{Command: "send", Prompt: "append beyond watermark", Attachments: map[string]artifacts.Ref{}}, "append")
	all := append([]Entry{}, first.Entries...)
	cursor := first.Cursor
	pages := 1
	for cursor != "" {
		page, err := c.Replay(runctx, human, session.ID, PageRequest{Cursor: cursor, Limit: 100})
		if err != nil || page.Watermark != first.Watermark || !page.Complete || len(mustJSON(page)) > c.cfg.Limits.Page {
			t.Fatal("unstable page", err)
		}
		all = append(page.Entries, all...)
		cursor = page.Cursor
		pages++
	}
	if string(mustJSON(all)) != string(mustJSON(expected)) {
		t.Fatal("paging lost, duplicated, or reordered recorded entries")
	}
	refreshed, err := c.Replay(runctx, human, session.ID, PageRequest{})
	if err != nil || refreshed.Watermark == first.Watermark {
		t.Fatal("refresh did not observe append", err)
	}
	before := calls.Load()
	if before != 9 {
		t.Fatal(before)
	}
	// Finishing at the large-history boundary must not depend on one page fitting.
	if _, err := c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *current.Form.RequestId, SubmissionID: "large-finish", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
	for runctx.Err() == nil {
		d, err := c.read(runctx, human, session.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Story.Status == story.WorkflowStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	reopened, err := c.Reopen(runctx, human, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh := waitInput(reopened.JobID)
	if calls.Load() != before || *fresh.Form.RequestId == *current.Form.RequestId {
		t.Fatal("reopen repeated inference or reused input")
	}
	// A cursor from the former execution cannot be spliced into the new lineage.
	if _, err = c.Replay(runctx, human, session.ID, PageRequest{Cursor: first.Cursor}); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrStale) {
		t.Fatal("old execution cursor accepted", err)
	}
	if _, err = c.Respond(runctx, human, session.ID, input.StructuredSubmission{RequestID: *fresh.Form.RequestId, SubmissionID: "large-reclosed", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("SQLite worker: %d retained entries, %d encoded bytes, %d pages; append watermark, finish and reopen verified; model calls=%d", len(expected), len(mustJSON(expected)), pages, before)
}

// Exercise actual story/input services over embedded SQLite, with no Cortex or
// deployment dependency. Model/release resolution remain deterministic fixtures.
func exerciseWorkerContextCapture(t *testing.T, ctx context.Context, c *Client, session Session, waitInput func(string) *input.UserInputDetails, calls *atomic.Int32) {
	t.Helper()
	current := waitInput(session.JobID)
	source := Source{Kind: "story", JobID: session.JobID}
	captured, err := c.Capture(ctx, human, source)
	if err != nil || captured.Source != source || captured.At.IsZero() || !strings.Contains(captured.Text, "worker reply") || !strings.Contains(captured.Text, "hello") || len(captured.Artifacts) == 0 {
		t.Fatalf("story capture: %+v, %v", captured, err)
	}
	if _, err := c.Capture(ctx, Caller{TenantID: "denied", Actor: human.Actor}, source); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized source capture: %v", err)
	}
	ref, err := recipejob.SubmitRecipeJob(ctx, recipejob.BuildStartJobRequest{TenantID: human.TenantID, JobID: "review-source", Target: c.cfg.Root, Recipe: "review-fixture", Inputs: map[string]any{}}, c.host.Submitter)
	if err != nil {
		t.Fatal(err)
	}
	first := waitInput(ref.JobID)
	reviewSource := Source{Kind: "review", JobID: ref.JobID, RequestID: *first.Form.RequestId}
	review, err := c.Capture(ctx, human, reviewSource)
	if err != nil || review.At.IsZero() || review.Source != reviewSource || !strings.Contains(review.Text, "First contextual review") {
		t.Fatalf("review capture: %+v, %v", review, err)
	}
	wrong := reviewSource
	wrong.RequestID = "forged-request"
	if _, err := c.Capture(ctx, human, wrong); !errors.Is(err, ErrStale) {
		t.Fatalf("forged review identity: %v", err)
	}
	out, err := c.host.Input.SubmitFormResponse(ctx, human.TenantID, ref.JobID, input.FormSubmission{RequestID: reviewSource.RequestID, SubmissionID: "advance-review", Fields: map[string]any{"decision": "continue"}}, human.Actor)
	if err != nil || out.Receipt == nil || out.Receipt.Actor != human.Actor {
		t.Fatalf("review response: %+v, %v", out, err)
	}
	var second *input.UserInputDetails
	for ctx.Err() == nil {
		second = waitInput(ref.JobID)
		if *second.Form.RequestId != reviewSource.RequestID {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if second == nil || *second.Form.RequestId == reviewSource.RequestID {
		t.Fatal("review did not advance")
	}
	if _, err := c.Capture(ctx, human, reviewSource); !errors.Is(err, ErrStale) {
		t.Fatalf("replaced review captured: %v", err)
	}
	newReview, err := c.Capture(ctx, human, Source{Kind: "review", JobID: ref.JobID, RequestID: *second.Form.RequestId})
	if err != nil || !strings.Contains(newReview.Text, "Replacement contextual review") || strings.Contains(review.Text, "Replacement contextual review") {
		t.Fatalf("review replacement changed prior capture: %v", err)
	}
	still := waitInput(session.JobID)
	if *still.Form.RequestId != *current.Form.RequestId || calls.Load() != 1 {
		t.Fatal("context reads consumed assistant input or invoked inference")
	}
	if _, err := c.Respond(ctx, human, session.ID, input.StructuredSubmission{RequestID: *still.Form.RequestId, SubmissionID: "context-finish", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
}

func exerciseWorkerMediaDownloads(t *testing.T, ctx context.Context, c *Client, session Session, waitInput func(string) *input.UserInputDetails, calls *atomic.Int32) {
	t.Helper()
	pixel, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	// Media is transported as opaque bytes. These container-header fixtures do
	// not assert playback, decoding, understanding or provider media support.
	expected := map[string][]byte{
		"pixel.png": pixel,
		"clip.mp4":  []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"),
		"sound.wav": []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00"),
		"plain.txt": []byte("plain bytes"),
		"report.md": []byte("# report"),
		"data.json": []byte(`{"ok":true}`),
	}
	mimeTypes := map[string]string{"pixel.png": "image/png", "clip.mp4": "video/mp4", "sound.wav": "audio/x-wav", "plain.txt": "text/plain", "report.md": "text/markdown", "data.json": "application/json"}
	pending := waitInput(session.JobID)
	uploads := map[string]any{}
	for _, name := range []string{"pixel.png", "clip.mp4", "sound.wav"} {
		uploads[name] = jobdb.NewArtifactFromBytes(name, expected[name])
	}
	_, err = c.Respond(ctx, human, session.ID, input.StructuredSubmission{RequestID: *pending.Form.RequestId, SubmissionID: "media-upload", Response: map[string]any{"command": "send", "prompt": "return these files with text documents", "attachments": uploads}})
	if err != nil {
		t.Fatal(err)
	}
	var fresh *input.UserInputDetails
	for ctx.Err() == nil {
		fresh = waitInput(session.JobID)
		if *fresh.Form.RequestId != *pending.Form.RequestId {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	verify := func() {
		t.Helper()
		page, err := c.Replay(ctx, human, session.ID, PageRequest{})
		if err != nil || len(page.Entries) != 4 {
			t.Fatalf("media replay entries=%d: %v", len(page.Entries), err)
		}
		reply := page.Entries[3]
		if reply.Role != "assistant" || len(reply.Artifacts) != len(expected) {
			t.Fatalf("returned artifact evidence: %+v", reply)
		}
		seen := map[string]bool{}
		for _, artifact := range reply.Artifacts {
			r, err := c.OpenArtifact(ctx, human, session.ID, artifact.ID)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(r)
			closeErr := r.Close()
			want, exists := expected[artifact.Name]
			// Extension MIME may include parameters; WAV has platform aliases.
			mimeOK := strings.Split(r.MIME, ";")[0] == mimeTypes[artifact.Name]
			if artifact.Name == "sound.wav" && (r.MIME == "audio/wav" || r.MIME == "audio/vnd.wave") {
				mimeOK = true
			}
			if readErr != nil || closeErr != nil || !exists || seen[artifact.Name] || sha256.Sum256(body) != sha256.Sum256(want) || r.Size != int64(len(want)) || r.Filename != artifact.Name || !mimeOK {
				t.Fatalf("download %s mime=%s size=%d read=%v close=%v", artifact.Name, r.MIME, r.Size, readErr, closeErr)
			}
			seen[artifact.Name] = true
		}
		if _, err := c.OpenArtifact(ctx, human, session.ID, "unbound-artifact"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unbound artifact: %v", err)
		}
		if calls.Load() != 2 {
			t.Fatalf("reads invoked inference: %d", calls.Load())
		}
	}
	verify()
	if _, err := c.Respond(ctx, human, session.ID, input.StructuredSubmission{RequestID: *fresh.Form.RequestId, SubmissionID: "media-close", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		d, err := c.read(ctx, human, session.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Story.Status == story.WorkflowStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, err = New(c.host, c.cfg)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := c.Reopen(ctx, human, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := waitInput(reopened.JobID)
	verify()
	if _, err := c.Respond(ctx, human, session.ID, input.StructuredSubmission{RequestID: *last.Form.RequestId, SubmissionID: "media-final-close", Response: Command{Command: "finish"}}); err != nil {
		t.Fatal(err)
	}
}

func exerciseWorkerMembership(t *testing.T, ctx context.Context, c *Client, first Session, waitInput func(string) *input.UserInputDetails, calls *atomic.Int32) {
	t.Helper()
	waitInput(first.JobID)
	peer := Caller{TenantID: human.TenantID, Actor: input.Actor{Kind: "human", ID: "peer"}}
	second, err := c.Start(ctx, peer, StartInput{RequestID: "peer", SubmittedAt: time.Unix(11, 0), Prompt: "peer conversation", Source: Source{Kind: "global"}})
	if err != nil {
		t.Fatal(err)
	}
	waitInput(second.JobID)
	for _, pair := range []struct {
		who          Caller
		own, foreign Session
	}{{human, first, second}, {peer, second, first}} {
		who, own, foreign := pair.who, pair.own, pair.foreign
		details, err := c.Get(ctx, who, own.ID)
		if err != nil || details.Current == nil {
			t.Fatalf("own get: %v", err)
		}
		listed, err := c.List(ctx, who, PageRequest{})
		if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].ID != own.ID {
			t.Fatalf("membership discovery: %+v %v", listed, err)
		}
		page, err := c.Replay(ctx, who, own.ID, PageRequest{})
		if err != nil || len(page.Entries) != 2 || len(page.Entries[1].Artifacts) != 1 {
			t.Fatalf("own replay: %+v %v", page, err)
		}
		reader, err := c.OpenArtifact(ctx, who, own.ID, page.Entries[1].Artifacts[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || string(body) != "document bytes" {
			t.Fatal("own download failed", readErr, closeErr)
		}
		active, err := c.Reopen(ctx, who, own.ID)
		if err != nil || active.JobID != own.JobID {
			t.Fatalf("own active reopen: %+v %v", active, err)
		}
		foreignInput := waitInput(foreign.JobID)
		checks := map[string]func() error{
			"get":    func() error { _, err := c.Get(ctx, who, foreign.ID); return err },
			"replay": func() error { _, err := c.Replay(ctx, who, foreign.ID, PageRequest{}); return err },
			"artifact": func() error {
				_, err := c.OpenArtifact(ctx, who, foreign.ID, page.Entries[1].Artifacts[0].ID)
				return err
			},
			"reopen": func() error { _, err := c.Reopen(ctx, who, foreign.ID); return err },
			"respond": func() error {
				_, err := c.Respond(ctx, who, foreign.ID, input.StructuredSubmission{RequestID: *foreignInput.Form.RequestId, SubmissionID: "foreign-close", Response: Command{Command: "finish"}})
				return err
			},
			"source-start": func() error {
				_, err := c.Start(ctx, who, StartInput{RequestID: "foreign-source", SubmittedAt: time.Unix(12, 0), Prompt: "read foreign", Source: Source{Kind: "story", JobID: foreign.ID}})
				return err
			},
		}
		for operation, check := range checks {
			if err := check(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("foreign %s: %v", operation, err)
			}
		}
		if still := waitInput(foreign.JobID); *still.Form.RequestId != *foreignInput.Form.RequestId {
			t.Fatal("foreign operation consumed input")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("membership reads invoked inference: %d", calls.Load())
	}
	for _, pair := range []struct {
		who     Caller
		session Session
	}{{human, first}, {peer, second}} {
		current := waitInput(pair.session.JobID)
		if _, err := c.Respond(ctx, pair.who, pair.session.ID, input.StructuredSubmission{RequestID: *current.Form.RequestId, SubmissionID: "own-close", Response: Command{Command: "finish"}}); err != nil {
			t.Fatal(err)
		}
	}
}
