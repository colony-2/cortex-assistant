package turncheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobinspect"
)

type history struct {
	tasks  []jobinspect.Task
	values map[string]any
	err    error
}

func (h history) Inspect(_ context.Context, r jobinspect.Request) (*jobinspect.Response, error) {
	if h.err != nil {
		return nil, h.err
	}
	if r.Task != "" {
		b, e := json.Marshal(h.values[r.Task])
		return &jobinspect.Response{Value: b}, e
	}
	return &jobinspect.Response{Job: &jobinspect.Job{ID: r.JobID}, Tasks: h.tasks}, nil
}
func (history) Download(context.Context, jobinspect.Request) (io.ReadCloser, error) {
	return nil, errors.New("unexpected download")
}
func recordedTurn() (history, Turn, []any) {
	receipt := &input.Receipt{RequestID: "request", SubmissionID: "send", Actor: input.Actor{Kind: "human", ID: "person"}}
	turn := Turn{JobID: "job", Protocol: "cortex-assistant/4", Receipt: receipt}
	h := history{values: map[string]any{}}
	add := func(id, kind string, v any) {
		at := time.Unix(int64(len(h.tasks)), 0)
		typ := "extension_execution:extension_execution"
		if kind == "await-human" {
			typ = "input:collect_user_input"
		}
		h.tasks = append(h.tasks, jobinspect.Task{ID: id, Type: typ, Path: "cortex-assistant-session/prompt/sequence/" + kind, Attempt: 1, Status: "succeeded", StartedAt: &at})
		h.values[id] = v
	}
	add("input", "await-human", input.Output{Receipt: receipt})
	resultAt := func(kind string) map[string]any {
		return map[string]any{"protocol": turn.Protocol, "kind": kind, "origin": Origin{JobID: "job", Invocation: kind, Path: strings.Split("cortex-assistant-session/prompt/sequence/"+kind, "/")}}
	}
	accept := resultAt("accept")
	accept["receipt"] = receipt
	add("accept", "accept", accept)
	response := resultAt("response")
	origin := response["origin"].(Origin)
	origin.Path = strings.Split("cortex-assistant-session/infer/sequence/response", "/")
	response["origin"] = origin
	reply := Reply{Text: "own validated reply", Citations: []string{"source"}, Artifacts: []json.RawMessage{json.RawMessage(`{"id":"artifact","name":"document"}`)}}
	response["response"] = reply
	add("response", "response", response)
	h.tasks[2].Path = strings.Join(origin.Path, "/")
	form := resultAt("form")
	form["form"] = map[string]any{"request": map[string]any{"assistant": reply}, "response_schema": map[string]any{"type": "object"}}
	add("form", "form", form)
	replay := []any{map[string]any{"origin": func() Origin { o := origin; o.Attempt = 1; o.Task = 3; return o }(), "role": "assistant", "status": "succeeded", "text": reply.Text, "citations": reply.Citations, "artifacts": reply.Artifacts}}
	return h, turn, replay
}
func TestIndependentTurnEvidence(t *testing.T) {
	h, turn, replay := recordedTurn()
	p, e := Check(context.Background(), h, turn, replay)
	if e != nil || p.InputTask.ID != "input" || p.AcceptTask.ID != "accept" || p.FormTask.ID != "form" {
		t.Fatal(p, e)
	}
	for _, name := range []string{"earlier", "empty", "errored", "foreign-job", "origin", "artifact", "unaccepted", "missing-form", "read-error", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			h, turn, replay := recordedTurn()
			switch name {
			case "earlier":
				h.tasks = h.tasks[1:]
				turn.Receipt.SubmissionID = "other"
			case "empty":
				h.values["response"].(map[string]any)["response"] = Reply{}
			case "errored":
				h.values["response"].(map[string]any)["response"] = Reply{Text: "safe error", Error: "invalid"}
			case "foreign-job":
				turn.JobID = "other"
			case "origin":
				replay[0].(map[string]any)["origin"] = Origin{JobID: "job", Invocation: "earlier"}
			case "artifact":
				replay[0].(map[string]any)["artifacts"] = []any{}
			case "unaccepted":
				delete(h.values["accept"].(map[string]any), "receipt")
			case "missing-form":
				h.tasks = h.tasks[:2]
			case "read-error":
				h.err = io.ErrUnexpectedEOF
			case "cancelled":
				h.err = context.Canceled
			}
			_, err := Check(context.Background(), h, turn, replay)
			if err == nil {
				t.Fatal("invalid turn accepted")
			}
			missing := name == "empty" || name == "errored" || name == "unaccepted"
			if errors.Is(err, ErrMissingValidReply) != missing {
				t.Fatal("wrong failure category", err)
			}
			if missing && err.Error() != "missing valid reply in job job after request/send" {
				t.Fatal(err)
			}
			if h.err != nil && !errors.Is(err, h.err) {
				t.Fatal("lost read error", err)
			}
		})
	}
}
