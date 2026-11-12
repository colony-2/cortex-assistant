// discussion-op executes one policy step using the task-scoped c2j helper.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/colony-2/c2j/pkg/childbroker"
	"github.com/colony-2/c2j/pkg/jobcontext"
	"github.com/colony-2/c2j/pkg/recipejob"
	"github.com/colony-2/cortex-assistant/assistant"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "assistant: discussion step unavailable")
		os.Exit(1)
	}
}
func run() error {
	current, ok, err := jobcontext.CurrentFromEnv(os.Getenv)
	if err != nil || !ok {
		return assistant.ErrForbidden
	}
	broker, ok, err := jobcontext.ChildJobBrokerFromEnv(os.Getenv)
	if err != nil || !ok {
		return assistant.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, assistant.OrdinaryEnvelopeMaxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > assistant.OrdinaryEnvelopeMaxBytes {
		return assistant.ErrLimit
	}
	var in struct {
		assistant.OrdinaryInput
		Outbox string `json:"artifact_outbox_path,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
		return assistant.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, docs, err := assistant.RunOrdinary(ctx, ordinaryAPI{JobClient: childbroker.JobClient{Broker: broker}, broker: broker}, current, in.OrdinaryInput)
	if err != nil {
		return err
	}
	if len(docs) > 0 {
		if !filepath.IsAbs(in.Outbox) {
			return assistant.ErrInvalid
		}
		for _, d := range docs {
			if err = os.WriteFile(filepath.Join(in.Outbox, d.Name), []byte(d.Text), 0600); err != nil {
				return err
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"output": result})
}

// Target identity comes from the current task's authorized registered-cell lookup.
type ordinaryAPI struct {
	childbroker.JobClient
	broker jobcontext.ChildJobBroker
}

func (a ordinaryAPI) ResolveTarget(ctx context.Context, name string) (recipejob.ResolvedTarget, error) {
	cell, err := childbroker.ResolveCell(ctx, a.broker, name)
	if err != nil {
		return recipejob.ResolvedTarget{}, err
	}
	return recipejob.ResolvedTarget{CellID: cell.CellID, CellName: cell.Cell, CellRepo: cell.CellRepo, RepositorySource: "https://" + cell.CellRepo + ".git", DefaultRef: "main"}, nil
}
