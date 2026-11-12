package assistant

import (
	"errors"
	"github.com/colony-2/c2j/pkg/input"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/story"
)

func TestFrozenV2Descriptor(t *testing.T) {
	c, s, _, _ := client(t)
	oldCommit := strings.Repeat("2", 40)
	descriptor := ReleaseDescriptor{
		Recipe: "git+https://github.com/colony-2/cortex-assistant.git//session.yaml@" + oldCommit,
		Commit: oldCommit, Protocol: "cortex-assistant/2",
	}
	c.cfg.LegacyReleases = []ReleaseDescriptor{descriptor}
	if err := validateLegacy(c.cfg); err != nil {
		t.Fatal(err)
	}
	r := fixture(c)
	r.Recipe.Source.SubmittedSelector = descriptor.Recipe
	r.Recipe.Source.ResolvedCommit = descriptor.Commit
	r.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "recorded v2 reply")}
	s.jobs["job"] = r
	d, err := c.project(r)
	if err != nil || len(d.Entries) != 2 || d.Entries[1].Text != "recorded v2 reply" {
		t.Fatalf("frozen reply lost: data=%+v err=%v", d, err)
	}
	if _, err = c.Reopen(ctx, human, "job"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read descriptor enabled execution: %v", err)
	}
	if len(s.restartRequests) != 0 {
		t.Fatal("read descriptor restarted execution")
	}
	r.Recipe.Source.ResolvedCommit = commit
	if _, err = c.project(r); !errors.Is(err, ErrForbidden) {
		t.Fatalf("substituted bytes accepted: %v", err)
	}
}

func TestLegacyDescriptorIdentity(t *testing.T) {
	base := ReleaseDescriptor{Recipe: selector, Commit: commit, Protocol: "cortex-assistant/1"}
	for _, protocol := range []string{"cortex-assistant/1", "cortex-assistant/2"} {
		r := base
		r.Protocol = protocol
		if err := validateLegacy(Config{LegacyReleases: []ReleaseDescriptor{r}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*ReleaseDescriptor){
		func(r *ReleaseDescriptor) { r.Protocol = "cortex-assistant/99" },
		func(r *ReleaseDescriptor) { r.Commit = strings.Repeat("2", 40) },
		func(r *ReleaseDescriptor) {
			r.Recipe = "git+https://example.invalid/cortex-assistant.git//session.yaml@" + commit
		},
		func(r *ReleaseDescriptor) { r.Recipe = "prefix" + selector },
	} {
		r := base
		change(&r)
		if err := validateLegacy(Config{LegacyReleases: []ReleaseDescriptor{r}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid descriptor accepted: %+v err=%v", r, err)
		}
	}
	if err := validateLegacy(Config{Recipe: selector, LegacyReleases: []ReleaseDescriptor{base}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("current release also accepted as legacy")
	}
	if err := validateLegacy(Config{LegacyReleases: []ReleaseDescriptor{base, base}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate descriptor accepted")
	}
}

// A current host explicitly approves each historical wire format. None becomes
// a goal-identified v4 session and read-only descriptors enable no new execution.
func TestGoalHostExplicitLegacyMappings(t *testing.T) {
	current, _, _ := ordinaryFacade(t)
	current.cfg.Protocol = GoalProtocol
	current.cfg.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit
	current.cfg.LegacyReleases = nil
	mappings := []map[string]any{}
	for i, protocol := range []string{"cortex-assistant/1", Protocol, OrdinaryProtocol} {
		t.Run(protocol, func(t *testing.T) {
			release := ReleaseDescriptor{Protocol: protocol, Commit: strings.Repeat(string(rune('3'+i)), 40)}
			release.Recipe = legacySelector(protocol, release.Commit)
			current.cfg.LegacyReleases = append(current.cfg.LegacyReleases, release)
			if err := validateLegacy(current.cfg); err != nil {
				t.Fatal(err)
			}
			record := fixture(current)
			record.GoalID = "historical-goal"
			record.Recipe.Source.SubmittedSelector, record.Recipe.Source.ResolvedCommit = release.Recipe, release.Commit
			args := record.Root.Input.(StartArgs)
			args.Protocol = protocol
			record.Root.Input = args
			record.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, "historical reply")}
			if protocol == "cortex-assistant/1" {
				for _, n := range record.Root.Children {
					n.OpID = "./ops/assistant"
				}
			}
			if protocol == OrdinaryProtocol {
				frozen := FrozenSnapshot{TenantID: human.TenantID, Source: Source{Kind: "global"}, At: time.Unix(100, 0), Documents: []SnapshotDocument{}}
				record.Root.Input = map[string]any{"protocol": protocol, "session_id": "job", "prompt": "hello", "snapshot": frozen}
				capture := OrdinaryResult{Protocol: protocol, Kind: "capture", Origin: Locator{JobID: "job", Invocation: "capture", Path: strings.Split(ordinaryResultPath("capture"), "/")}, SessionID: "job", Prompt: "hello", Snapshot: &frozen}
				response := OrdinaryResult{Protocol: protocol, Kind: "response", Origin: Locator{JobID: "job", Invocation: "reply", Path: strings.Split(ordinaryResultPath("response"), "/")}, Response: &Response{Text: "historical reply"}}
				record.Root.Children = []*story.JobRunStoryNode{node("capture", 1, capture), node("response", 2, response)}
				for _, n := range record.Root.Children {
					n.OpID = "./ops/discussion"
				}
			}
			before := string(mustJSON(record))
			projection, err := current.project(record)
			if err != nil || projection.Args.SessionID != "job" || len(projection.Entries) != 2 || projection.Entries[1].Text != "historical reply" {
				t.Fatal("historical semantics changed", projection, err)
			}
			if string(mustJSON(record)) != before {
				t.Fatal("read migrated historical tasks")
			}
			approved, ok := current.release(release.Recipe)
			if !ok || approved.Reopen {
				t.Fatal("read descriptor granted reopen", approved)
			}
			mappings = append(mappings, map[string]any{"descriptor": release, "session_id": projection.Args.SessionID, "goal_id": projection.Story.GoalID, "history": projection.Entries, "retained_bytes_unchanged": true})
		})
	}
	writeLifecycleCase(t, "legacy-mappings", mappings)
}

// Independent reviewer probes: same retained v2 data through original and v4 hosts.
func TestHistoricalLimitsThroughCurrentHost(t *testing.T) {
	setup := func(t *testing.T) (*Client, *Client, *fixtureStory, *fixtureControl) {
		original, stories, ctl, _ := client(t)
		ordinary, _, _ := ordinaryFacade(t)
		cfg := ordinary.cfg
		cfg.Protocol = GoalProtocol
		cfg.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit
		current, err := New(original.host, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return original, current, stories, ctl
	}
	t.Run("recorded_human_prompt", func(t *testing.T) {
		original, current, stories, _ := setup(t)
		record := fixture(original)
		record.Root.Children = []*story.JobRunStoryNode{captureNode(), inputNode(2, Command{Command: "send", Prompt: strings.Repeat("a", 20<<10)})}
		stories.jobs["job"] = record
		before, err := original.Replay(ctx, human, "job", PageRequest{})
		if err != nil || before.Entries[1].Role != "user" {
			t.Fatal("invalid baseline", before, err)
		}
		after, err := current.Replay(ctx, human, "job", PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("original role=%s bytes=%d; current role=%s bytes=%d error=%q", before.Entries[1].Role, len(before.Entries[1].Text), after.Entries[1].Role, len(after.Entries[1].Text), after.Entries[1].Error)
		if after.Entries[1].Role != "user" || after.Entries[1].Text != before.Entries[1].Text {
			t.Fatal("valid historical message was reinterpreted")
		}
	})
	t.Run("stored_download", func(t *testing.T) {
		original, current, stories, ctl := setup(t)
		record := fixture(original)
		args := record.Root.Input.(StartArgs)
		body := []byte(strings.Repeat("a", 20<<10))
		stored := ref("legacy.txt", int64(len(body)))
		args.Attachments["legacy.txt"] = stored
		record.Root.Input = args
		stories.jobs["job"] = record
		ctl.files["legacy.txt"] = body
		aid := describe("legacy.txt", stored).ID
		before, err := original.OpenArtifact(ctx, human, "job", aid)
		if err != nil {
			t.Fatal("invalid baseline", err)
		}
		before.Close()
		after, err := current.OpenArtifact(ctx, human, "job", aid)
		if err == nil {
			after.Close()
		}
		t.Logf("retained bytes=%d; original download=success; current error=%v", len(body), err)
		if err != nil {
			t.Fatal("available historical bytes rejected", err)
		}
	})
	t.Run("pending_prompt_submission", func(t *testing.T) {
		original, current, _, _ := setup(t)
		in := input.StructuredSubmission{RequestID: "original-request", SubmissionID: "send", Response: Command{Command: "send", Prompt: strings.Repeat("a", 20<<10)}}
		if _, _, err := original.freezeSubmissionForProtocol(ctx, human, Protocol, in, nil); err != nil {
			t.Fatal("invalid baseline", err)
		}
		_, _, err := current.freezeSubmissionForProtocol(ctx, human, Protocol, in, nil)
		t.Logf("protocol=%s prompt bytes=%d; original accepted; current error=%v", Protocol, 20<<10, err)
		if err != nil {
			t.Fatal("historical submission limits replaced", err)
		}
	})
	t.Run("escaped_recorded_reply", func(t *testing.T) {
		original, current, stories, _ := setup(t)
		record := fixture(original)
		record.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, strings.Repeat("<", 40<<10))}
		stories.jobs["job"] = record
		before, err := original.Replay(ctx, human, "job", PageRequest{})
		if err != nil {
			t.Fatal("invalid baseline", err)
		}
		_, err = current.Replay(ctx, human, "job", PageRequest{})
		t.Logf("original reply bytes=%d encoded page bytes=%d; current error=%v", 40<<10, len(mustJSON(before)), err)
		if err != nil {
			t.Fatal("readable historical reply rejected", err)
		}
	})
}

// Contract boundaries use authenticated legacy records. The synthetic page
// exercises the transport policy independently of individual reply validation.
func TestHistoricalLimitBoundaries(t *testing.T) {
	original, stories, ctl, _ := client(t)
	current, _, _ := ordinaryFacade(t)
	current.cfg.Protocol = GoalProtocol
	current.cfg.Recipe = "git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@" + commit
	current, err := New(original.host, current.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{DefaultLimits().Human, DefaultLimits().Human + 1} {
		in := input.StructuredSubmission{RequestID: "legacy", SubmissionID: "boundary", Response: Command{Command: "send", Prompt: strings.Repeat("a", size)}}
		_, _, err := current.freezeSubmissionForProtocol(ctx, human, Protocol, in, nil)
		if (err == nil) != (size == DefaultLimits().Human) || err != nil && !errors.Is(err, ErrLimit) {
			t.Fatalf("human bytes=%d: %v", size, err)
		}
		record := fixture(original)
		record.Root.Children = []*story.JobRunStoryNode{captureNode(), inputNode(2, in.Response.(Command))}
		stories.jobs["job"] = record
		page, err := current.Replay(ctx, human, "job", PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if (page.Entries[1].Role == "user") != (size == DefaultLimits().Human) {
			t.Fatalf("human replay boundary %d: %s", size, page.Entries[1].Role)
		}
	}
	for _, size := range []int{DefaultLimits().File, DefaultLimits().File + 1} {
		record := fixture(original)
		args := record.Root.Input.(StartArgs)
		stored := ref("legacy.txt", int64(size))
		args.Attachments["legacy.txt"] = stored
		record.Root.Input = args
		stories.jobs["job"] = record
		ctl.files["legacy.txt"] = []byte(strings.Repeat("f", size))
		body, err := current.OpenArtifact(ctx, human, "job", describe("legacy.txt", stored).ID)
		if (err == nil) != (size == DefaultLimits().File) || err != nil && !errors.Is(err, ErrLimit) {
			t.Fatalf("file bytes=%d: %v", size, err)
		}
		if err == nil {
			got, readErr := io.ReadAll(body)
			body.Close()
			if readErr != nil || string(got) != string(ctl.files["legacy.txt"]) {
				t.Fatal("download changed", readErr)
			}
		}
	}
	record := fixture(original)
	record.Root.Children = []*story.JobRunStoryNode{captureNode(), responseNode(2, strings.Repeat("<", DefaultLimits().Assistant))}
	stories.jobs["job"] = record
	page, err := current.Replay(ctx, human, "job", PageRequest{})
	if err != nil || page.Entries[1].Text != strings.Repeat("<", DefaultLimits().Assistant) {
		t.Fatal("escaped historical reply boundary", err)
	}
	for _, size := range []int{DefaultLimits().Assistant, DefaultLimits().Assistant + 1} {
		model := ModelOutput{Response: string(mustJSON(ModelResult{Text: strings.Repeat("<", size), Documents: []Document{}, ReturnArtifactIDs: []string{}}))}
		_, _, err := current.ValidateResponse(ctx, human, "job", Locator{JobID: "job", Task: 2}, model)
		if (err == nil) != (size == DefaultLimits().Assistant) || err != nil && !errors.Is(err, ErrLimit) {
			t.Fatalf("escaped response bytes=%d: %v", size, err)
		}
	}

	d, err := current.project(record)
	if err != nil {
		t.Fatal(err)
	}
	d.Entries = []Entry{{Role: "assistant", Status: "succeeded", Origin: Locator{JobID: "job", Task: 2}, Text: "<"}}
	page, err = current.page(human, d, PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	d.Entries[0].Text += strings.Repeat("a", DefaultLimits().Page-len(mustJSON(page)))
	page, err = current.page(human, d, PageRequest{})
	if err != nil || len(mustJSON(page)) != DefaultLimits().Page {
		t.Fatal("exact legacy page", len(mustJSON(page)), err)
	}
	d.Entries[0].Text += "a"
	if _, err = current.page(human, d, PageRequest{}); !errors.Is(err, ErrLimit) {
		t.Fatal("legacy page excess", err)
	}
	if current.cfg.Limits != OrdinaryLimits() {
		t.Fatal("historical operation changed current policy")
	}
	writeLifecycleCase(t, "legacy-limit-boundaries", map[string]any{"human_exact_bytes": DefaultLimits().Human, "file_exact_bytes": DefaultLimits().File, "escaped_reply_bytes": DefaultLimits().Assistant, "page_exact_bytes": DefaultLimits().Page, "excess_rejected": true, "current_limits_unchanged": true})
}

func TestHistoricalSameProtocolLimits(t *testing.T) {
	current, stories, _, _ := client(t)
	current.cfg.Limits.Human = 16 << 10
	oldCommit := strings.Repeat("2", 40)
	release := ReleaseDescriptor{Recipe: legacySelector(Protocol, oldCommit), Commit: oldCommit, Protocol: Protocol}
	current.cfg.LegacyReleases = []ReleaseDescriptor{release}
	record := fixture(current)
	record.Root.Children = []*story.JobRunStoryNode{captureNode(), inputNode(2, Command{Command: "send", Prompt: strings.Repeat("a", 20<<10)})}
	stories.jobs["job"] = record
	primary, err := current.Replay(ctx, human, "job", PageRequest{})
	if err != nil || primary.Entries[1].Role != "invalid-input" {
		t.Fatal("preferred configured cap ignored", err)
	}
	record.Recipe.Source.SubmittedSelector = release.Recipe
	record.Recipe.Source.ResolvedCommit = release.Commit
	historical, err := current.Replay(ctx, human, "job", PageRequest{})
	if err != nil || historical.Entries[1].Role != "user" || len(historical.Entries[1].Text) != 20<<10 {
		t.Fatal("same-protocol historical contract tightened", err)
	}
	d, err := current.project(record)
	if err != nil {
		t.Fatal(err)
	}
	in := input.StructuredSubmission{RequestID: "pending", SubmissionID: "same-protocol", Response: Command{Command: "send", Prompt: strings.Repeat("a", 20<<10)}}
	if _, _, err = current.forExecution(d).freezeSubmissionForProtocol(ctx, human, Protocol, in, nil); err != nil {
		t.Fatal("same-protocol pending policy", err)
	}
	if current.cfg.Limits.Human != 16<<10 {
		t.Fatal("preferred limits mutated")
	}
}
