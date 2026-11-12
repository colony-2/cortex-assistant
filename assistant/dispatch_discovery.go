package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	recipeops "github.com/colony-2/c2j/pkg/ops/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// Discover only. A terminal recipes.run failure cannot be repaired by restarting
// its recipe: the new parent would allocate a different child. Keep that failure
// and attach an independently verified accepted destination to its actual origin.
func (c *Client) reconcileFailedDispatches(ctx context.Context, w Caller, d *replayData) {
	if d.Args.Protocol != GoalProtocol {
		return
	}
	var proposal, confirmed *Proposal
	for i := range d.Entries {
		e := &d.Entries[i]
		if e.Role == "assistant" {
			proposal, confirmed = e.Proposal, nil
		}
		if e.Role == "confirm" && e.Receipt != nil && (e.Receipt.Actor.Kind == "human" || e.Receipt.Actor.Kind == "user") {
			// Projection has already validated the exact proposal ID and hash against
			// this input's authoritative receipt. Do not consult a new live proposal.
			confirmed, proposal = proposal, nil
		}
		if e.Role != "dispatch" || e.Status == "succeeded" {
			continue
		}
		if e.Status != "failed" && e.Status != "canceled" {
			continue
		}
		e.Recovery = "unavailable"
		if confirmed == nil {
			continue
		}
		var task *story.JobRunStoryNode
		var find func(*story.JobRunStoryNode)
		find = func(n *story.JobRunStoryNode) {
			if n == nil {
				return
			}
			if n.TaskOrdinal != nil && *n.TaskOrdinal == e.Origin.Task && n.InvocationID == e.Origin.Invocation && strings.Join(n.Path, "/") == strings.Join(e.Origin.Path, "/") {
				task = n
			}
			for _, ch := range n.Children {
				find(ch)
			}
		}
		find(d.Story.Root)
		if task == nil || task.StepID != "start" {
			continue
		}
		child, err := c.discoverConfirmedDispatch(ctx, w, e.Origin, d.Story.GoalID, *confirmed, task.Input)
		if err != nil {
			continue
		}
		e.Destination, e.DestinationGoal, e.Recovery = child.JobID, child.GoalID, "accepted"
	}
}

func (c *Client) discoverConfirmedDispatch(ctx context.Context, w Caller, origin Locator, parentGoal string, proposal Proposal, taskInput any) (recipejob.RecipeJob, error) {
	var zero recipejob.RecipeJob
	// Verify the retained actual operation as well as the preceding confirmation.
	// This route deliberately supports exactly one build without attachments.
	var operation recipeops.MultipleRecipes
	// The runtime input includes defaulted Git/Wait fields; decode only the fields
	// needed here, then compare the full persisted child inputs below.
	if err := json.Unmarshal(mustJSON(taskInput), &operation); err != nil || len(operation.Recipes) != 1 {
		return zero, ErrUnavailable
	}
	expected := operation.Recipes[0]
	if expected.Name != "build" || expected.CellName != proposal.Target.CellName || !sameRepository(expected.CellRepo, proposal.Target.CellRepo) || string(mustJSON(expected.Inputs)) != string(mustJSON(map[string]any{"type": "build", "prompt": proposal.Prompt})) {
		return zero, ErrUnavailable
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(origin.Invocation)))
	cursor := ""
	seen := map[string]bool{}
	var children []recipejob.RecipeJob
	for {
		page, err := recipejob.ListChildRecipeJobs(ctx, controlLister{c.host.Control}, recipejob.ListChildRecipeJobsRequest{TenantID: w.TenantID, ParentTenantID: w.TenantID, ParentJobID: origin.JobID, ParentInvocationHash: hash, Stores: []jobdb.JobStore{jobdb.JobStoreActive, jobdb.JobStoreArchived}, PageSize: 100, PageToken: cursor})
		if err != nil {
			return zero, ErrUnavailable
		}
		children = append(children, page.Jobs...)
		if len(children) > 1 {
			return zero, ErrConflict
		}
		if page.NextPageToken == "" {
			break
		}
		if seen[page.NextPageToken] {
			return zero, ErrUnavailable
		}
		seen[page.NextPageToken], cursor = true, page.NextPageToken
	}
	if len(children) != 1 {
		return zero, ErrUnavailable
	}
	child := children[0]
	if child.TenantID != w.TenantID || child.JobID == "" || child.GoalID == "" || child.CellID != proposal.Target.CellID || child.CellName != proposal.Target.CellName || !sameRepository(child.RepositorySource, proposal.Target.RepositorySource) || child.RecipeName != expected.Name || child.Parent == nil || child.Parent.JobID != origin.JobID || child.Parent.TenantID != w.TenantID || child.Parent.InvocationHash != hash || parentGoal == "" || child.Parent.GoalID != parentGoal || child.Parent.GoalID == child.GoalID {
		return zero, ErrUnavailable
	}
	if err := c.authorize(ctx, w, child.JobID); err != nil {
		return zero, err
	}
	run, err := c.host.History.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: jobdb.JobKey{TenantId: w.TenantID, JobId: child.JobID}, IncludeInputs: true})
	if err != nil || run.Start.Input == nil {
		return zero, ErrUnavailable
	}
	var start workflowctl.StartJob
	if json.Unmarshal(mustJSON(run.Start.Input.Data), &start) != nil || start.TenantId != w.TenantID || start.JobID != child.JobID || start.RecipeName != expected.Name || start.Cell != child.CellName || !sameRepository(start.CellRepo, child.RepositorySource) || string(mustJSON(start.Inputs)) != string(mustJSON(expected.Inputs)) || len(expected.Artifacts) != 0 || len(start.Artifacts) != 0 || len(start.ArtifactRefs) != 0 || start.GitRef != operation.GitRef || !sameRepository(start.JobContext.GitBase.BaseRepo, proposal.Target.RepositorySource) || start.JobContext.GitBase.BaseRef != expected.Git.BaseRef || start.JobContext.GitBase.ResolvedBaseHash != expected.Git.BaseHash || start.JobContext.GitBase.GitAuthor != expected.Git.Author || start.Parent == nil || start.Parent.TenantID != w.TenantID || start.Parent.JobID != origin.JobID || start.Parent.GoalID != parentGoal || start.Parent.InvocationHash != hash || run.Job.GoalID != child.GoalID || run.Job.JobKey.TenantId != w.TenantID || run.Job.JobKey.JobId != child.JobID {
		return zero, ErrUnavailable
	}
	return child, nil
}

func sameRepository(a, b string) bool {
	x, xe := jobdb.NormalizeCellRepo(a)
	y, ye := jobdb.NormalizeCellRepo(b)
	return xe == nil && ye == nil && x == y
}
