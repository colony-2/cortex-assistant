package assistant

import (
	"context"
	"encoding/json"
	"io"
	"reflect"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// freezeSubmission bounds and snapshots Go uploads before passing them to c2j's
// ordinary artifact binder. No persistent artifact key is invented here.
func (c *Client) freezeSubmission(ctx context.Context, w Caller, in input.StructuredSubmission, p *Proposal) (input.StructuredSubmission, Command, error) {
	var cmd Command
	raw, ok := in.Response.(map[string]any)
	if !ok {
		var e error
		cmd, e = c.command(in.Response, p)
		if e == nil && cmd.Command == "send" {
			var normalized map[string]any
			if e = decode(in.Response, &normalized); e != nil {
				return in, cmd, e
			}
			if cmd.Attachments == nil {
				cmd.Attachments = map[string]artifacts.Ref{}
			}
			normalized["attachments"] = cmd.Attachments
			in.Response = normalized
		}
		return in, cmd, e
	}
	copyResponse := map[string]any{}
	for k, v := range raw {
		copyResponse[k] = v
	}
	checked := map[string]any{}
	for k, v := range raw {
		checked[k] = v
	}
	wire := map[string]any{}
	for k, v := range raw {
		wire[k] = v
	}
	var refs = map[string]artifacts.Ref{}
	uploads := map[string]any{}
	wireFiles := map[string]any{}
	total := int64(0)
	count := 0
	if value, exists := raw["attachments"]; exists {
		if raw["command"] != "send" {
			return in, cmd, ErrInvalid
		}
		files, ok := value.(map[string]any)
		if !ok {
			cmd, e := c.command(in.Response, p)
			return in, cmd, e
		}
		for name, v := range files {
			if !safeName(name) {
				return in, cmd, ErrInvalid
			}
			if a, ok := v.(jobdb.Artifact); ok {
				if a == nil || (reflect.ValueOf(a).Kind() == reflect.Ptr && reflect.ValueOf(a).IsNil()) || !safeName(a.Name()) || name != a.Name() {
					return in, cmd, ErrInvalid
				}
				r, e := a.Open()
				if e != nil {
					return in, cmd, e
				}
				b, e := io.ReadAll(io.LimitReader(r, int64(c.cfg.Limits.File)+1))
				_ = r.Close()
				if e != nil {
					return in, cmd, e
				}
				if len(b) > c.cfg.Limits.File || (a.Size() >= 0 && a.Size() != int64(len(b))) {
					return in, cmd, ErrLimit
				}
				total += int64(len(b))
				count++
				uploads[name] = jobdb.NewArtifactFromBytes(name, b)
				wireFiles[name] = map[string]any{"name": name, "data": b}
			} else {
				var ref artifacts.Ref
				if decode(v, &ref) != nil {
					return in, cmd, ErrInvalid
				}
				refs[name] = ref
				uploads[name] = v
				wireFiles[name] = v
			}
		}
		checked["attachments"] = refs
		copyResponse["attachments"] = uploads
		wire["attachments"] = wireFiles
	}
	var e error
	cmd, e = c.command(checked, p)
	if e != nil {
		return in, cmd, e
	}
	for name, r := range in.ArtifactRefs {
		if _, exists := uploads[name]; exists {
			if old, ok := refs[name]; !ok || string(mustJSON(old)) != string(mustJSON(r)) {
				return in, cmd, ErrConflict
			}
		}
		refs[name] = r
	}
	if e = c.validateRefs(refs); e != nil {
		return in, cmd, e
	}
	for _, r := range refs {
		total += r.Stored.Key.SizeBytes
		count++
	}
	if count > c.cfg.Limits.Count || total > int64(c.cfg.Limits.Aggregate) {
		return in, cmd, ErrLimit
	}
	if e = c.checkRefs(ctx, w, refs); e != nil {
		return in, cmd, e
	}
	encoded := in
	encoded.Response = wire
	b, e := json.Marshal(encoded)
	if e != nil {
		return in, cmd, e
	}
	if len(b) > c.cfg.Limits.Upload {
		return in, cmd, ErrLimit
	}
	in.Response = copyResponse
	return in, cmd, nil
}
