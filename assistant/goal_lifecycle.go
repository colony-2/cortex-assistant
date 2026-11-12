package assistant

import (
	"context"
	"fmt"
	"time"

	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/starter"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// GoalProtocol is a distinct execution contract. Existing descriptors and
// pending jobs keep their original protocol and restart semantics.
const GoalProtocol = "cortex-assistant/4"

func ordinaryProtocol(protocol string) bool {
	return protocol == OrdinaryProtocol || protocol == GoalProtocol
}

// Close addresses the captured job, never whichever candidate is newest now.
// A cancellation request is not a terminal outcome; call again to reconcile.
func (c *Client) Close(ctx context.Context, w Caller, capturedJob string) (workflowctl.JobInspection, error) {
	if c.protocol() != GoalProtocol {
		return workflowctl.JobInspection{}, ErrInvalid
	}
	if err := c.authorize(ctx, w, capturedJob); err != nil {
		return workflowctl.JobInspection{}, err
	}
	rows, err := c.rootJobs(ctx, w, []string{capturedJob}, "")
	if err != nil {
		return workflowctl.JobInspection{}, err
	}
	if len(rows.Jobs) != 1 || rows.Jobs[0].JobID != capturedJob {
		return workflowctl.JobInspection{}, ErrNotFound
	}
	release, approved := c.release(rows.Jobs[0].RecipeName)
	if !approved || release.Protocol != GoalProtocol {
		return workflowctl.JobInspection{}, ErrForbidden
	}
	goalID := rows.Jobs[0].GoalID
	inspector, ok := c.host.Control.(workflowctl.JobInspector)
	if !ok {
		return workflowctl.JobInspection{}, ErrUnavailable
	}
	key := jobdb.JobKey{TenantId: w.TenantID, JobId: capturedJob}
	state, err := inspector.InspectJob(ctx, key)
	if err != nil {
		return state, err
	}
	if state.JobKey != key || state.GoalID != goalID {
		return state, ErrUnavailable
	}
	if state.Terminal {
		return state, nil
	}
	if err = c.host.Control.Cancel(ctx, key); err != nil {
		return state, err
	}
	state, err = inspector.InspectJob(ctx, key)
	if err == nil && (state.JobKey != key || state.GoalID != goalID) {
		err = ErrUnavailable
	}
	return state, err
}

// PrepareReopen creates a fresh same-goal operation after the captured job is
// terminal. Retain its complete returned bytes before SubmitFrozenStart. Reuse
// the returned operation after uncertainty, rather than preparing another one.
// Multiple clients reopening simultaneously are outside this contract.
func (c *Client) PrepareReopen(ctx context.Context, w Caller, capturedJob, requestID string, submittedAt time.Time) (FrozenStart, error) {
	if c.protocol() != GoalProtocol || submittedAt.IsZero() || text(requestID, 512, false) != nil {
		return FrozenStart{}, ErrInvalid
	}
	d, err := c.read(ctx, w, capturedJob)
	if err != nil {
		return FrozenStart{}, err
	}
	if d.Args.Protocol != GoalProtocol || d.Snapshot == nil {
		return FrozenStart{}, ErrUnavailable
	}
	inspector, ok := c.host.Control.(workflowctl.JobInspector)
	if !ok {
		return FrozenStart{}, ErrUnavailable
	}
	key := jobdb.JobKey{TenantId: w.TenantID, JobId: capturedJob}
	state, err := inspector.InspectJob(ctx, key)
	if err != nil {
		return FrozenStart{}, err
	}
	if state.JobKey != key || state.GoalID != d.Story.GoalID {
		return FrozenStart{}, ErrUnavailable
	}
	if !state.Terminal {
		return FrozenStart{}, ErrNotAtPrompt
	}
	goal, err := c.host.Goals.GetGoal(ctx, jobdb.GoalKey{TenantId: w.TenantID, GoalID: d.Story.GoalID})
	if err != nil {
		return FrozenStart{}, fmt.Errorf("%w: goal state", ErrUnavailable)
	}
	if goal.State != jobdb.GoalOpen {
		return FrozenStart{}, ErrConflict
	}
	latest, err := c.discoverForGoal(ctx, w, d.Story.GoalID)
	if err != nil {
		return FrozenStart{}, err
	}
	if len(latest) != 1 || latest[0].Story.JobID != capturedJob {
		return FrozenStart{}, ErrStale
	}
	b := *c.cfg.Bindings
	id := "assistant-" + identity(w.TenantID, d.Story.GoalID, requestID)
	contextJob, prompt := d.ContextJob, ""
	if d.Capture != nil {
		contextJob = capturedJob
	}
	if contextJob == "" {
		prompt = d.OpeningPrompt
	}
	inputs := map[string]any{"protocol": GoalProtocol, "session_id": d.Story.GoalID, "previous_job": capturedJob, "prior_job": contextJob, "prompt": prompt, "snapshot": d.Snapshot, "base_url": b.BaseURL, "api_key": b.APIKey, "provider": b.Provider, "model": b.Model}
	if len(mustJSON(inputs)) > OrdinaryEnvelopeMaxBytes {
		return FrozenStart{}, ErrLimit
	}
	start, err := recipejob.BuildStartJob(recipejob.BuildStartJobRequest{TenantID: w.TenantID, GoalID: d.Story.GoalID, JobID: id, SubmittedAt: &submittedAt, Target: c.cfg.Root, Recipe: c.cfg.Recipe, Inputs: inputs})
	if err != nil {
		return FrozenStart{}, err
	}
	job, err := starter.PrepareRecipeJob(ctx, start, starter.StartRecipeJobOptions{})
	if err != nil {
		return FrozenStart{}, err
	}
	descriptor, err := starter.NewDescriptor(ctx, job)
	if err != nil {
		return FrozenStart{}, err
	}
	return c.freezeStart(w, descriptor, d.Story.GoalID, d.Args.Source)
}
