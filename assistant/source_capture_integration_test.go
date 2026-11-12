package assistant

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/cellref"
	"github.com/colony-2/c2j/pkg/contextual"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	"github.com/colony-2/c2j/pkg/recipe"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/starter"
	"github.com/colony-2/c2j/pkg/workflowctl"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func TestGoalExactReviewSourceRuntime(t *testing.T) { runOrdinaryWorker(t, "goal-review") }

func installOrdinaryReviewFixture(t *testing.T, repo string) {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	head, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../recipe-tests/ordinary-capture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("Actual child review 世界 text\n", 120) + "child-source-tail-after-1000"
	model := string(mustJSON(map[string]any{"text": "source producer", "documents": []any{map[string]any{"name": "first", "mime": "text/plain", "text": text}, map[string]any{"name": "second", "mime": "application/json", "text": `{"independent":"actual child JSON"}`}}, "return_artifact_ids": []any{}, "citations": []any{}}))
	// Candidate mode keeps the helper at C, separate from the unrelated source
	// producer fixture. Only document generation is canned; inference is actual.
	content := strings.ReplaceAll(string(raw), "./ops/discussion", "git+https://github.com/colony-2/cortex-assistant.git//ops/discussion@"+sourceFixtureCommit(strings.TrimSpace(string(head))))
	content = strings.ReplaceAll(content, `'{"text":"grounded","documents":[],"return_artifact_ids":[],"citations":[]}'`, "'"+model+"'")
	content = strings.Replace(content, "      outputs:\n        response:", "      outputs:\n        document_refs: '${{ sequence.response.artifacts }}'\n        response:", 1)
	content += "  document_refs: '${{ states.infer.outputs.document_refs }}'\n"
	if err = os.MkdirAll(filepath.Join(repo, ".c2/recipes"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(repo, ".c2/recipes/source-producer.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func exerciseOrdinaryReviewSource(t *testing.T, ctx context.Context, c *Client, w Caller, wrapped jobdbschema.WorkflowEngine, originalJob string) {
	t.Helper()
	raw := `id: review-page
schema_version: 2
sequence:
  - id: child
    op: recipe.run_and_get_result
    inputs:
      name: source-producer
      cell_name: root
  - id: review
    op: input
    inputs:
      form:
        kind: review
        title: Exact source review
        documents:
          first: '${{ sequence.child.outputs.outputs.document_refs.first }}'
          second: '${{ sequence.child.outputs.outputs.document_refs.second }}' 
        fields:
          - {id: decision, type: short_answer, question: Decision, required: true}
  - id: replacement
    op: input
    inputs:
      form:
        kind: review
        title: Replacement source review
        documents:
          first: '${{ sequence.child.outputs.outputs.document_refs.first }}'
          second: '${{ sequence.child.outputs.outputs.document_refs.second }}' 
        fields:
          - {id: decision, type: short_answer, question: Decision, required: true}
outputs: {}
`
	page, err := recipe.LoadRecipeFromString([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := starter.StartRecipeJob(ctx, workflowctl.StartJob{TenantId: w.TenantID, Cell: "root", CellRepo: "github.com/example/root", RecipeName: "review-page", GitRef: "main", JobContext: contextual.JobContext{Workflow: contextual.WorkflowContext{CellName: "root"}, GitBase: contextual.GitBaseContext{BaseRepo: "https://github.com/example/root.git", BaseRef: "main", GitAuthor: "Review Fixture <fixture@example.invalid>"}, CellResolution: &cellref.Context{SelfCell: "root", SelfRepo: "github.com/example/root"}}}, wrapped, *page)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(job, previous string) *input.UserInputDetails {
		for ctx.Err() == nil {
			d, e := c.host.Input.GetDetails(ctx, w.TenantID, job)
			if e == nil && d.Form.RequestId != nil && *d.Form.RequestId != previous {
				return d
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("source prompt", ctx.Err())
		return nil
	}
	form := wait(ref.JobID, "")
	source := Source{Kind: "review", JobID: ref.JobID, RequestID: *form.Form.RequestId}
	captured, err := c.Capture(ctx, w, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Artifacts) != 2 {
		t.Fatalf("child artifacts: %+v", captured)
	}
	frozen := FrozenSnapshot{TenantID: w.TenantID, Source: source, At: captured.At, Documents: []SnapshotDocument{}}
	hashes := map[string]string{}
	for _, a := range captured.Artifacts {
		reader := c.host.Control.GetArtifactLazy(ctx, w.TenantID, a.Ref.Stored.Key)
		r, e := reader.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		ce := r.Close()
		if e != nil || ce != nil {
			t.Fatal(e, ce)
		}
		mime := "text/plain"
		if a.Name == "second" {
			mime = "application/json"
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(b))
		hashes[a.Name] = hash
		if a.Ref.Stored.Key.JobId == ref.JobID || a.Ref.Stored.Key.JobId == originalJob {
			t.Fatal("review child provenance substituted")
		}
		frozen.Documents = append(frozen.Documents, SnapshotDocument{ID: a.Name, Producer: Locator{JobID: a.Ref.Stored.Key.JobId, Task: a.Ref.Stored.Key.TaskOrdinal}, Ref: &a.Ref, MIME: mime, Outcome: "included", Text: string(b), SHA256: hash, Size: int64(len(b))})
	}
	// Exercise every declared coverage outcome with actual source references.
	// The denied read crosses the host authorization boundary; the unavailable
	// read targets a missing artifact in the actual SQLite runtime.
	base := frozen.Documents[0]
	for _, d := range frozen.Documents {
		if d.ID == "first" {
			base = d
		}
	}
	denied := *c
	denied.host.Authorize = func(context.Context, Caller, string) error { return ErrForbidden }
	if e := denied.authorize(ctx, w, base.Producer.JobID); !errors.Is(e, ErrForbidden) {
		t.Fatal("denied producer", e)
	}
	for _, outcome := range []string{"denied", "unavailable", "unsupported", "budget_omitted", "truncated"} {
		d := base
		d.ID = outcome
		d.Outcome = outcome
		d.Text = ""
		d.SHA256 = ""
		if outcome == "unavailable" {
			missing := *base.Ref
			stored := *missing.Stored
			stored.Key.Name = "expired-review-source"
			missing.Stored = &stored
			d.Ref = &missing
			if r, e := c.host.Control.GetArtifactLazy(ctx, w.TenantID, stored.Key).Open(); e == nil {
				r.Close()
				t.Fatal("missing source unexpectedly opened")
			}
		}
		if outcome == "unsupported" {
			d.MIME = "application/octet-stream"
		}
		if outcome == "truncated" {
			d.Text = clipped(base.Text, 2001)
			d.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(d.Text)))
		}
		frozen.Documents = append(frozen.Documents, d)
	}
	children, e := recipejob.ListChildRecipeJobs(ctx, controlLister{c.host.Control}, recipejob.ListChildRecipeJobsRequest{TenantID: w.TenantID, ParentTenantID: w.TenantID, ParentJobID: ref.JobID, AllParentInvocations: true, Stores: []jobdb.JobStore{jobdb.JobStoreActive, jobdb.JobStoreArchived}})
	if e != nil || len(children.Jobs) != 1 || children.Jobs[0].JobID != base.Producer.JobID {
		t.Fatal("original review child", children, e)
	}
	selected := snapshotSelection(frozen)
	in := StartInput{RequestID: "actual-review", SubmittedAt: frozen.At, Prompt: "Discuss the exact selected review", Source: source}
	next, err := c.StartFrozen(ctx, w, in, frozen, selected)
	if err != nil {
		t.Fatal(err)
	}
	wait(next.JobID, "")
	got, err := c.Get(ctx, w, next.ID)
	if err != nil || got.Snapshot == nil || string(mustJSON(got.Snapshot)) != string(mustJSON(frozen)) {
		t.Fatal("frozen source", err)
	}
	for _, outcome := range []string{"denied", "unavailable", "unsupported", "budget_omitted", "truncated"} {
		if got.Coverage[outcome] != outcome {
			t.Fatal("coverage substituted", outcome, got.Coverage)
		}
	}
	if _, err = c.host.Input.SubmitFormResponse(ctx, w.TenantID, ref.JobID, input.FormSubmission{RequestID: source.RequestID, SubmissionID: "replace-review", Fields: map[string]any{"decision": "continue"}}, w.Actor); err != nil {
		t.Fatal(err)
	}
	fresh := wait(ref.JobID, source.RequestID)
	if _, err = c.Capture(ctx, w, source); !errors.Is(err, ErrStale) {
		t.Fatal("replaced review accepted", err)
	}
	changed := selected
	changed.Source.RequestID = *fresh.Form.RequestId
	if _, err = c.PrepareStartFrozen(ctx, w, in, frozen, changed); !errors.Is(err, ErrForbidden) {
		t.Fatal("substituted exact request", err)
	}
	for _, mutation := range []func(*FrozenSnapshot){func(s *FrozenSnapshot) { s.TenantID = "foreign" }, func(s *FrozenSnapshot) { s.Documents[0].Producer.JobID = ref.JobID }, func(s *FrozenSnapshot) {
		s.Documents[0].Ref = func() *artifacts.Ref {
			r := artifacts.NewExternalRef("arbitrary", "https://unselected.invalid", false)
			return &r
		}()
	}, func(s *FrozenSnapshot) { s.Documents = append(s.Documents, s.Documents[0]) }} {
		var forged FrozenSnapshot
		if decode(frozen, &forged) != nil {
			t.Fatal("clone")
		}
		mutation(&forged)
		if _, err = c.PrepareStartFrozen(ctx, w, in, forged, selected); err == nil {
			t.Fatal("forged source accepted")
		}
	}
	retained, err := c.Get(ctx, w, next.ID)
	if err != nil || string(mustJSON(retained.Snapshot)) != string(mustJSON(frozen)) {
		t.Fatal("replacement recaptured source", err)
	}
	writeLifecycleCase(t, "actual-review-source", map[string]any{"source_job": ref, "exact_request": source.RequestID, "replacement_request": *fresh.Form.RequestId, "frozen_snapshot": frozen, "actual_children": children.Jobs, "coverage": got.Coverage, "download_sha256": hashes, "history": got.Replay, "stale_capture_rejected": true, "forged_selection_rejected": true, "fixture_boundary": "Actual ordinary child helper materialization, review input forms and SQLite artifacts. Trusted test capture freezes selected refs; Cortex HTTP/browser is not exercised. Assistant uses actual resolved immutable inference and deterministic HTTP provider."})
	if _, err = c.Close(ctx, w, next.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Close(ctx, w, originalJob); err != nil {
		t.Fatal(err)
	}
	if err = c.host.Control.Cancel(ctx, ref.Key()); err != nil {
		t.Fatal(err)
	}
}
