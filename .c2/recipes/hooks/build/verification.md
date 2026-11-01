# Documentation verification

Selection baseline: cortex-assistant main `d2c4a33352f3f0e888b172946fca69caffd573df`, independently refreshed 2026-11-01. Its complete tracked inventory is `PURPOSE.md`. No workflow, manifest, application or test command exists at that revision. This hook checks responsibility-document usability, not assistant behavior or CI parity. Refresh this mapping when implementation or CI appears; unfamiliar candidate files fail pending coverage review.

## Destination grammar (defined before implementation)

Supported inline links are `[label](destination)` and images `![label](destination)`. Labels are nonempty, single-line, without brackets or escapes. Destinations are nonempty bare tokens without whitespace, parentheses, quotes, angle brackets or escapes. Percent-encoded path characters are decoded before filesystem containment checking. Local fragments/queries do not change the target existence check; fragment contents are not validated. URL schemes and protocol-relative URLs are external and never fetched; fragment-only destinations are reported unchecked. Empty, malformed, reference, shortcut, escaped or nested bracket forms and complex destinations fail as unsupported. Markdown HTML links/autolinks, including email autolinks, are unsupported. Escaped bracket/image forms are rejected before supported links are consumed. Fenced code blocks and inline backtick code are literal content, excluded from destination checks. Unclosed code delimiters fail rather than hiding remaining content. This intentionally conservative grammar is not full Markdown conformance.

## Title and prose content

Optional ATX closing hashes are removed before deciding whether a top-level title is nonempty. HTML comments do not supply rendered title or prose content. Comment-only, heading-only, indented-code-only and thematic-break-only bodies cannot satisfy the prose requirement. Remaining HTML blocks fail explicitly as unsupported; unclosed comments fail rather than hiding remaining content. Ordinary prose and nonempty headings with closing hashes remain valid.

## Operation

Existing Git and Python 3 standard library are required, with no installation or scheduling declarations. The hook uses bounded subprocesses, captures original outcomes, and writes complete logs inside `verification-evidence.json` in operation outbox storage. Missing Python or unwritable artifact storage causes an operation error rather than a fabricated result. A failed checker result is finalized as failed even when logging succeeds. No secrets or environment dumps are logged.

The runner checks managed root/target containment, runtime candidate identity, and pre/post HEAD, index bytes, staged entries and tracked content. It never repairs detected mutation. Fixtures run outside the managed repository. The output summary is documentation-only. Omitted: application compilation/lint/tests, serving protocol, durability/restart, tenancy, adaptation, rendering, goals and consumer integration; hosted runners, caching and platform matrices; all publication/deployment.

Run `python3 -m unittest discover -s tests -v`. The suite also runs actual wrapper integration using `c2j test run` on generated disposable fixtures and a read-only checkout of recipes revision `84a889884fcf78f44de9ab80dcfb99ddf5329d5e`. Git network access is needed to read that source, not to submit jobs. Set `VERIFY_TEST_EVIDENCE` to an external directory to retain integration results. No deprecations or replacements of existing tests are introduced.
