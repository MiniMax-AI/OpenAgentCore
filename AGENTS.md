# OpenAgentCore development

OpenAgentCore is the independent execution service behind Parsar. Product business
code, its database and migrations stay in the Parsar repository. Read
[CONTRIBUTING.md](CONTRIBUTING.md) before changing code.

## Interfaces and credentials

Core serves three namespaces. Each has one kind of caller and its own credential;
no credential works in another namespace.

| Namespace | Caller | Credential | Contents |
| --- | --- | --- | --- |
| `/v1` | Applications | Project API key | Exactly the pinned OpenAI Agents API route set ([upstream.json](contracts/agents-api/upstream.json): SDK 3.13.0, `agents=v1`). Fields the official types lack live only in `x_agents_core` on Agents and Sessions (`harness`, `model_provider`). Nothing about deployment belongs here. |
| `/core/v1` | Core Web's server and operator scripts | Core key; Core stores its digest | Everything about operating the deployment: Projects and keys, resource reads and deletion, Session archive, credential issuance (node enrollment tokens, executor credentials), observability, audit, sandbox deployment and nodes. |
| `/api/v1` | Nodes, Runtime daemons, self-hosted executors | Machine credentials: enrollment tokens and executor credentials issued through `/core/v1`; nodes register their own credential with an enrollment token; Core writes daemon credentials into hosted sandboxes | Machine connections only; each credential works only on its own routes. |

- Core Web calls only `/core/v1`, through the Core clients in
  `packages/agents-client` (`AdminClient`, `SandboxAdminClient`,
  `CoreMetricsClient`). It never calls `/v1` or `/api/v1`, never uses
  `OpenAIAgentsClient` and never forwards machine transport.
- Users sign in to Web with the Core key. Web's server forwards signed-in,
  same-origin `/core/v1` requests with that key and never sends it to a browser.
  The Core key cannot call `/v1`; a Project API key cannot call `/core/v1`.
- A new console need becomes a `/core/v1` route. Never add console or operations
  features to `/v1`, or application features to `/core/v1`.
- List every route with its caller and credential in [docs/api/README.md](docs/api/README.md).

## Ownership

Backend: Core, `contracts/`, `packages/agents-client`, `services/core-console`,
the daemon, adapters, node service and installer. Frontend: `apps/web` and
`docs/web`. Agree on API shapes across the boundary; do not edit the other side's
code to make a test pass.

## Making changes

- Work in an isolated Git worktree on a feature branch and submit a PR.
- Run `make sqlc-generate` after query changes and `make openapi` after handler
  annotation changes; review the generated diffs. `make openapi` writes
  `openapi.yaml` (`/v1`), `core.openapi.yaml` (`/core/v1`) and
  `runtime.openapi.yaml` (`/api/v1`) in `contracts/agents-api/`. Update the
  matching contract there and the API index in the same change.
- Do not add product database dependencies, bypass Core execution ownership, or
  reproduce execution truth in a client.
- Run `make check` before reporting completion.

## Review

Choose independent blind review by risk: security, shared lifecycle ownership or
cross-package behavior. Give the reviewer only requirements, acceptance criteria,
boundaries, the repository path and the comparison baseline. Small verified fixes
may use self-review. Never use `codex exec` as a reviewer.

Documentation and code comments are English; user-facing copy may be bilingual.
