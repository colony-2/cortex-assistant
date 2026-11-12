package assistant

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	recipeops "github.com/colony-2/c2j/pkg/ops/recipe"
	"github.com/colony-2/c2j/pkg/story"
)

// TaskResult is the actual result of a helper invocation, never accumulated history.
type TaskResult struct {
	Kind        string         `json:"kind"`
	Origin      Locator        `json:"origin"`
	Capture     *Capture       `json:"capture,omitempty"`
	Response    *Response      `json:"response,omitempty"`
	Prepared    *Prepared      `json:"prepared,omitempty"`
	Destination string         `json:"destination,omitempty"`
	Error       string         `json:"error,omitempty"`
	Form        *input.Config  `json:"form,omitempty"`
	Command     *Command       `json:"command,omitempty"`
	Omitted     []Locator      `json:"omitted,omitempty"`
	ModelInput  map[string]any `json:"model_input,omitempty"`
}
type replayData struct {
	ContextJob    string
	OpeningPrompt string
	Previous      string
	Snapshot      *FrozenSnapshot
	Coverage      map[string]string
	Ancestors     []string
	Story         *story.JobRunStory
	Args          StartArgs
	Entries       []Entry
	Capture       *Capture
	Prepared      *Prepared
	// Accepted inputs are a transient projection of actual task outputs, used to
	// reconcile a frozen retry after its waiting task has already completed.
	Inputs []input.Output
}

func (c *Client) read(ctx context.Context, w Caller, job string) (*replayData, error) {
	return c.readVisited(ctx, w, job, map[string]bool{})
}
func (c *Client) readVisited(ctx context.Context, w Caller, job string, visited map[string]bool) (*replayData, error) {
	if visited[job] || len(visited) >= 256 {
		return nil, ErrUnavailable
	}
	visited[job] = true
	if e := c.authorize(ctx, w, job); e != nil {
		return nil, e
	}
	rows, e := c.rootJobs(ctx, w, []string{job}, "")
	if e != nil {
		return nil, e
	}
	if len(rows.Jobs) == 0 {
		return nil, ErrNotFound
	}
	if len(rows.Jobs) != 1 || rows.Jobs[0].JobID != job || !c.approved(rows.Jobs[0].RecipeName) {
		return nil, ErrForbidden
	}
	s, e := c.host.Story.GetJobRunStory(ctx, story.GetJobRunStoryRequest{ProjectID: w.TenantID, JobID: job})
	if e != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	if s != nil && s.Root == nil {
		release, ok := c.release(rows.Jobs[0].RecipeName)
		if ok && release.Protocol == GoalProtocol {
			s, e = c.projectUnstarted(ctx, w, job, release)
			if e != nil {
				return nil, e
			}
		}
	}
	d, e := c.project(s)
	if e != nil {
		return nil, e
	}
	if d.Snapshot != nil && d.Snapshot.TenantID != w.TenantID {
		return nil, ErrForbidden
	}
	c.reconcileFailedDispatches(ctx, w, d)
	if e = c.bindLineage(ctx, w, d, visited); e != nil {
		return nil, e
	}
	return d, nil
}
func (c *Client) project(s *story.JobRunStory) (*replayData, error) {
	if s == nil || s.Root == nil || s.JobID == "" {
		return nil, ErrUnavailable
	}
	release, ok := c.release(s.Recipe.Source.SubmittedSelector)
	if !ok || s.Recipe.ID != RecipeID || (s.Recipe.Source.ResolvedCommit != release.Commit && !(s.Recipe.Source.Kind == "retainedSubmission" && s.Recipe.Source.ResolvedCommit == "" && release.Protocol == GoalProtocol)) {
		return nil, ErrForbidden
	}
	var args StartArgs
	var startSnapshot *FrozenSnapshot
	rawArgs := map[string]any{}
	if e := decode(s.Root.Input, &rawArgs); e != nil {
		return nil, e
	}
	if ref, ok, e := artifacts.ParseValue(rawArgs["source"]); ok {
		if e != nil {
			return nil, e
		}
		var source any
		if json.Unmarshal(ref.Value, &source) != nil {
			return nil, ErrInvalid
		}
		rawArgs["source"] = source
	}
	if ordinaryProtocol(release.Protocol) {
		var frozen FrozenSnapshot
		if decode(rawArgs["snapshot"], &frozen) != nil || ValidateFrozenSnapshot(frozen, snapshotSelection(frozen)) != nil {
			return nil, ErrInvalid
		}
		args.Protocol, _ = rawArgs["protocol"].(string)
		args.SessionID, _ = rawArgs["session_id"].(string)
		args.Prompt, _ = rawArgs["prompt"].(string)
		args.Source = frozen.Source
		if release.Protocol == GoalProtocol {
			args.SessionID = s.GoalID
		}
		startSnapshot = &frozen
	} else if e := decode(rawArgs, &args); e != nil {
		return nil, e
	}
	if args.Protocol != release.Protocol || args.SessionID == "" {
		return nil, ErrInvalid
	}
	if e := args.Source.validate(); e != nil {
		return nil, e
	}
	d := &replayData{Story: s, Args: args, Snapshot: startSnapshot}
	c = c.forExecution(d)
	if release.Protocol == GoalProtocol {
		d.ContextJob, _ = rawArgs["prior_job"].(string)
		d.Previous, _ = rawArgs["previous_job"].(string)
		if _, ok := rawArgs["previous_job"]; !ok {
			d.Previous = d.ContextJob
		}
		d.OpeningPrompt = args.Prompt
		if d.Previous == "" && args.Prompt == "" {
			return nil, ErrInvalid
		}
	}
	initial := Entry{Origin: Locator{JobID: args.SessionID, Task: 0}, Role: "user", Status: "succeeded", Text: args.Prompt}
	for name, ref := range args.Attachments {
		initial.Artifacts = append(initial.Artifacts, describe(name, ref))
	}
	sort.Slice(initial.Artifacts, func(i, j int) bool { return initial.Artifacts[i].ID < initial.Artifacts[j].ID })
	if release.Protocol == GoalProtocol {
		initial.Origin.JobID = s.JobID
	}
	if args.Prompt != "" && (release.Protocol != GoalProtocol || d.Previous == "") {
		d.Entries = append(d.Entries, initial)
	}
	type item struct {
		n    *story.JobRunStoryNode
		role string
	}
	var nodes []item
	var walk func(*story.JobRunStoryNode, string)
	walk = func(n *story.JobRunStoryNode, selector string) {
		if n == nil {
			return
		}
		if n.Kind == "op" {
			selector = n.OpID
		}
		if (n.Kind == "opStep" || (n.Kind == "op" && len(n.Children) == 0)) && n.TaskOrdinal != nil && len(n.Path) >= 4 {
			role := n.Path[3]
			expected := ActivityName
			if release.Protocol == "cortex-assistant/1" {
				expected = "./ops/assistant"
			}
			if ordinaryProtocol(release.Protocol) {
				expected = "./ops/discussion"
			}
			if role == "dispatch" && release.Protocol == GoalProtocol {
				expected = "recipes.run"
			}
			if role == "await-human" {
				expected = "input"
			}
			if (role != "assemble" || ordinaryProtocol(release.Protocol)) && selector == expected && taskRolePath(n.Path, role) && (role != "await-human" || n.StepID == "collect_user_input") {
				nodes = append(nodes, item{n, role})
			}
		}
		for _, ch := range n.Children {
			walk(ch, selector)
		}
	}

	walk(s.Root, "")
	sort.SliceStable(nodes, func(i, j int) bool { return *nodes[i].n.TaskOrdinal < *nodes[j].n.TaskOrdinal })
	seen := map[string]bool{}
	for _, it := range nodes {
		n, role := it.n, it.role
		loc := Locator{JobID: s.JobID, Task: *n.TaskOrdinal, Invocation: n.InvocationID, Path: n.Path}
		if release.Protocol == GoalProtocol {
			loc.Attempt = n.Attempt
		}
		entry := Entry{Origin: loc, Role: role, Status: string(n.Status)}
		if n.Status != "succeeded" {
			if n.Error != nil {
				entry.Error = n.Error.Message
			}
			d.Entries = append(d.Entries, entry)
			continue
		}
		if release.Protocol == GoalProtocol && role == "dispatch" {
			var started recipeops.StartedJobs
			if decode(n.Output, &started) != nil || len(started.Items) != 1 || started.Items[0].JobID == "" || started.Items[0].GoalID == "" || startSnapshot == nil || started.Items[0].TenantID != startSnapshot.TenantID {
				return nil, ErrUnavailable
			}
			entry.Destination = started.Items[0].JobID
			entry.DestinationGoal = started.Items[0].GoalID
			d.Entries = append(d.Entries, entry)
			continue
		}
		if role == "await-human" {
			var out input.Output
			if e := decode(n.Output, &out); e != nil || out.Receipt == nil {
				return nil, ErrUnavailable
			}
			d.Inputs = append(d.Inputs, out)
			cmd, commandErr := c.command(out.Response, c.latestResponse(d).Proposal)
			key := "input/" + out.Receipt.RequestID + "/" + out.Receipt.SubmissionID
			if seen[key] {
				continue
			}
			seen[key] = true
			entry.Receipt = out.Receipt
			if commandErr != nil {
				entry.Role = "invalid-input"
				entry.Error = "Accepted input is invalid; inspect the recorded input for full evidence."
				d.Entries = append(d.Entries, entry)
				continue
			}
			entry.Text = cmd.Prompt
			entry.Role = "user"
			if cmd.Command != "send" {
				entry.Role = cmd.Command
			}
			for name, ref := range cmd.Attachments {
				entry.Artifacts = append(entry.Artifacts, describe(name, ref))
			}
			sort.Slice(entry.Artifacts, func(i, j int) bool { return entry.Artifacts[i].ID < entry.Artifacts[j].ID })
		} else {
			var out TaskResult
			if ordinaryProtocol(release.Protocol) {
				var ordinary OrdinaryResult
				if decode(n.Output, &ordinary) != nil || ordinary.Protocol != release.Protocol || ordinary.Kind != role || strings.Join(ordinary.Origin.Path, "/") != strings.Join(n.Path[:4], "/") {
					return nil, ErrUnavailable
				}
				out = TaskResult{Kind: ordinary.Kind, Origin: ordinary.Origin, Response: ordinary.Response, Form: ordinary.Form, Command: ordinary.Command, ModelInput: ordinary.ModelInput, Destination: ordinary.Destination}
				if role == "capture" {
					if ordinary.PriorJob != d.ContextJob || ordinary.SessionID != args.SessionID || ordinary.Prompt != args.Prompt || ordinary.Snapshot == nil || ordinary.Snapshot.Source != args.Source || string(mustJSON(ordinary.Snapshot)) != string(mustJSON(startSnapshot)) {
						return nil, ErrUnavailable
					}
					frozen := ordinary.Snapshot
					prompt, _, err := SnapshotPrompt(*frozen, snapshotSelection(*frozen), SnapshotMaxBytes)
					if err != nil {
						return nil, err
					}
					out.Capture = &Capture{Source: frozen.Source, At: frozen.At, Text: prompt}
					d.Snapshot = frozen
				}
				if role == "assemble" {
					d.Coverage = ordinary.Coverage
					continue
				}
			} else if e := decode(n.Output, &out); e != nil || out.Kind != role {
				return nil, ErrUnavailable
			}
			if out.Origin.JobID == "" || out.Origin.Invocation == "" {
				return nil, ErrUnavailable
			}
			entry.Origin = out.Origin
			entry.Origin.Task = *n.TaskOrdinal
			if release.Protocol == GoalProtocol {
				entry.Origin.Attempt = n.Attempt
			}
			key := role + "/" + out.Origin.JobID + "/" + out.Origin.Invocation
			if seen[key] {
				continue
			}
			seen[key] = true
			if role == "capture" {
				if d.Capture != nil || out.Capture == nil {
					return nil, ErrUnavailable
				}
				d.Capture = out.Capture
				continue
			}
			if role == "prepare-action" {
				d.Prepared = out.Prepared
				if out.Error == "" {
					continue
				}
				entry.Role = "error"
				entry.Error = out.Error
			}
			if role == "accept" {
				if out.Error == "" {
					continue
				}
				entry.Role = "error"
				entry.Error = out.Error
			}
			if role == "response" {
				if out.Response == nil {
					return nil, ErrUnavailable
				}
				entry.Role = "assistant"
				entry.Text = out.Response.Text
				entry.Citations = append([]string(nil), out.Response.Citations...)
				entry.Artifacts = out.Response.Artifacts
				for _, doc := range out.Response.Documents {
					var found bool
					for _, key := range n.ArtifactKeys {
						if key.Name == doc.Name && key.SizeBytes == doc.Size {
							a := describe(doc.Name, artifacts.NewStoredRef(key))
							a.MIME = doc.MIME
							entry.Artifacts = append(entry.Artifacts, a)
							found = true
							break
						}
					}
					if !found {
						return nil, ErrUnavailable
					}
				}
				entry.Proposal = out.Response.Proposal
				entry.Error = out.Response.Error
			}
			if role == "dispatched" {
				continue
			} // The original recipes.run task above owns the history entry.
			if role == "dispatch" {
				entry.Role = "dispatch"
				entry.Destination = out.Destination
				entry.Error = out.Error
			}
		}
		d.Entries = append(d.Entries, entry)
	}
	return d, nil
}

type cursor struct {
	Kind      string `json:"kind"`
	Tenant    string `json:"tenant"`
	Job       string `json:"job"`
	Session   string `json:"session"`
	End       int    `json:"end"`
	Watermark int    `json:"watermark"`
	Digest    string `json:"digest"`
}

func (c *Client) seal(v cursor) string {
	b, _ := json.Marshal(v)
	m := hmac.New(sha256.New, c.cfg.CursorKey)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (c *Client) unseal(s string) (cursor, error) {
	var v cursor
	if len(s) > 16384 {
		return v, ErrInvalid
	}
	p := strings.Split(s, ".")
	if len(p) != 2 || len(s) > 4096 {
		return v, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return v, ErrInvalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return v, ErrInvalid
	}
	m := hmac.New(sha256.New, c.cfg.CursorKey)
	m.Write(b)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return v, ErrForbidden
	}
	if e := decode(json.RawMessage(b), &v); e != nil {
		return v, e
	}
	return v, nil
}
func digest(entries []Entry) string { b, _ := json.Marshal(entries); return identity(string(b)) }
func completed(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Status == "succeeded" {
			out = append(out, e)
		}
	}
	return out
}
func (c *Client) page(w Caller, d *replayData, p PageRequest) (ReplayPage, error) {
	c = c.forExecution(d)
	entries := completed(d.Entries)
	if d.Args.Protocol == GoalProtocol {
		entries = nil
		for _, e := range d.Entries {
			if e.Status == "succeeded" || e.Status == "failed" || e.Status == "canceled" {
				entries = append(entries, e)
			}
		}
	}
	end, water := len(entries), len(entries)
	hash := digest(entries)
	if p.Cursor != "" {
		v, e := c.unseal(p.Cursor)
		if e != nil {
			return ReplayPage{}, e
		}
		if v.Kind != "replay" || v.Tenant != w.TenantID || (d.Args.Protocol != GoalProtocol && v.Job != d.Story.JobID) || v.Session != d.Args.SessionID {
			return ReplayPage{}, ErrForbidden
		}
		if v.Watermark < 0 || v.Watermark > len(entries) || v.End < 0 || v.End > v.Watermark || digest(entries[:v.Watermark]) != v.Digest {
			return ReplayPage{}, ErrStale
		}
		end, water, hash = v.End, v.Watermark, v.Digest
	}
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	out := ReplayPage{Entries: []Entry{}, Complete: true, Watermark: hash}
	start := end
	for start > 0 && len(out.Entries) < limit {
		candidate := append([]Entry{entries[start-1]}, out.Entries...)
		test := out
		test.Entries = candidate
		test.HasMore = start > 1
		if test.HasMore {
			test.Cursor = c.seal(cursor{Kind: "replay", Tenant: w.TenantID, Job: d.Story.JobID, Session: d.Args.SessionID, End: start - 1, Watermark: water, Digest: hash})
		}
		raw, e := json.Marshal(test)
		if e != nil {
			return out, e
		}
		if len(raw) > c.cfg.Limits.Page {
			if len(out.Entries) == 0 {
				return out, ErrLimit
			}
			break
		}
		out.Entries = candidate
		start--
	}
	out.HasMore = start > 0
	if out.HasMore {
		out.Cursor = c.seal(cursor{Kind: "replay", Tenant: w.TenantID, Job: d.Story.JobID, Session: d.Args.SessionID, End: start, Watermark: water, Digest: hash})
	}
	return out, nil
}
func (c *Client) Replay(ctx context.Context, w Caller, id string, p PageRequest) (ReplayPage, error) {
	d, e := c.resolve(ctx, w, id)
	if e != nil {
		return ReplayPage{}, e
	}
	return c.page(w, d, p)
}

func taskRolePath(path []string, role string) bool {
	state := map[string]string{"capture": "capture", "assemble": "infer", "response": "infer", "await-human": "prompt", "accept": "prompt", "prepare-action": "action", "dispatch": "action", "dispatched": "action"}[role]
	if state == "" || len(path) < 4 || len(path) > 5 {
		return false
	}
	if path[0] != RecipeID || path[1] != state || path[2] != "sequence" || path[3] != role {
		return false
	}
	return len(path) == 4 || strings.HasPrefix(path[4], "step:")
}
