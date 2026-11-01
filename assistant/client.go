package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func New(h Host, cfg Config) (*Client, error) {
	if h.Goals == nil || h.History == nil || h.Control == nil || h.Story == nil || h.Input == nil || h.Submitter == nil || h.Authorize == nil || h.ResolveCell == nil || cfg.Root.RepositorySource == "" || len(cfg.CursorKey) < 32 {
		return nil, ErrInvalid
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(cfg.RecipeCommit) || !strings.HasSuffix(cfg.Recipe, "@"+cfg.RecipeCommit) || !strings.Contains(cfg.Recipe, "cortex-assistant.git//session.yaml@") {
		return nil, ErrInvalid
	}
	if cfg.BuildRecipe == "" {
		cfg.BuildRecipe = "build"
	}
	if cfg.BuildRecipe != "build" {
		return nil, fmt.Errorf("%w: only catalog build recipe is supported", ErrInvalid)
	}
	l, e := boundedLimits(cfg.Limits)
	if e != nil {
		return nil, e
	}
	cfg.Limits = l
	if err := validateLegacy(cfg); err != nil {
		return nil, err
	}
	cfg.LegacyReleases = append([]ReleaseDescriptor(nil), cfg.LegacyReleases...)
	cfg.CursorKey = bytes.Clone(cfg.CursorKey)
	return &Client{h, cfg}, nil
}
func (c *Client) authorize(ctx context.Context, w Caller, job string) error {
	if w.TenantID == "" || w.Actor.ID == "" || w.Actor.Kind != "human" && w.Actor.Kind != "user" && w.Actor.Kind != "agent" {
		return ErrForbidden
	}
	if len(job) > 512 || strings.TrimSpace(job) == "" {
		return ErrInvalid
	}
	if e := c.host.Authorize(ctx, w, job); e != nil {
		return fmt.Errorf("%w: %w", ErrForbidden, e)
	}
	return nil
}
func (c *Client) Start(ctx context.Context, w Caller, in StartInput) (Session, error) {
	if e := c.authorize(ctx, w, "start"); e != nil {
		return Session{}, e
	}
	if e := text(in.RequestID, 512, false); e != nil {
		return Session{}, e
	}
	if in.SubmittedAt.IsZero() {
		return Session{}, fmt.Errorf("%w: freeze SubmittedAt with RequestID", ErrInvalid)
	}
	if e := text(in.Prompt, c.cfg.Limits.Human, false); e != nil {
		return Session{}, e
	}
	if e := in.Source.validate(); e != nil {
		return Session{}, e
	}
	if e := c.validateRefs(in.Attachments); e != nil {
		return Session{}, e
	}
	if in.Source.Kind != "global" {
		if e := c.authorize(ctx, w, in.Source.JobID); e != nil {
			return Session{}, e
		}
	}
	if e := c.checkRefs(ctx, w, in.Attachments); e != nil {
		return Session{}, e
	}
	if in.Attachments == nil {
		in.Attachments = map[string]artifacts.Ref{}
	}
	id := "assistant-" + identity(w.TenantID, in.RequestID)
	args := StartArgs{Protocol, id, in.Prompt, in.Source, in.Attachments}
	b, _ := json.Marshal(args)
	var inputs map[string]any
	_ = json.Unmarshal(b, &inputs)
	inputs["source"] = artifacts.Ref{Kind: artifacts.RefKindValue, Format: "json", Value: mustJSON(in.Source)}
	req := recipejob.BuildStartJobRequest{TenantID: w.TenantID, JobID: id, SubmittedAt: &in.SubmittedAt, Target: c.cfg.Root, Recipe: c.cfg.Recipe, Inputs: inputs}
	for _, r := range in.Attachments {
		req.ArtifactRefs = append(req.ArtifactRefs, r)
	}
	sort.Slice(req.ArtifactRefs, func(i, j int) bool {
		return string(mustJSON(req.ArtifactRefs[i])) < string(mustJSON(req.ArtifactRefs[j]))
	})
	ref, e := recipejob.SubmitRecipeJob(ctx, req, c.host.Submitter)
	if e != nil {
		return Session{}, e
	}
	return Session{ID: id, JobID: ref.JobID, GoalID: ref.GoalID, Status: "submitted", Source: in.Source, Limits: c.cfg.Limits}, nil
}
func (c *Client) resolve(ctx context.Context, w Caller, id string) (*replayData, error) {
	// Logical session identity survives restart. A direct execution ID remains inspectable.
	d, e := c.read(ctx, w, id)
	if e != nil {
		return nil, e
	}
	if id != d.Args.SessionID {
		return d, nil
	}
	rows, err := c.discover(ctx, w)
	if err != nil {
		return nil, err
	}
	for _, other := range rows {
		if other.Args.SessionID == d.Args.SessionID {
			return other, nil
		}
	}

	return d, nil
}
func (c *Client) Get(ctx context.Context, w Caller, id string) (Session, error) {
	d, e := c.resolve(ctx, w, id)
	if e != nil {
		return Session{}, e
	}
	return c.session(ctx, w, d)
}
func (c *Client) session(ctx context.Context, w Caller, d *replayData) (Session, error) {
	s := Session{ID: d.Args.SessionID, JobID: d.Story.JobID, GoalID: d.Story.GoalID, Status: string(d.Story.Status), Source: d.Args.Source, Limits: c.cfg.Limits}
	var e error
	s.Replay, e = c.page(w, d, PageRequest{})
	if e != nil {
		return s, e
	}
	s.Current, e = c.host.Input.GetDetails(ctx, w.TenantID, d.Story.JobID)
	if e != nil {
		if !errors.Is(e, input.ErrInputNotPending) && !errors.Is(e, jobdb.ErrJobNotFound) {
			return s, e
		}
		s.Current = nil
	}
	for _, en := range d.Entries {
		if en.Status != "succeeded" {
			s.Progress = append(s.Progress, en)
		}
	}
	return s, nil
}
func (c *Client) List(ctx context.Context, w Caller, p PageRequest) (SessionPage, error) {
	if e := c.authorize(ctx, w, "list"); e != nil {
		return SessionPage{}, e
	}
	rows, e := c.discover(ctx, w)
	if e != nil {
		return SessionPage{}, e
	}
	ids := []string{}
	for _, d := range rows {
		ids = append(ids, d.Args.SessionID+"/"+d.Story.JobID)
	}
	digest := identity(string(mustJSON(ids)))
	off := 0
	if p.Cursor != "" {
		v, e := c.unseal(p.Cursor)
		if e != nil {
			return SessionPage{}, e
		}
		if v.Kind != "list" || v.Tenant != w.TenantID {
			return SessionPage{}, ErrForbidden
		}
		if v.Digest != digest || v.End < 0 || v.End > len(rows) {
			return SessionPage{}, ErrStale
		}
		off = v.End
	}
	size := p.Limit
	if size <= 0 || size > 100 {
		size = 50
	}
	end := off + size
	if end > len(rows) {
		end = len(rows)
	}
	out := SessionPage{Sessions: []Session{}}
	for _, d := range rows[off:end] {
		s, e := c.session(ctx, w, d)
		if e != nil {
			return out, e
		}
		out.Sessions = append(out.Sessions, s)
	}
	if end < len(rows) {
		out.Cursor = c.seal(cursor{Kind: "list", Tenant: w.TenantID, End: end, Digest: digest})
	}
	return out, nil
}

func (c *Client) Respond(ctx context.Context, w Caller, id string, in input.StructuredSubmission) (input.Output, error) {
	if w.Actor.Kind != "human" && w.Actor.Kind != "user" {
		return input.Output{}, ErrForbidden
	}
	d, e := c.resolve(ctx, w, id)
	if e != nil {
		return input.Output{}, e
	}
	details, e := c.host.Input.GetDetails(ctx, w.TenantID, d.Story.JobID)
	if e != nil {
		if errors.Is(e, input.ErrInputNotPending) {
			return input.Output{}, ErrNotAtPrompt
		}
		return input.Output{}, fmt.Errorf("assistant: read current input: %w", e)
	}
	if details.Form.RequestId == nil || *details.Form.RequestId != in.RequestID {
		return input.Output{}, ErrStale
	}
	if details.Form.Kind != nil && *details.Form.Kind != "" {
		return input.Output{}, ErrForbidden
	}
	if details.Form.Request == nil {
		return input.Output{}, ErrInvalid
	}
	var req PromptRequest
	if e := decode(*details.Form.Request, &req); e != nil || req.Protocol != d.Args.Protocol || req.SessionID != d.Args.SessionID {
		return input.Output{}, ErrForbidden
	}
	in, cmd, e := c.freezeSubmission(ctx, w, in, req.Proposal)
	if e != nil {
		return input.Output{}, e
	}
	// Non-map callers contain stored refs only; validate their encoded envelope too.
	b, e := json.Marshal(in)
	if e != nil {
		return input.Output{}, e
	}
	if len(b) > c.cfg.Limits.Upload {
		return input.Output{}, ErrLimit
	}
	refs := map[string]artifacts.Ref{}
	for n, r := range cmd.Attachments {
		refs[n] = r
	}
	for n, r := range in.ArtifactRefs {
		if old, ok := refs[n]; ok && string(mustJSON(old)) != string(mustJSON(r)) {
			return input.Output{}, ErrConflict
		}
		refs[n] = r
	}
	if e = c.validateRefs(refs); e != nil {
		return input.Output{}, e
	}
	if e = c.checkRefs(ctx, w, refs); e != nil {
		return input.Output{}, e
	}
	if cmd.Command == "confirm" {
		target, e := c.host.ResolveCell(ctx, w, req.Proposal.Target.RepositorySource)
		if e != nil {
			return input.Output{}, e
		}
		if target.RepositorySource != req.Proposal.Target.RepositorySource || target.DefaultRef != req.Proposal.Target.DefaultRef {
			return input.Output{}, ErrStale
		}
	}
	return c.host.Input.SubmitStructuredResponse(ctx, w.TenantID, d.Story.JobID, in, w.Actor)
}
func (c *Client) Reopen(ctx context.Context, w Caller, id string) (Session, error) {
	d, e := c.resolve(ctx, w, id)
	if e != nil {
		return Session{}, e
	}
	if release, ok := c.release(d.Story.Recipe.Source.SubmittedSelector); !ok || !release.Reopen {
		return Session{}, ErrUnavailable
	}
	if d.Story.Status == story.WorkflowStatusRunning {
		return c.session(ctx, w, d)
	}
	mode := "new"
	if d.Story.Status != story.WorkflowStatusCompleted {
		goal, err := c.host.Goals.GetGoal(ctx, jobdb.GoalKey{TenantId: w.TenantID, GoalID: d.Story.GoalID})
		if err != nil {
			return Session{}, fmt.Errorf("%w: goal state: %v", ErrUnavailable, err)
		}
		switch goal.State {
		case jobdb.GoalOpen:
			mode = "continue"
		case jobdb.GoalFailed, jobdb.GoalSucceeded:
			mode = "new"
		default:
			return Session{}, ErrUnavailable
		}
	}
	allowed := awaitScopes(d.Story.Root)
	if d.Story.Status != story.WorkflowStatusCompleted && d.Prepared != nil {
		actionScopes := failedDispatchScopes(d.Story.Root)
		if len(actionScopes) > 0 {
			allowed = actionScopes
		}
	}
	var scope *story.RestartScope
	for i := range d.Story.RestartScopes {
		s := &d.Story.RestartScopes[i]
		if allowed[s.ScopeID] && (scope == nil || s.ChapterOrdinal > scope.ChapterOrdinal) {
			scope = s
		}
	}
	if scope == nil {
		return Session{}, ErrUnavailable
	}
	_, e = c.host.Story.GetRestartContext(ctx, story.GetRestartContextRequest{ProjectID: w.TenantID, JobID: d.Story.JobID, ScopeID: scope.ScopeID})
	if e != nil {
		return Session{}, fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	req := story.RestartRecipeJobRequest{ProjectID: w.TenantID, JobID: d.Story.JobID, ScopeID: scope.ScopeID, RequestID: identity("reopen", w.TenantID, d.Args.SessionID, d.Story.JobID, scope.ScopeID)}
	req.Goal.Mode = mode
	r, e := c.host.Story.RestartRecipeJob(ctx, req)
	if e != nil {
		return Session{}, e
	}
	return Session{ID: d.Args.SessionID, JobID: r.JobID, GoalID: r.GoalID, Status: "submitted", Source: d.Args.Source, Limits: c.cfg.Limits}, nil
}
func awaitScopes(root *story.JobRunStoryNode) map[string]bool {
	out := map[string]bool{}
	var walk func(*story.JobRunStoryNode)
	var hasPath func(*story.JobRunStoryNode) bool
	hasPath = func(n *story.JobRunStoryNode) bool {
		if n == nil {
			return false
		}
		if taskRolePath(n.Path, "await-human") {
			return true
		}
		for _, ch := range n.Children {
			if hasPath(ch) {
				return true
			}
		}
		return false
	}
	walk = func(n *story.JobRunStoryNode) {
		if n == nil {
			return
		}
		if n.Kind == "op" && n.OpID == "input" && n.RestartScopeID != "" && hasPath(n) {
			out[n.RestartScopeID] = true
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(root)
	return out
}

// Recovery of an ambiguous action resumes dispatch with its recorded prepared
// input, rather than rewinding confirmation and choosing a new timestamp.
func failedDispatchScopes(root *story.JobRunStoryNode) map[string]bool {
	out := map[string]bool{}
	var pathMatch func(*story.JobRunStoryNode) bool
	pathMatch = func(n *story.JobRunStoryNode) bool {
		if n == nil {
			return false
		}
		if taskRolePath(n.Path, "dispatch") {
			return true
		}
		for _, ch := range n.Children {
			if pathMatch(ch) {
				return true
			}
		}
		return false
	}
	var walk func(*story.JobRunStoryNode)
	walk = func(n *story.JobRunStoryNode) {
		if n == nil {
			return
		}
		if n.Kind == "op" && (n.OpID == ActivityName || n.OpID == "./ops/assistant") && n.Status != "succeeded" && n.RestartScopeID != "" && pathMatch(n) {
			out[n.RestartScopeID] = true
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(root)
	return out
}
func describe(name string, r artifacts.Ref) Artifact {
	size := int64(0)
	if r.Stored != nil {
		size = r.Stored.Key.SizeBytes
	}
	return Artifact{ID: identity("artifact", r.Name, string(mustJSON(r))), Ref: r, Name: name, MIME: mime.TypeByExtension(filepath.Ext(name)), Size: size, Available: true}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func (c *Client) readRef(ctx context.Context, w Caller, r artifacts.Ref) ([]byte, error) {
	if r.Kind != artifacts.RefKindStored || r.Validate() != nil {
		return nil, ErrInvalid
	}
	if e := c.authorize(ctx, w, r.Stored.Key.JobId); e != nil {
		return nil, e
	}
	a := c.host.Control.GetArtifactLazy(ctx, w.TenantID, r.Stored.Key)
	if a == nil {
		return nil, ErrUnavailable
	}
	reader, e := a.Open()
	if e != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	defer reader.Close()
	b, e := io.ReadAll(io.LimitReader(reader, int64(c.cfg.Limits.File)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > c.cfg.Limits.File || int64(len(b)) != r.Stored.Key.SizeBytes {
		return nil, ErrLimit
	}
	return b, nil
}
func (c *Client) checkRefs(ctx context.Context, w Caller, refs map[string]artifacts.Ref) error {
	for _, r := range refs {
		if _, e := c.readRef(ctx, w, r); e != nil {
			return e
		}
	}
	return nil
}
func (c *Client) OpenArtifact(ctx context.Context, w Caller, id, aid string) (ArtifactReader, error) {
	d, e := c.resolve(ctx, w, id)
	if e != nil {
		return ArtifactReader{}, e
	}
	var candidates []Artifact
	for n, r := range d.Args.Attachments {
		candidates = append(candidates, describe(n, r))
	}
	if d.Capture != nil {
		candidates = append(candidates, d.Capture.Artifacts...)
	}
	for _, en := range d.Entries {
		candidates = append(candidates, en.Artifacts...)
	}
	for _, a := range candidates {
		if a.ID == aid {
			b, e := c.readRef(ctx, w, a.Ref)
			if e != nil {
				return ArtifactReader{}, e
			}
			return ArtifactReader{io.NopCloser(bytes.NewReader(b)), a.Name, a.MIME, int64(len(b))}, nil
		}
	}
	return ArtifactReader{}, ErrNotFound
}
