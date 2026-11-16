// Package assistanttest provides versioned ordinary assistant fixtures and checks.
// It never supplies a second transcript or bypasses real assistant acceptance.
package assistanttest

import (
	"context"
	"errors"
	"time"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/cortex-assistant/internal/turncheck"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type Turn = turncheck.Turn
type TurnEvidence = turncheck.Evidence

var ErrMissingValidReply = turncheck.ErrMissingValidReply

// AwaitPrompt waits for a new real input form in the expected job. Read failures
// propagate; an invalid model response may still produce a safe prompt.
func AwaitPrompt(ctx context.Context, host assistant.Host, client *assistant.Client, caller assistant.Caller, session, job, afterRequest string) (assistant.Session, error) {
	for {
		details, err := host.Input.GetDetails(ctx, caller.TenantID, job)
		if err != nil && !errors.Is(err, input.ErrInputNotPending) {
			return assistant.Session{}, err
		}
		if err == nil && details != nil && details.Form.RequestId != nil && *details.Form.RequestId != "" && *details.Form.RequestId != afterRequest {
			current, e := client.Get(ctx, caller, session)
			if e != nil {
				return current, e
			}
			if current.JobID != job {
				return current, errors.New("prompt belongs to wrong job")
			}
			return current, nil
		}
		if inspector, ok := host.Control.(workflowctl.JobInspector); ok {
			state, e := inspector.InspectJob(ctx, jobdb.JobKey{TenantId: caller.TenantID, JobId: job})
			if e != nil {
				return assistant.Session{}, e
			}
			if state.Terminal {
				return assistant.Session{}, errors.New("job terminated before expected prompt")
			}
		}
		select {
		case <-ctx.Done():
			return assistant.Session{}, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// CheckTurn reads exact recorded task outputs and every replay page. Use an
// already composed Host: this check starts no workers and owns no resources.
func CheckTurn(ctx context.Context, api jobinspect.API, client *assistant.Client, caller assistant.Caller, session string, turn Turn) (TurnEvidence, error) {
	var entries []assistant.Entry
	seen := map[string]bool{}
	for cursor := ""; ; {
		page, err := client.Replay(ctx, caller, session, assistant.PageRequest{Cursor: cursor})
		if err != nil {
			return TurnEvidence{}, err
		}
		entries = append(entries, page.Entries...)
		if !page.HasMore {
			break
		}
		if page.Cursor == "" || seen[page.Cursor] {
			return TurnEvidence{}, errors.New("invalid replay cursor")
		}
		seen[page.Cursor] = true
		cursor = page.Cursor
	}
	return turncheck.Check(ctx, api, turn, entries)
}
