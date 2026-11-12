package assistant

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobinspect"
	recipeops "github.com/colony-2/c2j/pkg/ops/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// OrdinaryTargetResolver is provided by the existing task-scoped broker. A
// model's cell string is only a lookup request, never an approved destination.
type OrdinaryTargetResolver interface {
	ResolveTarget(context.Context, string) (recipejob.ResolvedTarget, error)
}

func goalModelSchema() map[string]any {
	s := OrdinaryModelSchema()
	s["properties"].(map[string]any)["create_goal"] = ModelSchema()["properties"].(map[string]any)["create_goal"]
	return s
}

func resolveOrdinaryTarget(ctx context.Context, api jobinspect.API, cell string) (recipejob.ResolvedTarget, error) {
	resolver, ok := api.(OrdinaryTargetResolver)
	if !ok {
		return recipejob.ResolvedTarget{}, ErrUnavailable
	}
	target, err := resolver.ResolveTarget(ctx, cell)
	if err != nil {
		return target, err
	}
	if target.CellID == "" || target.CellName == "" || target.CellRepo == "" || target.RepositorySource == "" {
		return target, ErrForbidden
	}
	return target, nil
}

func ordinaryGoalModel(ctx context.Context, api jobinspect.API, model ModelOutput, snapshot FrozenSnapshot, coverage map[string]string, artifacts map[string]Artifact, origin Locator) (Response, []Document, error) {
	if len(model.Response) > OrdinaryLimits().Model {
		return Response{}, nil, ErrLimit
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(model.Response), &fields) != nil {
		return Response{}, nil, ErrInvalid
	}
	var goal *GoalRequest
	if raw, ok := fields["create_goal"]; ok {
		if decode(raw, &goal) != nil {
			return Response{}, nil, ErrInvalid
		}
		delete(fields, "create_goal")
	}
	model.Response = string(mustJSON(fields))
	response, docs, err := validateOrdinaryModelArtifacts(model, snapshot, coverage, artifacts)
	if err != nil || goal == nil {
		return response, docs, err
	}
	if text(goal.Cell, 4096, false) != nil || text(goal.Prompt, OrdinaryLimits().Human, false) != nil || len(mustJSON(goal.Prompt))-2 > OrdinaryLimits().Human {
		return Response{}, nil, ErrLimit
	}
	target, err := resolveOrdinaryTarget(ctx, api, goal.Cell)
	if err != nil {
		return Response{}, nil, err
	}
	p := Proposal{ID: identity("proposal", origin.JobID, origin.Invocation), Target: target, Prompt: goal.Prompt}
	p.Hash = identity(p.ID, string(mustJSON(target)), p.Prompt)
	response.Proposal = &p
	return response, docs, nil
}

// Parsing retained input is distinct from authorizing confirmation. Accept and
// prepare compare it with the exact preceding proposal and human receipt.
func ordinaryRecordedCommand(v any, protocol string) (Command, error) {
	var cmd Command
	if decode(v, &cmd) != nil {
		return cmd, ErrInvalid
	}
	if protocol == GoalProtocol && cmd.Command == "confirm" {
		if text(cmd.ProposalID, 512, false) != nil || text(cmd.ProposalHash, 512, false) != nil {
			return cmd, ErrInvalid
		}
		c := Client{cfg: Config{Limits: OrdinaryLimits()}}
		return c.command(v, &Proposal{ID: cmd.ProposalID, Hash: cmd.ProposalHash})
	}
	return ordinaryCommandForProtocol(v, protocol)
}

func prepareOrdinaryDispatch(ctx context.Context, api jobinspect.API, current jobcontext.Current, records []ordinaryRecord, latest Response) (*recipeops.MultipleRecipes, error) {
	if latest.Proposal == nil {
		return nil, ErrStale
	}
	var accepted *OrdinaryResult
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.result != nil && r.result.Kind == "accept" {
			accepted = r.result
			break
		}
	}
	if accepted == nil || accepted.Origin.JobID != current.JobID || accepted.Command == nil || accepted.Receipt == nil || (accepted.Receipt.Actor.Kind != "human" && accepted.Receipt.Actor.Kind != "user") {
		return nil, ErrForbidden
	}
	c := Client{cfg: Config{Limits: OrdinaryLimits()}}
	if cmd, err := c.command(accepted.Command, latest.Proposal); err != nil || cmd.Command != "confirm" {
		return nil, ErrStale
	}
	target, err := resolveOrdinaryTarget(ctx, api, latest.Proposal.Target.CellName)
	if err != nil {
		return nil, err
	}
	if !sameOrdinaryTarget(target, latest.Proposal.Target) {
		return nil, ErrStale
	}
	// The standard recipe operation owns the retained invocation, destination,
	// inherited context, task authority and replay; do not put it inside a recipe
	// retry clause, which deliberately allocates a new logical invocation.
	return &recipeops.MultipleRecipes{GitRef: target.DefaultRef, Recipes: []recipeops.SingleRecipe{{Name: "build", CellName: target.CellName, CellRepo: target.CellRepo, Inputs: map[string]any{"type": "build", "prompt": latest.Proposal.Prompt}, Git: recipeops.SingleRecipeGit{BaseRepo: target.RepositorySource, BaseRef: target.DefaultRef}}}}, nil
}

func ordinaryDispatchResult(ctx context.Context, api jobinspect.API, job string) (string, error) {
	cursor := ""
	seen := map[string]bool{}
	destination := ""
	for {
		page, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: job, Limit: 100, Cursor: cursor})
		if err != nil || page == nil || page.Job == nil || page.Job.ID != job {
			return "", ErrUnavailable
		}
		for _, task := range page.Tasks {
			if task.Path != RecipeID+"/action/sequence/dispatch" || task.Type != "recipes.run:start" || task.Status != "succeeded" {
				continue
			}
			field := ""
			value, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: job, Task: task.ID, Attempt: task.Attempt, Field: &field})
			if err != nil || value == nil || task.OutputAvailability != "available" {
				return "", ErrUnavailable
			}
			var out recipeops.StartedJobs
			if decode(value.Value, &out) != nil || len(out.Items) != 1 || out.Items[0].JobID == "" || out.Items[0].GoalID == "" {
				return "", ErrUnavailable
			}
			destination = out.Items[0].JobID
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return "", ErrUnavailable
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	if strings.TrimSpace(destination) == "" {
		return "", ErrUnavailable
	}
	return destination, nil
}

func sameOrdinaryTarget(a, b recipejob.ResolvedTarget) bool {
	ar, ae := jobdb.NormalizeCellRepo(a.CellRepo)
	br, be := jobdb.NormalizeCellRepo(b.CellRepo)
	as, ase := jobdb.NormalizeCellRepo(a.RepositorySource)
	bs, bse := jobdb.NormalizeCellRepo(b.RepositorySource)
	return ae == nil && be == nil && ase == nil && bse == nil && a.CellID != "" && a.CellID == b.CellID && a.CellName == b.CellName && ar == br && ar == as && br == bs && a.DefaultRef == b.DefaultRef
}

// Confirmation is consumed even if a later task's result is unavailable. A
// reopened discussion must not offer the same already accepted action again.
func ordinaryFormResponse(records []ordinaryRecord, protocol string) Response {
	var latest Response
	for _, record := range records {
		if protocol == GoalProtocol && record.human != nil {
			c := Client{cfg: Config{Limits: OrdinaryLimits()}}
			if cmd, e := c.command(record.human.Response, latest.Proposal); e == nil && cmd.Command == "confirm" {
				latest = Response{Error: "The confirmed submission has no recorded result. Inspect its original execution before retrying."}
			}
		}
		r := record.result
		if r == nil {
			continue
		}
		if r.Response != nil {
			latest = *r.Response
		}
		if protocol == GoalProtocol && r.Kind == "accept" && r.Command != nil && r.Command.Command == "confirm" {
			latest = Response{Error: "The confirmed submission has no recorded result. Inspect its original execution before retrying."}
		}
		if r.Kind == "dispatched" {
			latest = Response{Text: "Goal submitted: " + r.Destination}
		}
	}
	return latest
}
