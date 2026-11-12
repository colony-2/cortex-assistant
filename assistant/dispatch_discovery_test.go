package assistant

import (
	"context"
	"testing"

	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type dispatchDiscoveryControl struct {
	workflowctl.WorkflowControl
	alter func(jobdb.ListJobsRequest, []workflowctl.JobItem, string, error) ([]workflowctl.JobItem, string, error)
}

func (c dispatchDiscoveryControl) ListJobs(ctx context.Context, q jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
	rows, next, err := c.WorkflowControl.ListJobs(ctx, q)
	return c.alter(q, rows, next, err)
}

type dispatchDiscoveryHistory struct {
	HistoryReader
	alter func(*jobdb.GetJobRunResponse)
}

func (h dispatchDiscoveryHistory) GetJobRun(ctx context.Context, q jobdb.GetJobRunRequest) (jobdb.GetJobRunResponse, error) {
	run, err := h.HistoryReader.GetJobRun(ctx, q)
	h.alter(&run)
	return run, err
}
func exerciseDispatchDiscoveryNegatives(t *testing.T, ctx context.Context, c *Client, w Caller, origin Locator, proposal Proposal) {
	t.Helper()
	st, err := c.host.Story.GetJobRunStory(ctx, story.GetJobRunStoryRequest{ProjectID: w.TenantID, JobID: origin.JobID})
	if err != nil {
		t.Fatal(err)
	}
	var taskInput any
	var find func(*story.JobRunStoryNode)
	find = func(n *story.JobRunStoryNode) {
		if n == nil {
			return
		}
		if n.TaskOrdinal != nil && *n.TaskOrdinal == origin.Task {
			taskInput = n.Input
		}
		for _, ch := range n.Children {
			find(ch)
		}
	}
	find(st.Root)
	cases := []string{"missing", "duplicate", "wrong-cell", "wrong-repository", "wrong-recipe", "wrong-parent", "wrong-goal", "wrong-prompt", "missing-history", "history-tenant", "history-parent", "history-invocation", "denied", "repeated-cursor"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			clone := *c
			clone.host.Control = dispatchDiscoveryControl{WorkflowControl: c.host.Control, alter: func(q jobdb.ListJobsRequest, rows []workflowctl.JobItem, next string, err error) ([]workflowctl.JobItem, string, error) {
				switch name {
				case "missing":
					return nil, "", nil
				case "duplicate":
					return append(rows, rows...), "", err
				case "wrong-cell":
					for i := range rows {
						rows[i].CellID = "replaced"
					}
				case "wrong-repository":
					for i := range rows {
						rows[i].CellRepo = "github.com/example/foreign"
					}
				case "wrong-recipe", "wrong-parent", "wrong-goal":
					for i := range rows {
						m, _ := recipejob.JobMetadataFromRaw(rows[i].Metadata)
						if name == "wrong-recipe" {
							m.RecipeName = "other"
						} else if name == "wrong-goal" {
							m.ParentGoalID = "other"
						} else {
							m.ParentInvocationHash = "other"
						}
						rows[i].Metadata = mustJSON(m)
					}
				case "repeated-cursor":
					return nil, "same", nil
				}
				return rows, next, err
			}}
			p := proposal
			if name == "wrong-prompt" {
				p.Prompt += " changed"
			}
			if name == "missing-history" || name == "history-tenant" || name == "history-parent" || name == "history-invocation" {
				clone.host.History = dispatchDiscoveryHistory{HistoryReader: c.host.History, alter: func(run *jobdb.GetJobRunResponse) {
					if name == "missing-history" {
						run.Start.Input = nil
						return
					}
					if name == "history-tenant" {
						run.Job.JobKey.TenantId = "foreign"
						return
					}
					var start workflowctl.StartJob
					if decode(run.Start.Input.Data, &start) != nil || start.Parent == nil {
						t.Fatal("history fixture parent")
					}
					if name == "history-parent" {
						start.Parent.JobID = "other"
					} else {
						start.Parent.InvocationHash = "other"
					}
					run.Start.Input.Data = mustJSON(start)
				}}
			}
			if name == "denied" {
				clone.host.Authorize = func(context.Context, Caller, string) error { return ErrForbidden }
			}
			child, e := clone.discoverConfirmedDispatch(ctx, w, origin, st.GoalID, p, taskInput)
			if e == nil || child.JobID != "" {
				t.Fatalf("unsafe recovery %s: %+v %v", name, child, e)
			}
		})
	}
	dispatchEvidence(t, "dispatch-discovery-negatives", map[string]any{"origin": origin, "cases_rejected": cases, "fixture_boundary": "Actual accepted SQLite child and retained dispatch input; returned read views are altered individually. No submit adapter is invoked."})
}
