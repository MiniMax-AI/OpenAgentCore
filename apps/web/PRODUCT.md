# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary user is the administrator who deployed Parsar Core: a self-hosted,
OpenAI Agents API compatible execution service. After signing in to the paired
console they need to answer quickly: is the service healthy, is there enough
sandbox capacity, how much is each project using, and where is work failing.
They also create projects and issue their keys, enroll execution nodes, and clean
up or redistribute assets between projects.

API callers (application developers, and Parsar itself) use the Agents API from
their own code with the keys of their project, not this console. The console is
the administrator's management tool, comparable to what the provider of a hosted
API runs internally: it manages the service and its projects, it does not build or
run things on a caller's behalf.

## Product Purpose

A management console for one Parsar Core deployment. Success: the administrator
lands on health, capacity, usage and failures across every project; inspects any
project's Agents, Environment templates, Skills, Files, Vaults and Session history
together with the API key that created each of them; deletes assets (for example a
leaked Credential); manages projects and their named keys; and administers sandbox
nodes.

## Positioning

The console runs beside the administrator's own Core, with execution, files and
credentials on infrastructure they control. It shows only evidence Core actually
reports and never invents readiness, traffic or zero values for missing data. It
is not a playground: there is no Agent builder, Session composer or request
workbench.

## Operating Context

- Paired console (`services/core-console`): the administrator signs in with the
  deployment's Core key, the administration credential the installer writes to
  `secrets/core.key` under the installation directory (by default
  `~/.parsar/core/secrets/core.key`; keeping and rotating it is described in
  [Core key](../../docs/getting-started/operations.md#core-key)). There are no
  console accounts or usernames. The browser sends the key only to sign in and
  keeps only the session cookie; the console server holds the Core key and forwards
  the Web API (`/core/v1/**`, including sandbox administration under
  `/core/v1/sandbox/**`). The console never calls `/v1`.
- The Core key is not an Agents API identity and cannot call `/v1`. An administrator
  who wants to call the Agents API issues a project API key like any other caller.
- `/console/config` reports the node installer (`node_installer`,
  `node_installer_sha256`) and the self-hosted executor installer
  (`self_hosted_installer`, `self_hosted_installer_sha256`); an installer is
  offered only with a 64-hex digest. It also lists the providers it has node files
  for (`node_artifacts`); without the deployment's provider, Add node says so and
  issues no command. Signing in grants administration, so sandbox
  administration is available unless the console explicitly reports
  `sandbox_admin: false`; then the Nodes page explains that it is not configured
  and the fleet figures show as unavailable.
- Chinese and English UI; light and dark themes; reduced motion honored.

## Information Architecture

- **Monitor**: Overview (service status, running Sessions, sandbox slots, Sessions
  needing attention, 24-hour Session activity, the topology of Core and its nodes
  with a popover glance at each, the attention table, usage by project), Agent metrics (requests, errors,
  duration, tokens, models, tools, Agents and API keys for 1 h / 6 h / 24 h / 7 d),
  Sandbox metrics (node capacity and hosted Runtimes across projects; a node or a
  sandbox opens in a dialog with its figures and CPU and memory charts), Session log
  (every Session, read-only, opening one Session's history; a self-hosted
  Session's page also has its environment's executor credentials).
- **Resources**: Agents, Environment templates, Skills, Files, Vaults. Each list
  shows one project or all projects, with a Project column when all are shown and a
  Creator column naming the creating key. Detail pages show the resource's facts
  and offer Delete.
- **Platform**: Projects and keys (projects, their assets and usage, named keys,
  write history), Nodes (sandbox setup as pages — where sandboxes run, the
  backend or E2B account, the size of each sandbox (own machines only; E2B takes the
  template build's), a review, and advanced settings
  with the complete form — then the node list with each node's capacity, host
  figures and allocations, enrollment, renaming, sandbox limits and removal; Add node
  asks for the node's sandbox limits before it issues the one-time command, which installs
  the node with sudo as a system service (a disclosure gives the command without sudo, as a
  user service); it issues none before the installation is read, while the public URL is
  loopback, or when the console lacks the provider's node files; after Remove, a dialog gives
  the host's uninstall command), System (the
  installation's public address, API base URL, installation ID and source commit, read-only;
  each harness's default model, set, replaced or cleared there beside its read-only startup
  state; the sandbox configuration every project shares, with a link to Nodes where it
  changes; and Core's startup settings from config.json, with the file and the apply command
  that change them).
- A node whose provider is not ready names the reason (Docker unreachable, no Docker
  limits, missing Runtime image, no KVM, missing microsandbox components, a host too
  small) and its fix in the help tip beside its status, wherever that status shows.
- **E2B deployments** have no machines: the Nodes entry becomes Sandbox backend,
  and Overview and Sandbox metrics show the sandboxes Core holds in E2B's cloud
  (running, starting, size, template build) instead of node capacity, with no node column
  or Add node action; a sandbox's dialog adds its disk use.
- **microsandbox** suspends idle sandboxes into snapshots, so its nodes show how
  many sleep (Core's retained minus active) on the Nodes list, a node's page, Sandbox
  metrics and Overview; a node's allocations show how long each has been suspended and
  about when Core reclaims it. Docker never suspends and shows none of it.
- **Getting started**: signing in opens the console on the Overview; nothing is
  forced first. While a step is to do, a Getting started checklist on the Overview
  shows four steps, in any order, each with its state and one action: sandboxes
  ready (a saved deployment and a node online and ready, or a saved E2B deployment
  whose template build is not reported as not ready), a default model on the default
  harness (on any enabled harness when none is default),
  a project with an active key, and a first Session. Completion comes from reads the
  console already makes. It can be hidden; Show Getting started in the sidebar
  opens it again, and it ends with a brief "You're set". The optional
  three-chapter tour of the console (Monitor, Resources, Platform) opens from it,
  on the sign-in stage.
- Terminology: API terms stay in English in the Chinese UI (Agent, Session, Turn,
  Skill, Vault, Credential, API key). The sign-in credential is the Core key
  ("Core Key"); keys issued in a project for applications are project API keys
  ("项目 API Key").

## Capabilities and Constraints

- **Projects and keys.** A project owns an isolated set of assets shared by all of
  its named API keys; projects do not see each other's assets. Issuing or revoking
  a key never touches assets. Archiving a project revokes every key and keeps its
  assets viewable and deletable. Key plaintext is shown once, at issuance, and never
  stored by the console. Beside it the console tells developers to set
  `OPENAI_BASE_URL` (the installation's API base URL) and `OPENAI_API_KEY` (the key).
- **Web API only.** Every read and write goes through `/core/v1/**`. The console
  holds no API key and sends nothing to `/v1`.
- **No asset writes except delete.** Assets are created and changed only by
  a project's keys through the Agents API. The console does not create or edit
  Agents or Templates, upload Skills or Files, create or replace Credentials, start
  Sessions, send input or cancel work. Deletion follows the public deletion rules;
  a busy Session is not deletable and the console never cancels work to make it so.
- **Secrets stay write-only.** Credential tokens, Template environment variables and
  setup commands are never returned, to the administrator included. An Agent's saved
  model provider shows its protocol, base URL, limits and whether a key is configured,
  never the key.
- **Creators.** Core records the key behind every write. The console shows the
  creating key of each asset and a project's write history; an asset an
  administrator copied in an earlier release shows as Admin copy and an asset
  without a record as Unknown.
- **Session history is read-only.** A Session page reads the Session, its Items and
  Turns and polls while work is in flight; there is no live event stream.
- **Executor credentials.** Only Core issues the credential file a self-hosted
  executor needs, with the deployment's Core key. A Session page whose environment
  is self-hosted has an Executor credentials section: issue a credential (shown
  once as one line of JSON, to copy or download, never stored), rotate it (the
  old one stops working immediately) or revoke it (the executor disconnects and
  won't retry; its container keeps running until stopped). The file lets one
  executor connect for that environment only; it cannot call the Agents API.
- **Connect a host.** When the console serves the self-hosted installer, the
  section also gives the command that installs the executor on the
  administrator's host from Core's `public_url` (checksum-verified, no secret in
  it; the installer asks for the credential at a hidden prompt, or reads
  `--credential-file`). The same command is safe to rerun. Revoking or rotating
  disconnects the host's executor; reconnecting takes that same credential,
  rotated (Rotate on a revoked row restores it), and the same command, because a
  newly issued credential does not reconnect an environment that already
  connected. Without a `public_url`, with a loopback one, or when the Session's
  `remote_url` is not `wss://`, the section says why instead of showing a
  command.
- **Figures.** Project, Agent and key usage comes from Core's summary; Agent run,
  tool and activity figures are still assembled in the browser from bounded reads
  and state their coverage. Metrics that need new Core endpoints are recorded as
  backend requirements, not simulated. Usage is cumulative per Session and is not
  billing.
- Runtime CPU and memory exist only for Core-managed hosted sandboxes.
- Preserve workflow safety: confirmed deletion, no automatic retry of uncertain
  writes, no secrets in browser storage.

## Brand Commitments

- Product name: Parsar Core. Parsar mark assets in `apps/web/public/`.
- Keep the Parsar visual identity shared with the public landing (`site/`):
  neutral grays and a quiet indigo accent. The console uses Inter and Geist Mono
  on Beautiful UI's foundation tokens and structure; `DESIGN.md` records the system.

## Evidence on Hand

- Browser acceptance in `apps/web/e2e/`: one test per acceptance behavior against
  `fixture-console.mjs`, a synthetic console service with deterministic data.
- No customer data, benchmarks or usage claims exist; do not fabricate them.

## Product Principles

1. Operations first: health, capacity, usage and failures lead.
2. Manage, don't operate: the administrator views, deletes and manages projects
   and keys; assets belong to the projects' keys.
3. Report evidence, not assumptions: missing data stays visibly missing.
4. One page grammar everywhere: the same header, toolbar, tables, metrics and
   states on every screen.
5. Projects are the unit: every asset shows the project that owns it and the key
   that created it.
6. Deployment-level truth (nodes, configuration) is distinct from project assets.

## Accessibility & Inclusion

Keyboard navigation with visible focus, reduced-motion support, and bilingual
Chinese/English copy through the existing i18n modules.
