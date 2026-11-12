package assistant_test

import (
	"context"
	"time"

	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/secrets"
	"github.com/colony-2/cortex-assistant/assistant"
)

// The embedding application supplies existing runtime collaborators, a published
// immutable commit, and a stable deployment cursor key. No runner is installed.
func ExampleClient_StartFrozen() {
	start := func(ctx context.Context, host assistant.Host, root recipejob.ResolvedTarget, releasedCommit string, cursorKey []byte, caller assistant.Caller, in assistant.StartInput, captured assistant.FrozenSnapshot, selected assistant.SnapshotSelection) (assistant.Session, error) {
		client, err := assistant.New(host, assistant.Config{
			Root: root, Protocol: assistant.OrdinaryProtocol,
			Recipe:       "git+https://github.com/colony-2/cortex-assistant.git//discussion.yaml@" + releasedCommit,
			RecipeCommit: releasedCommit, CursorKey: cursorKey,
			Bindings: &assistant.OrdinaryBindings{BaseURL: "https://provider.example.invalid/exact/root", APIKey: secrets.Reference{Name: "assistant_api_key"}, Provider: "openai", Model: "approved-model"},
		})
		if err != nil {
			return assistant.Session{}, err
		}
		// in.RequestID, in.SubmittedAt and captured/selected were frozen together
		// at Cortex's request boundary; retries reuse every field unchanged.
		return client.StartFrozen(ctx, caller, in, captured, selected)
	}
	_ = start
}

// Persist the complete prepared operation in the trusted host's existing
// request record before publication. An ambiguous result retries that operation.
func ExampleClient_PrepareStartFrozen() {
	start := func(ctx context.Context, host assistant.Host, root recipejob.ResolvedTarget, publishedCommit string, cursorKey []byte, caller assistant.Caller, in assistant.StartInput, snapshot assistant.FrozenSnapshot, selected assistant.SnapshotSelection, retain func(assistant.FrozenStart) error) (assistant.Session, error) {
		client, err := assistant.New(host, assistant.Config{
			Root: root, Protocol: assistant.GoalProtocol,
			Recipe:       "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + publishedCommit,
			RecipeCommit: publishedCommit, CursorKey: cursorKey,
			Bindings: &assistant.OrdinaryBindings{BaseURL: "https://provider.example.invalid/exact/root", APIKey: secrets.Reference{Name: "assistant_api_key"}, Provider: "openai", Model: "approved-model"},
		})
		if err != nil {
			return assistant.Session{}, err
		}
		frozen, err := client.PrepareStartFrozen(ctx, caller, in, snapshot, selected)
		if err != nil {
			return assistant.Session{}, err
		}
		if err = retain(frozen); err != nil {
			return assistant.Session{}, err
		}
		// Keep frozen on any error; do not prepare again or choose another destination.
		return client.SubmitFrozenStart(ctx, caller, frozen)
	}
	_ = start
}

func ExampleClient_PrepareReopen() {
	reopen := func(ctx context.Context, client *assistant.Client, caller assistant.Caller, capturedJob, requestID string, submittedAt time.Time, retain func(assistant.FrozenStart) error) (assistant.Session, error) {
		state, err := client.Close(ctx, caller, capturedJob)
		if err != nil {
			return assistant.Session{}, err
		}
		if !state.Terminal {
			return assistant.Session{}, assistant.ErrNotAtPrompt
		}
		frozen, err := client.PrepareReopen(ctx, caller, capturedJob, requestID, submittedAt)
		if err != nil {
			return assistant.Session{}, err
		}
		if err = retain(frozen); err != nil {
			return assistant.Session{}, err
		}
		// A replacement host retries this saved operation with the same CursorKey.
		// Goal identity comes from the receipt; capturedJob never retargets a successor.
		return client.SubmitFrozenStart(ctx, caller, frozen)
	}
	_ = reopen
}
