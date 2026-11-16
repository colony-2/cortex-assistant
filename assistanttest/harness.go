package assistanttest

import (
	"context"
	"errors"
	"sync"

	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/template/colonycel"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	workerworkflow "github.com/colony-2/c2j/pkg/worker/workflow"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/jobdb/pkg/jobdb"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

// Options borrows every supplied adapter. Initialize the ordinary built-in
// operation registry normally in the application; Compose never changes it.
// Scoped retains allocation, StageExecution and task fault/authority wrappers.
// AdaptHost runs before client construction and preserves caller fault wrappers.
type Options struct {
	Scoped      *executionruntime.Runtime
	Schemas     jobdb.JobSchemaRegistry
	Goals       assistant.GoalReader
	Tenant      string
	Registry    *workerops.ActivityRegistry
	Config      assistant.Config
	Authorize   func(context.Context, assistant.Caller, string) error
	ResolveCell func(context.Context, assistant.Caller, string) (recipejob.ResolvedTarget, error)
	AdaptHost   func(assistant.Host) (assistant.Host, error)
	// Owned is explicit ownership transfer, closed in reverse order after workers
	// stop. Omit caller-created fixture transports to keep them caller-owned.
	Owned         []func(context.Context) error
	WorkerOptions compiler.RecipeJobWorkerOptions
}
type Harness struct {
	Host     assistant.Host
	Config   assistant.Config
	Client   *assistant.Client
	Engine   jobworkflow.Engine
	Control  *workerworkflow.SWFWorkflowControl
	Input    *input.Runtime
	cancel   context.CancelFunc
	done     chan struct{}
	owned    []func(context.Context) error
	once     sync.Once
	closeErr error
}

func Compose(ctx context.Context, o Options) (h *Harness, err error) {
	h = &Harness{Config: o.Config, owned: append([]func(context.Context) error(nil), o.Owned...)}
	defer func() {
		if err != nil {
			err = errors.Join(err, h.Close(context.WithoutCancel(ctx)))
			h = nil
		}
	}()
	if o.Scoped == nil || o.Schemas == nil || o.Goals == nil || o.Tenant == "" || o.Registry == nil {
		return h, errors.New("ordinary runtime, schemas, goals, tenant and registry required")
	}
	engine, e := jobworkflow.NewEngineBuilder().WithRuntime(o.Scoped).WithWorkerTenantId(o.Tenant).BuildEngine()
	if e != nil {
		return h, e
	}
	h.Engine = engine
	wrapped := jobdbschema.WorkflowEngine{Engine: engine, Registry: o.Schemas}
	h.Control = &workerworkflow.SWFWorkflowControl{Engine: wrapped}
	h.Input, e = input.NewRuntime(h.Control, nil)
	if e != nil {
		return h, e
	}
	resolver := compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{})
	stories, e := story.New(story.ServiceConfig{Engine: wrapped, SchemaRegistry: o.Schemas, RootSourceResolver: resolver, CELOptionsProvider: colonycel.NewBuilder(colonycel.Options{})})
	if e != nil {
		return h, e
	}
	h.Host = assistant.Host{Goals: o.Goals, History: wrapped, Control: h.Control, Input: h.Input, Story: stories, Submitter: wrapped, Authorize: o.Authorize, ResolveCell: o.ResolveCell}
	if o.AdaptHost != nil {
		h.Host, e = o.AdaptHost(h.Host)
		if e != nil {
			return h, e
		}
	}
	h.Client, e = assistant.New(h.Host, o.Config)
	if e != nil {
		return h, e
	}
	workerOptions := o.WorkerOptions
	if workerOptions.RootSourceResolver == nil {
		workerOptions.RootSourceResolver = resolver
	}
	workerOptions.Allocation = o.Scoped.Allocation
	if workerOptions.StageExecution == nil {
		workerOptions.StageExecution = o.Scoped.Stage
	}
	suppliedWrap := workerOptions.WrapTaskWorker
	workerOptions.WrapTaskWorker = func(w jobworkflow.TaskWorker) jobworkflow.TaskWorker {
		w = o.Scoped.WrapTaskWorker(w)
		if suppliedWrap != nil {
			w = suppliedWrap(w)
		}
		return w
	}
	workers, e := compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(h.Host.Control).Build(), o.Registry, workerOptions)
	if e != nil {
		return h, e
	}
	if e = engine.RegisterWorkers(workers); e != nil {
		return h, e
	}
	runctx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	h.done = make(chan struct{})
	go func() { defer close(h.done); engine.Run(runctx) }()
	return h, nil
}

// Close is idempotent, joins the one owned runner before closing transferred
// resources, and leaves borrowed storage/configuration/transports open.
func (h *Harness) Close(ctx context.Context) error {
	h.once.Do(func() {
		if h.cancel != nil {
			h.cancel()
		}
		if h.done != nil {
			<-h.done
		}
		for i := len(h.owned) - 1; i >= 0; i-- {
			h.closeErr = errors.Join(h.closeErr, h.owned[i](ctx))
		}
	})
	return h.closeErr
}
