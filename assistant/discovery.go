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
	return recipejob.ListRecipeJobs(ctx, controlLister{c.host.Control}, recipejob.ListRecipeJobsRequest{TenantID: w.TenantID, RepositorySource: c.cfg.Root.RepositorySource, JobIDs: ids, PageSize: 100, PageToken: token})
}

// Discovery exhausts backend pages before grouping executions. A session cannot
// occur twice merely because its restarts straddle a backend page boundary.
func (c *Client) discover(ctx context.Context, w Caller) ([]*replayData, error) {
	latest := map[string]*replayData{}
	token := ""
	seen := map[string]bool{}
	for {
		rows, err := c.rootJobs(ctx, w, nil, token)
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

func lineageContains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
