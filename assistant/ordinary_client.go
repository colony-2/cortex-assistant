package assistant

import (
	"context"
	"net/url"
	"strings"
	"unicode"

	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/secrets"
	"github.com/colony-2/c2j/pkg/starter"
)

// reconcileOrdinarySubmission returns the authoritative recorded output for a
// frozen retry. It reads only the current execution's actual input tasks; it
// cannot manufacture receipts or complete a different pending occurrence.
func (c *Client) reconcileOrdinarySubmission(ctx context.Context, w Caller, d *replayData, in input.StructuredSubmission) (input.Output, bool, error) {
	for _, out := range d.Inputs {
		if out.Receipt == nil || out.Receipt.RequestID != in.RequestID || out.Receipt.SubmissionID != in.SubmissionID {
			continue
		}
		if out.Receipt.Actor != w.Actor {
			return input.Output{}, true, ErrConflict
		}
		var proposal *Proposal
		if d.Args.Protocol == GoalProtocol {
			var command Command
			if decode(out.Response, &command) != nil {
				return input.Output{}, true, ErrUnavailable
			}
			if command.Command == "confirm" {
				for _, entry := range d.Entries {
					if entry.Proposal != nil && entry.Proposal.ID == command.ProposalID && entry.Proposal.Hash == command.ProposalHash {
						proposal = entry.Proposal
					}
				}
			}
		}
		frozen, requested, err := c.freezeSubmissionForProtocol(ctx, w, d.Args.Protocol, in, proposal)
		if err != nil {
			return input.Output{}, true, ErrConflict
		}
		recorded, err := c.command(out.Response, proposal)
		if err != nil || (d.Args.Protocol != GoalProtocol && len(out.ArtifactRefs) != 0) {
			return input.Output{}, true, ErrUnavailable
		}
		if d.Args.Protocol == GoalProtocol {
			if err := c.reconcileGoalAttachments(ctx, w, frozen, out, &requested, recorded); err != nil {
				return input.Output{}, true, err
			}
		}
		// Empty optional files have the same meaning for Go and JSON callers.
		if len(requested.Attachments) == 0 {
			requested.Attachments = nil
		}
		if len(recorded.Attachments) == 0 {
			recorded.Attachments = nil
		}
		if string(mustJSON(requested)) != string(mustJSON(recorded)) {
			return input.Output{}, true, ErrConflict
		}
		return out, true, nil
	}
	return input.Output{}, false, nil
}

// OrdinaryBindings is trusted server configuration. Cortex forwards an opaque
// named reference, never a plaintext key or a browser-selected endpoint/model.
type OrdinaryBindings struct {
	BaseURL  string
	APIKey   secrets.Reference
	Provider string
	Model    string
}

func (b OrdinaryBindings) validate() error {
	u, err := url.Parse(b.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsFunc(b.BaseURL, unicode.IsSpace) {
		return ErrInvalid
	}
	if b.Provider != "openai" || text(b.Model, 512, false) != nil || strings.ContainsAny(b.Model, ":/\\") || strings.ContainsFunc(b.Model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return ErrInvalid
	}
	if _, ok, err := secrets.Parse(b.APIKey); err != nil || !ok || b.APIKey.Name == "" || b.APIKey.Default != nil || b.APIKey.OmitIfUnavailable || b.APIKey.Required {
		return ErrInvalid
	}
	return nil
}

// OrdinaryLimits are encoded-envelope-aware ceilings for the v3 facade. Text
// limits still count UTF-8 bytes; every persisted JSON envelope is checked too.
func OrdinaryLimits() Limits {
	return Limits{Human: 16 << 10, Assistant: 32 << 10, Model: 128 << 10, Source: 64 << 10, File: 16 << 10, Count: 4, Aggregate: 64 << 10, Upload: 192 << 10, Document: 16 << 10, Page: 192 << 10}
}
func ordinaryLimitsValid(l Limits) bool {
	max := OrdinaryLimits()
	a := []int{l.Human, l.Assistant, l.Model, l.Source, l.File, l.Count, l.Aggregate, l.Upload, l.Document, l.Page}
	b := []int{max.Human, max.Assistant, max.Model, max.Source, max.File, max.Count, max.Aggregate, max.Upload, max.Document, max.Page}
	for i, v := range a {
		if v <= 0 || v > b[i] {
			return false
		}
	}
	return true
}

// StartFrozen accepts evidence only from the trusted capture boundary. Selection
// must come from that boundary independently of browser/model input. The frozen
// timestamp, request ID and snapshot are reused unchanged on retries.
func (c *Client) StartFrozen(ctx context.Context, w Caller, in StartInput, snapshot FrozenSnapshot, selected SnapshotSelection) (Session, error) {
	frozen, err := c.PrepareStartFrozen(ctx, w, in, snapshot, selected)
	if err != nil {
		return Session{}, err
	}
	return c.SubmitFrozenStart(ctx, w, frozen)
}

// PrepareStartFrozen prepares complete submission bytes without publishing a job.
// The trusted host retains the returned value before SubmitFrozenStart and reuses
// it unchanged after transport errors; preparing again is not a retry.
func (c *Client) PrepareStartFrozen(ctx context.Context, w Caller, in StartInput, snapshot FrozenSnapshot, selected SnapshotSelection) (FrozenStart, error) {
	if !ordinaryProtocol(c.protocol()) || c.cfg.Bindings == nil {
		return FrozenStart{}, ErrInvalid
	}
	if err := c.authorize(ctx, w, "start"); err != nil {
		return FrozenStart{}, err
	}
	if err := text(in.RequestID, 512, false); err != nil {
		return FrozenStart{}, err
	}
	if in.SubmittedAt.IsZero() {
		return FrozenStart{}, ErrInvalid
	}
	if err := text(in.Prompt, c.cfg.Limits.Human, false); err != nil {
		return FrozenStart{}, err
	}
	if c.protocol() == GoalProtocol && len(mustJSON(in.Prompt))-2 > c.cfg.Limits.Human {
		return FrozenStart{}, ErrLimit
	}
	if len(in.Attachments) != 0 {
		return FrozenStart{}, ErrUnavailable
	}
	if selected.TenantID != w.TenantID || in.Source != snapshot.Source {
		return FrozenStart{}, ErrForbidden
	}
	if err := ValidateFrozenSnapshot(snapshot, selected); err != nil {
		return FrozenStart{}, err
	}
	if snapshot.Source.Kind != "global" {
		if err := c.authorize(ctx, w, snapshot.Source.JobID); err != nil {
			return FrozenStart{}, err
		}
	}
	id := "assistant-" + identity(w.TenantID, in.RequestID)
	bindings := *c.cfg.Bindings
	inputs := map[string]any{"protocol": c.protocol(), "session_id": id, "prompt": in.Prompt, "snapshot": snapshot, "base_url": bindings.BaseURL, "api_key": bindings.APIKey, "provider": bindings.Provider, "model": bindings.Model}
	if len(mustJSON(inputs)) > OrdinaryEnvelopeMaxBytes {
		return FrozenStart{}, ErrLimit
	}
	start, err := recipejob.BuildStartJob(recipejob.BuildStartJobRequest{TenantID: w.TenantID, JobID: id, SubmittedAt: &in.SubmittedAt, Target: c.cfg.Root, Recipe: c.cfg.Recipe, Inputs: inputs})
	if err != nil {
		return FrozenStart{}, err
	}
	job, err := starter.PrepareRecipeJob(ctx, start, starter.StartRecipeJobOptions{})
	if err != nil {
		return FrozenStart{}, err
	}
	descriptor, err := starter.NewDescriptor(ctx, job)
	if err != nil {
		return FrozenStart{}, err
	}
	if c.protocol() == GoalProtocol {
		id = ""
	} // The authoritative receipt supplies the initial goal identity.
	return c.freezeStart(w, descriptor, id, in.Source)
}
