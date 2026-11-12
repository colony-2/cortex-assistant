package assistant

import (
	"errors"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func TestLatestForGoalFollowsRecordedContinuationAcrossPages(t *testing.T) {
	c, ss, ctl, _ := client(t)
	ctl.backendPage = 1
	parent := fixture(c)
	parent.GoalID = "discussion-goal"
	parent.Status = story.WorkflowStatusCompleted
	parent.StartedAt = time.Unix(500, 0)
	parent.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "retained reply")}
	ss.jobs["job"] = parent
	child := fixture(c)
	child.JobID = "next"
	child.GoalID = parent.GoalID
	child.StartedAt = time.Unix(1, 0) // Clock order is deliberately misleading.
	child.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "retained reply")}
	ss.jobs["next"] = child
	ctl.runs = map[string]jobdb.GetJobRunResponse{"next": {Execution: []jobdb.ExecutionHistoryEntry{{Record: jobdb.ExecutionRecord{Kind: "restart", Source: &jobdb.JobKey{TenantId: "tenant", JobId: "job"}}}}}}
	// Unreadable unrelated sessions must not affect a goal-scoped lookup.
	other := fixture(c)
	other.JobID, other.GoalID = "aaa-unrelated", "other-goal"
	ss.jobs[other.JobID] = other
	session, err := c.LatestForGoal(ctx, human, parent.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "job" || session.JobID != "next" || session.GoalID != parent.GoalID || session.Replay.Entries[1].Origin.JobID != "job" {
		t.Fatalf("lost continuation identity/original origins: %+v", session)
	}
	if _, err = c.LatestForGoal(ctx, human, "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent goal: %v", err)
	}
	if _, err = c.LatestForGoal(ctx, human, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid goal: %v", err)
	}
}

func TestLatestForGoalRejectsCompetingTips(t *testing.T) {
	c, ss, ctl, _ := client(t)
	ctl.backendPage = 1
	parent := fixture(c)
	parent.GoalID = "goal"
	ss.jobs["job"] = parent
	ctl.runs = map[string]jobdb.GetJobRunResponse{}
	for _, id := range []string{"branch-a", "branch-b"} {
		child := fixture(c)
		child.JobID, child.GoalID = id, parent.GoalID
		ss.jobs[id] = child
		ctl.runs[id] = jobdb.GetJobRunResponse{Execution: []jobdb.ExecutionHistoryEntry{{Record: jobdb.ExecutionRecord{Kind: "restart", Source: &jobdb.JobKey{TenantId: "tenant", JobId: "job"}}}}}
	}
	if _, err := c.LatestForGoal(ctx, human, "goal"); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing tips: %v", err)
	}
}
