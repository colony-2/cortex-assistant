package assistant

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/jobref"
)

// Remove actual fixture storage, not a facade response. The otherwise unchanged
// runtime must preserve the distinction between retained metadata and lost bytes.
func exerciseActualRetentionLoss(t *testing.T, ctx context.Context, c *Client, w Caller, key jobref.Ref, dbPath, blobDir string) {
	t.Helper()
	session, err := c.Get(ctx, w, key.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	var artifact Artifact
	for _, e := range session.Replay.Entries {
		if e.Role == "assistant" && len(e.Artifacts) > 0 {
			artifact = e.Artifacts[0]
		}
	}
	if artifact.ID == "" {
		t.Fatal("missing generated document")
	}
	r, err := c.OpenArtifact(ctx, w, key.GoalID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(body) != "generated document bytes" {
		t.Fatal("initial bytes", err)
	}
	removed := 0
	err = filepath.WalkDir(blobDir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if string(b) == string(body) {
			removed++
			return os.Remove(path)
		}
		return nil
	})
	if err != nil || removed == 0 {
		t.Fatal("did not remove stored bytes", removed, err)
	}
	if _, err = c.OpenArtifact(ctx, w, key.GoalID, artifact.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing download invented bytes", err)
	}
	retained, err := c.Get(ctx, w, key.GoalID)
	if err != nil {
		t.Fatal("metadata should remain inspectable", err)
	}
	page, err := c.Replay(ctx, w, key.GoalID, PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for {
		state, e := c.Close(ctx, w, key.JobID)
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
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, "DELETE FROM jobdb_chapter_chapters WHERE story_id = ? AND ordinal = ?", key.JobID, artifact.Ref.Stored.Key.TaskOrdinal)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatal("did not remove retained chapter", count, err)
	}
	if _, err = c.Replay(ctx, w, key.GoalID, PageRequest{Cursor: page.Cursor}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing history became empty/successful", err)
	}
	if _, err = c.PrepareReopen(ctx, w, key.JobID, "missing-history", time.Unix(1000, 0)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing history allowed reopen", err)
	}
	writeLifecycleCase(t, "actual-retention-loss", map[string]any{"artifact": artifact, "download_before_loss": string(body), "removed_blob_files": removed, "metadata_after_byte_loss": retained.Replay, "removed_chapters": count, "download_outcome": "unavailable", "replay_outcome": "unavailable", "reopen_outcome": "unavailable", "fixture_boundary": "Actual fixture blob deletion and SQLite chapter deletion; no production records touched, no response substitution."})
}
