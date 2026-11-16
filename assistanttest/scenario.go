package assistanttest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/c2j/pkg/recipetest"
	"github.com/colony-2/cortex-assistant/assistant"
)

type Scenario struct {
	Replies []Reply `json:"replies"`
}

// User is the exact latest USER value produced by the real assistant helper.
// Response is a raw JSON string so malformed-output negatives remain possible.
type Reply struct {
	User     string `json:"user"`
	Response string `json:"response"`
}

func modelID(user string) string { return fmt.Sprintf("assistanttest-%x", sha256.Sum256([]byte(user))) }
func (s Scenario) validate() error {
	if len(s.Replies) == 0 {
		return errors.New("scenario has no required replies")
	}
	seen := map[string]bool{}
	for _, r := range s.Replies {
		if r.User == "" || seen[r.User] {
			return errors.New("scenario requires unique nonempty USER values")
		}
		seen[r.User] = true
	}
	return nil
}

// NativeMocks matches the unchanged production leaf and exact node. c2j consumes
// these in order, rejects unmatched invocations and fails unused required mocks.
// Add only path-specific passthrough mocks for real policy/helpers.
func NativeMocks(s Scenario) []recipetest.OpMock {
	match := recipetest.OpMockMatch{Selector: assistant.OrdinaryModelOperation, NodePath: "cortex-assistant-session/infer/sequence/infer/cortex-assistant-inference/infer"}
	mocks := make([]recipetest.OpMock, 0, len(s.Replies))
	for _, r := range s.Replies {
		mocks = append(mocks, recipetest.OpMock{Match: match, Behavior: recipetest.MockBehavior{Mode: "return", Outputs: map[string]any{"response": r.Response, "model": modelID(r.User), "usage": map[string]any{}}}})
	}
	return mocks
}

// CheckScenario requires one actual substituted inference outcome for every
// declared reply. It is separate from CheckTurn: a model outcome is not a reply.
func CheckScenario(ctx context.Context, api jobinspect.API, s Scenario, jobs ...string) error {
	if err := s.validate(); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, job := range jobs {
		for cursor := ""; ; {
			page, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: job, Cursor: cursor, Limit: 100})
			if err != nil {
				return err
			}
			if page == nil || page.Job == nil || page.Job.ID != job {
				return errors.New("wrong scenario job")
			}
			for _, t := range page.Tasks {
				if t.Path != "cortex-assistant-session/infer/sequence/infer/cortex-assistant-inference/infer" || t.Type != "extension_execution:extension_execution" {
					continue
				}
				if t.Status != "succeeded" {
					return errors.New("scenario inference failed")
				}
				field := ""
				v, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: job, Task: t.ID, Attempt: t.Attempt, Field: &field})
				if err != nil {
					return err
				}
				var out struct {
					Model string `json:"model"`
				}
				if v == nil || json.Unmarshal(v.Value, &out) != nil || !strings.HasPrefix(out.Model, "assistanttest-") {
					return errors.New("wrong fixture inference outcome")
				}
				counts[out.Model]++
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	for _, r := range s.Replies {
		id := modelID(r.User)
		if counts[id] != 1 {
			return fmt.Errorf("required scenario reply %q occurred %d times", r.User, counts[id])
		}
		delete(counts, id)
	}
	if len(counts) > 0 {
		return errors.New("unexpected scenario replies")
	}
	return nil
}
