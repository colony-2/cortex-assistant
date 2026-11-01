package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/input"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type respondReadControl struct {
	*fixtureControl
	task  workflowctl.TaskHandle
	err   error
	reads int
}

func (c *respondReadControl) GetWaitingTask(context.Context, jobdb.JobKey) (workflowctl.TaskHandle, error) {
	c.reads++
	return c.task, c.err
}

type respondReadTask struct {
	workflowctl.TaskHandle
	data     jobdb.TaskData
	finished int
}

func (t *respondReadTask) JobKey() jobdb.JobKey {
	return jobdb.JobKey{TenantId: human.TenantID, JobId: "job"}
}
func (t *respondReadTask) TaskType() string                             { return "input:collect_user_input" }
func (t *respondReadTask) Data() (jobdb.TaskData, error)                { return t.data, nil }
func (t *respondReadTask) Finish(context.Context, jobdb.TaskData) error { t.finished++; return nil }

func TestRespondPreservesInputReadFailures(t *testing.T) {
	backendFailure := errors.New("backend unavailable")
	for _, tc := range []struct {
		name               string
		waitErr, errorWant error
		missingPayload     bool
	}{
		{name: "not-pending", waitErr: input.ErrInputNotPending, errorWant: ErrNotAtPrompt},
		{name: "backend", waitErr: backendFailure, errorWant: backendFailure},
		{name: "cancelled", waitErr: context.Canceled, errorWant: context.Canceled},
		{name: "deadline", waitErr: context.DeadlineExceeded, errorWant: context.DeadlineExceeded},
		{name: "restoration", missingPayload: true, errorWant: input.ErrStorage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, stories, control, submitter := client(t)
			stories.jobs["job"] = fixture(c)
			task := &respondReadTask{data: jobdb.NewTaskDataOrPanic(workerops.ActivityInvocationRequest{InputArtifact: "missing-invocation.json"})}
			fault := &respondReadControl{fixtureControl: control, err: tc.waitErr}
			if tc.missingPayload {
				fault.task = task
			}
			runtime, err := input.NewRuntime(fault, nil)
			if err != nil {
				t.Fatal(err)
			}
			c.host.Input = runtime
			out, err := c.Respond(ctx, human, "job", input.StructuredSubmission{RequestID: "request", SubmissionID: "submission", Response: Command{Command: "finish"}})
			if !errors.Is(err, tc.errorWant) {
				t.Fatalf("got %v, want cause %v", err, tc.errorWant)
			}
			if tc.errorWant != ErrNotAtPrompt && errors.Is(err, ErrNotAtPrompt) {
				t.Fatalf("read failure disguised as state: %v", err)
			}
			if tc.missingPayload && !strings.Contains(err.Error(), "restore pending input") {
				t.Fatalf("restoration cause lost: %v", err)
			}
			if fault.reads != 1 || task.finished != 0 || len(submitter.jobs) != 0 || out.Receipt != nil {
				t.Fatalf("read failure submitted/completed input: reads=%d finished=%d submitted=%d receipt=%+v", fault.reads, task.finished, len(submitter.jobs), out.Receipt)
			}
		})
	}
}
