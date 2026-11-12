package assistant

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/colony-2/c2j/pkg/starter"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

// FrozenStart is submitted content, including the complete prepared descriptor.
// Keep it with the logical operation in the trusted host, before publication.
// It contains named secret references, not resolved credentials. It is not a
// transcript or a session store. CursorKey must survive host replacement.
type FrozenStart struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

type frozenStartPayload struct {
	Version    int                `json:"version"`
	Caller     Caller             `json:"caller"`
	SessionID  string             `json:"session_id"`
	Source     Source             `json:"source"`
	Descriptor starter.Descriptor `json:"descriptor"`
}

func (c *Client) startMAC(payload []byte) []byte {
	h := hmac.New(sha256.New, c.cfg.CursorKey)
	h.Write([]byte("cortex-assistant/frozen-start/1\x00"))
	h.Write(payload)
	return h.Sum(nil)
}

func (c *Client) freezeStart(w Caller, descriptor starter.Descriptor, id string, source Source) (FrozenStart, error) {
	payload, err := json.Marshal(frozenStartPayload{1, w, id, source, descriptor})
	if err != nil {
		return FrozenStart{}, err
	}
	return FrozenStart{Payload: payload, MAC: hex.EncodeToString(c.startMAC(payload))}, nil
}

// SubmitFrozenStart reconciles the exact prepared operation, without rebuilding
// its timestamp, destination, bindings, metadata or artifacts. On any ambiguous
// result retain frozen and retry this method; do not allocate a new operation.
func (c *Client) SubmitFrozenStart(ctx context.Context, w Caller, frozen FrozenStart) (Session, error) {
	if err := c.authorize(ctx, w, "start"); err != nil {
		return Session{}, err
	}
	mac, err := hex.DecodeString(frozen.MAC)
	if err != nil || !hmac.Equal(mac, c.startMAC(frozen.Payload)) {
		return Session{}, ErrConflict
	}
	var payload frozenStartPayload
	if decode(frozen.Payload, &payload) != nil || payload.Version != 1 {
		return Session{}, ErrInvalid
	}
	if payload.Caller != w || payload.Descriptor.Job.TenantId != w.TenantID {
		return Session{}, ErrForbidden
	}
	if payload.Source.Kind != "global" {
		if err := c.authorize(ctx, w, payload.Source.JobID); err != nil {
			return Session{}, err
		}
	}
	ref, err := payload.Descriptor.Submit(ctx, c.host.Submitter)
	if err != nil {
		return Session{}, err
	}
	if ref.Validate() != nil || ref.TenantID != w.TenantID || ref.JobID != payload.Descriptor.Job.JobID {
		return Session{}, jobdb.ErrSubmissionReceiptUnavailable
	}
	id := payload.SessionID
	if id == "" {
		id = ref.GoalID
	}
	return Session{ID: id, JobID: ref.JobID, GoalID: ref.GoalID, Status: "submitted", Source: payload.Source, Limits: c.cfg.Limits}, nil
}
