package assistant

import (
	"context"
	"fmt"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type GoalReader interface {
	GetGoal(context.Context, jobdb.GoalKey) (jobdb.GoalInfo, error)
}

// HistoryReader is the existing runtime history reader, not a transport or store.
type HistoryReader interface {
	GetJobRun(context.Context, jobdb.GetJobRunRequest) (jobdb.GetJobRunResponse, error)
}

// bindLineage verifies the runtime's restart source before assigning ancestor
// locators. Equal ordinals or equal text alone never establish shared history.
func (c *Client) bindLineage(ctx context.Context, w Caller, d *replayData, visited map[string]bool) error {
	if d.Args.Protocol == GoalProtocol {
		if d.Story.GoalID != d.Args.SessionID {
			return ErrUnavailable
		}
		if d.Previous == "" {
			return nil
		}
		parent, err := c.readVisited(ctx, w, d.Previous, visited)
		if err != nil {
			return err
		}
		if parent.Args.Protocol != GoalProtocol || parent.Story.GoalID != d.Story.GoalID || parent.Args.Source != d.Args.Source || string(mustJSON(parent.Snapshot)) != string(mustJSON(d.Snapshot)) {
			return ErrUnavailable
		}
		contextJob := parent.ContextJob
		if parent.Capture != nil {
			contextJob = parent.Story.JobID
		}
		if d.ContextJob != contextJob || (contextJob == "" && d.Args.Prompt != parent.OpeningPrompt) || (contextJob != "" && d.Args.Prompt != "") {
			return ErrUnavailable
		}
		d.OpeningPrompt = parent.OpeningPrompt
		d.Ancestors = append(append([]string{}, parent.Ancestors...), parent.Story.JobID)
		d.Entries = append(append([]Entry{}, parent.Entries...), d.Entries...)
		d.Inputs = append(append([]input.Output{}, parent.Inputs...), d.Inputs...)
		return nil
	}
	if d.Story.JobID == d.Args.SessionID {
		return nil
	}
	run, e := c.host.History.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: jobdb.JobKey{TenantId: w.TenantID, JobId: d.Story.JobID}})
	if e != nil {
		return fmt.Errorf("%w: restart source: %v", ErrUnavailable, e)
	}
	var source *jobdb.JobKey
	for _, ev := range run.Execution {
		if ev.Record.Kind == "restart" {
			source = ev.Record.Source
		}
	}
	if source == nil || source.TenantId != w.TenantID || source.JobId == d.Story.JobID {
		return ErrUnavailable
	}
	parent, e := c.readVisited(ctx, w, source.JobId, visited)
	if e != nil {
		return e
	}
	d.Ancestors = append(append([]string{}, parent.Ancestors...), parent.Story.JobID)
	if string(mustJSON(parent.Args)) != string(mustJSON(d.Args)) {
		return ErrUnavailable
	}
	for i := range d.Entries {
		en := &d.Entries[i]
		if en.Origin.Task == 0 {
			continue
		}
		matched := false
		for _, old := range parent.Entries {
			same := en.Receipt != nil && old.Receipt != nil && en.Receipt.RequestID == old.Receipt.RequestID && en.Receipt.SubmissionID == old.Receipt.SubmissionID
			same = same || (en.Receipt == nil && old.Receipt == nil && en.Origin.JobID == old.Origin.JobID && en.Origin.Invocation != "" && en.Origin.Invocation == old.Origin.Invocation)
			if !same {
				continue
			}
			a, b := *en, old
			a.Origin, b.Origin = Locator{}, Locator{}
			if string(mustJSON(a)) != string(mustJSON(b)) {
				return ErrUnavailable
			}
			en.Origin = old.Origin
			matched = true
			break
		}
		if !matched && en.Origin.JobID != d.Story.JobID {
			return ErrUnavailable
		}
	}
	return nil
}
