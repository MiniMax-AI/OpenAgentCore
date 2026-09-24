# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary user is the operator who deployed Parsar Core: a self-hosted,
OpenAI Agents API compatible execution service. After signing in to the paired
console they need to answer four questions quickly: is the service healthy, is
there enough sandbox capacity, how much is it being used, and where is work
failing. They also enroll execution nodes, issue and revoke Agent API keys and
keep Skills, templates and Vaults in order.

API callers (application developers) use the Agents API from their own code, not
this console. The console is the operator's back office, comparable to what the
provider of a hosted API runs internally: it manages the service, it does not
build things on the callers' behalf. Parsar Core is open source and a small team
deploys one instance for itself, so the console serves one deployment and one
project; there is no tenant or customer concept.

## Product Purpose

An operations back office for one Parsar Core deployment, comparable in role to
the provider-side console behind a hosted API rather than the developer
platform in front of it. Success: the operator lands on health, capacity, usage
and failures; inspects Agents, Sessions and Turns as resources; manages nodes,
keys and configuration; and reaches the debugging playground only when needed.

## Positioning

The console runs beside the operator's own Core, with execution, files and
credentials on infrastructure they control. It shows only evidence Core
actually reports and never invents readiness, traffic or zero values for
missing data.

## Operating Context

- Paired console (`services/core-console`): single local administrator account,
  forwards `/v1` with the console's project credential and allowlisted
  `/core/v1/sandbox` admin routes with its server-side admin token.
- Web-only consoles may connect to an existing Core without sandbox admin or
  key management capabilities (`/console/config`).
- Chinese and English UI; light and dark themes; reduced-motion honored.

## Information Architecture

Confirmed with the user on 2026-09-24 (top-level groups are theirs; the pages
inside each group were left to design):

- Monitor: Overview (health, capacity, Session status, attention), Agent
  metrics, Sandbox metrics, Session log. There is no System page and no
  platform-facts strip; maintenance shows as a status beside the deployment.
- Resources: the objects callers use by ID in this single-project deployment —
  Agents (list, create, edit), Skills, Environment templates, Vaults.
- Platform: Nodes, API keys (still called API keys; one key per caller name is
  not enforced by Core).
- Playground: API workbench (fixed-format requests: form on the left, the exact
  curl and JSON body on the right, copy or send, response below; also the
  look-up-by-ID tool), Session console, and the first-run introduction.
  Agents and Sessions created in the workbench carry
  `metadata.created_by = console-playground`.
- Terminology: API terms stay in English in the Chinese UI (Agent, Session,
  Turn, Skill, Vault, API key).
- First-run introduction: one continuous animation — the request path (your
  code → Parsar Core → machines and sandboxes → Agent) is the progress
  indicator; each step lights the next segment.

## Capabilities and Constraints

- Data comes only from the existing public client (`packages/agents-client`)
  and console routes; Core has no aggregate or statistics endpoint. Project
  totals are aggregated in the browser from complete paginated lists (bounded
  at about 10,000 items). Deployment-wide data is limited to sandbox nodes,
  allocations and deployment state.
- Runtime CPU and memory exist only for Core-managed hosted sandboxes; runtime
  history is per Session and bounded.
- Metrics that need new Core endpoints are recorded as backend requirements for
  discussion, not simulated in the browser.
- Preserve existing workflow safety: confirmed deletion, no automatic retry of
  uncertain writes, connection-change discards, secrets never in browser storage.

## Brand Commitments

- Product name: Parsar Core. Parsar mark assets in `apps/web/public/`.
- Keep the existing Parsar visual identity shared with the public landing
  (`site/`): neutral grays, violet/indigo accent, system UI fonts. Unify the
  console within it rather than replacing it.

## Evidence on Hand

- Fixture Core for development and acceptance: `apps/web/e2e/fixture-core.mjs`.
- No customer data, benchmarks or usage claims exist; do not fabricate them.

## Product Principles

1. Operations first: health, capacity, usage and failures lead; building and
   chatting are debugging tools.
2. Report evidence, not assumptions: missing data stays visibly missing.
3. One page grammar everywhere: the same header, actions, tables, metrics and
   states on every screen.
4. Resources are inspected before they are edited.
5. Deployment-level truth (nodes, keys, configuration) is distinct from
   project-level resources.

## Accessibility & Inclusion

Keyboard navigation with visible focus, reduced-motion support, and bilingual
Chinese/English copy through the existing i18n modules.
