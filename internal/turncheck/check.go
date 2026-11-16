// Package turncheck checks independent recorded turns. It deliberately has no
// assistant dependency so the production repository and external harness share it.
package turncheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobinspect"
)

var ErrMissingValidReply = errors.New("missing valid reply")

type Turn struct {
	JobID    string         `json:"job_id"`
	Protocol string         `json:"protocol"`
	Receipt  *input.Receipt `json:"receipt,omitempty"`
	Opening  bool           `json:"opening"`
}
type Origin struct {
	JobID      string   `json:"job_id"`
	Invocation string   `json:"invocation"`
	Path       []string `json:"path"`
	Task       int64    `json:"task"`
	Attempt    int      `json:"attempt"`
}
type Document struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}
type Reply struct {
	Documents []Document        `json:"documents,omitempty"`
	Text      string            `json:"text"`
	Error     string            `json:"error,omitempty"`
	Citations []string          `json:"citations,omitempty"`
	Artifacts []json.RawMessage `json:"artifacts,omitempty"`
}
type Evidence struct {
	Turn         Turn             `json:"turn"`
	InputTask    *jobinspect.Task `json:"input_task,omitempty"`
	AcceptTask   *jobinspect.Task `json:"accept_task,omitempty"`
	ResponseTask jobinspect.Task  `json:"response_task"`
	FormTask     jobinspect.Task  `json:"form_task"`
	Origin       Origin           `json:"origin"`
	Reply        Reply            `json:"reply"`
	Replay       json.RawMessage  `json:"replay"`
}
type result struct {
	Protocol string         `json:"protocol"`
	Kind     string         `json:"kind"`
	Origin   Origin         `json:"origin"`
	Response *Reply         `json:"response"`
	Receipt  *input.Receipt `json:"receipt"`
	Form     *struct {
		Request struct {
			Assistant Reply `json:"assistant"`
		} `json:"request"`
		ResponseSchema map[string]any `json:"response_schema"`
	} `json:"form"`
}

// Check requires the first form after this turn's input to follow this turn's
// validated response. Earlier jobs/replies cannot satisfy a missing reply.
// replay must encode the facade's complete []Entry, including paged entries.
func Check(ctx context.Context, api jobinspect.API, turn Turn, replay any) (Evidence, error) {
	var proof Evidence
	proof.Turn = turn
	if api == nil || turn.JobID == "" || turn.Protocol == "" || turn.Opening == (turn.Receipt != nil) {
		return proof, errors.New("invalid turn boundary")
	}
	if turn.Receipt != nil && (turn.Receipt.RequestID == "" || turn.Receipt.SubmissionID == "" || (turn.Receipt.Actor.Kind != "human" && turn.Receipt.Actor.Kind != "user")) {
		return proof, errors.New("invalid human receipt")
	}
	var tasks []jobinspect.Task
	seen := map[string]bool{}
	for cursor := ""; ; {
		page, err := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: turn.JobID, Cursor: cursor, Limit: 100})
		if err != nil {
			return proof, fmt.Errorf("read turn tasks: %w", err)
		}
		if page == nil || page.Job == nil || page.Job.ID != turn.JobID {
			return proof, errors.New("wrong turn job")
		}
		tasks = append(tasks, page.Tasks...)
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return proof, errors.New("repeated task cursor")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	// Inspect returns persisted invocation order; wall-clock timestamps are not ancestry.
	read := func(t jobinspect.Task, v any) error {
		field := ""
		r, e := api.Inspect(ctx, jobinspect.Request{Operation: "log", JobID: turn.JobID, Task: t.ID, Attempt: t.Attempt, Field: &field})
		if e != nil {
			return e
		}
		if r == nil {
			return errors.New("missing task output")
		}
		return json.Unmarshal(r.Value, v)
	}
	boundary, accepted := false, turn.Opening
	var reply *result
	for _, task := range tasks {
		if task.Status != "succeeded" {
			continue
		}
		if task.Path == "cortex-assistant-session/prompt/sequence/await-human" && task.Type == "input:collect_user_input" {
			var v input.Output
			if err := read(task, &v); err != nil {
				return proof, err
			}
			if !boundary && turn.Receipt != nil && reflect.DeepEqual(v.Receipt, turn.Receipt) {
				boundary = true
				copy := task
				proof.InputTask = &copy
				continue
			}
			if boundary && reply == nil {
				break
			}
		}
		if task.Type != "extension_execution:extension_execution" {
			continue
		}
		if task.Path != "cortex-assistant-session/capture/sequence/capture" && task.Path != "cortex-assistant-session/infer/sequence/response" && task.Path != "cortex-assistant-session/prompt/sequence/accept" && task.Path != "cortex-assistant-session/prompt/sequence/form" {
			continue
		}
		if !boundary && !turn.Opening {
			continue
		}
		var r result
		if err := read(task, &r); err != nil {
			return proof, err
		}
		if !strings.HasSuffix(task.Path, "/"+r.Kind) || r.Protocol != turn.Protocol || r.Origin.JobID != turn.JobID || strings.Join(r.Origin.Path, "/") != task.Path || r.Origin.Invocation == "" {
			return proof, errors.New("invalid helper origin")
		}
		if turn.Opening && !boundary && r.Kind == "capture" {
			boundary = true
			continue
		}
		if !boundary {
			continue
		}
		if r.Kind == "accept" && reflect.DeepEqual(r.Receipt, turn.Receipt) {
			accepted = true
			copy := task
			proof.AcceptTask = &copy
		}
		if r.Kind == "response" && accepted && r.Response != nil {
			proof.ResponseTask = task
			proof.Origin = r.Origin
			proof.Reply = *r.Response
		}
		if r.Kind == "response" && accepted && r.Response != nil && r.Response.Error == "" && strings.TrimSpace(r.Response.Text) != "" {
			if reply != nil {
				return proof, errors.New("multiple replies before prompt")
			}
			reply = &r
			proof.ResponseTask = task
			proof.Origin = r.Origin
			proof.Reply = *r.Response
		}
		if r.Kind == "form" {
			proof.FormTask = task
			if reply == nil {
				return proof, missing(turn)
			}
			if r.Form == nil || len(r.Form.ResponseSchema) == 0 || r.Form.Request.Assistant.Text != reply.Response.Text || r.Form.Request.Assistant.Error != "" || !reflect.DeepEqual(r.Form.Request.Assistant.Citations, reply.Response.Citations) || !equalArtifacts(r.Form.Request.Assistant.Artifacts, reply.Response.Artifacts) || !reflect.DeepEqual(r.Form.Request.Assistant.Documents, reply.Response.Documents) {
				return proof, errors.New("reply/form mismatch")
			}
			break
		}
	}
	if !boundary {
		return proof, errors.New("missing accepted input boundary")
	}
	if reply == nil {
		return proof, errors.New("turn has not reached next real form")
	}
	if proof.FormTask.ID == "" {
		return proof, errors.New("missing next real form")
	}
	raw, err := json.Marshal(replay)
	if err != nil {
		return proof, err
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return proof, errors.New("invalid replay")
	}
	for _, raw := range entries {
		var e struct {
			Origin Origin `json:"origin"`
			Role   string `json:"role"`
			Status string `json:"status"`
			Reply
		}
		if json.Unmarshal(raw, &e) != nil {
			return proof, errors.New("invalid replay entry")
		}
		if e.Origin.JobID == turn.JobID && e.Origin.Invocation == reply.Origin.Invocation && e.Role == "assistant" && e.Status == "succeeded" && e.Error == "" && e.Text == reply.Response.Text && reflect.DeepEqual(e.Citations, reply.Response.Citations) && reflect.DeepEqual(e.Origin.Path, reply.Origin.Path) && (e.Origin.Attempt == proof.ResponseTask.Attempt || (turn.Protocol == "cortex-assistant/3" && e.Origin.Attempt == 0)) && matchingArtifacts(e.Artifacts, *reply.Response, proof.ResponseTask) {
			proof.Origin = e.Origin
			proof.Replay = raw
			return proof, nil
		}
	}
	return proof, errors.New("validated reply lacks matching succeeded replay origin")
}
func missing(t Turn) error {
	id := "opening"
	if t.Receipt != nil {
		id = t.Receipt.RequestID + "/" + t.Receipt.SubmissionID
	}
	return fmt.Errorf("%w in job %s after %s", ErrMissingValidReply, t.JobID, id)
}

// Compare decoded objects so insignificant JSON key ordering cannot hide or
// manufacture an artifact identity mismatch.
func equalArtifacts(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var x, y any
		if json.Unmarshal(a[i], &x) != nil || json.Unmarshal(b[i], &y) != nil || !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}

func matchingArtifacts(actual []json.RawMessage, reply Reply, task jobinspect.Task) bool {
	if len(actual) != len(reply.Artifacts)+len(reply.Documents) {
		return false
	}
	if !equalArtifacts(actual[:len(reply.Artifacts)], reply.Artifacts) {
		return false
	}
	for i, doc := range reply.Documents {
		var artifact struct {
			Name string `json:"name"`
			MIME string `json:"mime"`
			Size int64  `json:"size"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(actual[len(reply.Artifacts)+i], &artifact) != nil || artifact.ID == "" || artifact.Name != doc.Name || artifact.MIME != doc.MIME || artifact.Size != doc.Size {
			return false
		}
		stored := false
		for _, a := range task.Artifacts {
			if a.Name == doc.Name && a.Size == doc.Size {
				stored = true
			}
		}
		if !stored {
			return false
		}
	}
	return true
}
