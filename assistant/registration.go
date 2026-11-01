package assistant

import (
	"context"
	"fmt"
	"sync"

	"github.com/colony-2/c2j/pkg/ops"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
)

// Definition contains schema/step metadata only. The compiler's process registry
// needs it, but it never holds a host Client and cannot execute assistant work.
// Recipe op names select the operation; c2j expands its run step to the task route
// cortex_assistant:run.
func Definition() ops.RegisterableOp { return definition }

var definition = ops.NewOp().WithType(ActivityName).WithVersion("2.0.0").AddStep("run", ops.NewStepWithDeps(func(ops.OpDependencies, context.Context, ActivityInput) (map[string]any, error) {
	return nil, fmt.Errorf("%w: assistant host activity is not installed", ErrUnavailable)
})).BuildOrPanic()
var definitionMu sync.Mutex

// RegisterActivity installs the executable only in this instance's registry.
// Existing compiler metadata is accepted only when it is our fail-closed
// definition. Host-bound and foreign conflicts never overwrite registrations.
// Call before WorkSet construction; registry mutation is a startup operation.
func RegisterActivity(registry *workerops.ActivityRegistry, activity ops.RegisterableOp) error {
	if registry == nil || activity == nil || activity.GetMetadata().Type != ActivityName || activity.GetMetadata().Version != "2.0.0" || len(activity.TaskChain()) != 1 || activity.TaskChain()[0].Name != "run" {
		return ErrInvalid
	}
	definitionMu.Lock()
	defer definitionMu.Unlock()
	if old, ok := ops.Get(ActivityName); ok && old != definition {
		return fmt.Errorf("%w: global assistant definition", ErrConflict)
	}
	if old, ok := registry.Get(ActivitySelector); ok {
		if old.Activity != definition {
			return fmt.Errorf("%w: assistant activity already installed", ErrConflict)
		}
		old.Activity = activity
		old.Step = activity.TaskChain()[0]
		registry.UpdateRegistration(ActivitySelector, old)
	} else if err := registry.Register(activity); err != nil {
		return err
	}
	ops.Register(definition)
	return nil
}
