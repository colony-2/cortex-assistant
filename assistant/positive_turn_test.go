package assistant

import (
	"context"
	"testing"

	"github.com/colony-2/c2j/pkg/jobinspect"
	"github.com/colony-2/cortex-assistant/internal/turncheck"
)

func positiveTurn(ctx context.Context, api jobinspect.API, facade *Client, caller Caller, session string, turn turncheck.Turn) (turncheck.Evidence, error) {
	var entries []Entry
	for cursor := ""; ; {
		page, err := facade.Replay(ctx, caller, session, PageRequest{Cursor: cursor})
		if err != nil {
			return turncheck.Evidence{}, err
		}
		entries = append(entries, page.Entries...)
		if !page.HasMore {
			break
		}
		cursor = page.Cursor
	}
	return turncheck.Check(ctx, api, turn, entries)
}
func requirePositiveTurn(t *testing.T, ctx context.Context, api jobinspect.API, facade *Client, caller Caller, session string, turn turncheck.Turn) turncheck.Evidence {
	t.Helper()
	proof, err := positiveTurn(ctx, api, facade, caller, session, turn)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}
