package assistant

import (
	"errors"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func TestUnstartedSubmissionRequiresRetainedIdentity(t *testing.T) {
	c, _, ctl, _ := client(t)
	release := ReleaseDescriptor{Recipe: "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit, Commit: commit, Protocol: GoalProtocol}
	snapshot := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(1, 0), Documents: []SnapshotDocument{}}
	inputs := map[string]any{"snapshot": snapshot, "prompt": "original", "protocol": GoalProtocol}
	start := workflowctl.StartJob{TenantId: human.TenantID, JobID: "job", Cell: c.cfg.Root.CellName, CellRepo: c.cfg.Root.CellRepo, RecipeName: release.Recipe, Inputs: inputs, InputHash: recipejob.InputHash(inputs)}
	key := jobdb.JobKey{TenantId: human.TenantID, JobId: "job"}
	for _, test := range []string{"valid", "missing", "tenant", "job", "recipe", "hash", "goal", "key"} {
		t.Run(test, func(t *testing.T) {
			candidate := start
			run := jobdb.GetJobRunResponse{Job: jobdb.JobRunSummary{JobKey: key, GoalID: "goal", Status: jobdb.JobStatusCancelled}}
			switch test {
			case "tenant":
				candidate.TenantId = "foreign"
			case "job":
				candidate.JobID = "foreign"
			case "recipe":
				candidate.RecipeName = "foreign"
			case "hash":
				candidate.InputHash = "corrupt"
			case "goal":
				run.Job.GoalID = ""
			case "key":
				run.Job.JobKey.JobId = "foreign"
			}
			// Match the actual retained JSON decoding boundary, including loss of Go types.
			var wire any
			if err := decode(candidate, &wire); err != nil {
				t.Fatal(err)
			}
			run.Start.Input = &jobdb.TaskIO{Data: mustJSON(wire)}
			if test == "missing" {
				run.Start.Input = nil
			}
			ctl.runs = map[string]jobdb.GetJobRunResponse{"job": run}
			projected, err := c.projectUnstarted(ctx, human, "job", release)
			if test != "valid" {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("accepted %s: %v", test, err)
				}
				return
			}
			if err != nil || projected.Root == nil || len(projected.Root.Children) != 0 || projected.Recipe.Source.ResolvedCommit != "" {
				t.Fatalf("invented execution or lost inputs: %+v %v", projected, err)
			}
		})
	}
}
