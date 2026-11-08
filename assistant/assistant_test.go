package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobref"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

var ctx = context.Background()
var human = Caller{"tenant", input.Actor{ID: "person", Kind: "human"}}

const commit = "1111111111111111111111111111111111111111"
const selector = "git+https://github.com/colony-2/cortex-assistant.git//session.yaml@" + commit

type fixtureStory struct {
	story.Service
	jobs            map[string]*story.JobRunStory
	rows            []story.WorkflowSummary
	restartRequests []story.RestartRecipeJobRequest
	requests        int
}

func (s *fixtureStory) GetJobRunStory(ctx context.Context, r story.GetJobRunStoryRequest) (*story.JobRunStory, error) {
	s.requests++
	d, ok := s.jobs[r.JobID]
	if !ok {
		return nil, story.ErrNotFound
	}
	return d, nil
}
func (s *fixtureStory) ListWorkflows(ctx context.Context, r story.ListWorkflowsRequest) ([]story.WorkflowSummary, error) {
	if r.Offset >= len(s.rows) {
		return nil, nil
	}
	end := r.Offset + r.Limit
	if end > len(s.rows) {
		end = len(s.rows)
	}
	return s.rows[r.Offset:end], nil
}
func (s *fixtureStory) GetRestartContext(ctx context.Context, r story.GetRestartContextRequest) (*story.RestartContext, error) {
	if r.ScopeID == "" {
		return nil, ErrUnavailable
	}
	return &story.RestartContext{ScopeID: r.ScopeID}, nil
}
func (s *fixtureStory) RestartRecipeJob(ctx context.Context, r story.RestartRecipeJobRequest) (*story.RestartRecipeJobResponse, error) {
	s.restartRequests = append(s.restartRequests, r)
	return &story.RestartRecipeJobResponse{JobID: "next", GoalID: "next-goal", TenantID: r.ProjectID}, nil
}

type fixtureControl struct {
	list        func(jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error)
	listCalls   int
	backendPage int
	runs        map[string]jobdb.GetJobRunResponse
	workflowctl.WorkflowControl
	story *fixtureStory
	files map[string][]byte
	reads int
}

func (c *fixtureControl) GetArtifactLazy(ctx context.Context, tenant string, key jobdb.ArtifactKey) jobdb.Artifact {
	c.reads++
	if b, ok := c.files[key.Name]; ok {
		return jobdb.NewArtifactFromBytes(key.Name, b)
	}
	return nil
}

type fixtureSubmitter struct{ jobs []jobdb.SubmitJob }

func (s *fixtureSubmitter) SubmitJobRef(ctx context.Context, j jobdb.SubmitJob) (jobref.Ref, error) {
	s.jobs = append(s.jobs, j)
	return jobref.Ref{TenantID: "tenant", JobID: "submitted", GoalID: "goal"}, nil
}
func client(t *testing.T) (*Client, *fixtureStory, *fixtureControl, *fixtureSubmitter) {
	t.Helper()
	s := &fixtureStory{jobs: map[string]*story.JobRunStory{}}
	ctl := &fixtureControl{story: s, files: map[string][]byte{}}
	sub := new(fixtureSubmitter)
	in, e := input.NewRuntime(ctl, nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := New(Host{Goals: ctl, History: ctl, Control: ctl, Story: s, Input: in, Submitter: sub, Authorize: func(ctx context.Context, w Caller, id string) error {
		if w.TenantID != "tenant" || strings.HasPrefix(id, "foreign") {
			return ErrForbidden
		}
		return nil
	}, ResolveCell: func(ctx context.Context, w Caller, n string) (recipejob.ResolvedTarget, error) {
		if n != "cell" && n != "https://github.com/example/cell.git" {
			return recipejob.ResolvedTarget{}, ErrForbidden
		}
		return recipejob.ResolvedTarget{CellName: "cell", RepositorySource: "https://github.com/example/cell.git", DefaultRef: "main"}, nil
	}}, Config{Root: recipejob.ResolvedTarget{CellName: "root", RepositorySource: "https://github.com/example/root.git", DefaultRef: "main"}, Recipe: selector, RecipeCommit: commit, CursorKey: []byte(strings.Repeat("secret", 8))})
	if e != nil {
		t.Fatal(e)
	}
	return c, s, ctl, sub
}
func fixture(c *Client) *story.JobRunStory {
	args := StartArgs{Protocol: Protocol, SessionID: "job", Prompt: "hello", Source: Source{Kind: "global"}, Attachments: map[string]artifacts.Ref{}}
	return &story.JobRunStory{JobID: "job", Status: story.WorkflowStatusRunning, StartedAt: time.Unix(1, 0), Recipe: story.JobRunStoryRecipe{ID: RecipeID, Source: story.JobRunStoryRecipeSource{SubmittedSelector: c.cfg.Recipe, ResolvedCommit: c.cfg.RecipeCommit}}, Root: &story.JobRunStoryNode{ID: RecipeID, Kind: "recipe", Input: args}}
}
func node(role string, ord int64, result any) *story.JobRunStoryNode {
	state := map[string]string{"capture": "capture", "response": "infer", "await-human": "prompt", "accept": "prompt", "prepare-action": "action", "dispatch": "action"}[role]
	return &story.JobRunStoryNode{Path: []string{RecipeID, state, "sequence", role}, ID: role, Kind: "op", OpID: ActivityName, TaskOrdinal: &ord, InvocationID: fmt.Sprintf("inv%d", ord), Status: "succeeded", Output: result}
}
func result(role string, ord int64) *TaskResult {
	return &TaskResult{Kind: role, Origin: Locator{JobID: "job", Invocation: fmt.Sprintf("inv%d", ord)}}
}
func captureNode() *story.JobRunStoryNode {
	r := result("capture", 1)
	r.Capture = &Capture{Source: Source{Kind: "global"}, Text: "captured"}
	return node("capture", 1, r)
}
func responseNode(ord int64, text string) *story.JobRunStoryNode {
	r := result("response", ord)
	r.Response = &Response{Text: text}
	return node("response", ord, r)
}
func inputNode(ord int64, cmd Command) *story.JobRunStoryNode {
	n := node("await-human", ord, nil)
	n.OpID = "input"
	n.TaskOrdinal = nil
	n.Children = []*story.JobRunStoryNode{{Path: append(append([]string{}, n.Path...), "step:collect_user_input"), Kind: "opStep", StepID: "collect_user_input", TaskOrdinal: &ord, Status: "succeeded", Output: input.Output{Response: cmd, Receipt: &input.Receipt{RequestID: fmt.Sprint(ord), SubmissionID: "send", Actor: human.Actor, SubmittedAt: time.Unix(ord, 0)}}}}
	return n
}

func TestClosedCommands(t *testing.T) {
	c, _, _, _ := client(t)
	p := &Proposal{ID: "id", Hash: "hash"}
	tests := []struct {
		name string
		v    any
		ok   bool
	}{
		{"send", map[string]any{"command": "send", "prompt": "hello", "attachments": map[string]any{}}, true},
		{"empty", map[string]any{"command": "send", "prompt": "  "}, false},
		{"extra", map[string]any{"command": "send", "prompt": "hi", "tenant": "foreign"}, false},
		{"finish", map[string]any{"command": "finish"}, true},
		{"extra-empty", map[string]any{"command": "finish", "prompt": ""}, false},
		{"confirm", map[string]any{"command": "confirm", "proposal_id": "id", "proposal_hash": "hash"}, true},
		{"stale", map[string]any{"command": "confirm", "proposal_id": "id", "proposal_hash": "changed"}, false},
		{"unknown", map[string]any{"command": "execute"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, e := c.command(tc.v, p)
			if (e == nil) != tc.ok {
				t.Fatalf("err=%v", e)
			}
		})
	}
	if _, e := c.command(Command{Command: "confirm", ProposalID: "id", ProposalHash: "hash"}, nil); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
}
func TestTextByteBoundaries(t *testing.T) {
	for _, max := range []int{32 << 10, 64 << 10, 128 << 10, 1 << 20} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			if e := text(strings.Repeat("a", max), max, false); e != nil {
				t.Fatal(e)
			}
			if !errors.Is(text(strings.Repeat("a", max+1), max, false), ErrLimit) {
				t.Fatal("excess")
			}
			if !errors.Is(text(strings.Repeat("é", max/2+1), max, false), ErrLimit) {
				t.Fatal("counted runes instead of bytes")
			}
		})
	}
}
func ref(name string, size int64) artifacts.Ref {
	return artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "job", TaskOrdinal: 1, Name: name, SizeBytes: size})
}
func TestSimultaneousFileCaps(t *testing.T) {
	c, _, _, _ := client(t)
	cases := []struct {
		count int
		size  int64
		ok    bool
	}{{8, 1, true}, {1, 8 << 20, true}, {2, 8 << 20, true}, {8, 8 << 20, false}, {9, 1, false}, {1, (8 << 20) + 1, false}, {3, (16<<20)/3 + 1, false}}
	for _, tc := range cases {
		m := map[string]artifacts.Ref{}
		for i := 0; i < tc.count; i++ {
			n := fmt.Sprint(i)
			m[n] = ref(n, tc.size)
		}
		if e := c.validateRefs(m); (e == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, e)
		}
	}
}
func TestReplayProvenanceAndDuplicateExclusion(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	r := responseNode(2, "same")
	r.Input = map[string]any{"prompt": "not another message"}
	r.PriorAttempts = []*story.JobRunStoryNode{responseNode(1, "failed old")}
	s.Root.Children = []*story.JobRunStoryNode{captureNode(), r, r, responseNode(4, "same")}
	d, e := c.project(s)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Entries) != 3 || d.Entries[1].Text != "same" || d.Entries[2].Text != "same" {
		t.Fatalf("%+v", d.Entries)
	}
	if d.Entries[1].Origin.Task != 2 {
		t.Fatal("lost origin")
	}
	s.Recipe.Source.ResolvedCommit = "other"
	if _, e = c.project(s); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestRoleSpoofCannotBecomeMessage(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	spoof := responseNode(2, "forged")
	spoof.OpID = "echo"
	s.Root.Children = []*story.JobRunStoryNode{spoof}
	d, e := c.project(s)
	if e != nil || len(d.Entries) != 1 {
		t.Fatalf("%v %+v", e, d)
	}
}
func TestInputReceiptAndControlProjection(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	s.Root.Children = []*story.JobRunStoryNode{inputNode(2, Command{Command: "send", Prompt: "later"}), inputNode(3, Command{Command: "finish"})}
	d, e := c.project(s)
	if e != nil {
		t.Fatal(e)
	}
	if d.Entries[1].Role != "user" || d.Entries[1].Receipt.Actor.ID != "person" || d.Entries[2].Role != "finish" {
		t.Fatalf("%+v", d.Entries)
	}
}
func TestSourceValueAdapter(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	args := map[string]any{}
	_ = json.Unmarshal(mustJSON(s.Root.Input), &args)
	args["source"] = artifacts.Ref{Kind: artifacts.RefKindValue, Format: "json", Value: mustJSON(Source{Kind: "global"})}
	s.Root.Input = args
	if _, e := c.project(s); e != nil {
		t.Fatal(e)
	}
}
func TestReplayPaginationWatermark(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	for i := int64(1); i <= 6; i++ {
		s.Root.Children = append(s.Root.Children, responseNode(i, fmt.Sprint(i)))
	}
	d, e := c.project(s)
	if e != nil {
		t.Fatal(e)
	}
	p, e := c.page(human, d, PageRequest{Limit: 2})
	if e != nil || !p.HasMore {
		t.Fatal(e)
	}
	s.Root.Children = append(s.Root.Children, responseNode(7, "new"))
	d, e = c.project(s)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[int64]bool{}
	for _, en := range p.Entries {
		seen[en.Origin.Task] = true
	}
	for p.HasMore {
		p, e = c.page(human, d, PageRequest{Cursor: p.Cursor, Limit: 2})
		if e != nil {
			t.Fatal(e)
		}
		for _, en := range p.Entries {
			if seen[en.Origin.Task] {
				t.Fatal("duplicate")
			}
			seen[en.Origin.Task] = true
		}
	}
	if len(seen) != 7 || seen[7] {
		t.Fatal(seen)
	}
}
func TestForeignAndTamperedCursors(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	d, _ := c.project(s)
	v := c.seal(cursor{Kind: "replay", Tenant: "foreign", Job: "job", Session: "job", End: 1, Watermark: 1, Digest: digest(completed(d.Entries))})
	if _, e := c.page(human, d, PageRequest{Cursor: v}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := c.unseal(v + "x"); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestEncodedPageLimitAndLargeHistory(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	for i := int64(1); i < 100; i++ {
		s.Root.Children = append(s.Root.Children, responseNode(i, strings.Repeat("\x01", 60000)))
	}
	d, e := c.project(s)
	if e != nil {
		t.Fatal(e)
	}
	p, e := c.page(human, d, PageRequest{})
	if e != nil || !p.HasMore {
		t.Fatal(e)
	}
	if len(mustJSON(p)) > c.cfg.Limits.Page {
		t.Fatal("encoded page overflow")
	}
	if len(d.Entries) != 100 {
		t.Fatal("total history cutoff")
	}
}
func TestModelPromptDoesNotRecurseOrIncludePending(t *testing.T) {
	c, ss, _, _ := client(t)
	s := fixture(c)
	s.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "actual")}
	n := node("infer", 3, map[string]any{"prompt": "secret recursive prompt"})
	n.Status = "running"
	s.Root.Children = append(s.Root.Children, n)
	ss.jobs["job"] = s
	p, _, e := c.Prompt(ctx, human, "job")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(p, "actual") || strings.Contains(p, "secret recursive") {
		t.Fatal(p)
	}
}
func TestMalformedResponseFailsBeforeArtifacts(t *testing.T) {
	c, ss, _, _ := client(t)
	ss.jobs["job"] = fixture(c)
	for _, raw := range []string{`not json`, `{"text":"x","documents":[],"return_artifact_ids":[],"command":"shell"}`, `{"text":"x","documents":[{"name":"../escape","text":"hi","mime":"text/plain"}],"return_artifact_ids":[]}`, `{"text":"x","documents":[],"return_artifact_ids":["forged"]}`} {
		if _, _, e := c.ValidateResponse(ctx, human, "job", Locator{JobID: "job", Invocation: "turn"}, ModelOutput{Response: raw}); e == nil {
			t.Fatal(raw)
		}
	}
}
func TestWorkerOversizedResponseReturnsBoundedError(t *testing.T) {
	c, ss, _, _ := client(t)
	ss.jobs["job"] = fixture(c)
	raw := string(mustJSON(ModelResult{Text: strings.Repeat("x", c.cfg.Limits.Assistant+1)}))
	r, docs, e := c.Worker(ctx, human, Locator{JobID: "job", Invocation: "turn"}, "response", ModelOutput{Response: raw}, "", "")
	if e != nil || len(docs) != 0 || r.Response.Error == "" {
		t.Fatalf("%v %+v", e, r)
	}
}
func TestDocumentsAndExactGoalProposal(t *testing.T) {
	c, ss, _, _ := client(t)
	ss.jobs["job"] = fixture(c)
	m := ModelResult{Text: "answer", Documents: []Document{{Name: "answer.md", Text: "hello", MIME: "text/markdown"}}, ReturnArtifactIDs: []string{}, CreateGoal: &GoalRequest{Cell: "cell", Prompt: "exact prompt"}}
	r, docs, e := c.ValidateResponse(ctx, human, "job", Locator{JobID: "job", Invocation: "turn"}, ModelOutput{Response: string(mustJSON(m)), Model: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if len(docs) != 1 || r.Proposal.Prompt != "exact prompt" || r.Proposal.Hash == "" {
		t.Fatalf("%+v", r)
	}
}
func TestArtifactMembershipAndActualBytes(t *testing.T) {
	c, ss, ctl, _ := client(t)
	s := fixture(c)
	r := result("response", 2)
	r.Response = &Response{Artifacts: []Artifact{describe("file.txt", ref("file.txt", 5))}}
	s.Root.Children = []*story.JobRunStoryNode{node("response", 2, r)}
	ss.jobs["job"] = s
	ctl.files["file.txt"] = []byte("hello")
	a, e := c.OpenArtifact(ctx, human, "job", r.Response.Artifacts[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(a)
	_ = a.Close()
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
	if _, e = c.OpenArtifact(ctx, human, "job", "forged"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	ctl.files["file.txt"] = []byte("extra bytes")
	if _, e = c.readRef(ctx, human, ref("file.txt", 5)); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	before := ctl.reads
	if _, e = c.readRef(ctx, Caller{"foreign", human.Actor}, ref("file.txt", 5)); !errors.Is(e, ErrForbidden) || ctl.reads != before {
		t.Fatal("read before authorization")
	}
}
func TestPreparePersistsExactReceiptAndStableDestination(t *testing.T) {
	c, ss, _, sub := client(t)
	s := fixture(c)
	r := result("response", 2)
	r.Response = &Response{Proposal: &Proposal{ID: "p", Hash: "h", Target: recipejob.ResolvedTarget{CellName: "cell", RepositorySource: "https://github.com/example/cell.git", DefaultRef: "main"}, Prompt: "exact"}}
	s.Root.Children = []*story.JobRunStoryNode{node("response", 2, r), inputNode(3, Command{Command: "confirm", ProposalID: "p", ProposalHash: "h"})}
	ss.jobs["job"] = s
	a, e := c.Prepare(ctx, human, "job")
	if e != nil {
		t.Fatal(e)
	}
	b, e := c.Prepare(ctx, human, "job")
	if e != nil || !reflect.DeepEqual(a, b) || a.Request.Inputs["prompt"] != "exact" {
		t.Fatal(e)
	}
	if len(sub.jobs) != 0 {
		t.Fatal("prepare submitted")
	}
	r2 := result("prepare-action", 4)
	r2.Prepared = &a
	s.Root.Children = append(s.Root.Children, node("prepare-action", 4, r2))
	if _, e = c.Dispatch(ctx, human, "job"); e != nil {
		t.Fatal(e)
	}
	if len(sub.jobs) != 1 {
		t.Fatal("missing submit")
	}
}
func TestReopenUsesActualScopeAndStableRequest(t *testing.T) {
	c, ss, _, _ := client(t)
	s := fixture(c)
	s.Status = story.WorkflowStatusCompleted
	s.RestartScopes = []story.RestartScope{{ScopeID: "actual-scope", ChapterOrdinal: 9}}
	n := inputNode(9, Command{Command: "finish"})
	n.RestartScopeID = "actual-scope"
	s.Root.Children = []*story.JobRunStoryNode{n}
	ss.jobs["job"] = s
	for i := 0; i < 2; i++ {
		if _, e := c.Reopen(ctx, human, "job"); e != nil {
			t.Fatal(e)
		}
	}
	a, b := ss.restartRequests[0], ss.restartRequests[1]
	if a.ScopeID != "actual-scope" || a.RequestID != b.RequestID || a.Goal.Mode != "new" || a.ChapterOrdinal != nil {
		t.Fatalf("%+v", a)
	}
}
func TestMissingReplayFailsExplicitly(t *testing.T) {
	c, _, _, _ := client(t)
	if _, e := c.read(ctx, human, "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	s := fixture(c)
	r := responseNode(2, "x")
	r.Output = map[string]any{"kind": "response"}
	s.Root.Children = []*story.JobRunStoryNode{r}
	if _, e := c.project(s); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}

func (c *fixtureControl) ListJobs(ctx context.Context, req jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
	c.listCalls++
	if c.list != nil {
		return c.list(req)
	}
	out := []workflowctl.JobItem{}
	for id := range c.story.jobs {
		if len(req.JobKeys) > 0 && req.JobKeys[0].JobId != id {
			continue
		}
		out = append(out, workflowctl.JobItem{JobSummary: jobdb.JobSummary{JobKey: jobdb.JobKey{TenantId: "tenant", JobId: id}, Cell: "root", CellRepo: "github.com/example/root", JobType: "recipe", Metadata: mustJSON(map[string]any{"repo": "https://github.com/example/root.git", "recipe": selector})}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobKey.JobId < out[j].JobKey.JobId })
	if c.backendPage > 0 {
		start, _ := strconv.Atoi(req.PageToken)
		end := start + c.backendPage
		if start >= len(out) {
			return nil, "", nil
		}
		if end > len(out) {
			end = len(out)
		}
		next := ""
		if end < len(out) {
			next = strconv.Itoa(end)
		}
		return out[start:end], next, nil
	}
	return out, "", nil
}

func (c *fixtureControl) GetJobRun(ctx context.Context, req jobdb.GetJobRunRequest) (jobdb.GetJobRunResponse, error) {
	if r, ok := c.runs[req.JobKey.JobId]; ok {
		return r, nil
	}
	return jobdb.GetJobRunResponse{}, ErrUnavailable
}

func TestModelMandatoryFieldsAndExplicitSourceOmission(t *testing.T) {
	c, ss, _, _ := client(t)
	ss.jobs["job"] = fixture(c)
	for _, raw := range []string{`{}`, `{"text":"","documents":[],"return_artifact_ids":null}`} {
		if _, _, e := c.ValidateResponse(ctx, human, "job", Locator{}, ModelOutput{Response: raw}); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	cap := captureNode()
	cap.Output.(*TaskResult).Capture.Text = strings.Repeat("x", c.cfg.Limits.Model)
	ss.jobs["job"].Root.Children = []*story.JobRunStoryNode{cap}
	p, _, e := c.Prompt(ctx, human, "job")
	if e != nil || !strings.Contains(p, "source omitted for model prompt budget") {
		t.Fatalf("%v %d", e, len(p))
	}
}
func TestUploadFreezesActualBytesAndChecksEnvelope(t *testing.T) {
	c, _, _, _ := client(t)
	in := input.StructuredSubmission{Response: map[string]any{"command": "send", "prompt": "hello", "attachments": map[string]any{"x.txt": jobdb.NewArtifactFromBytes("x.txt", []byte("bytes"))}}}
	frozen, _, e := c.freezeSubmission(ctx, human, in, nil)
	if e != nil {
		t.Fatal(e)
	}
	a := frozen.Response.(map[string]any)["attachments"].(map[string]any)["x.txt"].(jobdb.Artifact)
	b, e := a.Bytes(ctx)
	if e != nil || string(b) != "bytes" {
		t.Fatal(e, string(b))
	}
	if _, e = a.ArtifactKey(); e == nil {
		t.Fatal("invented persistent key")
	}
	c.cfg.Limits.Upload = 1
	if _, _, e = c.freezeSubmission(ctx, human, in, nil); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	c.cfg.Limits.Upload = DefaultLimits().Upload
	c.cfg.Limits.File = 4
	if _, _, e = c.freezeSubmission(ctx, human, in, nil); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
func TestDirectInvalidInputRetainsReceiptAndReturnsPromptError(t *testing.T) {
	c, ss, _, _ := client(t)
	s := fixture(c)
	ss.jobs["job"] = s
	n := inputNode(3, Command{Command: "send", Prompt: "valid"})
	out := n.Children[0].Output.(input.Output)
	out.Response = map[string]any{"command": "send", "prompt": "hello", "execute": "shell"}
	n.Children[0].Output = out
	s.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "hi"), n}
	d, e := c.read(ctx, human, "job")
	if e != nil || d.Entries[len(d.Entries)-1].Receipt == nil {
		t.Fatal(e)
	}
	r, _, e := c.Worker(ctx, human, Locator{JobID: "job", Invocation: "accept"}, "accept", ModelOutput{}, "", "")
	if e != nil || r.Command.Command != "invalid" || r.Error == "" {
		t.Fatal(e, r)
	}
}
func TestRestartSourceBindsCopiedOriginsAndExcludesDivergence(t *testing.T) {
	c, ss, ctl, _ := client(t)
	parent := fixture(c)
	parent.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "prefix"), inputNode(3, Command{Command: "send", Prompt: "retained"}), responseNode(4, "divergent")}
	ss.jobs["job"] = parent
	child := fixture(c)
	child.JobID = "next"
	child.StartedAt = time.Unix(2, 0)
	child.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "prefix"), inputNode(3, Command{Command: "send", Prompt: "retained"})}
	ss.jobs["next"] = child
	ctl.runs = map[string]jobdb.GetJobRunResponse{"next": {Execution: []jobdb.ExecutionHistoryEntry{{Record: jobdb.ExecutionRecord{Kind: "restart", Source: &jobdb.JobKey{TenantId: "tenant", JobId: "job"}}}}}}
	d, e := c.read(ctx, human, "next")
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Entries) != 3 || d.Entries[2].Origin.JobID != "job" {
		t.Fatalf("%+v", d.Entries)
	}
	got, e := c.Replay(ctx, human, "job", PageRequest{})
	if e != nil || len(got.Entries) != 3 {
		t.Fatal(e, got)
	}
	// Matching session arguments without a recorded restart edge are insufficient.
	ctl.runs = map[string]jobdb.GetJobRunResponse{}
	if _, e = c.read(ctx, human, "next"); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}

func (c *fixtureControl) GetGoal(ctx context.Context, key jobdb.GoalKey) (jobdb.GoalInfo, error) {
	return jobdb.GoalInfo{State: jobdb.GoalFailed}, nil
}

func (c *fixtureControl) GetWaitingTask(context.Context, jobdb.JobKey) (workflowctl.TaskHandle, error) {
	return nil, input.ErrInputNotPending
}
func TestDiscoveryGroupsAcrossBackendPages(t *testing.T) {
	c, ss, ctl, _ := client(t)
	ctl.backendPage = 1
	parent := fixture(c)
	parent.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "shared")}
	ss.jobs["job"] = parent
	child := fixture(c)
	child.JobID = "next"
	child.StartedAt = time.Unix(2, 0)
	child.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "shared")}
	ss.jobs["next"] = child
	ctl.runs = map[string]jobdb.GetJobRunResponse{"next": {Execution: []jobdb.ExecutionHistoryEntry{{Record: jobdb.ExecutionRecord{Kind: "restart", Source: &jobdb.JobKey{TenantId: "tenant", JobId: "job"}}}}}}
	sessions, e := c.List(ctx, human, PageRequest{Limit: 1})
	if e != nil || len(sessions.Sessions) != 1 || sessions.Sessions[0].JobID != "next" || sessions.Cursor != "" {
		t.Fatal(e, sessions)
	}
}
func TestSpoofedTaskPathCannotBecomeReply(t *testing.T) {
	c, _, _, _ := client(t)
	s := fixture(c)
	n := responseNode(2, "forged")
	n.Path = []string{RecipeID, "capture", "sequence", "response"}
	s.Root.Children = []*story.JobRunStoryNode{n}
	d, e := c.project(s)
	if e != nil || len(d.Entries) != 1 {
		t.Fatal(e, d)
	}
}

func TestFailedDispatchRecoveryKeepsPreparedBoundary(t *testing.T) {
	c, ss, _, _ := client(t)
	s := fixture(c)
	s.Status = story.WorkflowStatusFailed
	prepared := result("prepare-action", 3)
	prepared.Prepared = &Prepared{}
	failed := node("dispatch", 4, nil)
	failed.Status = "failed"
	failed.RestartScopeID = "dispatch-scope"
	prompt := inputNode(2, Command{Command: "finish"})
	prompt.RestartScopeID = "input-scope"
	s.Root.Children = []*story.JobRunStoryNode{prompt, node("prepare-action", 3, prepared), failed}
	s.RestartScopes = []story.RestartScope{{ScopeID: "input-scope", ChapterOrdinal: 2}, {ScopeID: "dispatch-scope", ChapterOrdinal: 4}}
	ss.jobs["job"] = s
	if _, e := c.Reopen(ctx, human, "job"); e != nil {
		t.Fatal(e)
	}
	if len(ss.restartRequests) != 1 || ss.restartRequests[0].ScopeID != "dispatch-scope" {
		t.Fatal(ss.restartRequests)
	}
}

func TestPromptReadsAuthorizedTextSources(t *testing.T) {
	for _, source := range []string{"initial", "later", "review"} {
		t.Run(source, func(t *testing.T) {
			c, ss, ctl, _ := client(t)
			s := fixture(c)
			body := strings.Repeat("x", 1200) + "unique source content"
			ref := artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "source", TaskOrdinal: 1, Name: "source.txt", SizeBytes: int64(len(body))})
			ctl.files["source.txt"] = []byte(body)
			capture := captureNode()
			switch source {
			case "initial":
				args := s.Root.Input.(StartArgs)
				args.Attachments["source.txt"] = ref
				s.Root.Input = args
			case "later":
				s.Root.Children = append(s.Root.Children, inputNode(2, Command{Command: "send", Prompt: "read it", Attachments: map[string]artifacts.Ref{"source.txt": ref}}))
			case "review":
				capture.Output.(*TaskResult).Capture.Artifacts = []Artifact{describe("source.txt", ref)}
			}
			s.Root.Children = append([]*story.JobRunStoryNode{capture}, s.Root.Children...)
			ss.jobs["job"] = s
			p, _, err := c.Prompt(ctx, human, "job")
			if err != nil || !strings.Contains(p, body) || !strings.Contains(p, "ATTACHMENT") {
				t.Fatalf("text missing: %v %s", err, p)
			}
			c.cfg.Limits.Model = 8192
			ctl.files["source.txt"] = []byte(strings.Repeat("€", 400))
			// A declared/actual mismatch must fail rather than trusting metadata.
			if _, _, err = c.Prompt(ctx, human, "job"); !errors.Is(err, ErrLimit) {
				t.Fatal(err)
			}
			ref.Stored.Key.JobId = "foreign-source"
			args := s.Root.Input.(StartArgs)
			args.Attachments["forbidden.txt"] = ref
			s.Root.Input = args
			if _, _, err = c.Prompt(ctx, human, "job"); !errors.Is(err, ErrForbidden) {
				t.Fatal(err)
			}
		})
	}
}

func TestAttachmentContextBoundsAndFormats(t *testing.T) {
	c, _, ctl, _ := client(t)
	d := &replayData{Args: StartArgs{Attachments: map[string]artifacts.Ref{}}}
	add := func(name string, b []byte) {
		ctl.files[name] = b
		d.Args.Attachments[name] = artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "source", TaskOrdinal: 1, Name: name, SizeBytes: int64(len(b))})
	}
	add("a.txt", []byte(strings.Repeat("€", 3000)))
	p, omissions, err := c.attachmentContext(ctx, human, d, 2048)
	if err != nil || len(p) > 2048 || len(omissions) != 1 || omissions[0].JobID != "source" || !strings.Contains(p, "truncated") || !utf8.ValidString(p) {
		t.Fatal(err, len(p), p)
	}
	d.Args.Attachments = map[string]artifacts.Ref{}
	add("binary.txt", []byte{0xff, 0})
	add("image.png", []byte("not model text"))
	p, _, err = c.attachmentContext(ctx, human, d, 2048)
	if err != nil || strings.Contains(p, "not model text") || !strings.Contains(p, "Omitted attachments") {
		t.Fatal(err, p)
	}
}

func TestDispatchPresentationRequiresRecordedSuccess(t *testing.T) {
	c, _, _, _ := client(t)
	successful := c.latestResponse(&replayData{Entries: []Entry{{Role: "dispatch", Status: "succeeded", Destination: "actual-job"}}})
	if successful.Error != "" || successful.Text != "Goal submitted: actual-job" {
		t.Fatal(successful)
	}
	for _, row := range []Entry{
		{Role: "dispatch", Status: "succeeded", Error: "forbidden"},
		{Role: "dispatch", Status: "succeeded", Error: "conflicting payload", Destination: "existing"},
		{Role: "dispatch", Status: "succeeded"},
	} {
		c, _, _, _ := client(t)
		r := c.latestResponse(&replayData{Entries: []Entry{row}})
		if r.Error == "" || strings.Contains(r.Text, "submitted") {
			t.Fatal(r)
		}
	}
	c, ss, _, sub := client(t)
	s := fixture(c)
	s.Root.Children = []*story.JobRunStoryNode{captureNode()}
	ss.jobs["job"] = s
	out, _, err := c.Worker(ctx, human, Locator{JobID: "job", Invocation: "inv2"}, "dispatch", ModelOutput{}, "", "")
	if err != nil || out.Error == "" {
		t.Fatal(out, err)
	}
	s.Root.Children = append(s.Root.Children, node("dispatch", 2, out))
	form, err := c.Form(ctx, human, "job")
	if err != nil {
		t.Fatal(err)
	}
	var request PromptRequest
	if err = decode(form.Request, &request); err != nil || request.Assistant.Error == "" || request.Assistant.Text != "" || len(sub.jobs) != 0 {
		t.Fatal(request, err)
	}
}

func TestWorkerDispatchRejectionsRemainErrorsInForm(t *testing.T) {
	for _, mode := range []string{"eligibility", "registration", "stale-confirmation", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			c, ss, _, sub := client(t)
			s := fixture(c)
			response := result("response", 2)
			response.Response = &Response{Proposal: &Proposal{ID: "p", Hash: "h", Target: recipejob.ResolvedTarget{CellName: "cell", RepositorySource: "https://github.com/example/cell.git", DefaultRef: "main"}, Prompt: "exact"}}
			s.Root.Children = []*story.JobRunStoryNode{node("response", 2, response), inputNode(3, Command{Command: "confirm", ProposalID: "p", ProposalHash: "h"})}
			ss.jobs["job"] = s
			prepared, err := c.Prepare(ctx, human, "job")
			if err != nil {
				t.Fatal(err)
			}
			record := result("prepare-action", 4)
			record.Prepared = &prepared
			s.Root.Children = append(s.Root.Children, node("prepare-action", 4, record))
			if mode == "eligibility" {
				c.host.ResolveCell = func(context.Context, Caller, string) (recipejob.ResolvedTarget, error) {
					return recipejob.ResolvedTarget{}, ErrForbidden
				}
			} else if mode == "registration" {
				changed := response.Response.Proposal.Target
				changed.CellName = "other"
				c.host.ResolveCell = func(context.Context, Caller, string) (recipejob.ResolvedTarget, error) { return changed, nil }
			} else if mode == "stale-confirmation" {
				s.Root.Children[1] = inputNode(3, Command{Command: "confirm", ProposalID: "p", ProposalHash: "obsolete"})
			} else {
				// Existing job with another payload at the frozen destination is a conflict.
				ss.jobs[prepared.Request.JobID] = fixture(c)
			}
			out, _, err := c.Worker(ctx, human, Locator{JobID: "job", Invocation: "inv5"}, "dispatch", ModelOutput{}, "", "")
			if err != nil || out.Error == "" {
				t.Fatal(out, err)
			}
			s.Root.Children = append(s.Root.Children, node("dispatch", 5, out))
			form, err := c.Form(ctx, human, "job")
			if err != nil {
				t.Fatal(err)
			}
			var request PromptRequest
			if err = decode(form.Request, &request); err != nil || request.Assistant.Error != out.Error || request.Assistant.Text != "" || request.Proposal != nil || len(sub.jobs) != 0 {
				t.Fatal(request, err)
			}
		})
	}
}

// Exercise backend rows, not only request construction: registration names can
// be stale, repository transports differ, and filtered pages may be empty.
func TestCanonicalRootIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, tenant, cell, repo string
		allowed                  bool
	}{
		{"canonical", "tenant", "root", "github.com/example/root", true},
		{"https", "tenant", "root", "https://github.com/example/root.git", true},
		{"ssh", "tenant", "root", "git@github.com:example/root.git", true},
		{"other-repo", "tenant", "root", "github.com/example/other", false},
		{"prefix", "tenant", "root", "github.com/example/root-extra", false},
		{"other-cell", "tenant", "other", "github.com/example/root", false},
		{"other-tenant", "other", "root", "github.com/example/root", false},
		{"invalid-repo", "tenant", "root", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ss, ctl, sub := client(t)
			ss.jobs["job"] = fixture(c)
			ctl.list = func(req jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
				return []workflowctl.JobItem{{JobSummary: jobdb.JobSummary{JobKey: jobdb.JobKey{TenantId: tc.tenant, JobId: "job"}, Cell: tc.cell, CellRepo: tc.repo, JobType: "recipe", Metadata: mustJSON(map[string]any{"recipe": selector})}}}, "", nil
			}
			ds, err := c.discover(ctx, human)
			if err != nil {
				t.Fatal(err)
			}
			if (len(ds) == 1) != tc.allowed {
				t.Fatalf("discovery: %d sessions", len(ds))
			}
			_, err = c.read(ctx, human, "job")
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrNotFound) {
				t.Fatalf("lookup: %v", err)
			}
			if !tc.allowed && ss.requests != 0 {
				t.Fatal("read excluded session data")
			}
			if len(sub.jobs) != 0 {
				t.Fatal("lookup submitted work")
			}
		})
	}
}

func TestRootFilteredPagination(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, repeat := range []bool{false, true} {
			t.Run(fmt.Sprintf("direct=%v/repeat=%v", direct, repeat), func(t *testing.T) {
				c, ss, ctl, _ := client(t)
				ss.jobs["job"] = fixture(c)
				ctl.list = func(req jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
					if req.PageToken == "" || repeat {
						return []workflowctl.JobItem{{JobSummary: jobdb.JobSummary{JobKey: jobdb.JobKey{TenantId: "tenant", JobId: "excluded"}, Cell: "root", CellRepo: "github.com/example/other", JobType: "recipe", Metadata: mustJSON(map[string]any{"recipe": selector})}}}, "next", nil
					}
					return []workflowctl.JobItem{{JobSummary: jobdb.JobSummary{JobKey: jobdb.JobKey{TenantId: "tenant", JobId: "job"}, Cell: "root", CellRepo: "github.com/example/root", JobType: "recipe", Metadata: mustJSON(map[string]any{"recipe": selector})}}}, "", nil
				}
				var err error
				if direct {
					_, err = c.read(ctx, human, "job")
				} else {
					var ds []*replayData
					ds, err = c.discover(ctx, human)
					if !repeat && len(ds) != 1 {
						t.Fatalf("lost session beyond filtered page: %d", len(ds))
					}
				}
				if repeat && !errors.Is(err, ErrUnavailable) || !repeat && err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRootTenantAuthorizationBeforeReads(t *testing.T) {
	c, ss, ctl, sub := client(t)
	ss.jobs["job"] = fixture(c)
	other := human
	other.TenantID = "other"
	if _, err := c.List(ctx, other, PageRequest{}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := c.read(ctx, other, "job"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if ss.requests != 0 || ctl.listCalls != 0 || len(sub.jobs) != 0 {
		t.Fatal("cross-tenant access reached backend")
	}
}

func TestDiscoveryExcludesOtherRootWithSameSession(t *testing.T) {
	c, ss, ctl, _ := client(t)
	ss.jobs["job"] = fixture(c)
	other := fixture(c) // Same logical session; unrelated root must not conflict.
	other.JobID = "other-job"
	ss.jobs["other-job"] = other
	ctl.list = func(req jobdb.ListJobsRequest) ([]workflowctl.JobItem, string, error) {
		var out []workflowctl.JobItem
		for _, id := range []string{"job", "other-job"} {
			if len(req.JobKeys) > 0 && req.JobKeys[0].JobId != id {
				continue
			}
			repo := "github.com/example/root"
			if id == "other-job" {
				repo = "github.com/example/other"
			}
			out = append(out, workflowctl.JobItem{JobSummary: jobdb.JobSummary{JobKey: jobdb.JobKey{TenantId: "tenant", JobId: id}, Cell: "root", CellRepo: repo, JobType: "recipe", Metadata: mustJSON(map[string]any{"recipe": selector})}})
		}
		return out, "", nil
	}
	ds, err := c.discover(ctx, human)
	if err != nil || len(ds) != 1 || ds[0].Story.JobID != "job" || ss.requests != 1 {
		t.Fatalf("mixed configured roots: %+v %v reads=%d", ds, err, ss.requests)
	}
}

func TestRecipeProtocolAndModelBinding(t *testing.T) {
	raw, err := os.ReadFile("../session.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if Protocol != "cortex-assistant/2" || !strings.Contains(string(raw), "default_value: "+Protocol) {
		t.Fatal("recipe protocol changed")
	}
	if strings.Count(string(raw), "op: "+ModelOperation+"\n") != 1 {
		t.Fatal("recipe and facade model operation differ")
	}
}
