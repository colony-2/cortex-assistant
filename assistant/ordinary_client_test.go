package assistant

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/secrets"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func TestOrdinaryFacadeSendNormalizationAndUnsupportedFiles(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	for _, response := range []any{
		Command{Command: "send", Prompt: "hello"},
		map[string]any{"command": "send", "prompt": "hello"},
		map[string]any{"command": "send", "prompt": "hello", "attachments": nil},
		map[string]any{"command": "send", "prompt": "hello", "attachments": map[string]artifacts.Ref(nil)},
	} {
		before := string(mustJSON(response))
		out, _, err := c.freezeSubmission(ctx, human, input.StructuredSubmission{Response: response}, nil)
		if err != nil || !strings.Contains(string(mustJSON(out.Response)), `"attachments":{}`) {
			t.Fatalf("send must satisfy actual input schema: %s %v", mustJSON(out.Response), err)
		}
		if string(mustJSON(response)) != before {
			t.Fatal("caller-owned frozen submission mutated")
		}
	}
	for _, submission := range []input.StructuredSubmission{
		{Response: Command{Command: "send", Prompt: "hello", Attachments: map[string]artifacts.Ref{"x.txt": {}}}},
		{Response: map[string]any{"command": "send", "prompt": "hello", "attachments": map[string]any{"x.txt": jobdb.NewArtifactFromBytes("x.txt", []byte("bytes"))}}},
		{Response: map[string]any{"command": "send", "prompt": "hello", "attachments": map[string]artifacts.Ref{"x.txt": {}}}},
		{Response: Command{Command: "send", Prompt: "hello"}, ArtifactRefs: map[string]artifacts.Ref{"x.txt": {}}},
	} {
		if _, _, err := c.freezeSubmission(ctx, human, submission, nil); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unfinished ordinary attachment route accepted: %v", err)
		}
	}
}

func ordinaryFacade(t *testing.T) (*Client, *fixtureStory, *fixtureSubmitter) {
	t.Helper()
	old, stories, _, sub := client(t)
	sub.preserveDestination = true
	cfg := old.cfg
	cfg.Protocol = OrdinaryProtocol
	cfg.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion.yaml@" + commit
	cfg.Bindings = &OrdinaryBindings{BaseURL: "https://provider.example.invalid/exact/root", APIKey: secrets.Reference{Name: "provider_key"}, Provider: "openai", Model: "fixture"}
	cfg.Limits = Limits{}
	cfg.LegacyReleases = []ReleaseDescriptor{{Recipe: selector, Commit: commit, Protocol: Protocol}}
	c, err := New(old.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, stories, sub
}
func TestOrdinaryFacadeIdentityAndFrozenStart(t *testing.T) {
	c, _, sub := ordinaryFacade(t)
	if c.cfg.Limits != OrdinaryLimits() {
		t.Fatalf("ordinary caps changed: %+v", c.cfg.Limits)
	}
	release, ok := c.release(c.cfg.Recipe)
	if !ok || release.Protocol != OrdinaryProtocol || release.Reopen {
		t.Fatal("unverified reopen advertised", release)
	}
	s := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(100, 0), Documents: []SnapshotDocument{}}
	in := StartInput{RequestID: "frozen", SubmittedAt: time.Unix(100, 0), Prompt: "hello", Source: s.Source}
	started, err := c.StartFrozen(ctx, human, in, s, snapshotSelection(s))
	if err != nil || started.ID != "assistant-"+identity(human.TenantID, in.RequestID) {
		t.Fatal(started, err)
	}
	if len(sub.jobs) != 1 || !strings.Contains(string(mustJSON(sub.jobs)), OrdinaryProtocol) || !strings.Contains(string(mustJSON(sub.jobs)), "provider_key") {
		t.Fatal("declared opaque bindings missing")
	}
	if _, err = c.Start(ctx, human, in); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy Start reinterpreted for v3", err)
	}
	selected := snapshotSelection(s)
	selected.TenantID = "foreign"
	if _, err = c.StartFrozen(ctx, human, in, s, selected); !errors.Is(err, ErrForbidden) {
		t.Fatal("foreign selection accepted", err)
	}
	if len(sub.jobs) != 1 {
		t.Fatal("invalid start submitted")
	}
	for _, change := range []func(*Config){
		func(cfg *Config) { cfg.Protocol = Protocol },
		func(cfg *Config) { cfg.Recipe = selector },
		func(cfg *Config) { b := *cfg.Bindings; b.BaseURL = "http://provider.invalid"; cfg.Bindings = &b },
		func(cfg *Config) { b := *cfg.Bindings; b.APIKey = secrets.Reference{}; cfg.Bindings = &b },
		func(cfg *Config) { b := *cfg.Bindings; b.Model = "provider:model"; cfg.Bindings = &b },
		func(cfg *Config) { cfg.Limits.Human++ },
	} {
		cfg := c.cfg
		change(&cfg)
		if _, err := New(c.host, cfg); err == nil {
			t.Fatal("invalid route/binding/cap accepted")
		}
	}
}
func TestOrdinaryFacadeProjectsActualFormatAndLegacy(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	frozen := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(100, 0), Documents: []SnapshotDocument{}}
	r := fixture(c)
	r.Root.Input = map[string]any{"protocol": OrdinaryProtocol, "session_id": "job", "prompt": "hello", "snapshot": frozen, "base_url": c.cfg.Bindings.BaseURL, "api_key": c.cfg.Bindings.APIKey, "provider": "openai", "model": "fixture"}
	capture := node("capture", 1, OrdinaryResult{Protocol: OrdinaryProtocol, Kind: "capture", Origin: Locator{JobID: "job", Invocation: "inv1", Path: strings.Split(ordinaryResultPath("capture"), "/")}, SessionID: "job", Prompt: "hello", Snapshot: &frozen})
	capture.OpID = "./ops/discussion"
	response := node("response", 2, OrdinaryResult{Protocol: OrdinaryProtocol, Kind: "response", Origin: Locator{JobID: "job", Invocation: "inv2", Path: strings.Split(ordinaryResultPath("response"), "/")}, Response: &Response{Text: "actual v3 reply", Citations: []string{}}})
	response.OpID = "./ops/discussion"
	r.Root.Children = []*story.JobRunStoryNode{capture, response, inputNode(3, Command{Command: "finish"})}
	d, err := c.project(r)
	if err != nil || d.Snapshot == nil || len(d.Entries) != 3 || d.Entries[1].Text != "actual v3 reply" {
		t.Fatalf("projection: %+v %v", d, err)
	}
	frozen.Source = Source{Kind: "story", JobID: "substitution"}
	if _, err = c.project(r); err == nil {
		t.Fatal("capture replaced frozen start")
	}
	old := fixture(c)
	old.Recipe.Source.SubmittedSelector = selector
	old.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "frozen v2 reply")}
	d, err = c.project(old)
	if err != nil || len(d.Entries) != 2 || d.Entries[1].Text != "frozen v2 reply" {
		t.Fatal("legacy read changed", err)
	}
}
