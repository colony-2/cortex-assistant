package assistant

import (
	"context"
	"fmt"

	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobinspect"
)

// Retained context is read from original task results on demand, not copied
// into a context artifact or another conversation ledger.
func retainedGoalRecords(ctx context.Context, api jobinspect.API, current jobcontext.Current, capture *OrdinaryResult, visited map[string]bool) ([]ordinaryRecord, error) {
	job := capture.PriorJob
	if job == "" {
		return nil, nil
	}
	if visited[job] || len(visited) >= 256 {
		return nil, ErrConflict
	}
	visited[job] = true
	state, err := api.Inspect(ctx, jobinspect.Request{Operation: "show", JobID: job})
	if err != nil || state == nil || state.Job == nil {
		return nil, fmt.Errorf("%w: prior job %s", ErrUnavailable, job)
	}
	if state.Job.ID != job || state.Job.Tenant != current.TenantID || state.Job.Goal != current.GoalID || state.Job.Cell != current.CellName {
		return nil, ErrForbidden
	}
	records, err := readOrdinaryRecordsForProtocol(ctx, api, job, GoalProtocol)
	if err != nil {
		return nil, fmt.Errorf("%w: prior job %s", err, job)
	}
	var parent *OrdinaryResult
	for _, r := range records {
		if r.result != nil && r.result.Kind == "capture" {
			if parent != nil {
				return nil, ErrConflict
			}
			parent = r.result
		}
	}
	if parent == nil || parent.SessionID != current.GoalID || parent.Snapshot == nil || string(mustJSON(parent.Snapshot)) != string(mustJSON(capture.Snapshot)) {
		return nil, ErrUnavailable
	}
	previous, err := retainedGoalRecords(ctx, api, current, parent, visited)
	if err != nil {
		return nil, err
	}
	return append(previous, records...), nil
}
