package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/story"
)

// Opt-in: credentials and endpoint are supplied by trusted host environment, not
// recorded recipe inputs. This runs the pinned extension with no interception.
func TestWorkerActualProvider(t *testing.T) {
	if os.Getenv("ASSISTANT_REAL_MODEL") == "" {
		t.Skip("set ASSISTANT_REAL_MODEL for configured provider acceptance")
	}
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "ASSISTANT_PROVIDER_EVIDENCE"} {
		if os.Getenv(key) == "" {
			t.Fatalf("real-provider run requires %s", key)
		}
	}
	runWorkerConversation(t, "provider", false)
}

func recordActualProviderEvidence(t *testing.T, ctx context.Context, c *Client, session Session, current *input.UserInputDetails, model, prompt string) {
	t.Helper()
	var request PromptRequest
	if current.Form.Request == nil || decode(*current.Form.Request, &request) != nil {
		t.Fatal("missing structured response")
	}
	response := request.Assistant
	page, err := c.Replay(ctx, human, session.ID, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}

	recorded, err := c.read(ctx, human, session.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var assembled map[string]any
	var generated ModelResult
	var inspect func(*story.JobRunStoryNode)
	inspect = func(n *story.JobRunStoryNode) {
		if n == nil {
			return
		}
		if len(n.Path) >= 4 && n.Path[1] == "infer" && n.Path[3] == "assemble" {
			var result TaskResult
			if decode(n.Output, &result) == nil && result.ModelInput != nil {
				assembled = result.ModelInput
			}
		}
		if taskRolePath(n.Path, "response") {
			var result TaskResult
			if decode(n.Output, &result) == nil && result.Response != nil {
				response = *result.Response
			}
		}
		if len(n.Path) >= 4 && n.Path[1] == "infer" && n.Path[3] == "infer" {
			var output ModelOutput
			if json.Unmarshal(mustJSON(n.Output), &output) == nil && output.Response != "" {
				_ = json.Unmarshal([]byte(output.Response), &generated)
			}
		}
		for _, child := range n.Children {
			inspect(child)
		}
	}
	inspect(recorded.Story.Root)
	if response.Error != "" || response.Text == "" || response.Model == "" || len(response.Usage) == 0 || len(response.Documents) != 1 {
		t.Fatalf("actual provider response incomplete: %+v", response)
	}

	if assembled == nil || len(generated.Documents) != 1 || recorded.Capture == nil {
		t.Fatalf("missing recorded provenance: request=%t documents=%d capture=%t", assembled != nil, len(generated.Documents), recorded.Capture != nil)
	}
	if !strings.Contains(string(mustJSON(assembled)), prompt) {
		t.Fatal("recorded model request lost initial text")
	}
	expectedDocument := generated.Documents[0]
	type download struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	}
	var downloads []download
	for _, entry := range page.Entries {
		if entry.Role != "assistant" {
			continue
		}
		for _, artifact := range entry.Artifacts {
			reader, err := c.OpenArtifact(ctx, human, session.ID, artifact.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if !strings.Contains(string(data), "candidate-provider-check-20261101") {
				t.Fatal("document lost source marker")
			}
			if artifact.Name != expectedDocument.Name || string(data) != expectedDocument.Text {
				t.Fatal("download differs from recorded generated document")
			}
			hash := sha256.Sum256(data)
			downloads = append(downloads, download{Name: artifact.Name, SHA256: fmtHash(hash), Bytes: len(data)})
		}
	}
	if len(downloads) != 1 {
		t.Fatalf("expected one downloaded document, got %d", len(downloads))
	}
	evidence := map[string]any{"timestamp": time.Now().UTC(), "provider": "openai", "requested_model": model, "model_operation": ModelOperation, "activity": ActivitySelector, "response": response, "source": session.Source, "capture": recorded.Capture, "model_request": assembled, "generated_document": expectedDocument, "prompt": prompt, "prompt_sha256": fmtHash(sha256.Sum256([]byte(prompt))), "replay": page, "downloads": downloads, "recipe_binding": map[string]any{"selector": selector, "commit": commit, "kind": "candidate-local resolver; not published release"}, "secret_binding": "codex_api_key", "endpoint_config": "codex_api_base_url", "helper_intercepted": false, "model_intercepted": false}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("ASSISTANT_PROVIDER_EVIDENCE"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func fmtHash(hash [32]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range hash {
		out[2*i], out[2*i+1] = digits[b>>4], digits[b&15]
	}
	return string(out)
}
