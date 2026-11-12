package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/jobdb/pkg/jobdb/runtime/toy"
)

func TestOrdinaryClosedResponseAndActualCoverage(t *testing.T) {
	s, _ := snapshotFixture()
	good := map[string]any{"text": "grounded", "documents": []any{}, "return_artifact_ids": []string{}, "citations": []string{s.Documents[0].ID}}
	marshal := func(v any) ModelOutput {
		b, _ := json.Marshal(v)
		return ModelOutput{Response: string(b), Model: "fixture"}
	}
	coverage := map[string]string{s.Documents[0].ID: "included"}
	response, _, err := validateOrdinaryModel(marshal(good), s, coverage)
	if err != nil || len(response.Citations) != 1 {
		t.Fatal(response, err)
	}
	if _, _, err = validateOrdinaryModel(marshal(good), s, map[string]string{s.Documents[0].ID: "budget_omitted"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("omitted source cited: %v", err)
	}
	good["citations"] = []string{"unknown"}
	if _, _, err = validateOrdinaryModel(marshal(good), s, coverage); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown source cited: %v", err)
	}
	good["citations"] = []string{}
	good["tools"] = []string{"shell"}
	if _, _, err = validateOrdinaryModel(marshal(good), s, coverage); err == nil {
		t.Fatal("executable/unknown output accepted")
	}
	delete(good, "tools")
	good["documents"] = []any{map[string]any{"name": "safe.md", "mime": "text/markdown"}}
	if _, _, err = validateOrdinaryModel(marshal(good), s, coverage); err == nil {
		t.Fatal("missing document text accepted")
	}
	good["documents"] = []any{map[string]any{"name": "../escape", "mime": "text/markdown", "text": "bad"}}
	if _, _, err = validateOrdinaryModel(marshal(good), s, coverage); err == nil {
		t.Fatal("unsafe document accepted")
	}
}

func TestOrdinaryCaptureExecutionAndTenantBinding(t *testing.T) {
	s, _ := snapshotFixture()
	current := jobcontext.Current{TenantID: s.TenantID, JobID: "discussion", JobType: "recipe", InvocationHash: "actual-invocation", InvocationPath: RecipeID + "/capture/sequence/capture"}
	api := &jobinspect.Service{Runtime: toy.New(), Tenant: s.TenantID}
	in := OrdinaryInput{Action: "capture", SessionID: "discussion", Prompt: "Discuss this source", Snapshot: &s}
	out, _, err := RunOrdinary(context.Background(), api, current, in)
	if err != nil || out.Origin.Invocation != current.InvocationHash || out.Snapshot.Documents[0].Text != s.Documents[0].Text {
		t.Fatal(out, err)
	}
	current.TenantID = "foreign"
	if _, _, err = RunOrdinary(context.Background(), api, current, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("tenant substitution: %v", err)
	}
	current.TenantID = s.TenantID
	current.InvocationPath += "/forged"
	if _, _, err = RunOrdinary(context.Background(), api, current, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("path substitution: %v", err)
	}
}

// This inspection fixture tests rejection at the retained-record boundary;
// actual operation/storage execution is covered by tests/test_ordinary.py.
type ordinaryInspectionFixture struct {
	jobinspect.API
	record OrdinaryResult
	path   string
}

func (f ordinaryInspectionFixture) Inspect(_ context.Context, req jobinspect.Request) (*jobinspect.Response, error) {
	if req.Field != nil {
		return &jobinspect.Response{Value: mustJSON(f.record)}, nil
	}
	return &jobinspect.Response{Job: &jobinspect.Job{ID: "discussion"}, Tasks: []jobinspect.Task{{ID: "recorded-task", Path: f.path, Type: "extension_execution:extension_execution", Status: "succeeded", Attempt: 1, OutputAvailability: "available"}}}, nil
}
func TestOrdinaryRecordedOriginBinding(t *testing.T) {
	path := ordinaryResultPath("capture")
	valid := OrdinaryResult{Protocol: OrdinaryProtocol, Kind: "capture", Origin: Locator{JobID: "discussion", Invocation: "recorded-invocation", Path: strings.Split(path, "/")}}
	for _, tc := range []struct {
		name   string
		change func(*OrdinaryResult)
	}{
		{"valid", func(r *OrdinaryResult) {}},
		{"foreign job", func(r *OrdinaryResult) { r.Origin.JobID = "foreign" }},
		{"foreign path", func(r *OrdinaryResult) { r.Origin.Path = []string{"foreign"} }},
		{"substituted kind", func(r *OrdinaryResult) { r.Kind = "response" }},
		{"missing invocation", func(r *OrdinaryResult) { r.Origin.Invocation = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.change(&r)
			records, err := readOrdinaryRecords(context.Background(), ordinaryInspectionFixture{record: r, path: path}, "discussion")
			if tc.name == "valid" {
				if err != nil || len(records) != 1 {
					t.Fatal(records, err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("substituted origin accepted: %v", err)
			}
		})
	}
}

func TestGoalCaptureEncodedOpeningPromptLimit(t *testing.T) {
	s, _ := snapshotFixture()
	current := jobcontext.Current{TenantID: s.TenantID, JobID: "discussion", GoalID: "goal", JobType: "recipe", InvocationHash: "capture-invocation", InvocationPath: RecipeID + "/capture/sequence/capture"}
	api := &jobinspect.Service{Runtime: toy.New(), Tenant: s.TenantID}
	for _, extra := range []string{"a", "aa"} {
		prompt := strings.Repeat("\na", OrdinaryLimits().Human/3) + extra
		_, _, err := RunOrdinary(context.Background(), api, current, OrdinaryInput{Protocol: GoalProtocol, Action: "capture", SessionID: "discussion", Prompt: prompt, Snapshot: &s})
		if extra == "a" && err != nil {
			t.Fatal("exact boundary", err)
		}
		if extra == "aa" && !errors.Is(err, ErrLimit) {
			t.Fatal("helper accepted encoded excess", err)
		}
	}
}
