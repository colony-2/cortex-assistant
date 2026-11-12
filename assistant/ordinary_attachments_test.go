package assistant

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type attachmentAPI struct {
	jobinspect.API
	body    string
	missing bool
	calls   int
}

func (a *attachmentAPI) Download(_ context.Context, q jobinspect.Request) (io.ReadCloser, error) {
	a.calls++
	if q.JobID != "producer" || q.Reference == nil {
		return nil, ErrForbidden
	}
	if a.missing {
		return nil, ErrUnavailable
	}
	return io.NopCloser(strings.NewReader(a.body)), nil
}
func TestGoalAttachmentInclusionAndMissingBytes(t *testing.T) {
	for _, tc := range []struct {
		name, body, outcome string
		missing             bool
	}{
		{"plain.txt", "text 世界", "included", false},
		{"data.json", `{"ok":true}`, "included", false},
		{"bad.json", "invalid", "unsupported", false},
		{"bad.txt", string([]byte{255}), "unsupported", false},
		{"gone.txt", "missing", "unavailable", true},
		{"pixel.png", "opaque", "unsupported", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "producer", TaskOrdinal: 5, Name: tc.name, SizeBytes: int64(len(tc.body))})
			cmd := Command{Command: "send", Prompt: "read", Attachments: map[string]artifacts.Ref{tc.name: ref}}
			record := ordinaryRecord{job: "producer", human: &input.Output{ArtifactRefs: cmd.Attachments}, task: jobinspect.Task{ID: "input-task", Attempt: 2}}
			api := &attachmentAPI{body: tc.body, missing: tc.missing}
			prompt, err := ordinaryAttachmentPrompt(context.Background(), api, record, cmd, OrdinaryPromptMaxBytes)
			if err != nil || !strings.Contains(prompt, `"outcome":"`+tc.outcome+`"`) || !strings.Contains(prompt, `"producer_attempt":2`) {
				t.Fatalf("%s %v", prompt, err)
			}
			if tc.outcome != "included" && strings.Contains(prompt, `"text":`) {
				t.Fatal("claimed unread bytes")
			}
			if tc.name == "pixel.png" && api.calls != 0 {
				t.Fatal("read opaque media")
			}
			if tc.outcome == "included" {
				limited, e := ordinaryAttachmentPrompt(context.Background(), api, record, cmd, 0)
				if e != nil || !strings.Contains(limited, `"outcome":"budget-omitted"`) || strings.Contains(limited, `"text":`) {
					t.Fatal("budget claim", limited, e)
				}
			}
			record.human.ArtifactRefs = nil
			if _, err = ordinaryAttachmentPrompt(context.Background(), api, record, cmd, OrdinaryPromptMaxBytes); !errors.Is(err, ErrUnavailable) {
				t.Fatal("accepted unbound reference", err)
			}
		})
	}
}
func TestGoalReturnedArtifactAuthorization(t *testing.T) {
	ref := artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "producer", TaskOrdinal: 5, Name: "pixel.png", SizeBytes: 8})
	a := describe("pixel.png", ref)
	for _, tc := range []struct {
		ids  []string
		pass bool
	}{
		{[]string{a.ID}, true}, {[]string{"forged"}, false}, {[]string{a.ID, a.ID}, false},
	} {
		model := ModelOutput{Response: string(mustJSON(map[string]any{"text": "reply", "documents": []Document{}, "return_artifact_ids": tc.ids, "citations": []string{}}))}
		response, _, err := validateOrdinaryModelArtifacts(model, FrozenSnapshot{}, nil, map[string]Artifact{a.ID: a})
		if (err == nil) != tc.pass {
			t.Fatal(tc.ids, err)
		}
		if tc.pass && (len(response.Artifacts) != 1 || response.Artifacts[0].Ref.Stored.Key != ref.Stored.Key) {
			t.Fatal("changed original ref")
		}
		if _, _, err = validateOrdinaryModel(model, FrozenSnapshot{}, nil); err == nil {
			t.Fatal("legacy accepted artifact return")
		}
	}
}
func TestHistoricalOrdinaryAttachmentPolicy(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	c.cfg.Protocol = GoalProtocol
	in := input.StructuredSubmission{Response: map[string]any{"command": "send", "prompt": "hello", "attachments": map[string]any{"plain.txt": jobdb.NewArtifactFromBytes("plain.txt", []byte("text"))}}}
	if _, _, err := c.freezeSubmissionForProtocol(ctx, human, OrdinaryProtocol, in, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("reinterpreted v3", err)
	}
	if _, _, err := c.freezeSubmissionForProtocol(ctx, human, GoalProtocol, in, nil); err != nil {
		t.Fatal(err)
	}
}

func TestGoalEncodedHumanLimit(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	for _, tc := range []struct {
		prompt string
		pass   bool
	}{
		{strings.Repeat("a", OrdinaryLimits().Human), true},
		{strings.Repeat("\na", OrdinaryLimits().Human/3) + "a", true},
		{strings.Repeat("\na", OrdinaryLimits().Human/3) + "aa", false},
	} {
		in := input.StructuredSubmission{Response: map[string]any{"command": "send", "prompt": tc.prompt}}
		_, _, err := c.freezeSubmissionForProtocol(ctx, human, GoalProtocol, in, nil)
		if (err == nil) != tc.pass {
			t.Fatalf("encoded prompt bytes %d: %v", len(mustJSON(tc.prompt))-2, err)
		}
	}
}

func TestGoalStandaloneBindingsCannotChangeFrozenCommand(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	ref := artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "producer", TaskOrdinal: 5, Name: "plain.txt", SizeBytes: 4})
	in := input.StructuredSubmission{Response: Command{Command: "send", Prompt: "hello"}, ArtifactRefs: map[string]artifacts.Ref{"plain.txt": ref}}
	if _, _, err := c.freezeSubmissionForProtocol(ctx, human, GoalProtocol, in, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted unrelated explicit binding", err)
	}
}
