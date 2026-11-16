// bindings-op validates only nonsecret values at the consuming task boundary.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/colony-2/cortex-assistant/assistant"
)

type bindings struct {
	BaseURL  string `json:"base_url"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func run(in io.Reader, out io.Writer) error {
	var b bindings
	dec := json.NewDecoder(in)
	dec.DisallowUnknownFields()
	if dec.Decode(&b) != nil || dec.Decode(new(any)) != io.EOF {
		return assistant.ErrInvalid
	}
	if err := assistant.ValidateInferenceBindings(b.BaseURL, b.Provider, b.Model); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"output": b})
}
func main() {
	if run(os.Stdin, os.Stdout) != nil {
		fmt.Fprintln(os.Stderr, "assistant: invalid inference bindings")
		os.Exit(1)
	}
}
