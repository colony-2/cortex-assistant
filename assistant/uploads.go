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
	// Frozen v3 jobs retain their original unsupported-attachment contract.
	if c.protocol() == OrdinaryProtocol {
		if len(in.ArtifactRefs) != 0 {
			return in, cmd, ErrUnavailable
		}
		if raw, ok := in.Response.(map[string]any); ok {
			if files, present := raw["attachments"]; present {
				value := reflect.ValueOf(files)
				if value.IsValid() && value.Kind() == reflect.Map && value.Len() != 0 {
					return in, cmd, ErrUnavailable
				}
			}
		} else {
			if err := decode(in.Response, &cmd); err != nil {
				return in, cmd, err
			}
			if len(cmd.Attachments) != 0 {
				return in, cmd, ErrUnavailable
			}
		}
	}
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
			if e == nil && cmd.Command == "send" && len(cmd.Attachments) == 0 {
				copyResponse["attachments"] = map[string]any{}
				in.Response = copyResponse
			}
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
	if cmd.Command == "send" {
		// The input schema requires an object even when callers omit files.
		// Normalize a copy so a frozen caller-owned retry remains unchanged.
		if _, exists := copyResponse["attachments"]; !exists {
			copyResponse["attachments"] = map[string]any{}
			wire["attachments"] = map[string]any{}
		}
	}
	for name, r := range in.ArtifactRefs {
		if c.protocol() == GoalProtocol {
			if _, ok := uploads[name]; !ok {
				return in, cmd, ErrInvalid
			}
		}
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

// A current facade may read historical jobs; submission policy follows the
// captured execution contract rather than the facade's preferred new release.
func (c *Client) freezeSubmissionForProtocol(ctx context.Context, w Caller, protocol string, in input.StructuredSubmission, p *Proposal) (input.StructuredSubmission, Command, error) {
	historical := c.forProtocol(protocol)
	frozen, cmd, err := historical.freezeSubmission(ctx, w, in, p)
	if err == nil && protocol == GoalProtocol {
		for name, ref := range in.ArtifactRefs {
			bound, ok := cmd.Attachments[name]
			if !ok || string(mustJSON(bound)) != string(mustJSON(ref)) {
				return in, cmd, ErrInvalid
			}
		}
	}
	if err == nil && protocol == GoalProtocol && cmd.Command == "send" && len(mustJSON(cmd.Prompt))-2 > historical.cfg.Limits.Human {
		return in, cmd, ErrLimit
	}
	return frozen, cmd, err
}
