package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyClosedAndExact(t *testing.T) {
	value := `{"base_url":"https://host.invalid/exact/%2f/","provider":"openai","model":"model-1"}`
	var out bytes.Buffer
	if err := run(strings.NewReader(value), &out); err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &got) != nil || string(got["output"]) != value {
		t.Fatal(out.String())
	}
	for _, bad := range []string{`{"base_url":"https://host.invalid","provider":"openai","model":"m","api_key":"private"}`, value + `{}`, `null`, strings.Repeat("x", 8193), `{"base_url":null,"provider":"openai","model":"m"}`} {
		out.Reset()
		if run(strings.NewReader(bad), &out) == nil || out.Len() != 0 {
			t.Fatal("invalid input emitted output")
		}
	}
}

func TestPolicyAcceptedEndpointRange(t *testing.T) {
	for _, size := range []int{4100, 20000} {
		b := bindings{BaseURL: "https://fixture.invalid/" + strings.Repeat("a", size), Provider: "openai", Model: "fixture"}
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := run(bytes.NewReader(raw), &out); err != nil {
			t.Fatal(size, err)
		}
		var got struct {
			Output bindings `json:"output"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Output != b {
			t.Fatal("accepted endpoint changed", size, err)
		}
	}
}
