package assistant

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobref"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type uncertainCloseControl struct {
	workflowctl.WorkflowControl
	mu        sync.Mutex
	lost      bool
	cancelled []jobdb.JobKey
}

func (c *uncertainCloseControl) InspectJob(ctx context.Context, key jobdb.JobKey) (workflowctl.JobInspection, error) {
	return c.WorkflowControl.(workflowctl.JobInspector).InspectJob(ctx, key)
}
func (c *uncertainCloseControl) Cancel(ctx context.Context, key jobdb.JobKey) error {
	if err := c.WorkflowControl.Cancel(ctx, key); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled = append(c.cancelled, key)
	if !c.lost {
		c.lost = true
		return errors.New("fixture: cancellation accepted, response lost")
	}
	return nil
}
func exerciseSendCloseRace(t *testing.T, ctx context.Context, c *Client, w Caller, key jobref.Ref) {
	t.Helper()
	session, err := c.Get(ctx, w, key.GoalID)
	if err != nil || session.Current == nil {
		t.Fatal(err)
	}
	initial := session.Replay
	in := input.StructuredSubmission{RequestID: *session.Current.Form.RequestId, SubmissionID: "send-close-race", Response: map[string]any{"command": "send", "prompt": "racing recorded prompt"}}
	uncertain := &uncertainCloseControl{WorkflowControl: c.host.Control}
	c.host.Control = uncertain
	gate := make(chan struct{})
	type sendResult struct {
		out input.Output
		err error
	}
	sent := make(chan sendResult, 1)
	closed := make(chan error, 1)
	go func() { <-gate; out, e := c.Respond(ctx, w, key.GoalID, in); sent <- sendResult{out, e} }()
	go func() { <-gate; _, e := c.Close(ctx, w, key.JobID); closed <- e }()
	close(gate)
	send, closeErr := <-sent, <-closed
	if closeErr == nil || !uncertain.lost {
		t.Fatal("uncertain cancellation was not exercised", closeErr)
	}
	if send.err != nil && !errors.Is(send.err, ErrNotAtPrompt) && !errors.Is(send.err, ErrStale) && !errors.Is(send.err, input.ErrInputNotPending) && !errors.Is(send.err, input.ErrStaleRequest) {
		t.Fatal("unexpected race outcome", send.err)
	}
	for {
		state, e := c.Close(ctx, w, key.JobID)
		if e != nil {
			t.Fatal(e)
		}
		if state.Terminal {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}
	retained, err := c.Get(ctx, w, key.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, e := range retained.Replay.Entries {
		if e.Role == "user" && e.Receipt != nil && e.Receipt.SubmissionID == in.SubmissionID {
			accepted++
			if send.err != nil || !reflect.DeepEqual(e.Receipt, send.out.Receipt) {
				t.Fatal("invented race receipt", e)
			}
		}
	}
	if (send.err == nil && accepted != 1) || (send.err != nil && accepted != 0) {
		t.Fatal("race receipt count", accepted, send.err)
	}
	if send.err == nil {
		retry, e := c.Respond(ctx, w, key.GoalID, in)
		if e != nil || !reflect.DeepEqual(retry.Receipt, send.out.Receipt) {
			t.Fatal("accepted race receipt lost", e)
		}
	}
	ctx = jobdb.WithPrimaryGoalSelectionAuthorizer(ctx, func(context.Context, jobdb.GoalKey, jobdb.ResolvedCell) error { return nil })
	operation, err := c.PrepareReopen(ctx, w, key.JobID, "after-send-close", time.Unix(1100, 0))
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.SubmitFrozenStart(ctx, w, operation)
	if err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		session, err = c.Get(ctx, w, key.GoalID)
		if err == nil && session.Current != nil && session.JobID == next.JobID {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err(), err)
	}
	if _, err = c.Close(ctx, w, key.JobID); err != nil {
		t.Fatal("delayed close", err)
	}
	state, err := uncertain.InspectJob(ctx, jobdb.JobKey{TenantId: w.TenantID, JobId: next.JobID})
	if err != nil || state.Terminal {
		t.Fatal("cancel retargeted successor", state, err)
	}
	for _, cancelled := range uncertain.cancelled {
		if cancelled.JobId != key.JobID {
			t.Fatal("wrong cancel target", cancelled)
		}
	}
	for _, old := range initial.Entries {
		found := false
		for _, e := range session.Replay.Entries {
			if reflect.DeepEqual(e, old) {
				found = true
			}
		}
		if !found {
			t.Fatal("opening/reply changed after race")
		}
	}
	writeLifecycleCase(t, "send-close-race", map[string]any{"initial_job": key, "reopened": next, "send_accepted": send.err == nil, "receipt": send.out.Receipt, "accepted_occurrences": accepted, "uncertain_close_reconciled": true, "cancelled_jobs": uncertain.cancelled, "history": session.Replay})
	if _, err = c.Close(ctx, w, next.JobID); err != nil {
		t.Fatal(err)
	}
}
