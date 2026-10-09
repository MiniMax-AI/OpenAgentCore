# Codex native MCP client invalidation

The Harness catalog owns the Codex version. [source.json](source.json) identifies its exact upstream source. [invalidate-mcp.patch](invalidate-mcp.patch) adds the local client invalidation operation required by the Runtime–Harness cancelled stdio MCP lifecycle. It leaves Codex's model and tool loop, thread history and ordinary MCP reconciliation in their native owners.

Run `python3 packages/codex-runtime/check-source.py /path/to/codex` against a clean checkout before applying the patch. The check verifies the source revision, catalog version and patch applicability and prints the patch digest for build provenance. Generated TypeScript and JSON schema exports are not native build inputs; the Rust protocol definitions and request macro compile the added operation directly.

## Private app-server protocol

The patched app-server declares `mcpServerInvalidation: true` in its `initialize` response. The Go adapter requires this declaration before preparing stdio MCP bindings; an upstream binary with the same version is insufficient.

`mcpServer/invalidate` accepts `{ "threadId": "<owned-root>", "serverNames": ["<binding-label>"] }`. Names must identify configured stdio servers. The method resolves the existing native spawn subtree, includes all its loaded threads, and invalidates the selected clients under each Session's existing MCP refresh semaphore. The operation is held by native owned tasks if its RPC caller disappears. Unloaded threads own no reusable runtime connection. Other labels and unrelated thread families remain untouched. Callers must first drain native work in the owned family and confirm closure of the selected host process scopes.

Every captured thread's current configuration is checked before invalidation starts; a descendant may omit a selected binding, but a matching non-stdio binding rejects the operation. The supported owner family is Codex's native thread-spawn subtree. OAC's unattended approval profile does not enable Guardian review, and both upstream Guardian implementations explicitly clear their MCP server configuration; arbitrary internal sessions outside this subtree are not added to this adapter operation.

Success returns `{ "serverNames": ["<binding-label>"] }`, sorted and deduplicated, after selected client shutdown completes. Ready clients have entered the native `ClientState::Closed`; pending startup is cancelled. Existing prepared calls retain their original client and cannot redirect to a replacement. This receipt acknowledges local invalidation, not remote tool completion or replacement readiness.

After the host `StopMCP` receipt and native invalidation receipt, the adapter calls maintained `config/mcpServer/reload`. Its dirty state applies through the existing native refresh flow before a subsequent Turn uses MCP. Reconciliation replaces closed clients and reuses unchanged healthy connections. No eager `mcpServerStatus/list` discovery, synthetic model Turn, polling delay or whole app-server restart establishes this fence.

The patch's `targeted_invalidation_*` Rust tests exercise closed-client replacement and unaffected connection identity through native reconciliation using controlled in-process transports. The Go adapter tests exercise cancellation latching, late starts, native call identity, host closure ordering, explicit declaration, receipt failures and Executor reuse. Native compilation and focused Rust tests must run in the qualified source build; source applicability alone does not qualify the binary.
