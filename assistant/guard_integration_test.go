package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/execution"
	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	"github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/permissions"
	"github.com/colony-2/c2j/pkg/recipe"
	"github.com/colony-2/c2j/pkg/starter"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/runtime/toy"
	wf "github.com/colony-2/jobdb/pkg/workflow"
)

func TestGuardedActivityAllocationDenial(t *testing.T) {
	c, _, _, _ := client(t)
	authorized := 0
	activity, err := NewActivity(c, ActivityOptions{Provider: "p", Model: "m", AuthorizeExecution: func(context.Context, jobcontext.Current) (Caller, error) { authorized++; return human, nil }})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := workerops.NewActivityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err = RegisterActivity(registry, activity); err != nil {
		t.Fatal(err)
	}
	rec, err := recipe.LoadRecipeFromString([]byte("id: cortex-assistant-session\nexecution: {resources: {memory: 16Gi}}\nsequence:\n - id: capture\n   op: cortex_assistant\n   inputs: {action: capture}\n"))
	if err != nil {
		t.Fatal(err)
	}
	base := toy.New()
	engine, err := wf.NewEngineBuilder().WithRuntime(base).BuildEngine()
	if err != nil {
		t.Fatal(err)
	}
	jobctx, git := compiler.GenerateTestContext()
	ref, err := starter.StartRecipeJob(context.Background(), workflowctl.StartJob{TenantId: "tenant", RecipeName: RecipeID, JobContext: jobctx, GitRef: git.ParentRef}, jobdbschema.WorkflowEngine{Engine: engine, Registry: base}, *rec)
	if err != nil {
		t.Fatal(err)
	}
	memory := "2Gi"
	allocation := execution.Allocation{SchemaVersion: 1, Resources: execution.Resources{Memory: &memory}}
	runtime := executionruntime.New(base, allocation)
	worker := compiler.NewRecipeJobWorker(compiler.RecipeJobWorkerOptions{Allocation: allocation, StageExecution: runtime.Stage, WrapTaskWorker: runtime.WrapTaskWorker})
	tasks := registry.GetTaskWorkers(ops.NewServiceDepsBuilder().Build())
	for i, task := range tasks {
		tasks[i] = runtime.WrapTaskWorker(task)
	}
	run, err := wf.GetJobForRun(context.Background(), runtime, wf.GetJobForRunRequest{JobKey: ref.Key(), JobWorker: worker, TaskWorkers: tasks, WorkerID: "guard-test", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := run.Run(nil)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := compiler.ExecutionHandoffFromOutcome(outcome)
	if err != nil || handoff == nil || outcome.Status != wf.JobRunSuspended {
		t.Fatalf("allocation bypass: %+v %v", outcome, err)
	}
	if authorized != 0 {
		t.Fatal("denied allocation reached assistant execution")
	}
	info, err := base.GetJob(context.Background(), jobdb.JobKey{TenantId: "tenant", JobId: ref.Key().JobId})
	if err != nil {
		t.Fatal(err)
	}
	demand, err := execution.PayloadDemand(info.ClientPayload)
	if err != nil || demand == nil || demand.Effective.Resources.Memory == nil || *demand.Effective.Resources.Memory != "16Gi" {
		t.Fatalf("required allocation lost: %+v %v", demand, err)
	}
}

// Host grants and c2j secret grants have different owners. Exercise both through
// a real guarded WorkSet, rebuilding all workers over retained storage on replay.
func TestGuardedActivityPermissionDenials(t *testing.T) {
	for _, boundary := range []string{"tenant", "source", "workspace", "secret"} {
		t.Run(boundary, func(t *testing.T) {
			c, history, _, submitter := client(t)
			metadataRegistry, err := workerops.NewActivityRegistry()
			if err != nil {
				t.Fatal(err)
			}
			if err = RegisterActivity(metadataRegistry, Definition()); err != nil {
				t.Fatal(err)
			}
			base := toy.New()
			engine, err := wf.NewEngineBuilder().WithRuntime(base).BuildEngine()
			if err != nil {
				t.Fatal(err)
			}
			action := "capture"

			source := fmt.Sprintf(`id: cortex-assistant-session
perms: {jobdb: {}}
state:
  initial: capture
  states:
    capture:
      sequence:
        - id: capture
          op: cortex_assistant
          inputs: {action: '%s'}
`, action)
			rec, err := recipe.LoadRecipeFromString([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			jobctx, git := compiler.GenerateTestContext()
			ref, err := starter.StartRecipeJob(context.Background(), workflowctl.StartJob{TenantId: "tenant", RecipeName: RecipeID, JobContext: jobctx, GitRef: git.ParentRef}, jobdbschema.WorkflowEngine{Engine: engine, Registry: base}, *rec)
			if err != nil {
				t.Fatal(err)
			}
			authorized := 0
			for replacement := 0; replacement < 2; replacement++ {
				// Reconstruct runtime, registry, bound activity and compiler worker. The
				// second run replays the same persisted failure, rather than a fresh job.
				runtime := executionruntime.New(base, execution.Allocation{SchemaVersion: 1})
				activity, err := NewActivity(c, ActivityOptions{Provider: "p", Model: "m", AuthorizeExecution: func(ctx context.Context, current jobcontext.Current) (Caller, error) {
					authorized++
					if boundary == "secret" {
						access := permissions.FromContext(ctx)
						if access == nil {
							t.Fatal("guard did not inject permission scope")
						}
						_, err := access.Resolve(ctx, []string{"denied_model_key"})
						if err == nil {
							t.Fatal("denied secret resolved")
						}
						return Caller{}, err
					}
					if current.TenantID != "tenant" {
						t.Fatal("lost execution tenant")
					}
					return Caller{}, fmt.Errorf("%s grant denied: %w", boundary, ErrForbidden)
				}})
				if err != nil {
					t.Fatal(err)
				}
				registry, err := workerops.NewActivityRegistry()
				if err != nil {
					t.Fatal(err)
				}
				if err = RegisterActivity(registry, activity); err != nil {
					t.Fatal(err)
				}
				workset, err := compiler.NewRecipeWorkerWithOptions(ops.NewServiceDepsBuilder().Build(), registry, compiler.RecipeJobWorkerOptions{Allocation: runtime.Allocation, StageExecution: runtime.Stage, WrapTaskWorker: runtime.WrapTaskWorker})
				if err != nil {
					t.Fatal(err)
				}
				var tasks []wf.TaskWorker
				for _, task := range workset.TaskWorkers {
					tasks = append(tasks, task)
				}
				run, err := wf.GetJobForRun(context.Background(), runtime, wf.GetJobForRunRequest{JobKey: ref.Key(), JobWorker: workset.JobWorker, TaskWorkers: tasks, WorkerID: fmt.Sprintf("denial-%d", replacement), LeaseDuration: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				outcome, err := run.Run(nil)
				if err != nil {
					t.Fatal(err)
				}
				want := boundary + " grant denied"
				if boundary == "secret" {
					want = "lease permission denied"
				}
				if outcome.JobError == nil || !strings.Contains(outcome.JobError.Error(), want) {
					t.Fatalf("denial lost on replacement %d: %+v", replacement, outcome)
				}
				if history.requests != 0 || len(submitter.jobs) != 0 {
					t.Fatal("denial reached protected source/submission")
				}
			}
			if authorized != 1 {
				t.Fatalf("expected one authorization and replayed failure, got %d", authorized)
			}
		})
	}
}
