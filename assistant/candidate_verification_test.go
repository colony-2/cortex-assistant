package assistant

// Candidate-mode helpers are enabled by the mandatory repository hook. Ordinary
// source-only tests retain their existing fixture boundary outside that hook.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/gitprofile"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/c2j/pkg/jobref"
	"github.com/colony-2/jobdb/pkg/jobdb"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
)

func sourceFixtureCommit(fallback string) string {
	if candidate := os.Getenv("ASSISTANT_VERIFY_CANDIDATE"); candidate != "" {
		return candidate
	}
	return fallback
}

func candidateGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("candidate git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func prepareCandidateCase(t *testing.T, candidate, checkout string) {
	t.Helper()
	if len(candidate) != 40 || checkout == "" || candidateGit(t, checkout, "rev-parse", "HEAD") != candidate {
		t.Fatal("candidate checkout identity mismatch")
	}
	if candidateGit(t, checkout, "status", "--porcelain") != "" {
		t.Fatal("candidate checkout is not clean")
	}
	root := os.Getenv("ASSISTANT_VERIFY_EVIDENCE")
	if root == "" {
		t.Fatal("candidate evidence directory is required")
	}
	dir := filepath.Join(root, filepath.FromSlash(t.Name()))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ASSISTANT_LIFECYCLE_EVIDENCE", "ASSISTANT_GOAL_EVIDENCE", "ASSISTANT_ORDINARY_WORKER_EVIDENCE"} {
		t.Setenv(name, dir)
	}
	t.Cleanup(func() {
		if candidateGit(t, checkout, "status", "--porcelain") != "" {
			t.Error("candidate source mutated")
		}
	})
}

func configureCandidateGit(t *testing.T, admin jobdb.ConfigurationAdmin, candidate, checkout string) {
	t.Helper()
	mirror := os.Getenv("ASSISTANT_VERIFY_MIRROR")
	if mirror == "" || candidateGit(t, mirror, "rev-parse", candidate+"^{tree}") != candidateGit(t, checkout, "rev-parse", "HEAD^{tree}") {
		t.Fatal("candidate mirror tree mismatch")
	}
	export := t.TempDir()
	if err := os.Symlink(mirror, filepath.Join(export, "candidate.git")); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(&cgi.Handler{Path: git, Args: []string{"http-backend"}, Dir: export, Env: []string{"GIT_PROJECT_ROOT=" + export, "GIT_HTTP_EXPORT_ALL=1"}})
	t.Cleanup(server.Close)
	ctx := celltest.Context(context.Background())
	config, err := admin.GetTenantConfiguration(ctx, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	var profile gitprofile.Profile
	if err := json.Unmarshal(config.Values[gitprofile.ProfilePrefix+"fixture"], &profile); err != nil {
		t.Fatal(err)
	}
	profile.Entries = append([]gitprofile.Entry{{URL: server.URL + "/candidate.git", RewriteFrom: []string{"https://github.com/colony-2/cortex-assistant.git"}}}, profile.Entries...)
	if _, err := admin.SetTenantConfigurationValue(ctx, jobdb.SetTenantConfigurationValueRequest{TenantID: "tenant", Name: gitprofile.ProfilePrefix + "fixture", Value: mustJSON(profile)}); err != nil {
		t.Fatal(err)
	}
}

func configureCandidateTLS(t *testing.T, server *httptest.Server, mode string) {
	t.Helper()
	cert := server.Certificate()
	if mode == "goal-guard-untrusted-tls" {
		// httptest uses a shared static certificate: generate a different authority
		// with openssl so the negative case cannot accidentally trust the server.
		file := filepath.Join(t.TempDir(), "untrusted.pem")
		key := filepath.Join(t.TempDir(), "private.pem")
		cmd := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=unrelated-fixture", "-keyout", key, "-out", file)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture CA: %v %s", err, output)
		}
		t.Setenv("NIX_SSL_CERT_FILE", file)
		return
	}
	file := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIX_SSL_CERT_FILE", file)
}

func verifyInferenceBytes(t *testing.T, store string, evidence map[string]any) {
	t.Helper()
	expected := map[string]string{
		"bin/pydantic":      "989549dce9440bd3068a38bd24e7ac8c99aa27cedeb97a2bc012f37c86ab42da",
		"libexec/main.py":   "6d8bee84fc9f17fb1daf42d89cfd842df31b0a1ea0704dd09efff4819134b989",
		"share/c2j/op.json": "c5b4d3ce5c44208e3df7bc8e5f4721bf3eb096604b76d0a6507608070275e082",
	}
	check := func() map[string]string {
		actual := map[string]string{}
		for name, hash := range expected {
			data, err := os.ReadFile(filepath.Join(store, name))
			if err != nil {
				t.Fatal(err)
			}
			actual[name] = fmt.Sprintf("%x", sha256.Sum256(data))
			if actual[name] != hash {
				t.Fatalf("pinned inference bytes changed: %s", name)
			}
		}
		return actual
	}
	evidence["sha256_before"] = check()
	t.Cleanup(func() { evidence["sha256_after"] = check() })
}

func exportCandidateCase(t *testing.T, mode, candidate string, key jobref.Ref, rt jobdb.WorkflowRuntime, pkg map[string]any, requests func() []map[string]any, secret string) {
	t.Helper()
	api := &jobinspect.Service{Runtime: rt, Tenant: "tenant"}
	inspection, err := api.Inspect(celltest.Context(context.Background()), jobinspect.Request{Operation: "log", JobID: key.JobID, Limit: 100})
	if err != nil {
		t.Error(err)
		return
	}
	directory := filepath.Join(os.Getenv("ASSISTANT_VERIFY_EVIDENCE"), filepath.FromSlash(t.Name()))
	sources := []any{}
	for _, task := range inspection.Tasks {
		if !strings.Contains(task.Type, "source_resolve") {
			continue
		}
		field := ""
		value, err := api.Inspect(celltest.Context(context.Background()), jobinspect.Request{Operation: "log", JobID: key.JobID, Task: task.ID, Attempt: task.Attempt, Field: &field})
		if err != nil {
			t.Error(err)
			continue
		}
		var source map[string]any
		if err = json.Unmarshal(value.Value, &source); err != nil {
			t.Error(err)
			continue
		}
		if selector, _ := source["submitted_selector"].(string); strings.Contains(selector, "github.com/colony-2/cortex-assistant.git") {
			if source["resolved_commit"] != candidate {
				t.Error("ordinary root resolved wrong candidate", source)
			}

			name, _ := source["recipe_yaml_artifact"].(string)
			reader, e := api.Download(celltest.Context(context.Background()), jobinspect.Request{JobID: key.JobID, Task: task.ID, Attempt: task.Attempt, Name: name})
			if e != nil {
				t.Error(e)
				continue
			}
			body, e := io.ReadAll(reader)
			reader.Close()
			if e != nil || strings.Contains(string(body), secret) {
				t.Error("invalid resolved recipe artifact", e)
				continue
			}
			if !strings.Contains(string(body), OrdinaryModelOperation) || !strings.Contains(string(body), candidate) {
				t.Error("resolved recipe lacks pinned inference/helper identity")
			}
			if e = os.WriteFile(filepath.Join(directory, "resolved-recipe.yaml"), body, 0600); e != nil {
				t.Error(e)
			}
			sources = append(sources, map[string]any{"task": task.ID, "attempt": task.Attempt, "source": source, "recipe_sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "recipe_artifact": "resolved-recipe.yaml"})
		}
	}
	if len(sources) == 0 {
		t.Error("missing actual canonical candidate resolution task")
	}
	// Cleanup computes after-hashes later; compute them here as well for durable export.
	if store, ok := pkg["store_path"].(string); ok {
		verifyInferenceBytes(t, store, pkg)
		pkg["sha256_after"] = pkg["sha256_before"]
	}
	outcome := "passed"
	if t.Failed() {
		outcome = "failed"
	}
	evidence := map[string]any{"test": t.Name(), "mode": mode, "outcome": outcome, "candidate": candidate, "tree": candidateGit(t, os.Getenv("ASSISTANT_VERIFY_CHECKOUT"), "rev-parse", "HEAD^{tree}"), "canonical_sources": sources, "tasks": inspection.Tasks, "requests": requests(), "package": pkg, "transport": "canonical assistant HTTPS URL mapped by supported tenant Git profile to exact-candidate local HTTP mirror; subprocess-only NIX_SSL_CERT_FILE HTTPS provider CA; no public publication claim", "initial_job": key}
	data := mustJSON(evidence)
	if strings.Contains(string(data), secret) {
		t.Error("synthetic credential in candidate evidence")
		return
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.json"), data, 0600); err != nil {
		t.Error(err)
	}
}

// Preserve the original source/task observation before intentional chapter/blob
// deletion. The after-loss behavior has its own actual-retention-loss artifact.
func finalizeCandidateRetentionCase(t *testing.T, candidate string, pkg map[string]any, secret string) {
	t.Helper()
	path := filepath.Join(os.Getenv("ASSISTANT_VERIFY_EVIDENCE"), filepath.FromSlash(t.Name()), "candidate.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Error("missing before-loss source evidence", err)
		return
	}
	var evidence map[string]any
	if json.Unmarshal(data, &evidence) != nil || evidence["candidate"] != candidate || evidence["test"] != t.Name() {
		t.Error("invalid before-loss source evidence")
		return
	}
	verifyInferenceBytes(t, pkg["store_path"].(string), pkg)
	pkg["sha256_after"] = pkg["sha256_before"]
	evidence["package"] = pkg
	evidence["inspection_phase"] = "source/task inspection before intentional retention loss; after-loss outcomes in actual-retention-loss.json"
	evidence["outcome"] = "passed"
	if t.Failed() {
		evidence["outcome"] = "failed"
	}
	data = mustJSON(evidence)
	if strings.Contains(string(data), secret) {
		t.Error("synthetic credential in retention evidence")
		return
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Error(err)
	}
}

// Dependency contract supplement: ordinary cases above exercise the same
// immutable package in durable jobs; these overlapping processes make isolation
// and the shared retry/repair deadline directly observable without live keys.
func TestPinnedInferenceHTTPSConcurrencyAndDeadline(t *testing.T) {
	candidate := os.Getenv("ASSISTANT_VERIFY_CANDIDATE")
	if candidate != "" {
		prepareCandidateCase(t, candidate, os.Getenv("ASSISTANT_VERIFY_CHECKOUT"))
	}
	resolve := exec.Command("nix", "--extra-experimental-features", "nix-command flakes", "build", "--no-link", "--no-write-lock-file", "--print-out-paths", strings.TrimPrefix(OrdinaryModelOperation, "nix:"))
	storeBytes, err := resolve.Output()
	if err != nil {
		t.Fatal("resolve immutable inference", err)
	}
	store := strings.TrimSpace(string(storeBytes))
	pkg := map[string]any{"store_path": store, "selector": OrdinaryModelOperation}
	verifyInferenceBytes(t, store, pkg)
	arrived, release := make(chan struct{}, 2), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	var mu sync.Mutex
	requests := []map[string]any{}
	deadlineCalls := 0
	var deadlineStarted time.Time
	keys := map[string]string{"first": "private-first-fixture-key", "second": "private-second-fixture-key", "deadline": "private-deadline-fixture-key"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/chat/completions")
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || keys[name] == "" || r.Header.Get("Authorization") != "Bearer "+keys[name] || body["model"] != name {
			t.Error("provider binding isolation failed")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		requests = append(requests, map[string]any{"path": r.URL.Path, "body": body})
		call := 0
		if name == "deadline" {
			deadlineCalls++
			call = deadlineCalls
			if deadlineStarted.IsZero() {
				deadlineStarted = time.Now()
			}
		}
		mu.Unlock()
		if name != "deadline" {
			arrived <- struct{}{}
			<-release
		} else if call == 1 {
			w.WriteHeader(503) // SDK retry is inside the same invocation budget.
			return
		} else if call >= 3 {
			select {
			case <-r.Context().Done():
			case <-time.After(4 * time.Second):
			}
			return
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) == 0 {
			t.Error("missing actual structured schema")
			w.WriteHeader(400)
			return
		}
		function := tools[0].(map[string]any)["function"].(map[string]any)
		arguments := string(mustJSON(map[string]any{"answer": name}))
		if name == "deadline" {
			arguments = `{"wrong":true}` // Independent schema rejection triggers repair.
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "object": "chat.completion", "created": 1, "model": name, "choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "result", "type": "function", "function": map[string]any{"name": function["name"], "arguments": arguments}}}}}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	}))
	defer server.Close()
	configureCandidateTLS(t, server, "package-contract")
	type invocation struct {
		name           string
		stdout, stderr []byte
		err            error
	}
	run := func(name string, seconds int) invocation {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(store, "bin/pydantic"))
		cmd.Stdin = strings.NewReader(string(mustJSON(map[string]any{"api": map[string]any{"base_url": server.URL + "/" + name, "api_key": keys[name]}, "default_provider": "openai", "default_model": name, "prompt": "bound request", "response_schema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"answer"}, "properties": map[string]any{"answer": map[string]any{"type": "string"}}}, "invocation_timeout": seconds})))
		cmd.Env = append(os.Environ(), "OPENAI_API_KEY=ambient-fixture", "OPENAI_BASE_URL=https://ambient.invalid", "HTTPS_PROXY=http://127.0.0.1:1", "OPENAI_CUSTOM_HEADERS=X-Ambient: fixture")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		return invocation{name, stdout, stderr.Bytes(), err}
	}
	completed := make(chan invocation, 2)
	for _, name := range []string{"first", "second"} {
		go func(name string) { completed <- run(name, 10) }(name)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(30 * time.Second):
			releaseAll()
			t.Fatal("concurrent inference requests did not overlap")
		}
	}
	releaseAll()
	results := []map[string]any{}
	for i := 0; i < 2; i++ {
		result := <-completed
		if result.err != nil || len(result.stderr) != 0 {
			t.Fatal("isolated HTTPS inference failed", result.err)
		}
		var envelope struct {
			Output struct {
				Response string `json:"response"`
				Model    string `json:"model"`
			} `json:"output"`
		}
		if json.Unmarshal(result.stdout, &envelope) != nil || envelope.Output.Model != result.name || !strings.Contains(envelope.Output.Response, result.name) {
			t.Fatal("concurrent inference result crossed bindings")
		}
		results = append(results, map[string]any{"model": result.name, "response": envelope.Output.Response})
	}
	deadline := run("deadline", 2)
	mu.Lock()
	elapsed, calls := time.Since(deadlineStarted), deadlineCalls
	mu.Unlock()
	if deadline.err == nil || len(deadline.stdout) != 0 || strings.TrimSpace(string(deadline.stderr)) != "pydantic: timeout" || calls < 3 || elapsed > 3500*time.Millisecond {
		t.Fatalf("overall retry/repair deadline not enforced: calls=%d elapsed=%s error=%v", calls, elapsed, deadline.err)
	}
	verifyInferenceBytes(t, store, pkg)
	pkg["sha256_after"] = pkg["sha256_before"]
	evidence := map[string]any{"candidate": candidate, "package": pkg, "requests": requests, "results": results, "overlapping_clients": 2, "deadline_requests": calls, "deadline_from_first_request_ms": elapsed.Milliseconds(), "invocation_timeout_seconds": 2, "deadline_outcome": "timeout", "tls": "HTTPS verify=True, child-only NIX_SSL_CERT_FILE", "audit": "Authorization matched privately; headers, keys and process environment excluded"}
	encoded := string(mustJSON(evidence))
	for _, key := range keys {
		if strings.Contains(encoded, key) {
			t.Fatal("private fixture credential in export")
		}
	}
	writeLifecycleCase(t, "inference-contract", evidence)
}
