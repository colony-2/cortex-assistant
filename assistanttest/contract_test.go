package assistanttest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/input/openapi"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/recipetest"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
)

type nativeContract struct {
	form    input.Config
	schema  any
	receipt input.Receipt
}

func runNativeContract(t *testing.T, ctx context.Context, source string, reply Reply) nativeContract {
	t.Helper()
	raw, err := os.ReadFile("../recipe-tests/contextual-goal.test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []recipetest.Case `json:"cases"`
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	c := suite.Cases[0]
	// Sources are exact shipped/candidate bytes. Fixture paths stay inside C.
	for name, fixture := range c.Runtime.Cells {
		for dest := range fixture.FileSources {
			fixture.FileSources[dest] = dest
		}
		c.Runtime.Cells[name] = fixture
	}
	c.Inputs["prompt"] = reply.User
	mocks := c.Mocks.Ops[:len(c.Mocks.Ops)-1]
	c.Mocks.Ops = append(mocks, NativeMocks(Scenario{Replies: []Reply{reply}})...)
	c.Assertions = c.Assertions[:3]
	recipeBytes, err := os.ReadFile(filepath.Join(source, "discussion-goal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := recipetest.ExpandInlineRecipeTarget(ctx, recipetest.TargetRecipe{Mode: "inline_recipe", Format: "yaml", Content: string(recipeBytes)}, recipetest.InlineTargetExpansionOptions{ProjectID: "0", RootFile: filepath.Join(source, "discussion-goal.yaml"), WorkingDir: source})
	if err != nil {
		t.Fatal(err)
	}
	result := recipetest.RunCase(ctx, recipetest.HarnessOptions{FixtureRoot: source, WorkRoot: t.TempDir(), Deps: coreops.NewServiceDepsBuilder().Build()}, "0", target, c, recipetest.ExecutionOptions{Timeout: "90s"})
	if result.Status != "passed" || result.Runtime == nil {
		t.Fatalf("native contract failed: %s %s", result.Status, result.FailureReason)
	}
	var contract nativeContract
	for _, call := range result.Runtime.Calls {
		if call.NodePath == "cortex-assistant-session/prompt/sequence/form" {
			b, _ := json.Marshal(call.Outputs["form"])
			if json.Unmarshal(b, &contract.form) != nil {
				t.Fatal("native form unavailable")
			}
		}
		if call.NodePath == "cortex-assistant-session/infer/sequence/assemble" {
			model := call.Outputs["model_input"].(map[string]any)
			contract.schema = model["response_schema"]
		}
	}
	for _, review := range result.Runtime.Reviews {
		if review.Receipt != nil {
			contract.receipt = *review.Receipt
		}
	}
	if contract.form.Request == nil || contract.schema == nil || contract.receipt.Actor.Kind != "test" || contract.receipt.RequestID == "" || contract.receipt.SubmissionID == "" || contract.receipt.SubmittedAt.IsZero() {
		t.Fatal("missing actual native form/schema/receipt", contract)
	}
	raw, _ = json.Marshal(result)
	if strings.Contains(string(raw), "synthetic-native-only") {
		t.Fatal("native report exposed credential")
	}
	return contract
}
func compareNativeConsumer(t *testing.T, n nativeContract, form openapi.InputFormConfig, receipt input.Receipt, modelSchema any) {
	t.Helper()
	canonical := func(v any) any { b, _ := json.Marshal(v); var out any; json.Unmarshal(b, &out); return out }
	if !reflect.DeepEqual(canonical(n.form.ResponseSchema), canonical(form.ResponseSchema)) || !reflect.DeepEqual(canonical(n.schema), canonical(modelSchema)) {
		t.Fatal("native/consumer helper schema drift")
	}
	// The real request includes dynamic session/origin and model metadata. Compare
	// the actual shape and validated text, while keeping those producer identities.
	var a, b map[string]any
	x, _ := json.Marshal(n.form.Request)
	json.Unmarshal(x, &a)
	x, _ = json.Marshal(form.Request)
	json.Unmarshal(x, &b)
	if !reflect.DeepEqual(mapKeys(a), mapKeys(b)) || a["protocol"] != b["protocol"] || a["assistant"].(map[string]any)["text"] != b["assistant"].(map[string]any)["text"] {
		t.Fatal("native/consumer real form drift")
	}
	x, _ = json.Marshal(n.receipt)
	json.Unmarshal(x, &a)
	x, _ = json.Marshal(receipt)
	json.Unmarshal(x, &b)
	if !reflect.DeepEqual(mapKeys(a), mapKeys(b)) || receipt.Actor.Kind != "human" || receipt.Actor.ID == "" || receipt.RequestID == "" || receipt.SubmissionID == "" || receipt.SubmittedAt.IsZero() {
		t.Fatal("native/consumer receipt metadata drift")
	}
}
func mapKeys(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// Required negative cases cannot pass because any error occurred. These small
// native cases check the framework's exact categories before costly helpers run.
func TestNativeAcceptanceRejectsMissingAndUnusedMocks(t *testing.T) {
	ctx := celltest.Context(context.Background())
	target := recipetest.TargetRecipe{Mode: "inline_recipe", Format: "yaml", Content: "id: finite\nop: command_execution\ninputs: {run: 'exit 99'}\n"}
	mock := recipetest.OpMock{Match: recipetest.OpMockMatch{Op: "command_execution"}, Behavior: recipetest.MockBehavior{Mode: "return", Outputs: map[string]any{"exit_code": 0}}}
	for _, tc := range []struct {
		name, category string
		mocks          []recipetest.OpMock
		target         recipetest.TargetRecipe
	}{
		{"unused", "mock_unused", []recipetest.OpMock{mock, mock}, target},
		{"unmatched", "mock_unused", []recipetest.OpMock{{Match: recipetest.OpMockMatch{Op: "__different__"}, Behavior: mock.Behavior}}, target},
		{"extra", "", []recipetest.OpMock{mock}, recipetest.TargetRecipe{Mode: "inline_recipe", Format: "yaml", Content: "id: finite\nsequence:\n - op: command_execution\n   inputs: {run: 'exit 99'}\n - op: command_execution\n   inputs: {run: 'exit 99'}\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := recipetest.RunCase(ctx, recipetest.HarnessOptions{FixtureRoot: t.TempDir(), WorkRoot: t.TempDir(), Deps: coreops.NewServiceDepsBuilder().Build()}, "0", tc.target, recipetest.Case{ID: tc.name, Type: "integration_case", Runtime: &recipetest.RuntimeCase{}, Mocks: recipetest.Mocks{Ops: tc.mocks}}, recipetest.ExecutionOptions{Timeout: "10s"})
			if result.Status != "failed" || result.FailureCategory != tc.category || (tc.name != "unused" && !strings.Contains(result.FailureReason, "no runtime fixture for command_execution at finite")) {
				t.Fatalf("wrong negative: %s %s %s", result.Status, result.FailureCategory, result.FailureReason)
			}
		})
	}
}
