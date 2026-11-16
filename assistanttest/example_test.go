package assistanttest_test

import (
	"context"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/cortex-assistant/assistanttest"
)

// Lower-level assertions borrow the consumer's existing Host and Client; they
// install no worker and own no adapter, storage or transport.
func ExampleCheckTurn() {
	var host assistant.Host
	var client *assistant.Client
	var inspector jobinspect.API
	var caller assistant.Caller
	ctx := context.Background()
	current, err := assistanttest.AwaitPrompt(ctx, host, client, caller, "goal", "job", "")
	if err != nil {
		return
	}
	_, _ = assistanttest.CheckTurn(ctx, inspector, client, caller, current.ID, assistanttest.Turn{JobID: current.JobID, Protocol: assistant.GoalProtocol, Opening: true})
}

// Compose borrows adapters in Options. Owned explicitly transfers cleanup;
// workers stop and join before those callbacks run, in reverse order.
func ExampleCompose() {
	ctx := context.Background()
	var options assistanttest.Options // caller supplies ordinary scoped runtime, schemas, goals, registry and config
	harness, err := assistanttest.Compose(ctx, options)
	if err != nil {
		return
	}
	defer harness.Close(context.WithoutCancel(ctx))
	_ = harness.Client
	_ = harness.Host
	_ = harness.Control
	_ = harness.Input
}
