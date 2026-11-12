package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/jobref"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

type uncertainStartSubmitter struct {
	requests [][]byte
	ref      jobref.Ref
}

func (s *uncertainStartSubmitter) SubmitJobRef(_ context.Context, job jobdb.SubmitJob) (jobref.Ref, error) {
	data, err := job.Data.GetData()
	if err != nil {
		return jobref.Ref{}, err
	}
	metadata := job
	metadata.Data = nil
	s.requests = append(s.requests, append(mustJSON(metadata), data...))
	if len(s.requests) == 1 {
		s.ref = jobref.Ref{TenantID: job.TenantId, JobID: job.JobID, GoalID: "recorded-goal"}
		return jobref.Ref{}, errors.New("fixture lost accepted receipt")
	}
	return s.ref, nil
}

func TestFrozenStartSurvivesReplacementAndUncertainReceipt(t *testing.T) {
	c, _, sub := ordinaryFacade(t)
	snapshot := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(100, 0), Documents: []SnapshotDocument{}}
	frozen, err := c.PrepareStartFrozen(ctx, human, StartInput{RequestID: "retained", SubmittedAt: snapshot.At, Prompt: "hello", Source: snapshot.Source}, snapshot, snapshotSelection(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.jobs) != 0 {
		t.Fatal("preparation published a job")
	}
	wire, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	uncertain := &uncertainStartSubmitter{}
	c.host.Submitter = uncertain
	if _, err = c.SubmitFrozenStart(ctx, human, frozen); err == nil {
		t.Fatal("lost receipt reported success")
	}
	// Replacement has different current bindings. Retrying uses the prepared
	// operation, including its original binding, without rebuilding it.
	host, cfg := c.host, c.cfg
	bindings := *cfg.Bindings
	bindings.Model = "replacement-model"
	cfg.Bindings = &bindings
	replacement, err := New(host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored FrozenStart
	if err = json.Unmarshal(wire, &restored); err != nil {
		t.Fatal(err)
	}
	session, err := replacement.SubmitFrozenStart(ctx, human, restored)
	if err != nil || session.JobID != uncertain.ref.JobID || session.GoalID != uncertain.ref.GoalID {
		t.Fatal(session, err)
	}
	if len(uncertain.requests) != 2 || !bytes.Equal(uncertain.requests[0], uncertain.requests[1]) {
		t.Fatal("retry changed prepared request")
	}
	if !bytes.Contains(uncertain.requests[1], []byte(`"model":"fixture"`)) || bytes.Contains(uncertain.requests[1], []byte("replacement-model")) {
		t.Fatal("retry recaptured bindings")
	}
	changed := restored
	changed.Payload = bytes.ReplaceAll(changed.Payload, []byte("hello"), []byte("forged"))
	if _, err = replacement.SubmitFrozenStart(ctx, human, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("modified prepared bytes accepted", err)
	}
	foreign := human
	foreign.Actor.ID = "another-human"
	if _, err = replacement.SubmitFrozenStart(ctx, foreign, restored); !errors.Is(err, ErrForbidden) {
		t.Fatal("caller substitution accepted", err)
	}
	if len(uncertain.requests) != 2 {
		t.Fatal("invalid operation was submitted")
	}
}

// Starts and sends share the v4 encoded-content budget. Check preparation
// before any job is published, including multibyte and escaping expansion.
func TestGoalEncodedOpeningPromptLimit(t *testing.T) {
	c, _, sub := ordinaryFacade(t)
	snapshot := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(100, 0), Documents: []SnapshotDocument{}}
	for _, tc := range []struct {
		name, prompt string
		pass         bool
	}{
		{"ascii-exact", strings.Repeat("a", OrdinaryLimits().Human), true},
		{"ascii-over", strings.Repeat("a", OrdinaryLimits().Human+1), false},
		{"escaped-exact", strings.Repeat("\na", OrdinaryLimits().Human/3) + "a", true},
		{"escaped-over", strings.Repeat("\na", OrdinaryLimits().Human/3) + "aa", false},
		{"html-exact", strings.Repeat("<", OrdinaryLimits().Human/6) + "aaaa", true},
		{"html-over", strings.Repeat("<", OrdinaryLimits().Human/6) + "aaaaa", false},
		{"multibyte-exact", strings.Repeat("界", OrdinaryLimits().Human/3) + "a", true},
		{"multibyte-over", strings.Repeat("界", OrdinaryLimits().Human/3) + "aa", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.cfg.Protocol = GoalProtocol
			in := StartInput{RequestID: tc.name, SubmittedAt: snapshot.At, Prompt: tc.prompt, Source: snapshot.Source}
			frozen, err := c.PrepareStartFrozen(ctx, human, in, snapshot, snapshotSelection(snapshot))
			if (err == nil) != tc.pass {
				t.Fatalf("encoded=%d raw=%d: %v", len(mustJSON(tc.prompt))-2, len(tc.prompt), err)
			}
			if tc.pass && len(frozen.Payload) == 0 {
				t.Fatal("missing prepared operation")
			}
		})
	}
	// Historical v3 accepted escaping expansion within its overall envelope.
	c.cfg.Protocol = OrdinaryProtocol
	if _, err := c.PrepareStartFrozen(ctx, human, StartInput{RequestID: "v3-escaped", SubmittedAt: snapshot.At, Prompt: strings.Repeat("<", OrdinaryLimits().Human), Source: snapshot.Source}, snapshot, snapshotSelection(snapshot)); err != nil {
		t.Fatal("changed v3 start semantics", err)
	}
	if len(sub.jobs) != 0 {
		t.Fatal("preparation published a job")
	}
}
