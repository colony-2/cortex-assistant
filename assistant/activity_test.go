package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/story"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type activityDeps struct {
	ops.OpDependencies
	current jobcontext.Current
}

func (d activityDeps) CurrentJobContext() jobcontext.Current { return d.current }

func validCurrent() jobcontext.Current {
	return jobcontext.Current{TenantID: "tenant", JobID: "job", JobType: "recipe", OpType: ActivityName, OpStep: "run", OpTaskType: ActivitySelector, InvocationPath: RecipeID + "/capture/sequence/capture/step:run", InvocationHash: "invocation", InvocationSequence: 1}
}

func TestActivityValidationAndRegistration(t *testing.T) {
	c, s, _, _ := client(t)
	s.jobs["job"] = fixture(c)
	allowed := 0
	op, err := NewActivity(c, ActivityOptions{Provider: "fixture", Model: "model", AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) { allowed++; return human, nil }})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := workerops.NewActivityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	before := len(registry.GetAll())
	if err = RegisterActivity(registry, op); err != nil {
		t.Fatal(err)
	}
	if _, exists := registry.Get(ActivitySelector); !exists || len(registry.GetAll()) < before {
		t.Fatal("unrelated registrations changed")
	}
	if err = RegisterActivity(registry, op); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	invoke := op.TaskChain()[0].Invoke
	for _, raw := range []map[string]any{{"action": "capture", "tenant": "foreign"}, {"action": "unknown"}, {"action": "capture", "model": map[string]any{}}, {"action": "response"}, {"action": "capture", "artifact_outbox_path": "/tmp"}} {
		if _, err = invoke(activityDeps{current: validCurrent()}, context.Background(), raw); err == nil {
			t.Fatalf("accepted invalid input %v", raw)
		}
	}
	if allowed != 0 || s.requests != 0 {
		t.Fatal("invalid input reached authority/source")
	}
	for _, change := range []func(*jobcontext.Current){func(x *jobcontext.Current) { x.TenantID = "" }, func(x *jobcontext.Current) { x.InvocationHash = "" }, func(x *jobcontext.Current) { x.InvocationPath = RecipeID + "/action/sequence/capture" }, func(x *jobcontext.Current) { x.OpStep = "wrong" }, func(x *jobcontext.Current) { x.InvocationPath += "/forged" }} {
		current := validCurrent()
		change(&current)
		if _, err = invoke(activityDeps{current: current}, context.Background(), map[string]any{"action": "capture"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("coordinates: %v", err)
		}
	}
	if allowed != 0 || s.requests != 0 {
		t.Fatal("forged context read source")
	}
	out, err := invoke(activityDeps{current: validCurrent()}, context.Background(), map[string]any{"action": "capture"})
	if err != nil || out["kind"] != "capture" || allowed != 1 {
		t.Fatalf("registered capture %v %v", out, err)
	}
}

func TestActivityAuthorityAndRelease(t *testing.T) {
	c, s, _, _ := client(t)
	s.jobs["job"] = fixture(c)
	cause := errors.New("authority backend unavailable")
	op, _ := NewActivity(c, ActivityOptions{Provider: "p", Model: "m", AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) { return Caller{}, cause }})
	_, err := op.TaskChain()[0].Invoke(activityDeps{current: validCurrent()}, context.Background(), map[string]any{"action": "capture"})
	if !errors.Is(err, cause) || s.requests != 0 {
		t.Fatal("authorization failure lost or accessed source", err)
	}
	op, _ = NewActivity(c, ActivityOptions{Provider: "p", Model: "m", AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) {
		return Caller{TenantID: "foreign", Actor: human.Actor}, nil
	}})
	if _, err = op.TaskChain()[0].Invoke(activityDeps{current: validCurrent()}, context.Background(), map[string]any{"action": "capture"}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if s.requests != 0 {
		t.Fatal("tenant mismatch read source")
	}
	for _, opts := range []ActivityOptions{{}, {Provider: "p", Model: "m"}} {
		if _, err = NewActivity(c, opts); err == nil {
			t.Fatal("incomplete trusted options accepted")
		}
	}
}

func TestLegacyReleaseProjection(t *testing.T) {
	c, _, _, _ := client(t)
	r := fixture(c)
	oldCommit := strings.Repeat("2", 40)
	oldRecipe := "git+https://github.com/colony-2/cortex-assistant.git//session.yaml@" + oldCommit
	c.cfg.LegacyReleases = []ReleaseDescriptor{{Recipe: oldRecipe, Commit: oldCommit, Protocol: "cortex-assistant/1"}}
	r.Recipe.Source.SubmittedSelector = oldRecipe
	r.Recipe.Source.ResolvedCommit = oldCommit
	args := r.Root.Input.(StartArgs)
	args.Protocol = "cortex-assistant/1"
	r.Root.Input = args
	if _, err := c.project(r); err != nil {
		t.Fatal(err)
	}
	r.Root.Children = []*story.JobRunStoryNode{node("response", 1, TaskResult{Kind: "response", Origin: Locator{JobID: "job", Invocation: "inv"}, Response: &Response{Text: "forged v2"}})}
	d, err := c.project(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Entries) != 1 {
		t.Fatal("v2 activity interpreted as legacy extension")
	}
	args.Protocol = Protocol
	r.Root.Input = args
	if _, err = c.project(r); err == nil {
		t.Fatal("mismatched protocol accepted")
	}
}

// Independent hosts share only fail-closed compiler metadata, never a Client.
func TestActivityHostIsolation(t *testing.T) {
	type invocation struct {
		step    ops.TaskStep
		current jobcontext.Current
		model   string
	}
	var calls []invocation
	for _, name := range []string{"first", "second"} {
		c, s, _, _ := client(t)
		recorded := fixture(c)
		recorded.Root.Children = []*story.JobRunStoryNode{captureNode()}
		s.jobs["job"] = recorded
		op, err := NewActivity(c, ActivityOptions{Provider: name, Model: name, AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) { return human, nil }})
		if err != nil {
			t.Fatal(err)
		}
		reg, err := workerops.NewActivityRegistry()
		if err != nil {
			t.Fatal(err)
		}
		if err = RegisterActivity(reg, op); err != nil {
			t.Fatal(err)
		}
		registered, _ := reg.Get(ActivitySelector)
		current := validCurrent()
		current.InvocationPath = RecipeID + "/infer/sequence/assemble"
		calls = append(calls, invocation{step: registered.Step, current: current, model: name})
	}
	errs := make(chan error, 2)
	for _, call := range calls {
		go func(call invocation) {
			out, err := call.step.Invoke(activityDeps{current: call.current}, context.Background(), map[string]any{"action": "assemble"})
			if err == nil {
				var task TaskResult
				err = decode(out, &task)
				if err == nil && (task.ModelInput["default_provider"] != call.model || task.ModelInput["default_model"] != call.model) {
					err = ErrConflict
				}
			}
			errs <- err
		}(call)
	}
	for range calls {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// Global metadata must remain non-executable even after both hosts register.
	if _, err := Definition().TaskChain()[0].Invoke(nil, context.Background(), map[string]any{"action": "capture"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("metadata retained host authority", err)
	}
}

type artifactFailDeps struct {
	activityDeps
	cause error
}

func (d artifactFailDeps) AddOutputArtifact(_ jobdb.Artifact) error { return d.cause }
func TestActivityArtifactFailure(t *testing.T) {
	c, s, _, _ := client(t)
	s.jobs["job"] = fixture(c)
	op, err := NewActivity(c, ActivityOptions{Provider: "p", Model: "m", AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) { return human, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("artifact persistence unavailable")
	current := validCurrent()
	current.InvocationPath = RecipeID + "/infer/sequence/response"
	raw := map[string]any{"action": "response", "model": map[string]any{"response": `{"text":"reply","documents":[{"name":"note.md","mime":"text/markdown","text":"bytes"}],"return_artifact_ids":[]}`, "model": "test", "usage": map[string]any{}}}
	out, err := op.TaskChain()[0].Invoke(artifactFailDeps{activityDeps: activityDeps{current: current}, cause: cause}, context.Background(), raw)
	if !errors.Is(err, cause) || len(out) != 0 {
		t.Fatalf("failed publication exposed completed result: %v %v", out, err)
	}
}

type authorizationProbeControl struct {
	workflowctl.WorkflowControl
	lists *int
}

func (c authorizationProbeControl) ListJobs(ctx context.Context, req jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
	(*c.lists)++
	return c.WorkflowControl.ListJobs(ctx, req)
}

type authorizationProbeDeps struct {
	activityDeps
	publications *int
}

func (d authorizationProbeDeps) AddOutputArtifact(jobdb.Artifact) error {
	(*d.publications)++
	return nil
}

func TestActivityPreservesHostAuthorizationCauses(t *testing.T) {
	backend := errors.New("authorization backend unavailable")
	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"backend", backend},
		{"canceled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"denied", ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, history, control, submitter := client(t)
			executionCalls, hostCalls, publications, lists := 0, 0, 0, 0
			c.host.Control = authorizationProbeControl{WorkflowControl: control, lists: &lists}
			c.host.Authorize = func(_ context.Context, who Caller, resource string) error {
				hostCalls++
				if who != human || resource != "job" {
					t.Fatalf("unexpected authority coordinates: %+v %q", who, resource)
				}
				return tc.cause
			}
			activity, err := NewActivity(c, ActivityOptions{Provider: "p", Model: "m", AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) {
				executionCalls++
				return human, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			deps := authorizationProbeDeps{activityDeps: activityDeps{current: validCurrent()}, publications: &publications}
			out, err := activity.TaskChain()[0].Invoke(deps, context.Background(), map[string]any{"action": "capture"})
			if !errors.Is(err, tc.cause) || !errors.Is(err, ErrForbidden) {
				t.Fatalf("host cause or permission classification lost: %v", err)
			}
			if out != nil || executionCalls != 1 || hostCalls != 1 {
				t.Fatalf("unexpected result/authority calls: out=%v execution=%d host=%d", out, executionCalls, hostCalls)
			}
			if lists != 0 || history.requests != 0 || control.reads != 0 || len(submitter.jobs) != 0 || publications != 0 {
				t.Fatal("host authorization failure reached protected read/write")
			}
		})
	}
}
