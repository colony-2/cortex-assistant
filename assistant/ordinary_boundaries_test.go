package assistant

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/story"
)

func TestGoalExactEncodedBoundaries(t *testing.T) {
	snapshot, _ := snapshotFixture()
	model := ModelOutput{Response: `{"text":"ok","documents":[],"return_artifact_ids":[],"citations":[]}`}
	exact := model
	exact.Response += strings.Repeat(" ", OrdinaryLimits().Model-len(exact.Response))
	if _, _, err := ordinaryGoalModel(ctx, nil, exact, snapshot, nil, nil, Locator{}); err != nil {
		t.Fatal("exact model cap", err)
	}
	over := exact
	over.Response += " "
	if _, _, err := ordinaryGoalModel(ctx, nil, over, snapshot, nil, nil, Locator{}); !errors.Is(err, ErrLimit) {
		t.Fatal("model cap excess", err)
	}
	// Historical v3 keeps its preexisting raw-model limit.
	if _, _, err := validateOrdinaryModel(over, snapshot, nil); err != nil {
		t.Fatal("reinterpreted v3", err)
	}
	for _, tc := range []struct {
		name, text string
		pass       bool
	}{
		{"assistant UTF-8", strings.Repeat("界", OrdinaryLimits().Assistant/3) + "aa", true},
		{"assistant excess", strings.Repeat("a", OrdinaryLimits().Assistant+1), false},
	} {
		value := ModelOutput{Response: string(mustJSON(map[string]any{"text": tc.text, "documents": []any{}, "return_artifact_ids": []any{}, "citations": []any{}}))}
		_, _, err := ordinaryGoalModel(ctx, nil, value, snapshot, nil, nil, Locator{})
		if (err == nil) != tc.pass {
			t.Fatal(tc.name, err)
		}
	}
	// Exercise exact encoded operation envelope through the helper boundary.
	path := ordinaryResultPath("capture")
	cap := OrdinaryResult{Protocol: GoalProtocol, Kind: "capture", Origin: Locator{JobID: "discussion", Invocation: "captured", Path: strings.Split(path, "/")}, SessionID: "goal", Snapshot: &snapshot}
	api := ordinaryInspectionFixture{record: cap, path: path}
	current := jobcontext.Current{TenantID: snapshot.TenantID, JobID: "discussion", GoalID: "goal", JobType: "recipe", InvocationHash: "response", InvocationPath: ordinaryResultPath("response")}
	in := OrdinaryInput{Protocol: GoalProtocol, Action: "response", Model: &model}
	in.Model.Usage = map[string]any{"fixture_padding": ""}
	fill := OrdinaryEnvelopeMaxBytes - len(mustJSON(in))
	in.Model.Usage["fixture_padding"] = strings.Repeat("a", fill)
	if len(mustJSON(in)) != OrdinaryEnvelopeMaxBytes {
		t.Fatal("envelope fixture")
	}
	if _, _, err := RunOrdinary(context.Background(), api, current, in); err != nil {
		t.Fatal("exact operation envelope", err)
	}
	in.Model.Usage["fixture_padding"] = strings.Repeat("a", fill+1)
	if _, _, err := RunOrdinary(context.Background(), api, current, in); !errors.Is(err, ErrLimit) {
		t.Fatal("envelope excess", err)
	}
	writeLifecycleCase(t, "encoded-boundaries", map[string]any{"model_exact_bytes": len(exact.Response), "model_excess_rejected": true, "operation_exact_bytes": OrdinaryEnvelopeMaxBytes, "operation_excess_rejected": true, "runtime_default_chapter_bytes": 256 << 10, "headroom_bytes": (256 << 10) - OrdinaryEnvelopeMaxBytes, "v3_raw_model_limit_preserved": true, "fixture_boundary": "Helper/model policy boundaries; separate full runtime large-history and pending-input offload tests establish runtime usability."})
}

func TestGoalCommandAuthorityFields(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	for _, field := range []string{"base_url", "api_key", "provider", "model", "selector", "recipe", "target", "limits", "tools", "execute_tools"} {
		response := map[string]any{"command": "send", "prompt": "hello", field: "untrusted"}
		if _, _, err := c.freezeSubmissionForProtocol(ctx, human, GoalProtocol, input.StructuredSubmission{Response: response}, nil); err == nil {
			t.Fatal("accepted authority field", field)
		}
	}
}

func TestGoalExactPageBoundary(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	d := &replayData{Args: StartArgs{Protocol: GoalProtocol, SessionID: "goal"}, Story: &story.JobRunStory{JobID: "job"}, Entries: []Entry{{Role: "assistant", Status: "succeeded", Origin: Locator{JobID: "job", Task: 2}, Text: "x"}}}
	page, err := c.page(human, d, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	extra := c.cfg.Limits.Page - len(mustJSON(page))
	d.Entries[0].Text += strings.Repeat("a", extra)
	exact, err := c.page(human, d, PageRequest{})
	if err != nil || len(mustJSON(exact)) != c.cfg.Limits.Page {
		t.Fatal("exact page", len(mustJSON(exact)), err)
	}
	d.Entries[0].Text += "a"
	if _, err = c.page(human, d, PageRequest{}); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized single entry became empty success", err)
	}
}

func TestGoalExactSnapshotBoundary(t *testing.T) {
	s, _ := snapshotFixture()
	s.Documents[0].Ref = nil
	s.Documents[0].Text = strings.Repeat("a", SnapshotDocumentMaxBytes)
	s.Documents = append(s.Documents, SnapshotDocument{ID: "second", Producer: Locator{JobID: "other-child", Task: 8}, MIME: "text/plain", Outcome: "included", Text: strings.Repeat("b", 31000)})
	update := func() {
		for i := range s.Documents {
			d := &s.Documents[i]
			d.Size = int64(len(d.Text))
			d.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(d.Text)))
		}
	}
	update()
	for i := 0; i < 4; i++ {
		s.Documents[1].Text += strings.Repeat("b", SnapshotMaxBytes-len(mustJSON(s)))
		update()
	}
	if len(mustJSON(s)) != SnapshotMaxBytes {
		t.Fatal("snapshot fixture size", len(mustJSON(s)))
	}
	if _, err := DecodeFrozenSnapshot(mustJSON(s), snapshotSelection(s)); err != nil {
		t.Fatal("exact snapshot", err)
	}
	s.Documents[1].Text += "b"
	update()
	if _, err := DecodeFrozenSnapshot(mustJSON(s), snapshotSelection(s)); !errors.Is(err, ErrLimit) {
		t.Fatal("snapshot excess", err)
	}
	writeLifecycleCase(t, "snapshot-boundary", map[string]any{"exact_encoded_bytes": SnapshotMaxBytes, "excess_rejected": true, "original_child_producers": true})
}
