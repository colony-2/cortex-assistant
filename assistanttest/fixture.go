package assistanttest

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/colony-2/c2j/pkg/gitprofile"
	"github.com/colony-2/c2j/pkg/recipejob"
	assets "github.com/colony-2/cortex-assistant"
	"github.com/colony-2/cortex-assistant/assistant"
)

//go:embed assets/*
var fixtureAssets embed.FS

type FixtureOptions struct {
	// SourceCommit is the exact production source identity C. SourceDirectory, if
	// set, must contain C with the same shipped bytes; dirty worktrees are rejected.
	SourceCommit     string
	SourceDirectory  string
	ScratchDirectory string
	Scenario         Scenario
}
type Fixture struct {
	ProductionCommit string
	// SourceVerified reports that SourceDirectory was checked at ProductionCommit.
	SourceVerified bool
	Commit         string
	Directory      string
	Selector       string
	// Profile is installed only into the caller's disposable tenant configuration.
	Profile gitprofile.Profile
	close   func() error
}

func (f Fixture) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}
func (f Fixture) Config(root recipejob.ResolvedTarget, cursorKey []byte) assistant.Config {
	return assistant.Config{Root: root, Protocol: assistant.GoalProtocol, Recipe: f.Selector, RecipeCommit: f.Commit, CursorKey: cursorKey}
}
func ProductionConfig(commit string, root recipejob.ResolvedTarget, cursorKey []byte) assistant.Config {
	return assistant.Config{Root: root, Protocol: assistant.GoalProtocol, Recipe: "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit, RecipeCommit: commit, CursorKey: cursorKey}
}
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	var stderr bytes.Buffer
	c.Stderr = &stderr
	b, e := c.Output()
	if e != nil {
		return nil, fmt.Errorf("fixture git %s: %w: %s", args[0], e, stderr.String())
	}
	return b, nil
}
func productionFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(assets.Production, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, e := assets.Production.ReadFile(path)
		if e == nil {
			files[path] = b
		}
		return e
	})
	return files, err
}

// PrepareFixture validates the shipped contract before creating F. Only the
// single inference selector and declared stub assets differ from production.
// The caller owns this disposable Git transport until explicitly transferring it.
func PrepareFixture(ctx context.Context, opts FixtureOptions) (Fixture, error) {
	var f Fixture
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(opts.SourceCommit) {
		return f, errors.New("immutable production commit required")
	}
	if err := opts.Scenario.validate(); err != nil {
		return f, err
	}
	files, err := productionFiles()
	if err != nil {
		return f, err
	}
	if opts.SourceDirectory != "" {
		status, e := git(ctx, opts.SourceDirectory, "status", "--porcelain")
		if e != nil {
			return f, e
		}
		if len(status) != 0 {
			return f, errors.New("source checkout is dirty")
		}
		head, e := git(ctx, opts.SourceDirectory, "rev-parse", "HEAD")
		if e != nil || strings.TrimSpace(string(head)) != opts.SourceCommit {
			return f, errors.New("source commit mismatch")
		}
		for name, want := range files {
			got, e := git(ctx, opts.SourceDirectory, "show", opts.SourceCommit+":"+name)
			if e != nil {
				return f, e
			}
			if !bytes.Equal(got, want) {
				return f, fmt.Errorf("production source drift: %s", name)
			}
		}
	}
	inference := string(files["inference.yaml"])
	if strings.Count(inference, assistant.OrdinaryModelOperation) != 1 {
		return f, errors.New("production inference selector drift")
	}
	directory, err := os.MkdirTemp(opts.ScratchDirectory, "assistanttest-")
	if err != nil {
		return f, err
	}
	failed := true
	defer func() {
		if failed {
			os.RemoveAll(directory)
		}
	}()
	files["inference.yaml"] = []byte(strings.Replace(inference, assistant.OrdinaryModelOperation, "./ops/assistanttest-inference", 1))
	manifestBytes, _ := fixtureAssets.ReadFile("assets/inference-manifest.json")
	var manifest map[string]any
	if json.Unmarshal(manifestBytes, &manifest) != nil {
		return f, errors.New("invalid pinned inference manifest")
	}
	manifest["command"] = []string{"go", "run", "main.go"}
	delete(manifest, "env")
	manifest["dependencies"] = []string{"nix:github:NixOS/nixpkgs/e7439b6b14ad3cc35d05608ebca9bce01a25f5f8#go"}
	files["ops/assistanttest-inference/op.yaml"], err = json.Marshal(manifest)
	if err != nil {
		return f, err
	}
	files["ops/assistanttest-inference/main.go"], _ = fixtureAssets.ReadFile("assets/stub.go.txt")
	replies := []map[string]string{}
	for _, r := range opts.Scenario.Replies {
		replies = append(replies, map[string]string{"user": r.User, "response": r.Response, "model": modelID(r.User)})
	}
	files["ops/assistanttest-inference/scenario.json"], err = json.Marshal(map[string]any{"replies": replies})
	if err != nil {
		return f, err
	}
	for name, b := range files {
		path := filepath.Join(directory, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return f, err
		}
		if err = os.WriteFile(path, b, 0600); err != nil {
			return f, err
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Assistant fixture"}, {"config", "user.email", "assistant-fixture@example.invalid"}, {"add", "."}, {"commit", "-qm", "checked assistant inference fixture"}} {
		if _, err = git(ctx, directory, args...); err != nil {
			return f, err
		}
	}
	head, err := git(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return f, err
	}
	f = Fixture{ProductionCommit: opts.SourceCommit, SourceVerified: opts.SourceDirectory != "", Commit: strings.TrimSpace(string(head)), Directory: directory}
	if f.Commit == f.ProductionCommit {
		return Fixture{}, errors.New("fixture and production identities must differ")
	}
	if err = CheckFixture(directory); err != nil {
		return Fixture{}, err
	}
	executable, err := exec.LookPath("git")
	if err != nil {
		return Fixture{}, err
	}
	server := httptest.NewServer(&cgi.Handler{Path: executable, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + directory, "GIT_HTTP_EXPORT_ALL=1"}})
	f.Selector = "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + f.Commit
	f.Profile = gitprofile.Profile{Version: 1, Entries: []gitprofile.Entry{{URL: server.URL + "/.git", RewriteFrom: []string{"https://github.com/colony-2/cortex-assistant.git"}}}}
	var once sync.Once
	var closeErr error
	f.close = func() error { once.Do(func() { server.Close(); closeErr = os.RemoveAll(directory) }); return closeErr }
	failed = false
	return f, nil
}

// CheckFixture rejects every production byte change other than the one declared
// leaf replacement. It also checks schemas, dependencies and the exact stub.
func CheckFixture(directory string) error {
	files, err := productionFiles()
	if err != nil {
		return err
	}
	files["inference.yaml"] = []byte(strings.Replace(string(files["inference.yaml"]), assistant.OrdinaryModelOperation, "./ops/assistanttest-inference", 1))
	for name, want := range files {
		got, e := os.ReadFile(filepath.Join(directory, name))
		if e != nil {
			return e
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("fixture production drift: %s", name)
		}
	}
	wantStub, _ := fixtureAssets.ReadFile("assets/stub.go.txt")
	got, err := os.ReadFile(filepath.Join(directory, "ops/assistanttest-inference/main.go"))
	if err != nil || !bytes.Equal(got, wantStub) {
		return errors.New("fixture stub drift")
	}
	actual, err := os.ReadFile(filepath.Join(directory, "ops/assistanttest-inference/op.yaml"))
	if err != nil {
		return err
	}
	var a, p map[string]any
	expected, _ := fixtureAssets.ReadFile("assets/inference-manifest.json")
	if json.Unmarshal(actual, &a) != nil || json.Unmarshal(expected, &p) != nil {
		return errors.New("invalid fixture manifest")
	}
	p["command"] = []string{"go", "run", "main.go"}
	delete(p, "env")
	p["dependencies"] = []string{"nix:github:NixOS/nixpkgs/e7439b6b14ad3cc35d05608ebca9bce01a25f5f8#go"}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(p)
	if !bytes.Equal(x, y) {
		return errors.New("fixture operation schema/dependency drift")
	}
	scenarioBytes, err := os.ReadFile(filepath.Join(directory, "ops/assistanttest-inference/scenario.json"))
	if err != nil {
		return err
	}
	var stored struct {
		Replies []struct {
			User     string `json:"user"`
			Response string `json:"response"`
			Model    string `json:"model"`
		} `json:"replies"`
	}
	decoder := json.NewDecoder(bytes.NewReader(scenarioBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&stored) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid fixture scenario")
	}
	scenario := Scenario{}
	for _, r := range stored.Replies {
		if r.Model != modelID(r.User) {
			return errors.New("fixture scenario model identity drift")
		}
		scenario.Replies = append(scenario.Replies, Reply{User: r.User, Response: r.Response})
	}
	if err = scenario.validate(); err != nil {
		return err
	}
	allowed := map[string]bool{"ops/assistanttest-inference/main.go": true, "ops/assistanttest-inference/op.yaml": true, "ops/assistanttest-inference/scenario.json": true}
	for name := range files {
		allowed[name] = true
	}
	var unexpected []string
	err = filepath.WalkDir(directory, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, e := filepath.Rel(directory, path)
		if e != nil {
			return e
		}
		if !allowed[filepath.ToSlash(rel)] {
			unexpected = append(unexpected, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return fmt.Errorf("undeclared fixture assets: %v", unexpected)
	}
	return nil
}
