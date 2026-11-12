package assistant

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/colony-2/c2j/pkg/jobref"
	"github.com/colony-2/c2j/pkg/secrets"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func TestOrdinaryResolvedGuardMatrix(t *testing.T) {
	for _, name := range []string{"empty-endpoint", "empty-model", "empty-provider", "invalid-provider", "prefixed-model", "missing-secret", "empty-secret", "ambient", "redirect", "citation", "executable", "malformed", "provider-error", "retry", "injection", "untrusted-tls"} {
		t.Run(name, func(t *testing.T) { runOrdinaryWorker(t, "goal-guard-"+name) })
	}
}
func applyOrdinaryGuardBindings(t *testing.T, mode string, c *Client, endpoint, secret string) {
	t.Helper()
	// Deliberately cross the trusted facade configuration boundary to prove the
	// actual declared include and immutable operation reject invalid bindings.
	switch strings.TrimPrefix(mode, "goal-guard-") {
	case "empty-endpoint":
		c.cfg.Bindings.BaseURL = ""
	case "empty-model":
		c.cfg.Bindings.Model = ""
	case "empty-provider":
		c.cfg.Bindings.Provider = ""
	case "invalid-provider":
		c.cfg.Bindings.Provider = "anthropic"
	case "prefixed-model":
		c.cfg.Bindings.Model = "openai:fixture"
	case "missing-secret":
		c.cfg.Bindings.APIKey = secrets.Reference{Name: "missing_fixture_key"}
	case "empty-secret":
		c.cfg.Bindings.APIKey = secrets.Reference{Name: "empty_fixture_key"}
	case "ambient":
		c.cfg.Bindings.BaseURL = ""
		for key, value := range map[string]string{"OPENAI_API_KEY": secret, "OPENAI_BASE_URL": endpoint, "HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1", "ALL_PROXY": "http://127.0.0.1:1", "NO_PROXY": "127.0.0.1,localhost"} {
			t.Setenv(key, value)
		}
	}
}
func exerciseOrdinaryGuard(t *testing.T, ctx context.Context, mode string, c *Client, w Caller, key jobref.Ref, secret string, requests []map[string]any, redirects int) {
	t.Helper()
	name := strings.TrimPrefix(mode, "goal-guard-")
	session, err := c.Get(ctx, w, key.GoalID)
	if err != nil || session.Current == nil {
		t.Fatal("safe prompt unavailable", err)
	}
	var request PromptRequest
	if decode(*session.Current.Form.Request, &request) != nil {
		t.Fatal("invalid form")
	}
	success := name == "retry" || name == "injection"
	if (request.Assistant.Error == "") != success {
		t.Fatalf("unexpected safe outcome %s: %+v", name, request.Assistant)
	}
	zero := name == "untrusted-tls" || name == "empty-endpoint" || name == "empty-model" || name == "empty-provider" || name == "invalid-provider" || name == "prefixed-model" || name == "missing-secret" || name == "empty-secret" || name == "ambient"
	if zero && len(requests) != 0 {
		t.Fatal("invalid binding reached provider", name, len(requests))
	}
	if !zero && (len(requests) < 1 || len(requests) > 4) {
		t.Fatal("unbounded/missing provider exchange", name, len(requests))
	}
	if name == "retry" && len(requests) != 2 {
		t.Fatal("retry fixture not exercised", len(requests))
	}
	if name == "injection" {
		if len(requests) != 1 || requests[0]["model"] != "fixture" || !strings.Contains(string(mustJSON(requests[0])), "https://unselected.invalid") {
			t.Fatal("untrusted source changed bindings or was omitted")
		}
	}
	if redirects != 0 {
		t.Fatal("followed provider redirect", redirects)
	}
	for _, e := range session.Replay.Entries {
		if !success && len(e.Artifacts) > 0 {
			t.Fatal("invalid response materialized artifacts")
		}
	}
	if len(request.Assistant.Error) > 256 {
		t.Fatal("unbounded safe error")
	}
	audit := auditOrdinaryRetention(t, ctx, c, w, key.JobID, secret)
	for _, value := range []any{session, request, requests, audit} {
		if strings.Contains(string(mustJSON(value)), secret) {
			t.Fatal("synthetic credential exported")
		}
	}
	// The child must leave the trusted host environment exactly as it found it.
	if name == "ambient" && (os.Getenv("OPENAI_API_KEY") != secret || os.Getenv("OPENAI_BASE_URL") == "") {
		t.Fatal("provider changed host environment")
	}
	writeLifecycleCase(t, "guard-"+name, map[string]any{"mode": name, "requests": requests, "provider_requests": len(requests), "redirect_target_requests": redirects, "assistant": request.Assistant, "history": session.Replay, "secret_audit": audit, "fixture_boundary": "Real v4 recipe, resolved helper and installed immutable inference. Only HTTP provider transport is a fixture; invalid binding cases deliberately bypass trusted facade configuration to verify declared includes."})
	if _, err = c.Close(ctx, w, key.JobID); err != nil {
		t.Fatal(err)
	}
}

// Inspect all retained inline history and every referenced task IO artifact.
// Publish locations/counts only. Private runtime-managed bytes are never exported.
func auditOrdinaryRetention(t *testing.T, ctx context.Context, c *Client, w Caller, job, secret string) map[string]any {
	t.Helper()
	run, err := c.host.History.GetJobRun(ctx, jobdb.GetJobRunRequest{JobKey: jobdb.JobKey{TenantId: w.TenantID, JobId: job}, IncludeInputs: true, IncludeOutputs: true, IncludeArtifacts: true, IncludeAttemptInputs: true})
	if err != nil {
		t.Fatal(err)
	}
	hits := []string{}
	if strings.Contains(string(mustJSON(run)), secret) {
		hits = append(hits, "inline history")
	}
	artifacts := 0
	scanned := map[string]bool{}
	var inspect func(string, *jobdb.TaskIO)
	inspect = func(location string, data *jobdb.TaskIO) {
		if data == nil {
			return
		}
		if strings.Contains(string(data.Data), secret) {
			hits = append(hits, location)
		}
		for _, a := range data.Artifacts {
			if a.Key == nil {
				t.Fatal("uninspectable retained IO artifact", location, a.Name)
			}
			id := string(mustJSON(a.Key))
			if scanned[id] {
				continue
			}
			scanned[id] = true
			art := c.host.Control.GetArtifactLazy(ctx, w.TenantID, *a.Key)
			if art == nil {
				t.Fatal("missing audit artifact", location, a.Name)
			}
			reader, e := art.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(reader)
			closeErr := reader.Close()
			if e != nil || closeErr != nil {
				t.Fatal(e, closeErr)
			}
			artifacts++
			if strings.Contains(string(b), secret) {
				hits = append(hits, location+"/"+a.Name)
			}
		}
	}
	inspect("start/input", run.Start.Input)
	for _, attempt := range run.Attempts {
		inspect("job/output", attempt.Output)
		for _, task := range attempt.Tasks {
			for _, a := range task.Attempts {
				inspect(task.TaskRunID+"/input", a.Input)
				inspect(task.TaskRunID+"/output", a.Output)
			}
		}
	}
	// Audit the story/diagnostic projection as a separate consumer surface.
	return map[string]any{"inline_history_contains_secret": strings.Contains(string(mustJSON(run)), secret), "private_retention_locations": hits, "io_artifacts_scanned": artifacts, "raw_history_published": false, "authorization_headers_published": false, "scope": "Complete retained run at this boundary, including attempt inputs, outputs and each IO artifact. Independent provider stores, deleted temporary process files and operator logs outside this runtime are not included."}
}
