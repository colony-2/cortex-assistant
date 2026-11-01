package assistant

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/colony-2/c2j/pkg/artifacts"
)

func identity(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func decode(v any, out any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(out); e != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, e)
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func text(s string, max int, empty bool) error {
	if !utf8.ValidString(s) || (!empty && strings.TrimSpace(s) == "") {
		return ErrInvalid
	}
	if len(s) > max {
		return ErrLimit
	}
	return nil
}
func (s Source) validate() error {
	switch s.Kind {
	case "global":
		if s.JobID == "" && s.RequestID == "" {
			return nil
		}
	case "story":
		if s.JobID != "" && s.RequestID == "" {
			return text(s.JobID, 512, false)
		}
	case "review":
		if s.JobID != "" && s.RequestID != "" {
			if e := text(s.JobID, 512, false); e != nil {
				return e
			}
			return text(s.RequestID, 512, false)
		}
	}
	return ErrInvalid
}
func safeName(n string) bool {
	return n != "" && len(n) <= 255 && n != "." && n != ".." && path.Base(n) == n && !strings.ContainsAny(n, "\\\x00\r\n")
}
func (c *Client) validateRefs(refs map[string]artifacts.Ref) error {
	if len(refs) > c.cfg.Limits.Count {
		return ErrLimit
	}
	var total int64
	for name, r := range refs {
		if !safeName(name) || r.Kind != artifacts.RefKindStored || r.Validate() != nil {
			return ErrInvalid
		}
		if r.Stored.Key.SizeBytes < 0 || r.Stored.Key.SizeBytes > int64(c.cfg.Limits.File) {
			return ErrLimit
		}
		total += r.Stored.Key.SizeBytes
	}
	if total > int64(c.cfg.Limits.Aggregate) {
		return ErrLimit
	}
	return nil
}
func (c *Client) command(v any, p *Proposal) (Command, error) {
	var cmd Command
	if e := decode(v, &cmd); e != nil {
		return cmd, e
	}
	var fields map[string]any
	if e := decode(v, &fields); e != nil {
		return cmd, e
	}
	allowed := map[string]bool{"command": true}
	switch cmd.Command {
	case "send":
		allowed["prompt"] = true
		allowed["attachments"] = true
	case "confirm":
		allowed["proposal_id"] = true
		allowed["proposal_hash"] = true
	}
	for name := range fields {
		if !allowed[name] {
			return cmd, ErrInvalid
		}
	}
	switch cmd.Command {
	case "send":
		if cmd.ProposalID != "" || cmd.ProposalHash != "" {
			return cmd, ErrInvalid
		}
		if e := text(cmd.Prompt, c.cfg.Limits.Human, false); e != nil {
			return cmd, e
		}
		return cmd, c.validateRefs(cmd.Attachments)
	case "finish":
		if cmd.Prompt != "" || len(cmd.Attachments) != 0 || cmd.ProposalID != "" || cmd.ProposalHash != "" {
			return cmd, ErrInvalid
		}
	case "confirm":
		if cmd.Prompt != "" || len(cmd.Attachments) != 0 || p == nil || cmd.ProposalID != p.ID || cmd.ProposalHash != p.Hash {
			return cmd, ErrStale
		}
	default:
		return cmd, ErrInvalid
	}
	return cmd, nil
}
func boundedLimits(l Limits) (Limits, error) {
	d := DefaultLimits()
	a := []*int{&l.Human, &l.Assistant, &l.Model, &l.Source, &l.File, &l.Count, &l.Aggregate, &l.Upload, &l.Document, &l.Page}
	b := []int{d.Human, d.Assistant, d.Model, d.Source, d.File, d.Count, d.Aggregate, d.Upload, d.Document, d.Page}
	for i, p := range a {
		if *p < 0 {
			return l, ErrInvalid
		}
		if *p == 0 || *p > b[i] {
			*p = b[i]
		}
	}
	if l.Page < 4096 || l.Model < 4096 {
		return l, ErrInvalid
	}
	return l, nil
}
