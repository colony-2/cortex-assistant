package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/c2j/pkg/recipejob"
)

type ordinaryTargetFixture struct {
	jobinspect.API
	target recipejob.ResolvedTarget
	err    error
}

func (f ordinaryTargetFixture) ResolveTarget(context.Context, string) (recipejob.ResolvedTarget, error) {
	return f.target, f.err
}
func TestGoalOrdinaryProposalPolicy(t *testing.T) {
	snapshot, _ := snapshotFixture()
	target := recipejob.ResolvedTarget{CellID: "registered-id", CellName: "canonical", CellRepo: "github.com/example/work", RepositorySource: "https://github.com/example/work.git", DefaultRef: "main"}
	api := ordinaryTargetFixture{target: target}
	model := ModelOutput{Response: string(mustJSON(map[string]any{"text": "proposal", "documents": []any{}, "return_artifact_ids": []any{}, "citations": []any{}, "create_goal": map[string]any{"cell": "alias", "prompt": "exact prompt"}}))}
	response, _, err := ordinaryGoalModel(context.Background(), api, model, snapshot, nil, nil, Locator{JobID: "discussion", Invocation: "response-1"})
	if err != nil || response.Proposal == nil || response.Proposal.Target.CellID != target.CellID {
		t.Fatal(response, err)
	}
	if _, _, err = validateOrdinaryModelArtifacts(model, snapshot, nil, nil); err == nil {
		t.Fatal("v3 accepted new proposal shape")
	}
	p := response.Proposal
	cmd := &Command{Command: "confirm", ProposalID: p.ID, ProposalHash: p.Hash}
	accept := OrdinaryResult{Kind: "accept", Origin: Locator{JobID: "discussion"}, Command: cmd, Receipt: &input.Receipt{Actor: input.Actor{Kind: "human", ID: "user"}}}
	records := []ordinaryRecord{{result: &accept}}
	current := jobcontext.Current{JobID: "discussion"}
	if dispatch, e := prepareOrdinaryDispatch(context.Background(), api, current, records, response); e != nil || dispatch.Recipes[0].Inputs["prompt"] != p.Prompt {
		t.Fatal(dispatch, e)
	}
	for _, change := range []func(*recipejob.ResolvedTarget){func(v *recipejob.ResolvedTarget) { v.CellID = "replacement" }, func(v *recipejob.ResolvedTarget) { v.CellName = "other" }, func(v *recipejob.ResolvedTarget) { v.CellRepo = "github.com/example/other" }, func(v *recipejob.ResolvedTarget) { v.RepositorySource = "https://github.com/example/other.git" }} {
		altered := api
		change(&altered.target)
		if _, e := prepareOrdinaryDispatch(context.Background(), altered, current, records, response); !errors.Is(e, ErrStale) {
			t.Fatal("changed confirmed target accepted", e)
		}
	}
	unavailable := api
	unavailable.err = ErrUnavailable
	if _, e := prepareOrdinaryDispatch(context.Background(), unavailable, current, records, response); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	accept.Receipt.Actor.Kind = "agent"
	if _, e := prepareOrdinaryDispatch(context.Background(), api, current, records, response); !errors.Is(e, ErrForbidden) {
		t.Fatal("nonhuman receipt", e)
	}
	// The real input receipt consumes the proposal before the accept helper runs.
	form := ordinaryFormResponse([]ordinaryRecord{{result: &OrdinaryResult{Kind: "response", Response: &response}}, {human: &input.Output{Response: cmd, Receipt: &input.Receipt{Actor: input.Actor{Kind: "human", ID: "user"}}}}}, GoalProtocol)
	if form.Proposal != nil || form.Error == "" {
		t.Fatal("uncertain confirmation offered again", form)
	}
}
