// Package assistant implements assistant policy over ordinary c2j job records.
// Replay pages are transient: the recorded tasks, not a transcript, are authoritative.
package assistant

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/c2j/pkg/input"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/c2j/pkg/story"
	"github.com/colony-2/c2j/pkg/workflowctl"
)

const Protocol = "cortex-assistant/2"
const RecipeID = "cortex-assistant-session"
const ModelOperation = "git+https://github.com/colony-2/c2ops.git//pydantic@f30202d13d197beaed7ba0bd0b120b04443728eb"

// OrdinaryModelOperation is the delivered explicit-binding package. The frozen
// v2 selector above remains unchanged for historical execution identities.
const OrdinaryModelOperation = "nix:github:colony-2/c2ops/6396e1fe6462084e85ea44d86d372bdf3b133db0#pydantic"

var (
	ErrForbidden   = errors.New("assistant: forbidden")
	ErrNotFound    = errors.New("assistant: not found")
	ErrConflict    = errors.New("assistant: conflict")
	ErrStale       = errors.New("assistant: stale input")
	ErrLimit       = errors.New("assistant: limit exceeded")
	ErrUnavailable = errors.New("assistant: retained content unavailable")
	ErrNotAtPrompt = errors.New("assistant: not at prompt")
	ErrInvalid     = errors.New("assistant: invalid value")
)

type Caller struct {
	TenantID string
	Actor    input.Actor
}
type Source struct {
	Kind      string `json:"kind"`
	JobID     string `json:"job_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}
type StartInput struct {
	RequestID string `json:"request_id"`
	// SubmittedAt must be frozen by the caller along with RequestID for retries.
	SubmittedAt time.Time                `json:"submitted_at"`
	Prompt      string                   `json:"prompt"`
	Source      Source                   `json:"source"`
	Attachments map[string]artifacts.Ref `json:"attachments"`
}
type StartArgs struct {
	Protocol    string                   `json:"protocol"`
	SessionID   string                   `json:"session_id"`
	Prompt      string                   `json:"prompt"`
	Source      Source                   `json:"source"`
	Attachments map[string]artifacts.Ref `json:"attachments"`
}
type Limits struct{ Human, Assistant, Model, Source, File, Count, Aggregate, Upload, Document, Page int }

func DefaultLimits() Limits {
	return Limits{32 << 10, 64 << 10, 128 << 10, 1 << 20, 8 << 20, 8, 16 << 20, 24 << 20, 1 << 20, 4 << 20}
}

type Host struct {
	Goals     GoalReader
	History   HistoryReader
	Control   workflowctl.WorkflowControl
	Story     story.Service
	Input     *input.Runtime
	Submitter recipejob.Submitter
	// Authorization is host access control only, never conversation/model policy.
	Authorize   func(context.Context, Caller, string) error
	ResolveCell func(context.Context, Caller, string) (recipejob.ResolvedTarget, error)
}

// ReleaseDescriptor approves one immutable execution format. Legacy
// histories remain readable; Reopen must only be enabled with its original route.
type ReleaseDescriptor struct {
	Recipe   string
	Commit   string
	Protocol string
	Reopen   bool
}
type Config struct {
	// Protocol is explicit for new execution formats; empty preserves frozen v2.
	Protocol       string
	Bindings       *OrdinaryBindings
	LegacyReleases []ReleaseDescriptor

	Root   recipejob.ResolvedTarget
	Recipe string
	// RecipeCommit and selector must identify the same immutable release.
	RecipeCommit string
	BuildRecipe  string
	Limits       Limits
	// CursorKey is deployment configuration, stable across hosts. Minimum 32 bytes.
	CursorKey []byte
}
type Client struct {
	host Host
	cfg  Config
}
type PageRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type Locator struct {
	JobID      string   `json:"job_id"`
	Task       int64    `json:"task"`
	Invocation string   `json:"invocation,omitempty"`
	Path       []string `json:"path,omitempty"`
	Attempt    int      `json:"attempt,omitempty"`
}
type Artifact struct {
	ID        string        `json:"id"`
	Ref       artifacts.Ref `json:"ref"`
	Name      string        `json:"name"`
	MIME      string        `json:"mime"`
	Size      int64         `json:"size"`
	Available bool          `json:"available"`
}
type Proposal struct {
	ID     string                   `json:"id"`
	Hash   string                   `json:"hash"`
	Target recipejob.ResolvedTarget `json:"target"`
	Prompt string                   `json:"prompt"`
}
type DocumentInfo struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}
type Response struct {
	Citations []string       `json:"citations,omitempty"`
	Documents []DocumentInfo `json:"documents,omitempty"`
	Text      string         `json:"text"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Proposal  *Proposal      `json:"proposal,omitempty"`
	Model     string         `json:"model,omitempty"`
	Usage     map[string]any `json:"usage,omitempty"`
	Error     string         `json:"error,omitempty"`
}
type Entry struct {
	Citations       []string       `json:"citations,omitempty"`
	Origin          Locator        `json:"origin"`
	Role            string         `json:"role"`
	Status          string         `json:"status"`
	Text            string         `json:"text,omitempty"`
	Artifacts       []Artifact     `json:"artifacts,omitempty"`
	Proposal        *Proposal      `json:"proposal,omitempty"`
	Receipt         *input.Receipt `json:"receipt,omitempty"`
	Destination     string         `json:"destination,omitempty"`
	DestinationGoal string         `json:"destination_goal,omitempty"`
	// Recovery describes read-only discovery, never a replacement task outcome.
	Recovery string `json:"recovery,omitempty"`
	Error    string `json:"error,omitempty"`
}
type ReplayPage struct {
	Entries   []Entry `json:"entries"`
	Cursor    string  `json:"cursor,omitempty"`
	HasMore   bool    `json:"has_more"`
	Complete  bool    `json:"complete"`
	Watermark string  `json:"watermark"`
}
type Session struct {
	Snapshot *FrozenSnapshot         `json:"snapshot,omitempty"`
	Coverage map[string]string       `json:"coverage,omitempty"`
	ID       string                  `json:"id"`
	JobID    string                  `json:"job_id"`
	GoalID   string                  `json:"goal_id"`
	Status   string                  `json:"status"`
	Source   Source                  `json:"source"`
	Limits   Limits                  `json:"limits"`
	Replay   ReplayPage              `json:"replay"`
	Progress []Entry                 `json:"progress,omitempty"`
	Current  *input.UserInputDetails `json:"current,omitempty"`
}
type SessionPage struct {
	Sessions []Session `json:"sessions"`
	Cursor   string    `json:"cursor,omitempty"`
}
type ArtifactReader struct {
	io.ReadCloser
	Filename string
	MIME     string
	Size     int64
}
type Command struct {
	Command      string                   `json:"command"`
	Prompt       string                   `json:"prompt,omitempty"`
	Attachments  map[string]artifacts.Ref `json:"attachments,omitempty"`
	ProposalID   string                   `json:"proposal_id,omitempty"`
	ProposalHash string                   `json:"proposal_hash,omitempty"`
}
type PromptRequest struct {
	Protocol  string    `json:"protocol"`
	SessionID string    `json:"session_id"`
	Replay    Locator   `json:"replay"`
	Assistant Response  `json:"assistant"`
	Proposal  *Proposal `json:"proposal,omitempty"`
}
