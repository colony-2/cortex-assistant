package assistant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/colony-2/c2j/pkg/artifacts"
	"github.com/colony-2/jobdb/pkg/jobdb"
)

func snapshotFixture() (FrozenSnapshot, SnapshotSelection) {
	text := strings.Repeat("evidence 世界\n", 150)
	h := sha256.Sum256([]byte(text))
	ref := artifacts.NewStoredRef(jobdb.ArtifactKey{JobId: "original-child", TaskOrdinal: 17, Name: "report.md", SizeBytes: int64(len(text))})
	d := SnapshotDocument{ID: "review-document", Producer: Locator{JobID: "original-child", Task: 17}, Ref: &ref, MIME: "text/markdown", Outcome: "included", Text: text, SHA256: hex.EncodeToString(h[:]), Size: int64(len(text))}
	s := FrozenSnapshot{TenantID: "tenant", Source: Source{Kind: "review", JobID: "parent", RequestID: "exact-request"}, At: time.Unix(100, 0).UTC(), Documents: []SnapshotDocument{d}}
	return s, SnapshotSelection{TenantID: s.TenantID, Source: s.Source, At: s.At, Documents: map[string]SnapshotDocument{d.ID: d}}
}

func TestFrozenSnapshotIncludedBytesAndCoverage(t *testing.T) {
	s, selected := snapshotFixture()
	prompt, coverage, err := SnapshotPrompt(s, selected, SnapshotMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	var actual SnapshotDocument
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(prompt, "SOURCE "))), &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Text != s.Documents[0].Text || len(actual.Text) <= 1000 || actual.Producer.JobID != "original-child" || coverage[actual.ID] != "included" {
		t.Fatalf("lost source bytes/provenance: %+v", actual)
	}
	prompt, coverage, err = SnapshotPrompt(s, selected, 1)
	if err != nil || prompt != "" || coverage[actual.ID] != "budget_omitted" {
		t.Fatalf("budget coverage: %q %v %v", prompt, coverage, err)
	}
	if s.Documents[0].Outcome != "included" {
		t.Fatal("render mutated frozen input")
	}
}

func TestFrozenSnapshotRejectsSubstitution(t *testing.T) {
	for name, mutate := range map[string]func(*FrozenSnapshot){
		"tenant":          func(s *FrozenSnapshot) { s.TenantID = "foreign" },
		"replaced-review": func(s *FrozenSnapshot) { s.Source.RequestID = "replacement" },
		"child-producer":  func(s *FrozenSnapshot) { s.Documents[0].Producer.JobID = "parent" },
		"task":            func(s *FrozenSnapshot) { s.Documents[0].Producer.Task++ },
		"text":            func(s *FrozenSnapshot) { s.Documents[0].Text = strings.Repeat("x", len(s.Documents[0].Text)) },
		"unselected":      func(s *FrozenSnapshot) { s.Documents[0].ID = "unselected" },
		"missing":         func(s *FrozenSnapshot) { s.Documents = nil },
		"duplicate":       func(s *FrozenSnapshot) { s.Documents = append(s.Documents, s.Documents[0]) },
		"url": func(s *FrozenSnapshot) {
			r := artifacts.NewExternalRef("report.md", "https://example.invalid", false)
			s.Documents[0].Ref = &r
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, selected := snapshotFixture()
			mutate(&s)
			if ValidateFrozenSnapshot(s, selected) == nil {
				t.Fatal("accepted substituted source")
			}
		})
	}
}

func TestFrozenSnapshotGlobalAndUnavailable(t *testing.T) {
	s, selected := snapshotFixture()
	s.Source = Source{Kind: "global"}
	selected.Source = s.Source
	if !errors.Is(ValidateFrozenSnapshot(s, selected), ErrForbidden) {
		t.Fatal("global captured page evidence")
	}
	s.Documents = nil
	selected.Documents = nil
	if err := ValidateFrozenSnapshot(s, selected); err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"denied", "unavailable", "unsupported", "budget_omitted"} {
		s, selected = snapshotFixture()
		d := &s.Documents[0]
		d.Outcome = outcome
		d.Text = ""
		d.SHA256 = ""
		selected.Documents[d.ID] = *d
		prompt, coverage, err := SnapshotPrompt(s, selected, SnapshotMaxBytes)
		if err != nil || prompt != "" || coverage[d.ID] != outcome {
			t.Fatalf("%s: %q %v %v", outcome, prompt, coverage, err)
		}
		d.Text = "hidden bytes"
		if ValidateFrozenSnapshot(s, selected) == nil {
			t.Fatal("unread bytes accepted")
		}
	}
}

func TestFrozenSnapshotBoundsAndTruncation(t *testing.T) {
	s, selected := snapshotFixture()
	d := &s.Documents[0]
	d.Text = strings.Repeat("x", SnapshotDocumentMaxBytes)
	d.Size = int64(len(d.Text))
	h := sha256.Sum256([]byte(d.Text))
	d.SHA256 = hex.EncodeToString(h[:])
	selected.Documents[d.ID] = *d
	if err := ValidateFrozenSnapshot(s, selected); err != nil {
		t.Fatal(err)
	}
	d.Text += "x"
	if !errors.Is(ValidateFrozenSnapshot(s, selected), ErrLimit) {
		t.Fatal("document limit not enforced")
	}
	s, selected = snapshotFixture()
	d = &s.Documents[0]
	d.Text = clipped(d.Text, 2001)
	d.Outcome = "truncated"
	h = sha256.Sum256([]byte(d.Text))
	d.SHA256 = hex.EncodeToString(h[:])
	selected.Documents[d.ID] = *d
	if err := ValidateFrozenSnapshot(s, selected); err != nil {
		t.Fatal(err)
	}
	d.Text += string([]byte{0xff})
	if !errors.Is(ValidateFrozenSnapshot(s, selected), ErrInvalid) {
		t.Fatal("invalid UTF-8 accepted")
	}
	// Escaping expansion counts against the encoded envelope, even when the
	// decoded text remains below the per-document cap.
	s, selected = snapshotFixture()
	d = &s.Documents[0]
	d.Text = strings.Repeat("<", 12000)
	d.Size = int64(len(d.Text))
	h = sha256.Sum256([]byte(d.Text))
	d.SHA256 = hex.EncodeToString(h[:])
	selected.Documents[d.ID] = *d
	if !errors.Is(ValidateFrozenSnapshot(s, selected), ErrLimit) {
		t.Fatal("encoded envelope limit not enforced")
	}
}

func TestFrozenSnapshotClosedWire(t *testing.T) {
	s, selected := snapshotFixture()
	raw, _ := json.Marshal(s)
	decoded, err := DecodeFrozenSnapshot(raw, selected)
	if err != nil || decoded.Documents[0].Text != s.Documents[0].Text {
		t.Fatalf("roundtrip: %v", err)
	}
	for _, bad := range [][]byte{
		append(append([]byte(nil), raw...), []byte(" {}")...),
		[]byte(strings.Replace(string(raw), `"tenant_id":`, `"unknown":true,"tenant_id":`, 1)),
		append(append([]byte(nil), raw...), 0xff),
	} {
		if _, err := DecodeFrozenSnapshot(bad, selected); err == nil {
			t.Fatal("invalid wire input accepted")
		}
	}
	s.At = s.At.Add(time.Second)
	if ValidateFrozenSnapshot(s, selected) == nil {
		t.Fatal("capture time substitution accepted")
	}
}

func TestFrozenSnapshotMultipleDocumentBudget(t *testing.T) {
	s, selected := snapshotFixture()
	second := SnapshotDocument{ID: "second-document", Producer: Locator{JobID: "another-child", Task: 8}, MIME: "text/plain", Outcome: "included", Text: "small independent source", Size: 24}
	h := sha256.Sum256([]byte(second.Text))
	second.SHA256 = hex.EncodeToString(h[:])
	s.Documents = append(s.Documents, second)
	selected.Documents[second.ID] = second
	prompt, coverage, err := SnapshotPrompt(s, selected, 600)
	if err != nil {
		t.Fatal(err)
	}
	if coverage["review-document"] != "budget_omitted" || coverage[second.ID] != "included" || !strings.Contains(prompt, second.Text) || strings.Contains(prompt, s.Documents[0].Text) {
		t.Fatalf("document coverage does not match read bytes: %v %q", coverage, prompt)
	}
	prompt, coverage, err = SnapshotPrompt(s, selected, SnapshotMaxBytes)
	if err != nil || !strings.Contains(prompt, "original-child") || !strings.Contains(prompt, "another-child") || coverage["review-document"] != "included" || coverage[second.ID] != "included" {
		t.Fatalf("multi-child provenance lost: %v %v", coverage, err)
	}
}
