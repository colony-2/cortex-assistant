package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/executionruntime"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobdbschema"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/c2j/pkg/jobref"
	coreops "github.com/colony-2/c2j/pkg/ops"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/template/colonycel"
	"github.com/colony-2/c2j/pkg/worker/compiler"
	workerops "github.com/colony-2/c2j/pkg/worker/ops"
	workerworkflow "github.com/colony-2/c2j/pkg/worker/workflow"
	"github.com/colony-2/jobdb/pkg/jobdb"
	sqliteruntime "github.com/colony-2/jobdb/pkg/jobdb/runtime/sqlite"
	jobworkflow "github.com/colony-2/jobdb/pkg/workflow"
)

// The runtime accepts the real operation; only its caller-visible receipt is lost.
type lostGoalReceiptSubmitter struct {
	submitter interface {
		SubmitJobRef(context.Context, jobdb.SubmitJob) (jobref.Ref, error)
	}
	accepted jobref.Ref
}

func (s *lostGoalReceiptSubmitter) SubmitJobRef(ctx context.Context, job jobdb.SubmitJob) (jobref.Ref, error) {
	ref, err := s.submitter.SubmitJobRef(ctx, job)
	if err != nil {
		return ref, err
	}
	s.accepted = ref
	return jobref.Ref{}, errors.New("fixture transport lost accepted receipt")
}

func exerciseGoalLifecycle(t *testing.T, ctx context.Context, facade *Client, caller Caller, old jobref.Ref, snapshot FrozenSnapshot, rt *sqliteruntime.Runtime, scoped *executionruntime.Runtime, registry *workerops.ActivityRegistry, withMedia bool, stop func(), run func(jobworkflow.Engine)) {
	t.Helper()
	ctx = jobdb.WithPrimaryGoalSelectionAuthorizer(ctx, func(_ context.Context, goal jobdb.GoalKey, target jobdb.ResolvedCell) error {
		if goal.TenantId != caller.TenantID || goal.GoalID != old.GoalID || target.Cell != "root" {
			return jobdb.ErrPrimaryGoalSelectionForbidden
		}
		return nil
	})
	original, err := facade.Get(ctx, caller, old.GoalID)
	if err != nil || original.ID != old.GoalID || original.Current == nil {
		t.Fatal("initial goal session", original, err)
	}
	if _, err = facade.PrepareReopen(ctx, caller, old.JobID, "too-early", time.Now()); !errors.Is(err, ErrNotAtPrompt) {
		t.Fatal("reopen before cancellation", err)
	}
	for {
		state, e := facade.Close(ctx, caller, old.JobID)
		if e != nil {
			t.Fatal(e)
		}
		if state.Terminal {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}
	frozen, err := facade.PrepareReopen(ctx, caller, old.JobID, "sequential-reopen", time.Unix(200, 0))
	if err != nil {
		t.Fatal("prepare sequential reopen", err)
	}
	var ctl *workerworkflow.SWFWorkflowControl
	workerReplacements := 0
	replaceWorker := func() {
		stop()
		replacement, err := jobworkflow.NewEngineBuilder().WithRuntime(scoped).WithWorkerTenantId("tenant").BuildEngine()
		if err != nil {
			t.Fatal(err)
		}
		wrapped := jobdbschema.WorkflowEngine{Engine: replacement, Registry: rt}
		ctl = &workerworkflow.SWFWorkflowControl{Engine: wrapped}
		workers, err := compiler.NewRecipeWorkerWithOptions(coreops.NewServiceDepsBuilder().WithWorkflowControl(ctl).Build(), registry, compiler.RecipeJobWorkerOptions{RootSourceResolver: compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{}), Allocation: scoped.Allocation, WrapTaskWorker: scoped.WrapTaskWorker, StageExecution: scoped.Stage})
		if err != nil {
			t.Fatal(err)
		}
		if err = replacement.RegisterWorkers(workers); err != nil {
			t.Fatal(err)
		}
		inputs, err := input.NewRuntime(ctl, nil)
		if err != nil {
			t.Fatal(err)
		}
		stories, err := story.New(story.ServiceConfig{Engine: wrapped, SchemaRegistry: rt, RootSourceResolver: compiler.NewRecipeSourceResolver(compiler.RecipeSourceResolverOptions{}), CELOptionsProvider: colonycel.NewBuilder(colonycel.Options{})})
		if err != nil {
			t.Fatal(err)
		}
		host := facade.host
		host.Control, host.History, host.Submitter, host.Input, host.Story = ctl, wrapped, wrapped, inputs, stories
		cfg := facade.cfg
		bindings := *cfg.Bindings
		fixtureURL := bindings.BaseURL
		if strings.HasPrefix(fixtureURL, "http://127.0.0.1:") {
			bindings.BaseURL = "https://fixture.invalid/exact/root"
			cfg.Bindings = &bindings
		}
		facade, err = New(host, cfg)
		if err != nil {
			t.Fatal(err)
		}
		facade.cfg.Bindings.BaseURL = fixtureURL
		run(replacement)
		workerReplacements++
	}
	replaceWorker()
	next, err := facade.SubmitFrozenStart(ctx, caller, frozen)
	if err != nil || next.ID != old.GoalID || next.GoalID != old.GoalID || next.JobID == old.JobID {
		t.Fatal("fresh same-goal receipt", next, err)
	}
	retry, err := facade.SubmitFrozenStart(ctx, caller, frozen)
	if err != nil || retry.JobID != next.JobID {
		t.Fatal("reopen retry", retry, err)
	}
	var ready Session
	for ctx.Err() == nil {
		ready, err = facade.Get(ctx, caller, old.GoalID)
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal("recovered goal history", err)
			}
			state, e := ctl.InspectJob(ctx, jobdb.JobKey{TenantId: "tenant", JobId: next.JobID})
			if e == nil && state.Terminal {
				t.Fatal("reopen terminated", state, err)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if ready.JobID == next.JobID && ready.Current != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if string(mustJSON(ready.Snapshot)) != string(mustJSON(snapshot)) {
		t.Fatal("reopen recaptured source")
	}
	for i, entry := range original.Replay.Entries {
		if i >= len(ready.Replay.Entries) || string(mustJSON(entry)) != string(mustJSON(ready.Replay.Entries[i])) {
			t.Fatal("original task origins changed")
		}
	}
	downloads := []map[string]any{}
	for _, entry := range original.Replay.Entries {
		for _, artifact := range entry.Artifacts {
			reader, e := facade.OpenArtifact(ctx, caller, old.GoalID, artifact.ID)
			if e != nil {
				t.Fatal("retained download", e)
			}
			body, e := io.ReadAll(reader)
			closeErr := reader.Close()
			if e != nil || closeErr != nil || string(body) != "generated document bytes" {
				t.Fatal("download changed after replacement", e, closeErr)
			}
			downloads = append(downloads, map[string]any{"artifact": artifact, "sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "size": len(body)})
		}
	}
	api := &jobinspect.Service{Runtime: rt, Tenant: "tenant"}
	records, err := readOrdinaryRecordsForProtocol(ctx, api, next.JobID, GoalProtocol)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.result != nil && (r.result.Kind == "assemble" || r.result.Kind == "response") {
			t.Fatal("reopen replayed inference")
		}
	}
	if _, err = facade.Close(ctx, caller, old.JobID); err != nil {
		t.Fatal("delayed captured close", err)
	}
	state, err := ctl.InspectJob(ctx, jobdb.JobKey{TenantId: "tenant", JobId: next.JobID})
	if err != nil || state.Terminal {
		t.Fatal("delayed close cancelled replacement", state, err)
	}
	request := *ready.Current.Form.RequestId
	media := map[string][]byte{}
	response := map[string]any{"command": "send", "prompt": "new prompt after sequential reopen"}
	if withMedia {
		// Opaque container headers prove byte transport, not playback or understanding.
		media = map[string][]byte{"plain.txt": []byte("retained upload text 世界"), "pixel.png": {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, "clip.mp4": []byte("\x00\x00\x00\x18ftypisom"), "sound.wav": []byte("RIFFfixtureWAVE")}
		files := map[string]any{}
		for name, body := range media {
			files[name] = jobdb.NewArtifactFromBytes(name, body)
		}
		response["attachments"] = files
	}
	submission := input.StructuredSubmission{RequestID: request, SubmissionID: "after-reopen", Response: response}
	var receipt input.Output
	concurrentOutcomes := []map[string]any{}
	ambiguousRecovery := map[string]any{}
	if withMedia {
		type sendResult struct {
			submission input.StructuredSubmission
			output     input.Output
			err        error
		}
		start := make(chan struct{})
		results := make(chan sendResult, 2)
		for _, id := range []string{"after-reopen", "competing-send"} {
			candidate := submission
			candidate.SubmissionID = id
			go func() {
				<-start
				output, e := facade.Respond(ctx, caller, old.GoalID, candidate)
				results <- sendResult{candidate, output, e}
			}()
		}
		close(start)
		accepted := 0
		for range 2 {
			result := <-results
			outcome := map[string]any{"submission_id": result.submission.SubmissionID, "accepted": result.err == nil}
			if result.err == nil {
				accepted++
				submission, receipt = result.submission, result.output
				outcome["receipt"] = result.output.Receipt
			} else {
				if !errors.Is(result.err, ErrStale) && !errors.Is(result.err, ErrNotAtPrompt) && !errors.Is(result.err, input.ErrInputNotPending) {
					t.Fatal("unexpected competing send outcome", result.err)
				}
				outcome["error"] = result.err.Error()
			}
			concurrentOutcomes = append(concurrentOutcomes, outcome)
		}
		if accepted != 1 {
			t.Fatalf("concurrent sends accepted %d operations", accepted)
		}
	} else {
		receipt, err = facade.Respond(ctx, caller, old.GoalID, submission)
		if err != nil {
			t.Fatal("send after reopen", err)
		}
	}
	if receipt.Receipt == nil {
		t.Fatal("send missing authoritative receipt")
	}
	for ctx.Err() == nil {
		ready, err = facade.Get(ctx, caller, old.GoalID)
		if err != nil {
			t.Fatal(err)
		}
		if ready.Current != nil && *ready.Current.Form.RequestId != request {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	retried, err := facade.Respond(ctx, caller, old.GoalID, submission)
	if err != nil || string(mustJSON(retried)) != string(mustJSON(receipt)) {
		t.Fatal("reopen send lost authoritative receipt", err)
	}
	if withMedia {
		found := false
		for i := len(ready.Replay.Entries) - 1; i >= 0; i-- {
			entry := ready.Replay.Entries[i]
			if entry.Role != "assistant" {
				continue
			}
			if len(entry.Artifacts) != 4 {
				t.Fatalf("model did not return authorized media and generated document: %+v", entry)
			}
			found = true
			break
		}
		if !found {
			t.Fatal("missing assistant media response")
		}
		changedFiles := map[string]any{}
		for name, body := range media {
			changedFiles[name] = jobdb.NewArtifactFromBytes(name, body)
		}
		changedFiles["plain.txt"] = jobdb.NewArtifactFromBytes("plain.txt", []byte("changed"))
		changed := input.StructuredSubmission{RequestID: request, SubmissionID: submission.SubmissionID, Response: map[string]any{"command": "send", "prompt": "new prompt after sequential reopen", "attachments": changedFiles}}
		if _, e := facade.Respond(ctx, caller, old.GoalID, changed); !errors.Is(e, ErrConflict) {
			t.Fatal("changed upload retry accepted", e)
		}
	}
	records, err = readOrdinaryRecordsForProtocol(ctx, api, next.JobID, GoalProtocol)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range records {
		if r.result != nil {
			counts[r.result.Kind]++
		}
	}
	if counts["capture"] != 1 || counts["assemble"] != 1 || counts["response"] != 1 || counts["accept"] != 1 {
		t.Fatal("replayed work", counts)
	}
	if _, err = facade.Close(ctx, caller, next.JobID); err != nil {
		t.Fatal(err)
	}
	if withMedia {
		operation, e := facade.PrepareReopen(ctx, caller, next.JobID, "reopen-with-media", time.Unix(300, 0))
		if e != nil {
			t.Fatal(e)
		}
		// Persist the frozen host operation before submission. Lose a receipt
		// after real SQLite acceptance, then recover through a new worker.
		retainedWire := mustJSON(operation)
		lost := &lostGoalReceiptSubmitter{submitter: facade.host.Submitter}
		facade.host.Submitter = lost
		if _, e = facade.SubmitFrozenStart(ctx, caller, operation); e == nil || lost.accepted.JobID == "" {
			t.Fatal("lost receipt did not follow acceptance", e)
		}
		ambiguousRecovery["accepted"] = lost.accepted
		ambiguousRecovery["frozen_sha256"] = fmt.Sprintf("%x", sha256.Sum256(retainedWire))
		// Replace the worker again after the upload was durably accepted.
		// Rebuild all collaborators; only runtime-managed storage survives.
		replaceWorker()
		operation = FrozenStart{}
		if e = json.Unmarshal(retainedWire, &operation); e != nil {
			t.Fatal(e)
		}
		third, e := facade.SubmitFrozenStart(ctx, caller, operation)
		if e != nil {
			t.Fatal(e)
		}
		if third.JobID != lost.accepted.JobID || third.GoalID != lost.accepted.GoalID {
			t.Fatal("uncertain retry allocated new identity", third, lost.accepted)
		}
		ambiguousRecovery["reconciled"] = third
		for ctx.Err() == nil {
			ready, e = facade.Get(ctx, caller, old.GoalID)
			if e == nil && ready.JobID == third.JobID && ready.Current != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		thirdRequest := *ready.Current.Form.RequestId
		_, e = facade.Respond(ctx, caller, old.GoalID, input.StructuredSubmission{RequestID: thirdRequest, SubmissionID: "retained-media-followup", Response: map[string]any{"command": "send", "prompt": "follow up with retained media context"}})
		if e != nil {
			t.Fatal(e)
		}
		for ctx.Err() == nil {
			ready, e = facade.Get(ctx, caller, old.GoalID)
			if e == nil && ready.Current != nil && *ready.Current.Form.RequestId != thirdRequest {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		seen := map[string]bool{}
		for _, entry := range ready.Replay.Entries {
			for _, a := range entry.Artifacts {
				want, ok := media[a.Name]
				if !ok || seen[a.Name] {
					continue
				}
				reader, e := facade.OpenArtifact(ctx, caller, old.GoalID, a.ID)
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(reader)
				closeErr := reader.Close()
				if e != nil || closeErr != nil || string(body) != string(want) {
					t.Fatal("retained media download changed", a.Name, e, closeErr)
				}
				downloads = append(downloads, map[string]any{"artifact": a, "sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "size": len(body), "origin": entry.Origin})
				seen[a.Name] = true
			}
		}
		if len(seen) != len(media) {
			t.Fatalf("lost media: %v", seen)
		}
		if _, e = facade.Close(ctx, caller, third.JobID); e != nil {
			t.Fatal(e)
		}
	}
	if directory := os.Getenv("ASSISTANT_GOAL_EVIDENCE"); directory != "" {
		if err = os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		evidence := map[string]any{"ambiguous_submit_recovery": ambiguousRecovery, "concurrent_send_outcomes": concurrentOutcomes, "worker_replacements": workerReplacements, "original": original, "reopened": ready, "receipt": receipt, "old_job": old, "new_job": next, "counts": counts, "downloads": downloads, "fixture_boundary": "Real ordinary resolved helper and inference package; provider success uses trusted loopback HTTP fixture, missing-secret case records a safe error. See paired provider request evidence when present."}
		if err = os.WriteFile(filepath.Join(directory, "sequential-reopen.json"), mustJSON(evidence), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
