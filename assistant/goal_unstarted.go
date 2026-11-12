package assistant

import (
	"context"
	"fmt"

	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// projectUnstarted uses the original retained submission when no worker has
// produced a story root. It projects no task, receipt or inferred reply.
func (c *Client) projectUnstarted(ctx context.Context, w Caller, job string, release ReleaseDescriptor) (*story.JobRunStory, error) {
	key := jobdb.JobKey{TenantId: w.TenantID, JobId: job}
	run, err := c.host.History.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: key, IncludeInputs: true})
	if err != nil || run.Start.Input == nil {
		return nil, fmt.Errorf("%w: submitted inputs for %s", ErrUnavailable, job)
	}
	var start workflowctl.StartJob
	if e := decode(run.Start.Input.Data, &start); e != nil {
		return nil, fmt.Errorf("%w: submitted input format: %v", ErrUnavailable, e)
	}
	// InputHash was calculated before submission with the typed snapshot.
	// Restore that wire ordering before checking the retained submission hash.
	var snapshot FrozenSnapshot
	if decode(start.Inputs["snapshot"], &snapshot) != nil {
		return nil, fmt.Errorf("%w: submitted snapshot", ErrUnavailable)
	}
	start.Inputs["snapshot"] = snapshot
	if start.TenantId != w.TenantID || start.JobID != job || start.Cell != c.cfg.Root.CellName || start.CellRepo != c.cfg.Root.CellRepo || start.RecipeName != release.Recipe || recipejob.InputHash(start.Inputs) != start.InputHash || run.Job.JobKey != key || run.Job.GoalID == "" {
		return nil, fmt.Errorf("%w: submitted identity", ErrUnavailable)
	}
	status := story.WorkflowStatusUnknown
	if run.Job.Status == jobdb.JobStatusCancelled {
		status = story.WorkflowStatusCanceled
	}
	return &story.JobRunStory{JobID: job, GoalID: run.Job.GoalID, StartedAt: run.Job.CreatedAt, Status: status, Recipe: story.JobRunStoryRecipe{ID: RecipeID, Source: story.JobRunStoryRecipeSource{Kind: "retainedSubmission", SubmittedSelector: release.Recipe}}, Root: &story.JobRunStoryNode{ID: RecipeID, Kind: "recipe", Input: start.Inputs}}, nil
}
