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
leaked Credential) and copies them between projects; manages projects and their
named keys; and administers sandbox nodes.

## Positioning

The console runs beside the administrator's own Core, with execution, files and
credentials on infrastructure they control. It shows only evidence Core actually
reports and never invents readiness, traffic or zero values for missing data. It
is not a playground: there is no Agent builder, Session composer or request
workbench.

## Operating Context

- Paired console (`services/core-console`): a single local administrator account
  (legacy installations keep Basic authentication). The browser holds only the
  console sign-in; the console server holds the deployment administrator
  credential and forwards the Web API (`/core/v1/admin/**`) and sandbox
  administration (`/core/v1/sandbox/**`). The console never calls `/v1`.
- The console account is not an Agents API identity. An administrator who wants to
  call the Agents API issues a key in a project like any other caller.
- `/console/config` reports whether sandbox administration is available; without
  it the Nodes page explains that it is not configured and the fleet figures show
  as unavailable.
- Chinese and English UI; light and dark themes; reduced motion honored.

## Information Architecture

- **Monitor**: Overview (service status, running Sessions, sandbox slots, Sessions
  needing attention, 24-hour Session activity, the topology of Core and its nodes
  with a popover glance at each, the attention table, usage by project), Agent metrics (requests, errors,
  duration, tokens, models, tools, Agents and API keys for 1 h / 6 h / 24 h / 7 d),
  Sandbox metrics (node capacity and hosted Runtimes across projects; a node or a
  sandbox opens in a dialog with its figures and CPU and memory charts), Session log
  (every Session, read-only, opening one Session's history).
- **Resources**: Agents, Environment templates, Skills, Files, Vaults. Each list
  shows one project or all projects, with a Project column when all are shown and a
  Creator column naming the creating key. Detail pages show the resource's facts
  and offer Copy and Delete.
- **Platform**: Projects and keys (projects, their assets and usage, named keys,
  write history), Nodes (sandbox setup as pages — where sandboxes run, the
  backend or E2B account, the size of each sandbox, a review, and advanced settings
  with the complete form — then the node list with each node's capacity, host
  figures and allocations, enrollment and removal), System (startup configuration
  and the sandbox deployment, including each sandbox's size and the Runtime).
- **First run**: after the administrator account is created and while no project
  exists, full-screen steps outside the shell create the first project (default name
  `Default`) and its first key, show the plaintext once with an example request,
  then give a three-chapter tour of the console (Monitor, Resources, Platform)
  before opening it. Signing in uses the same stage.
- Terminology: API terms stay in English in the Chinese UI (Agent, Session, Turn,
  Skill, Vault, Credential, API key).

## Capabilities and Constraints

- **Projects and keys.** A project owns an isolated set of assets shared by all of
  its named API keys; projects do not see each other's assets. Issuing or revoking
  a key never touches assets. Archiving a project revokes every key and keeps its
  assets viewable, deletable and copyable. Key plaintext is shown once, at issuance,
  and never stored by the console.
- **Web API only.** Every read and write goes through `/core/v1/admin/**` or
  `/core/v1/sandbox/**`. The console holds no API key and sends nothing to `/v1`.
- **No asset writes except delete and copy.** Assets are created and changed only by
  a project's keys through the Agents API. The console does not create or edit
  Agents or Templates, upload Skills or Files, create or replace Credentials, start
  Sessions, send input or cancel work. Deletion follows the public deletion rules;
  a busy Session is not deletable and the console never cancels work to make it so.
- **Copies are independent.** A copy lands in another active project with new IDs,
  optionally with its dependencies, and is never synchronised afterwards. Core
  re-encrypts secrets internally; OAuth Credentials with a refresh configuration are
  skipped. Sessions cannot be copied.
- **Secrets stay write-only.** Credential tokens, Template environment variables and
  setup commands are never returned, to the administrator included.
- **Creators.** Core records the key behind every write. The console shows the
  creating key of each asset and a project's write history; an administrator's copy
  shows as Admin copy and an asset without a record as Unknown.
- **Session history is read-only.** A Session page reads the Session, its Items and
  Turns and polls while work is in flight; there is no live event stream.
- **Figures.** Project, Agent and key usage comes from Core's summary; Agent run,
  tool and activity figures are still assembled in the browser from bounded reads
  and state their coverage. Metrics that need new Core endpoints are recorded as
  backend requirements, not simulated. Usage is cumulative per Session and is not
  billing.
- Runtime CPU and memory exist only for Core-managed hosted sandboxes.
- Preserve workflow safety: confirmed deletion, no automatic retry of uncertain
  writes, idempotent copies, no secrets in browser storage.

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
2. Manage, don't operate: the administrator views, deletes, copies and manages
   projects and keys; assets belong to the projects' keys.
3. Report evidence, not assumptions: missing data stays visibly missing.
4. One page grammar everywhere: the same header, toolbar, tables, metrics and
   states on every screen.
5. Projects are the unit: every asset shows the project that owns it and the key
   that created it.
6. Deployment-level truth (nodes, configuration) is distinct from project assets.

## Accessibility & Inclusion

Keyboard navigation with visible focus, reduced-motion support, and bilingual
Chinese/English copy through the existing i18n modules.
