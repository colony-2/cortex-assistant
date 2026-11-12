package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type lifecycleControl struct {
	*fixtureControl
	states    map[string]workflowctl.JobInspection
	cancelled []jobdb.JobKey
}

func (c *lifecycleControl) InspectJob(_ context.Context, key jobdb.JobKey) (workflowctl.JobInspection, error) {
	state, ok := c.states[key.JobId]
	if !ok {
		return state, jobdb.ErrJobNotFound
	}
	return state, nil
}
func (c *lifecycleControl) Cancel(_ context.Context, key jobdb.JobKey) error {
	c.cancelled = append(c.cancelled, key)
	state := c.states[key.JobId]
	state.Status = "cancel_requested"
	c.states[key.JobId] = state
	return nil
}
func TestGoalCloseCapturedQueuedProcessingAndWaiting(t *testing.T) {
	for _, status := range []string{"queued", "processing", "waiting"} {
		t.Run(status, func(t *testing.T) {
			legacy, ss, control, _ := client(t)
			cfg := legacy.cfg
			cfg.Protocol = GoalProtocol
			cfg.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit
			ordinary, _, _ := ordinaryFacade(t)
			cfg.Bindings, cfg.Limits = ordinary.cfg.Bindings, OrdinaryLimits()
			ctl := &lifecycleControl{fixtureControl: control, states: map[string]workflowctl.JobInspection{}}
			control.list = func(req jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
				rows := []workflowctl.JobItem{}
				for id, record := range ss.jobs {
					if len(req.JobKeys) > 0 && req.JobKeys[0].JobId != id {
						continue
					}
					rows = append(rows, workflowctl.JobItem{JobSummary: jobdb.JobSummary{JobKey: jobdb.JobKey{TenantId: human.TenantID, JobId: id}, GoalID: record.GoalID, Cell: "root", CellRepo: "github.com/example/root", JobType: "recipe", Metadata: mustJSON(map[string]any{"repo": "https://github.com/example/root.git", "recipe": record.Recipe.Source.SubmittedSelector})}})
				}
				return rows, "", nil
			}
			host := legacy.host
			host.Control = ctl
			c, err := New(host, cfg)
			if err != nil {
				t.Fatal(err)
			}
			queued := fixture(c)
			queued.GoalID, queued.Root = "goal", nil // No worker or helper has started.
			ss.jobs["job"] = queued
			key := jobdb.JobKey{TenantId: human.TenantID, JobId: "job"}
			ctl.states["job"] = workflowctl.JobInspection{JobKey: key, GoalID: "goal", Status: status}
			result, err := c.Close(ctx, human, "job")
			if err != nil || result.Terminal || result.Status != "cancel_requested" {
				t.Fatal("requested cancellation claimed completion", result, err)
			}
			if len(ctl.cancelled) != 1 || ctl.cancelled[0] != key {
				t.Fatal("close retargeted captured job")
			}
			state := ctl.states["job"]
			state.Terminal, state.Status = true, "cancelled"
			ctl.states["job"] = state
			result, err = c.Close(ctx, human, "job")
			if err != nil || !result.Terminal || len(ctl.cancelled) != 1 {
				t.Fatal("terminal reconciliation", result, err)
			}
			foreign := fixture(c)
			foreign.JobID, foreign.Recipe.Source.SubmittedSelector = "foreign", selector
			foreign.Status = story.WorkflowStatusRunning
			ss.jobs["foreign"] = foreign
			if _, err = c.Close(ctx, human, "foreign"); !errors.Is(err, ErrForbidden) {
				t.Fatal("foreign recipe close", err)
			}
			if len(ctl.cancelled) != 1 {
				t.Fatal("foreign cancellation issued")
			}
		})
	}
}
