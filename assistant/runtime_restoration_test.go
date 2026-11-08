package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/input"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type handle struct {
	workflowctl.TaskHandle
	data jobdb.TaskData
}

func (h handle) JobKey() jobdb.JobKey          { return jobdb.JobKey{TenantId: "tenant", JobId: "job"} }
func (h handle) TaskType() string              { return "input:collect_user_input" }
func (h handle) Data() (jobdb.TaskData, error) { return h.data, nil }

type control struct {
	workflowctl.WorkflowControl
	task handle
}

func (c control) GetWaitingTask(context.Context, jobdb.JobKey) (workflowctl.TaskHandle, error) {
	return c.task, nil
}
func (c control) ListJobs(context.Context, jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
	return []workflowctl.JobItem{{JobSummary: jobdb.JobSummary{JobKey: c.task.JobKey()}}}, "", nil
}

// Regression of the original public-API reproduction: both sizes must now
// preserve frozen identity and complete application payloads.
func TestPendingInputRestoration(t *testing.T) {
	ctx := context.Background()
	for _, size := range []int{1024, 64 << 10} {
		form := map[string]any{"request_id": "real-request", "requested_at": "2026-11-01T00:00:00Z", "request": map[string]any{"text": strings.Repeat("<", size)}, "response_schema": map[string]any{"type": "object"}}
		taskData, err := workerops.NewInvocationTaskData(workerops.ActivityInvocationRequest{Input: form})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := taskData.GetData()
		if err != nil {
			t.Fatal(err)
		}
		var envelope workerops.ActivityInvocationRequest
		if err = json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		offloaded := envelope.InputArtifact != ""
		runtime, err := input.NewRuntime(control{task: handle{data: taskData}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		details, err := runtime.GetDetails(ctx, "tenant", "job")
		if err != nil {
			t.Fatal(err)
		}
		missing := details.Form.RequestId == nil

		frozen, err := runtime.GetForm(ctx, "tenant", "job")
		if err != nil || frozen.RequestID != "real-request" || frozen.RequestedAt != form["requested_at"] || !reflect.DeepEqual(frozen.Request, form["request"]) || !reflect.DeepEqual(frozen.ResponseSchema, form["response_schema"]) {
			t.Fatalf("GetForm restoration size=%d: %+v %v", size, frozen, err)
		}
		if missing || *details.Form.RequestId != "real-request" || details.Form.Request == nil || !reflect.DeepEqual(*details.Form.Request, form["request"]) {
			t.Fatalf("GetDetails restoration size=%d: %+v", size, details.Form)
		}
		if offloaded != (size == 64<<10) {
			t.Fatalf("fixture did not exercise expected invocation offload: size=%d offloaded=%t", size, offloaded)
		}
		t.Logf("UTF-8 bytes=%d encoded form=%d offloaded=%t: identity, request and schema preserved", size, len(mustJSON(form)), offloaded)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestPendingInputRestorationUnavailable(t *testing.T) {
	data, err := workerops.NewInvocationTaskData(workerops.ActivityInvocationRequest{Input: map[string]any{"request_id": "frozen", "request": strings.Repeat("<", 64<<10)}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := data.GetData()
	if err != nil {
		t.Fatal(err)
	}
	var envelope workerops.ActivityInvocationRequest
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.InputArtifact == "" {
		t.Fatalf("expected offload: %v", err)
	}
	// Retain the recorded envelope while making its referenced input unavailable.
	runtime, err := input.NewRuntime(control{task: handle{data: jobdb.NewTaskDataOrPanic(envelope)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GetDetails(context.Background(), "tenant", "job"); !errors.Is(err, input.ErrStorage) || !strings.Contains(err.Error(), "restore pending input") {
		t.Fatalf("GetDetails silently lost restoration: %v", err)
	}
	if _, err := runtime.GetForm(context.Background(), "tenant", "job"); !errors.Is(err, input.ErrStorage) {
		t.Fatalf("GetForm silently lost restoration: %v", err)
	}
}
