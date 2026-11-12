package assistant

import "regexp"

// Limits belong to the selected execution contract. A preferred release's
// configuration must not tighten already accepted historical executions.
func (c *Client) withContract(protocol string) *Client {
	historical := *c
	historical.cfg.Protocol = protocol
	historical.cfg.Limits = DefaultLimits()
	if ordinaryProtocol(protocol) {
		historical.cfg.Limits = OrdinaryLimits()
	}
	return &historical
}

func (c *Client) forProtocol(protocol string) *Client {
	if protocol == c.protocol() {
		return c
	}
	return c.withContract(protocol)
}

func (c *Client) forExecution(d *replayData) *Client {
	if d.Story.Recipe.Source.SubmittedSelector == c.cfg.Recipe {
		return c
	}
	// project has authenticated the selector, commit and protocol against an
	// explicit release descriptor before any execution policy is selected.
	return c.withContract(d.Args.Protocol)
}

func validateLegacy(cfg Config) error {
	seen := map[string]bool{cfg.Recipe: true}
	for _, r := range cfg.LegacyReleases {
		if seen[r.Recipe] || (r.Protocol != "cortex-assistant/1" && r.Protocol != "cortex-assistant/2" && r.Protocol != OrdinaryProtocol) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(r.Commit) || r.Recipe != legacySelector(r.Protocol, r.Commit) {
			return ErrInvalid
		}
		seen[r.Recipe] = true
	}
	return nil
}
func (c *Client) release(selector string) (ReleaseDescriptor, bool) {
	if selector == c.cfg.Recipe {
		return ReleaseDescriptor{Recipe: c.cfg.Recipe, Commit: c.cfg.RecipeCommit, Protocol: c.protocol(), Reopen: c.protocol() != OrdinaryProtocol}, true
	}
	for _, r := range c.cfg.LegacyReleases {
		if r.Recipe == selector {
			return r, true
		}
	}
	return ReleaseDescriptor{}, false
}
func (c *Client) approved(selector string) bool { _, ok := c.release(selector); return ok }

func (c *Client) protocol() string {
	if c.cfg.Protocol == "" {
		return Protocol
	}
	return c.cfg.Protocol
}

func legacySelector(protocol, commit string) string {
	file := "session.yaml"
	if protocol == OrdinaryProtocol {
		file = "discussion.yaml"
	}
	return "git+https://github.com/colony-2/cortex-assistant.git//" + file + "@" + commit
}
