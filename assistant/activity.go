package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

const ActivityName = "cortex_assistant"
const ActivitySelector = ActivityName + ":run"

// ActivityOptions are trusted host composition inputs, never recipe/browser inputs.
// AuthorizeExecution must bind coordinates to an authorized session principal;
// possession of coordinates alone grants no authority.
type ActivityOptions struct {
	Provider           string
	Model              string
	AuthorizeExecution func(context.Context, jobcontext.Current) (Caller, error)
}

type ActivityInput struct {
	Action string       `json:"action"`
	Model  *ModelOutput `json:"model,omitempty"`
}

// NewActivity creates an instance-bound operation. Register it on the host's
// ActivityRegistry before constructing its guarded recipe WorkSet. It does not
// register globally, start an engine, create clients, or read environment files.
func NewActivity(client *Client, options ActivityOptions) (ops.RegisterableOp, error) {
	if client == nil || options.AuthorizeExecution == nil || strings.TrimSpace(options.Provider) == "" || strings.TrimSpace(options.Model) == "" {
		return nil, fmt.Errorf("%w: activity host, authorizer and provider/model required", ErrInvalid)
	}
	return ops.NewOp().WithType(ActivityName).WithVersion("2.0.0").
		AddStep("run", ops.NewStepWithDeps(func(deps ops.OpDependencies, ctx context.Context, in ActivityInput) (map[string]any, error) {
			data, err := json.Marshal(in)
			if err != nil {
				return nil, fmt.Errorf("%w: activity input", ErrInvalid)
			}
			if len(data) > 6*(6*(client.cfg.Limits.Count*client.cfg.Limits.Document+client.cfg.Limits.Assistant+client.cfg.Limits.Human)+(1<<20))+(1<<20) {
				return nil, ErrLimit
			}
			state := map[string]string{"capture": "capture", "assemble": "infer", "response": "infer", "form": "prompt", "accept": "prompt", "prepare-action": "action", "dispatch": "action"}[in.Action]
			if state == "" || (in.Action == "response") != (in.Model != nil) {
				return nil, ErrInvalid
			}
			current := deps.CurrentJobContext()
			path := strings.Split(current.InvocationPath, "/")
			if !current.HasJob() || current.InvocationHash == "" || current.InvocationSequence < 0 || current.JobType != "recipe" || current.OpType != ActivityName || current.OpStep != "run" || current.OpTaskType != ActivitySelector || len(path) < 4 || len(path) > 5 || path[0] != RecipeID || path[1] != state || path[2] != "sequence" || path[3] != in.Action || (len(path) == 5 && path[4] != "step:run") {
				return nil, fmt.Errorf("%w: activity execution coordinates", ErrForbidden)
			}
			who, err := options.AuthorizeExecution(ctx, current)
			if err != nil {
				return nil, fmt.Errorf("assistant: execution authorization: %w", err)
			}
			if who.TenantID != current.TenantID || who.Actor.ID == "" {
				return nil, ErrForbidden
			}
			// Check the execution's approved current release without loading
			// a large story twice. Every Worker action reads and authenticates
			// that story/protocol before protected effects through the facade.
			if err = client.authorize(ctx, who, current.JobID); err != nil {
				return nil, err
			}
			rows, err := client.rootJobs(ctx, who, []string{current.JobID}, "")
			if err != nil {
				return nil, err
			}
			if len(rows.Jobs) != 1 || rows.Jobs[0].JobID != current.JobID || rows.Jobs[0].RecipeName != client.cfg.Recipe {
				return nil, ErrForbidden
			}
			model := ModelOutput{}
			if in.Model != nil {
				model = *in.Model
			}
			result, docs, err := client.Worker(ctx, who, Locator{JobID: current.JobID, Invocation: current.InvocationHash, Path: path}, in.Action, model, options.Provider, options.Model)
			if err != nil {
				return nil, err
			}
			for _, doc := range docs {
				if err = deps.AddOutputArtifact(jobdb.NewArtifactFromBytes(doc.Name, []byte(doc.Text))); err != nil {
					return nil, fmt.Errorf("assistant: publish document: %w", err)
				}
			}
			// TaskResult contains upstream facade structs without schema tags.
			// Preserve its canonical JSON wire shape at the activity boundary.
			data, err = json.Marshal(result)
			if err != nil {
				return nil, err
			}
			var output map[string]any
			if err = json.Unmarshal(data, &output); err != nil {
				return nil, err
			}
			return output, nil
		})).Build()
}
