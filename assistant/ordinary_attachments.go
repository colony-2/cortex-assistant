package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// Attachment bytes remain in runtime storage. The model receives bounded text
// and explicit inclusion outcomes; opaque media is described but never read.
func ordinaryAttachmentPrompt(ctx context.Context, api jobinspect.API, record ordinaryRecord, cmd Command, budget int) (string, error) {
	names := make([]string, 0, len(cmd.Attachments))
	for name := range cmd.Attachments {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		ref := cmd.Attachments[name]
		bound := false
		for _, retained := range record.human.ArtifactRefs {
			if string(mustJSON(retained)) == string(mustJSON(ref)) {
				bound = true
				break
			}
		}
		if !bound {
			return "", ErrUnavailable
		}
		a := describe(name, ref)
		manifest := map[string]any{"artifact": a, "outcome": "unsupported", "producer_input_task": record.task.ID, "producer_attempt": record.task.Attempt}
		mime := strings.Split(a.MIME, ";")[0]
		if mime == "text/plain" || mime == "text/markdown" || mime == "application/json" {
			manifest["outcome"] = "unavailable"
			reader, err := api.Download(ctx, jobinspect.Request{JobID: record.job, Reference: &ref.Stored.Key})
			if err == nil {
				body, e := io.ReadAll(io.LimitReader(reader, int64(OrdinaryLimits().File)+1))
				closeErr := reader.Close()
				if e == nil && closeErr == nil && int64(len(body)) == a.Size && len(body) <= OrdinaryLimits().File {
					manifest["sha256"] = fmt.Sprintf("%x", sha256.Sum256(body))
					manifest["outcome"] = "unsupported"
					if utf8.Valid(body) && (mime != "application/json" || json.Valid(body)) {
						manifest["outcome"] = "included"
						manifest["text"] = string(body)
					}
				}
			}
		}
		if len(mustJSON(manifest))+len("ATTACHMENT \n")+out.Len() > budget {
			delete(manifest, "text")
			if manifest["outcome"] == "included" {
				manifest["outcome"] = "budget-omitted"
			}
		}
		out.WriteString("ATTACHMENT ")
		out.Write(mustJSON(manifest))
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// Compare a frozen upload to the actual recorded bytes after the prompt has
// advanced. A matching submission ID alone cannot make changed bytes a retry.
func (c *Client) reconcileGoalAttachments(ctx context.Context, w Caller, frozen input.StructuredSubmission, out input.Output, requested *Command, recorded Command) error {
	var raw map[string]any
	if value, ok := frozen.Response.(map[string]any); ok {
		raw = value
	} else if decode(frozen.Response, &raw) != nil {
		return ErrConflict
	}
	files := map[string]any{}
	if value, present := raw["attachments"]; present {
		if values, ok := value.(map[string]any); ok {
			files = values
		} else {
			var refs map[string]artifacts.Ref
			if decode(value, &refs) != nil {
				return ErrConflict
			}
			for name, ref := range refs {
				files[name] = ref
			}
		}
	}
	if len(files) != len(recorded.Attachments) {
		return ErrConflict
	}
	for name, value := range files {
		ref, ok := recorded.Attachments[name]
		if !ok {
			return ErrConflict
		}
		bound := false
		for _, r := range out.ArtifactRefs {
			if string(mustJSON(r)) == string(mustJSON(ref)) {
				bound = true
				break
			}
		}
		if !bound {
			return ErrUnavailable
		}
		if upload, ok := value.(jobdb.Artifact); ok {
			reader, err := upload.Open()
			if err != nil {
				return ErrUnavailable
			}
			body, err := io.ReadAll(io.LimitReader(reader, int64(c.cfg.Limits.File)+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				return ErrUnavailable
			}
			retained, err := c.readRef(ctx, w, ref)
			if err != nil {
				return err
			}
			if sha256.Sum256(body) != sha256.Sum256(retained) || len(body) != len(retained) {
				return ErrConflict
			}
		} else {
			var supplied artifacts.Ref
			if decode(value, &supplied) != nil || string(mustJSON(supplied)) != string(mustJSON(ref)) {
				return ErrConflict
			}
		}
	}
	requested.Attachments = recorded.Attachments
	return nil
}
