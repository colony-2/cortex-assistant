# Cortex Assistant

Own the reusable, browser-independent contextual assistant core, versioned serving protocol, and cell-local c2j session recipe. Serve durable multi-turn conversations executed as jobs in the configured project root and selected JobDB tenant, with bounded contextual review/story reads, cited source snapshots, transcripts, documents and returned artifacts (including files, images and videos), prompt-input finish, and restart at the last recorded prompt boundary.

Own model adaptation, checkpoint, document and general artifact formats, and narrowly scoped action orchestration. Goal creation is limited to existing catalog cells, requires confirmation of the exact target and prompt, and must deduplicate retries and restarts. Preserve logical session identity across runtime executions without depending on provider session caches or worker workspaces.

Cortex owns its HTTP/browser integration, tenant-aware context capture, session discovery and rendering. c2j owns recipe execution/input/restart semantics; JobDB owns durable runtime storage; c2ops owns packaged model operations. Root owns cell eligibility and catalog approval. This cell does not own arbitrary repository editing, shell execution, repository creation, compute dispatch, or a replacement workflow runtime.

Creation initializes these responsibilities only. The assistant feature, serving implementation, recipe publication, and consumer integration require subsequent consultation and implementation against an agreed versioned contract.
