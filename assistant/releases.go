package assistant

import (
	"regexp"
	"strings"
)

func validateLegacy(cfg Config) error {
	seen := map[string]bool{cfg.Recipe: true}
	for _, r := range cfg.LegacyReleases {
		if seen[r.Recipe] || r.Protocol != "cortex-assistant/1" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(r.Commit) || !strings.HasSuffix(r.Recipe, "@"+r.Commit) || !strings.Contains(r.Recipe, "cortex-assistant.git//session.yaml@") {
			return ErrInvalid
		}
		seen[r.Recipe] = true
	}
	return nil
}
func (c *Client) release(selector string) (ReleaseDescriptor, bool) {
	if selector == c.cfg.Recipe {
		return ReleaseDescriptor{Recipe: c.cfg.Recipe, Commit: c.cfg.RecipeCommit, Protocol: Protocol, Reopen: true}, true
	}
	for _, r := range c.cfg.LegacyReleases {
		if r.Recipe == selector {
			return r, true
		}
	}
	return ReleaseDescriptor{}, false
}
func (c *Client) approved(selector string) bool { _, ok := c.release(selector); return ok }
