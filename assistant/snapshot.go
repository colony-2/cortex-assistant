package assistant

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/colony-2/c2j/pkg/artifacts"
)

// SnapshotMaxBytes bounds the encoded frozen input, leaving room for the prompt,
// task envelope and control data below JobDB's 256 KiB chapter payload default.
const SnapshotMaxBytes = 64 << 10
const SnapshotDocumentMaxBytes = 32 << 10
const SnapshotDocumentCount = 32

// FrozenSnapshot is captured by the trusted request boundary, never by a worker
// looking up a mutable page. It belongs in the recorded start input, not a store.
type FrozenSnapshot struct {
	TenantID  string             `json:"tenant_id"`
	Source    Source             `json:"source"`
	At        time.Time          `json:"at"`
	Documents []SnapshotDocument `json:"documents"`
}

type SnapshotDocument struct {
	ID       string         `json:"id"`
	Producer Locator        `json:"producer"`
	Ref      *artifacts.Ref `json:"ref,omitempty"`
	MIME     string         `json:"mime"`
	Outcome  string         `json:"outcome"`
	Text     string         `json:"text"`
	SHA256   string         `json:"sha256"`
	Size     int64          `json:"size"`
}

// SnapshotSelection comes from the authoritative capture boundary. In
// particular, review RequestID and child producer coordinates must not be
// reconstructed from browser/model-supplied references. SHA256 binds captured
// bytes (the retained prefix for truncated text), not inaccessible original bytes.
type SnapshotSelection struct {
	TenantID  string
	Source    Source
	At        time.Time
	Documents map[string]SnapshotDocument
}

// DecodeFrozenSnapshot rejects unknown fields, invalid UTF-8, extra JSON values
// and oversized input before validating the authoritative selection.
func DecodeFrozenSnapshot(raw []byte, selected SnapshotSelection) (FrozenSnapshot, error) {
	var s FrozenSnapshot
	if len(raw) > SnapshotMaxBytes {
		return s, ErrLimit
	}
	if !utf8.Valid(raw) {
		return s, ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&s) != nil || dec.Decode(new(any)) != io.EOF {
		return FrozenSnapshot{}, ErrInvalid
	}
	if err := ValidateFrozenSnapshot(s, selected); err != nil {
		return FrozenSnapshot{}, err
	}
	return s, nil
}

// ValidateFrozenSnapshot checks a frozen input against its trusted selection.
// It performs no URL requests, child recursion or mutable source lookup.
func ValidateFrozenSnapshot(s FrozenSnapshot, selected SnapshotSelection) error {
	if selected.TenantID == "" || s.TenantID != selected.TenantID || s.Source != selected.Source {
		return ErrForbidden
	}
	if err := s.Source.validate(); err != nil {
		return err
	}
	if s.At.IsZero() || !s.At.Equal(selected.At) {
		return ErrInvalid
	}
	if s.Source.Kind == "global" && (len(s.Documents) != 0 || len(selected.Documents) != 0) {
		return ErrForbidden
	}
	if len(s.Documents) > SnapshotDocumentCount {
		return ErrLimit
	}
	if len(s.Documents) != len(selected.Documents) {
		return ErrForbidden
	}
	seen := map[string]bool{}
	for _, d := range s.Documents {
		want, ok := selected.Documents[d.ID]
		if !ok || seen[d.ID] || text(d.ID, 512, false) != nil {
			return ErrForbidden
		}
		seen[d.ID] = true
		if d.Producer.JobID == "" || d.Producer.Task < 0 || !reflect.DeepEqual(d.Producer, want.Producer) || !reflect.DeepEqual(d.Ref, want.Ref) {
			return ErrForbidden
		}
		if d.Ref != nil {
			if d.Ref.Kind != artifacts.RefKindStored || d.Ref.Validate() != nil || d.Ref.Stored.Key.JobId != d.Producer.JobID || d.Ref.Stored.Key.TaskOrdinal != d.Producer.Task {
				return ErrForbidden
			}
		}
		if d.MIME != want.MIME || d.Outcome != want.Outcome || d.SHA256 != want.SHA256 || d.Size != want.Size {
			return ErrForbidden
		}
		if d.Size < 0 || !utf8.ValidString(d.Text) {
			return ErrInvalid
		}
		if len(d.Text) > SnapshotDocumentMaxBytes {
			return ErrLimit
		}
		switch d.Outcome {
		case "included", "truncated":
			if d.MIME != "text/plain" && d.MIME != "text/markdown" && d.MIME != "application/json" {
				return ErrInvalid
			}
			if d.Size < int64(len(d.Text)) || (d.Outcome == "included" && d.Size != int64(len(d.Text))) || (d.Outcome == "truncated" && d.Size <= int64(len(d.Text))) {
				return ErrInvalid
			}
			if d.MIME == "application/json" && d.Outcome == "included" && !json.Valid([]byte(d.Text)) {
				return ErrInvalid
			}
			hash := sha256.Sum256([]byte(d.Text))
			if d.SHA256 != hex.EncodeToString(hash[:]) {
				return ErrInvalid
			}
		case "denied", "unavailable", "unsupported", "budget_omitted":
			if d.Text != "" || d.SHA256 != "" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return ErrInvalid
	}
	if len(raw) > SnapshotMaxBytes {
		return ErrLimit
	}
	return nil
}

// SnapshotPrompt includes whole captured documents in stable ID order. Coverage
// is per document; budget omissions never masquerade as read source text. The
// JSON encoding keeps source text and provenance visibly separate from policy.
func SnapshotPrompt(s FrozenSnapshot, selected SnapshotSelection, budget int) (string, map[string]string, error) {
	if err := ValidateFrozenSnapshot(s, selected); err != nil {
		return "", nil, err
	}
	if budget < 0 || budget > SnapshotMaxBytes {
		return "", nil, ErrLimit
	}
	docs := append([]SnapshotDocument(nil), s.Documents...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	coverage := make(map[string]string, len(docs))
	var out strings.Builder
	for _, d := range docs {
		coverage[d.ID] = d.Outcome
		if d.Outcome != "included" && d.Outcome != "truncated" {
			continue
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return "", nil, ErrInvalid
		}
		line := fmt.Sprintf("SOURCE %s\n", raw)
		if out.Len()+len(line) > budget {
			coverage[d.ID] = "budget_omitted"
			continue
		}
		out.WriteString(line)
	}
	return out.String(), coverage, nil
}
