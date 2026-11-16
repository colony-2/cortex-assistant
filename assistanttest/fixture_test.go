package assistanttest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/cortex-assistant/assistant"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	files, err := productionFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		p := filepath.Join(dir, name)
		if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Production source fixture"}, {"config", "user.email", "source@example.invalid"}, {"add", "."}, {"commit", "-qm", "unpublished production byte fixture"}} {
		if _, err = git(context.Background(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	b, err := git(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(b))
}
func TestFixtureContractAndDrift(t *testing.T) {
	source, c := sourceFixture(t)
	s := Scenario{Replies: []Reply{{User: "open", Response: `{"text":"valid","documents":[],"return_artifact_ids":[],"citations":[]}`}}}
	f, err := PrepareFixture(context.Background(), FixtureOptions{SourceCommit: c, SourceDirectory: source, Scenario: s})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !f.SourceVerified || f.Commit == c || !strings.HasSuffix(f.Selector, "@"+f.Commit) {
		t.Fatal("dishonest fixture identity")
	}
	for _, name := range []string{"discussion-goal.yaml", "inference.yaml", "assistant/ordinary.go", "cmd/discussion-op/main.go", "cmd/bindings-op/main.go", "go.mod", "go.sum", "ops/discussion/op.yaml", "ops/bindings/op.yaml", "ops/assistanttest-inference/op.yaml", "ops/assistanttest-inference/main.go", "ops/assistanttest-inference/scenario.json"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(f.Directory, name)
			b, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(p, append(b, ' '), 0600); e != nil {
				t.Fatal(e)
			}
			if name == "ops/assistanttest-inference/scenario.json" {
				os.WriteFile(p, []byte(`{"replies":[]}`), 0600)
			}
			// JSON whitespace does not change the manifest contract; change a schema instead.
			if name == "ops/assistanttest-inference/op.yaml" {
				var m map[string]any
				json.Unmarshal(b, &m)
				m["input_schema"] = map[string]any{"type": "string"}
				changed, _ := json.Marshal(m)
				os.WriteFile(p, changed, 0600)
			}
			if e = CheckFixture(f.Directory); e == nil {
				t.Fatal("drift accepted")
			}
			os.WriteFile(p, b, 0600)
		})
	}
	if err = CheckFixture(f.Directory); err != nil {
		t.Fatal(err)
	}
	// Serving altered bytes as C is explicitly rejected before fixture startup.
	p := filepath.Join(source, "inference.yaml")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "#pydantic", "#other", 1)), 0600)
	if _, err = PrepareFixture(context.Background(), FixtureOptions{SourceCommit: c, SourceDirectory: source, Scenario: s}); err == nil {
		t.Fatal("modified source under C accepted")
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

// Candidate verification uses the complete clean managed commit C. Ordinary local
// execution uses an honestly labelled synthetic production-byte source commit.
func executionSource(t *testing.T) (string, string) {
	t.Helper()
	if c := os.Getenv("ASSISTANT_VERIFY_CANDIDATE"); c != "" {
		source := os.Getenv("ASSISTANT_VERIFY_CHECKOUT")
		if source == "" {
			t.Fatal("candidate source checkout missing")
		}
		return source, c
	}
	return sourceFixture(t)
}

func TestProductionDescriptorAndManifestIdentity(t *testing.T) {
	source, c := executionSource(t)
	cfg := ProductionConfig(c, recipejob.ResolvedTarget{CellName: "root", CellRepo: "github.com/colony-2/cortex-assistant", RepositorySource: "https://github.com/colony-2/cortex-assistant.git", DefaultRef: "main"}, []byte(strings.Repeat("k", 32)))
	if cfg.RecipeCommit != c || !strings.HasSuffix(cfg.Recipe, "//discussion-goal.yaml@"+c) || cfg.Protocol != assistant.GoalProtocol || cfg.Bindings != nil {
		t.Fatal("production descriptor changed identity")
	}
	files, err := productionFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := git(context.Background(), source, "show", c+":"+name)
		if err != nil || string(got) != string(want) {
			t.Fatal("production source bytes differ", name, err)
		}
	}
	if strings.Count(string(files["inference.yaml"]), assistant.OrdinaryModelOperation) != 1 || strings.Contains(string(files["inference.yaml"]), "assistanttest-inference") {
		t.Fatal("production inference substituted")
	}
	manifest, err := fixtureAssets.ReadFile("assets/inference-manifest.json")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(manifest)) != "c5b4d3ce5c44208e3df7bc8e5f4721bf3eb096604b76d0a6507608070275e082" {
		t.Fatal("pinned manifest differs from actual adapter")
	}
	t.Logf("production-byte source C=%s; immutable inference=%s", c, assistant.OrdinaryModelOperation)
}
