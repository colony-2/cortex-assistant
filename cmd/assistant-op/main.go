// assistant-op is a cell-local c2j extension helper. Configuration and execution
// identity come from the worker, never model output or browser request fields.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/ops/extensions"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/template/colonycel"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerexport "github.com/colony-2/c2j/pkg/worker/export"
	workerworkflow "github.com/colony-2/c2j/pkg/worker/workflow"
	"github.com/colony-2/cortex-assistant/assistant"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/runtime/remote"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
	"golang.org/x/oauth2"
)

type settings struct {
	Config     assistant.Config                    `json:"config"`
	RuntimeURL string                              `json:"runtime_url"`
	TenantID   string                              `json:"tenant_id"`
	Provider   string                              `json:"provider"`
	Model      string                              `json:"model"`
	Catalog    map[string]recipejob.ResolvedTarget `json:"catalog"`
}
type request struct {
	Action string                `json:"action"`
	Model  assistant.ModelOutput `json:"model"`
	Outbox string                `json:"artifact_outbox_path"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	coreops.Replace(append(workerexport.GetAll(), extensions.GetExecutionOp(), input.GetOp())...)
	current, ok, e := jobcontext.CurrentFromEnv(os.Getenv)
	if e != nil {
		return e
	}
	if !ok || current.InvocationHash == "" {
		return errors.New("trusted c2j execution identity required")
	}
	f, e := os.Open(os.Getenv("CORTEX_ASSISTANT_CONFIG"))
	if e != nil {
		return errors.New("worker CORTEX_ASSISTANT_CONFIG is required")
	}
	defer f.Close()
	var cfg settings
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&cfg); e != nil {
		return e
	}
	if cfg.TenantID != current.TenantID {
		return assistant.ErrForbidden
	}
	var in request
	dec = json.NewDecoder(io.LimitReader(os.Stdin, 64<<20))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&in); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if token := os.Getenv("JOBDB_BEARER_TOKEN"); token != "" {
		httpClient = oauth2.NewClient(context.WithValue(ctx, oauth2.HTTPClient, httpClient), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"}))
	}
	runtime, e := remote.New(cfg.RuntimeURL, httpClient)
	if e != nil {
		return e
	}
	engine, e := jobworkflow.NewEngineBuilder().WithRuntime(runtime).BuildEngine()
	if e != nil {
		return e
	}
	wrapped := jobdbschema.WorkflowEngine{Engine: engine, Registry: runtime}
	ctl := &workerworkflow.SWFWorkflowControl{Engine: wrapped, PreferRuntimeRecipeResolution: true}
	inputs, e := input.NewRuntime(ctl, nil)
	if e != nil {
		return e
	}
	stories, e := story.New(story.ServiceConfig{Engine: wrapped, SchemaRegistry: runtime, RootSourceResolver: compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{}), CELOptionsProvider: colonycel.NewBuilder(colonycel.Options{})})
	if e != nil {
		return e
	}
	who := assistant.Caller{TenantID: current.TenantID, Actor: input.Actor{ID: current.JobID, Kind: "agent"}}
	host := assistant.Host{Goals: runtime, History: wrapped, Control: ctl, Story: stories, Input: inputs, Submitter: wrapped,
		Authorize: func(ctx context.Context, w assistant.Caller, id string) error {
			if w.TenantID != cfg.TenantID {
				return assistant.ErrForbidden
			}
			if id == "start" || id == "list" {
				return nil
			}
			_, e := ctl.InspectJob(ctx, jobdb.JobKey{TenantId: w.TenantID, JobId: id})
			return e
		},
		ResolveCell: func(ctx context.Context, w assistant.Caller, name string) (recipejob.ResolvedTarget, error) {
			if w.TenantID != cfg.TenantID {
				return recipejob.ResolvedTarget{}, assistant.ErrForbidden
			}
			for key, t := range cfg.Catalog {
				if key == name || t.RepositorySource == name {
					return t, nil
				}
			}
			return recipejob.ResolvedTarget{}, assistant.ErrForbidden
		},
	}
	client, e := assistant.New(host, cfg.Config)
	if e != nil {
		return e
	}
	origin := assistant.Locator{JobID: current.JobID, Invocation: current.InvocationHash, Path: []string{current.InvocationPath}}
	result, docs, e := client.Worker(ctx, who, origin, in.Action, in.Model, cfg.Provider, cfg.Model)
	if e != nil {
		return e
	}
	if len(docs) > 0 {
		if in.Outbox == "" || !filepath.IsAbs(in.Outbox) {
			return errors.New("worker artifact outbox is required")
		}
		for _, doc := range docs {
			if e = os.WriteFile(filepath.Join(in.Outbox, doc.Name), []byte(doc.Text), 0600); e != nil {
				return e
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"output": result})
}
