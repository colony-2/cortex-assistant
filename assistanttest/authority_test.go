package assistanttest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/recipetest"
	"github.com/colony-2/jobdb/pkg/jobdb/celltest"
)

// A forwarded value authorizes its declared destination, not a new lookup by
// foreign code. This uses the real production inference include and policy.
func TestNativeForeignSecretAuthority(t *testing.T) {
	ctx := celltest.Context(context.Background())
	for _, tc := range []struct{ name, binding, want string }{
		{"forwarded", "${{ inputs.api_key }}", ""},
		{"independent-lookup", "${{ context.secrets.codex_api_key }}", `input secret "codex_api_key" exceeds the enclosing limit: lease permission denied`},
		{"undeclared-forwarding", "${{ inputs.api_key }}", `input "api_key" is not marked secret`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			foreign := t.TempDir()
			files, err := productionFiles()
			if err != nil {
				t.Fatal(err)
			}
			// Source bytes are checked separately by PrepareFixture; copy the shipped
			// include unchanged, with a small authority wrapper in this foreign repo.
			for name, b := range files {
				p := filepath.Join(foreign, name)
				if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			secretDeclaration := "secret: true,"
			if tc.name == "undeclared-forwarding" {
				secretDeclaration = ""
			}
			wrapper := "id: foreign\nschema_version: 2\ninput_schema:\n  api_key: {type: string, " + secretDeclaration + " required: true}\ninputs:\n  api_key: '${{ inputs.api_key }}'\nsequence:\n - id: inference\n   include: inference.yaml\n   inputs:\n     base_url: https://foreign.invalid/exact/root\n     provider: openai\n     model: fixture\n     api_key: '" + tc.binding + "'\n     prompt: finite\n     response_schema: {type: object}\n"
			if err = os.WriteFile(filepath.Join(foreign, "foreign.yaml"), []byte(wrapper), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=Authority fixture", "-c", "user.email=authority@example.invalid", "commit", "-qm", "foreign include authority fixture"}} {
				if _, err = git(ctx, foreign, args...); err != nil {
					t.Fatal(err)
				}
			}
			commit, err := git(ctx, foreign, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			root := "id: parent\nschema_version: 2\nsequence:\n - id: foreign\n   include: git+file://" + foreign + "//foreign.yaml@" + strings.TrimSpace(string(commit)) + "\n   perms: {limit: {jobdb: {}}}\n   inputs:\n     api_key: '${{ context.secrets.codex_api_key }}'\n"
			target, _, err := recipetest.ExpandInlineRecipeTarget(ctx, recipetest.TargetRecipe{Mode: "inline_recipe", Format: "yaml", Content: root}, recipetest.InlineTargetExpansionOptions{ProjectID: "0", RootFile: filepath.Join(t.TempDir(), "parent.yaml")})
			if err != nil {
				if tc.want != "" && strings.Contains(err.Error(), tc.want) {
					return
				}
				t.Fatal(err)
			}
			fileSources := map[string]string{}
			for name := range files {
				fileSources[name] = name
			}
			c := recipetest.Case{ID: tc.name, Type: "integration_case", Runtime: &recipetest.RuntimeCase{Secrets: map[string]string{"codex_api_key": "synthetic-authority-only"}, Cells: map[string]recipetest.CellFixture{"root": {FileSources: fileSources}}}}
			if tc.want == "" {
				c.Mocks.Ops = []recipetest.OpMock{
					{Match: recipetest.OpMockMatch{NodePath: "parent/foreign/root/inference/cortex-assistant-inference/policy"}, Behavior: recipetest.MockBehavior{Mode: "passthrough"}},
					{Match: recipetest.OpMockMatch{NodePath: "parent/foreign/root/inference/cortex-assistant-inference/infer"}, Behavior: recipetest.MockBehavior{Mode: "return", Outputs: map[string]any{"response": "{}", "model": "fixture", "usage": map[string]any{}}}},
				}
			}
			result := recipetest.RunCase(ctx, recipetest.HarnessOptions{FixtureRoot: foreign, WorkRoot: t.TempDir(), Deps: coreops.NewServiceDepsBuilder().Build()}, "0", target, c, recipetest.ExecutionOptions{Timeout: "30s"})
			if tc.want == "" {
				if result.Status != "passed" {
					t.Fatalf("forwarding failed: %s %s", result.Status, result.FailureReason)
				}
			} else if result.Status != "failed" || !strings.Contains(result.FailureReason, tc.want) || strings.Contains(result.FailureReason, "no runtime fixture") {
				t.Fatalf("wrong denial: %s %s", result.Status, result.FailureReason)
			}
			b, _ := json.Marshal(result)
			if strings.Contains(string(b), "synthetic-authority-only") {
				t.Fatal("authority report exposed key")
			}
		})
	}
}
