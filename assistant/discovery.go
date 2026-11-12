package assistant

import (
	"context"
	"errors"
	"sort"

	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type controlLister struct{ workflowctl.WorkflowControl }

func (l controlLister) ListJobs(ctx context.Context, req jobdb.ListJobsRequest) (jobdb.ListJobsResponse, error) {
	rows, next, err := l.WorkflowControl.ListJobs(ctx, req)
	out := jobdb.ListJobsResponse{NextPageToken: next}
	for _, row := range rows {
		out.Jobs = append(out.Jobs, row.JobSummary)
	}
	return out, err
}
func (c *Client) rootJobs(ctx context.Context, w Caller, ids []string, token string) (recipejob.ListRecipeJobsResponse, error) {
	return c.rootJobsForGoal(ctx, w, ids, token, "")
}

func (c *Client) rootJobsForGoal(ctx context.Context, w Caller, ids []string, token, goal string) (recipejob.ListRecipeJobsResponse, error) {
	// Cell names select registered targets; canonical repository identity also
	// guards against a stale or mismatched registration. Preserve page tokens
	// even when every row on a page is filtered out.

	var out recipejob.ListRecipeJobsResponse
	seen := map[string]bool{}
	var goals []string
	if goal != "" {
		goals = []string{goal}
	}
	for {
		rows, err := recipejob.ListRecipeJobs(ctx, controlLister{c.host.Control}, recipejob.ListRecipeJobsRequest{TenantID: w.TenantID, CellNames: []string{c.cfg.Root.CellName}, JobIDs: ids, GoalIDs: goals, PageSize: 100, PageToken: token})
		if err != nil {
			return out, err
		}
		for _, row := range rows.Jobs {
			repo, err := jobdb.NormalizeCellRepo(row.RepositorySource)
			if err == nil && row.TenantID == w.TenantID && row.CellName == c.cfg.Root.CellName && repo == c.cfg.Root.CellRepo && (goal == "" || row.GoalID == goal) {
				out.Jobs = append(out.Jobs, row)
			}
		}
		out.NextPageToken = rows.NextPageToken
		// Direct lookups must also traverse empty filtered pages before deciding
		// that a session is absent. Discovery groups all pages itself.
		if len(ids) == 0 || rows.NextPageToken == "" {
			return out, nil
		}
		if seen[rows.NextPageToken] {
			return out, ErrUnavailable
		}
		seen[rows.NextPageToken] = true
		token = rows.NextPageToken
	}

}

// Discovery exhausts backend pages before grouping executions. A session cannot
// occur twice merely because its restarts straddle a backend page boundary.
func (c *Client) discover(ctx context.Context, w Caller) ([]*replayData, error) {
	return c.discoverForGoal(ctx, w, "")
}

func (c *Client) discoverForGoal(ctx context.Context, w Caller, goal string) ([]*replayData, error) {
	latest := map[string]*replayData{}
	token := ""
	seen := map[string]bool{}
	for {
		rows, err := c.rootJobsForGoal(ctx, w, nil, token, goal)
		if err != nil {
			return nil, err
		}
		for _, row := range rows.Jobs {
			// Unrelated root recipes are not assistant sessions.
			if !c.approved(row.RecipeName) {
				continue
			}
			d, err := c.read(ctx, w, row.JobID)
			if errors.Is(err, ErrForbidden) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if goal != "" && d.Story.GoalID != goal {
				return nil, ErrUnavailable
			}
			old := latest[d.Args.SessionID]
			switch {
			case old == nil || lineageContains(d.Ancestors, old.Story.JobID):
				latest[d.Args.SessionID] = d
			case old.Story.JobID == d.Story.JobID || lineageContains(old.Ancestors, d.Story.JobID):
			default:
				return nil, ErrConflict
			}
		}
		if rows.NextPageToken == "" {
			break
		}
		if seen[rows.NextPageToken] {
			return nil, ErrUnavailable
		}
		seen[rows.NextPageToken] = true
		token = rows.NextPageToken
	}
	out := make([]*replayData, 0, len(latest))
	for _, d := range latest {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Story.StartedAt.Equal(out[j].Story.StartedAt) {
			return out[i].Args.SessionID < out[j].Args.SessionID
		}
		return out[i].Story.StartedAt.After(out[j].Story.StartedAt)
	})
	return out, nil
}

// LatestForGoal resolves the unique current assistant continuation, not the
// newest timestamp. The caller uses its returned job and pending input identity
// after reload or native reopen. Historical jobs remain directly inspectable.
func (c *Client) LatestForGoal(ctx context.Context, w Caller, goal string) (Session, error) {
	if err := text(goal, 512, false); err != nil {
		return Session{}, err
	}
	if err := c.authorize(ctx, w, "list"); err != nil {
		return Session{}, err
	}
	rows, err := c.discoverForGoal(ctx, w, goal)
	if err != nil {
		return Session{}, err
	}
	if len(rows) == 0 {
		return Session{}, ErrNotFound
	}
	if len(rows) != 1 {
		return Session{}, ErrConflict
	}
	return c.session(ctx, w, rows[0])
}

func lineageContains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
