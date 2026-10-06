# Parsar Agent workbench

An independently started small application on OpenAgentCore. Manage models, Skills, HTTP MCP services and runtime configurations; compose reusable Agent configurations and start independent Sessions. Continue the same Session to keep its history and live workspace. There is no task layer or shared workspace.

## Run

Use Node 22.13+ (for built-in SQLite) and pnpm 10.30.3. Configure [Core](../../docs/getting-started/install.md), a deployment model/provider, and a Project API key. Store secrets in a private environment file outside the checkout:

```sh
export OAC_EXAMPLE_CORE_URL='http://127.0.0.1:8091'
export OAC_EXAMPLE_PROJECT_KEY='<project-api-key>'
pnpm --filter @oac/parsar-example... install --frozen-lockfile
pnpm --filter @oac/parsar-example dev
```

Open http://127.0.0.1:18180. The Core URL is an origin without `/v1`; remote origins require HTTPS. `OAC_EXAMPLE_PORT` changes the local port. For a built version, run `pnpm --filter @oac/parsar-example build` and then `pnpm --filter @oac/parsar-example start`.

## Main flow

1. **Models:** enter a Provider name, Base URL and API key, then fetch its `GET /v1/models` list. Select models with checkboxes or add custom model IDs, then save the Provider and selected models together. The dialog shows discovered and selected counts. For manual entry, skip discovery. No default group is created. Keys are stored in the local restricted SQLite file and never returned to the browser; leaving the key blank while editing preserves it. Discovery sends only that Provider's key and does not follow redirects. The list expects the OpenAI-shaped `data: [{id: "..."}]` response. Discovery failures leave manual entry available. Codex and Claude Code workspace Sessions pass the selected Provider URL/key explicitly to Core, using the harness protocol. These Sessions require an HTTPS Base URL and API key. Text-only Sessions and hosted MiniMax Code use Core's deployment connection; the creation dialog states that their Provider is only a catalog group. The example does not yet expose MiniMax Code's required token limits.
2. **Skills:** create a SKILL.md resource or upload a ZIP. Inspect versions, upload a new version, and choose the default. Core validates and stores bundles.
3. **MCP:** save anonymous HTTPS endpoints and bind them to Agents. Hosted Sessions install an inline environment Plugin; text-only Sessions use Core's service-origin MCP. Hosted HTTP MCP supports Claude Code and Codex, not MiniMax Code. Authentication, Vault management and OAuth setup are not included.
4. **Runtimes:** choose a Core-managed sandbox, text-only environment, or user machine. User machines select Linux/macOS/Windows and an existing absolute workspace path, plus an optional local capability directory. Each Session gets its own daemon installation and credential; choosing the same workspace path shares the host's files, so use distinct paths for isolated work. These are placement configurations, not online machine identities. Machine enrollment, fixed-node routing and self-hosted registration are omitted.
5. **Agents:** combine a model, harness, instructions, Skills and MCP services. Create, copy and edit configurations without allocating a runtime.
6. **Sessions:** open an Agent, choose a runtime and send the first message. Read streaming replies and tool activity, continue the conversation, cancel a running turn and reopen history. Unsupported Skill/MCP/runtime combinations are explained before creation. The Session's **Files** dialog uploads one file at a time (up to 5 MiB) through Core's public Environment Files API while idle. Each upload gets a unique path under `/workspace/inputs`; a path relative to the working directory is inserted into the message draft, not sent automatically. Unsent drafts, including uploaded file paths, survive reloads and navigation in the same browser tab and remain scoped to the Session. This keeps the API's `/workspace` alias distinct from the physical user-machine directory. Ask the Agent to save generated files under `./outputs`; published Artifacts can be paged and downloaded from the same dialog, even when the live workspace is unavailable. Downloads remain binary; errors stay in the dialog. Text-only Sessions have no file actions. Each hosted Session gets its own workspace; text-only has no workspace.

Agent edits and catalog updates apply to future Sessions. Core freezes existing Session configuration, including resolved Skill versions. Continuing a Session retains history and its workspace while the sandbox exists; archiving/replacing a sandbox is not a persistence guarantee. Saving configuration alone does not verify model availability or MCP connectivity. Skills require a hosted runtime.

## Ownership and storage

The loopback Node server has two small responsibilities: a fixed allowlist proxy for public Core resources, and `/app/` CRUD for product-owned configuration. One SQLite table stores typed JSON records. There is no ORM, background scheduler, execution database.

SQLite files live in `~/.oac/data/parsar-example/`. Set the absolute `OAC_EXAMPLE_DATA_DIR` to relocate them. A hash of the Core origin and Project key selects the file; changing either selects a different local catalog. Back up the SQLite file with the server stopped. The Core Project key is not stored in it. Provider keys are stored there. Builds and caches use `${OAC_DEV_HOME:-$HOME/.oac}`.

Use one server process and a dedicated Project for this local single-user example. The server rejects cross-origin writes and keeps the Project key out of browser responses. Session creation freezes a pending request in SQLite before calling Core, and uses the local Session ID as its idempotency key. An uncertain response can be recovered from the Agent's Session list, including after a server restart. Successful creation leaves only the Core Session reference and local title/Agent/runtime relationship; Core owns messages, turns and execution status. The browser consumes public SSE text deltas through a non-buffering proxy, reconnects after interruption, and reconciles with durable history to avoid duplicate final replies. Each send records request-return latency and first nonempty text-delta latency from browser request start. These independent observations remain in sessionStorage across reloads; missing observations are shown as a dash rather than inferred from history. Definite validation rejection removes the pending request so it can be corrected. Use one server process. Deleting a local resource is blocked while another local record references it; Session deletion/archival is outside this example. Skill deletion is a separate explicit Core operation. Earlier templates and instances become Agent configs on startup; they are never presented as actual Sessions.

A few more implementation details:

- **Providers and models** are saved together in one SQLite transaction. Editing reads a Provider and its models as one snapshot; changing a model advances the Provider's revision.
- **Browser calls** use `OpenAIAgentsClient`, with a small public HTTP reader for Artifacts (not yet exposed by that client); only Session creation goes through a small server adapter. Closing the browser aborts the upstream stream, never the running Session.
- **Workspaces:** hosted Sessions each get their own. User-machine Sessions use the selected host directory, so the same path means shared files.
- **Skills on a user machine** come only from local capability directories in this example; it rejects Agents bound to managed Skills instead of ignoring them. Core itself can deliver managed Skills to user machines through `x_agents_core.environment`.

## Validation

```sh
make check-example
make check
```

The example gate runs TypeScript, proxy and SQLite persistence/binding tests, a production build and browser acceptance. Install Chrome with `pnpm --filter @oac/parsar-example exec playwright install chrome` if necessary. Browser fixtures use ports 18180/18181 and isolated SQLite data under `~/.oac/tests/parsar-example/`. They verify Provider groups and model selection, live text before durable completion, resource management, multiple Sessions, continuation, cancellation, lost-response recovery, Skill versions and mobile/help behavior. Synthetic fixtures are not model execution.

The opt-in live browser probe requires an already started example backed by an isolated real Project:

```sh
OAC_EXAMPLE_LIVE_URL=http://127.0.0.1:18180 \
OAC_EXAMPLE_LIVE_MODEL='<configured-model>' \
pnpm --filter @oac/parsar-example test:live
```

It leaves sample resources for inspection and executes the configured model. It checks Skill/Plugin installation, same-Session file reuse, and workspace isolation between two Sessions. It asks the model to call DeepWiki; inspect the recorded tool activity to distinguish a successful call from an attempted call.

## Connect a user machine

Create a user-machine runtime and start a Session with an Agent using Codex or Claude Code. No initial Turn is sent. Open **Connect user machine** and run the Core-provided command on the target host. The installer downloads the matching native distribution, installs the selected harness and starts the connection. Windows Claude Code also requires Git Bash. The page reads Core's public Environment status and enables sending after it reports connected. The example backend does not need or accept the administrator Core key. Commands carry temporary Environment-scoped authorization; do not share them. Session refresh renews them.

The selected model Provider needs an HTTPS Base URL and API key. Its protocol is Responses for Codex and Anthropic for Claude Code. Core freezes and delivers the configuration to the enrolled executor. Local pending creation requests contain that configuration but browser Session responses omit the entire pending request.

Self-hosted capability sources are local directories under the current Core API. Managed Skill bindings fail explicitly; configure local Skills/Plugins in the runtime's capability directory before connecting. Bound HTTP MCP services also fail explicitly for self-hosted Sessions; configure them in local Plugins instead. MiniMax Code self-hosted configuration is not included in this example because its required token limits are not exposed.

The daemon runs with the starting user's permissions and adds no sandbox. Runtime home is separate per Session; stopping it preserves local files. Credential rotation and revocation remain Core console operations. Reuse the original installation directory when reconnecting.
