package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/configuration"
)

func TestContextualBindingsAndFrozenRetry(t *testing.T) {
	c, _, _ := ordinaryFacade(t)
	cfg := c.cfg
	cfg.Protocol = GoalProtocol
	cfg.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + cfg.RecipeCommit
	cfg.Bindings = nil
	contextual, err := New(c.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := contextual.bindingInputs()
	if err != nil {
		t.Fatal(err)
	}
	for key, name := range map[string]string{"base_url": "codex_api_base_url", "model": "codex_model"} {
		r, ok, err := configuration.Parse(inputs[key])
		if err != nil || !ok || r.Kind != "config" || r.Name != name || r.ExpectedType != "string" || len(r.Default) != 0 {
			t.Fatal(key, r, err)
		}
	}
	if inputs["provider"] != "openai" {
		t.Fatal(inputs)
	}
	snapshot := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(100, 0), Documents: []SnapshotDocument{}}
	frozen, err := contextual.PrepareStartFrozen(ctx, human, StartInput{RequestID: "contextual", SubmittedAt: snapshot.At, Prompt: "hello", Source: snapshot.Source}, snapshot, snapshotSelection(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	wire := mustJSON(frozen)
	for _, want := range []string{"codex_api_base_url", "codex_model", "codex_api_key", "$c2j_ref"} {
		if !bytes.Contains(wire, []byte(want)) {
			t.Fatal("missing frozen reference", want)
		}
	}
	uncertain := &uncertainStartSubmitter{}
	contextual.host.Submitter = uncertain
	if _, err = contextual.SubmitFrozenStart(ctx, human, frozen); err == nil {
		t.Fatal("missing uncertain response")
	}
	replacement, err := New(contextual.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored FrozenStart
	if json.Unmarshal(wire, &restored) != nil {
		t.Fatal("wire")
	}
	if _, err = replacement.SubmitFrozenStart(ctx, human, restored); err != nil {
		t.Fatal(err)
	}
	if len(uncertain.requests) != 2 || !bytes.Equal(uncertain.requests[0], uncertain.requests[1]) {
		t.Fatal("contextual retry changed bytes")
	}
	for _, b := range []*OrdinaryBindings{{}, {BaseURL: "https://host.invalid", Provider: "openai", Model: "fixture"}} {
		bad := cfg
		bad.Bindings = b
		if _, err = New(c.host, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("partial binding", err)
		}
	}
	legacy := cfg
	legacy.Protocol = OrdinaryProtocol
	legacy.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion.yaml@" + cfg.RecipeCommit
	if _, err = New(c.host, legacy); !errors.Is(err, ErrInvalid) {
		t.Fatal("v3 acquired contextual behavior", err)
	}
	explicit := cfg
	explicit.Bindings = c.cfg.Bindings
	literal, err := New(c.host, explicit)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_BASE_URL", "https://conflicting.invalid")
	t.Setenv("OPENAI_MODEL", "conflict")
	got, _ := literal.bindingInputs()
	if got["base_url"] != explicit.Bindings.BaseURL || got["model"] != explicit.Bindings.Model {
		t.Fatal("explicit changed", got)
	}
}
func TestNonsecretInferencePolicyMatrix(t *testing.T) {
	validURL := "https://host.invalid/exact/%2froot/"
	validModel := strings.Repeat("m", 512)
	if ValidateInferenceBindings(validURL, "openai", validModel) != nil {
		t.Fatal("exact valid strings rejected")
	}
	for _, v := range []struct{ name, url, provider, model string }{
		{"http", "http://host.invalid", "openai", "m"}, {"nohost", "https:///root", "openai", "m"}, {"userinfo", "https://u:p@host.invalid", "openai", "m"}, {"query", "https://host.invalid?a=b", "openai", "m"}, {"emptyquery", "https://host.invalid?", "openai", "m"}, {"fragment", "https://host.invalid#x", "openai", "m"}, {"emptyfragment", "https://host.invalid#", "openai", "m"}, {"whitespace", "https://host.invalid/ x", "openai", "m"}, {"backslashurl", "https://host.invalid/\\x", "openai", "m"}, {"provider", validURL, "anthropic", "m"}, {"prefix", validURL, "openai", "openai:m"}, {"slash", validURL, "openai", "a/m"}, {"backslash", validURL, "openai", "a\\m"}, {"large", validURL, "openai", strings.Repeat("é", 257)}, {"control", validURL, "openai", "a\x7fb"}, {"empty", validURL, "openai", ""},
	} {
		t.Run(v.name, func(t *testing.T) {
			if !errors.Is(ValidateInferenceBindings(v.url, v.provider, v.model), ErrInvalid) {
				t.Fatal("policy accepted invalid bindings")
			}
		})
	}
}
