package assistant

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/colony-2/c2j/pkg/configuration"
	"github.com/colony-2/c2j/pkg/secrets"
)

// ValidateInferenceBindings checks nonsecret routing values without changing them.
// Both literal host bindings and the execution-time policy operation use it.
func ValidateInferenceBindings(baseURL, provider, model string) error {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(baseURL, "\\#") || strings.ContainsFunc(baseURL, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return ErrInvalid
	}
	if provider != "openai" || text(model, 512, false) != nil || strings.ContainsAny(model, ":/\\") || strings.ContainsFunc(model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return ErrInvalid
	}
	return nil
}

// bindingInputs is shared by Start and Reopen. References freeze the lookup
// policy, not the values a future execution will consume. No lookup occurs here.
func (c *Client) bindingInputs() (map[string]any, error) {
	if c.cfg.Bindings != nil {
		b := *c.cfg.Bindings
		return map[string]any{"base_url": b.BaseURL, "provider": b.Provider, "model": b.Model, "api_key": b.APIKey}, nil
	}
	if c.protocol() != GoalProtocol {
		return nil, ErrInvalid
	}
	return map[string]any{
		"base_url": configuration.Reference{Kind: "config", Name: "codex_api_base_url", ExpectedType: "string"},
		"model":    configuration.Reference{Kind: "config", Name: "codex_model", ExpectedType: "string"},
		"provider": "openai", "api_key": secrets.Reference{Name: "codex_api_key"},
	}, nil
}
