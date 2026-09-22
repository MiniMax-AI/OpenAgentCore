# Environment Templates and initialization

Core owns reusable configuration through the five pinned
[Template operations](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/templates.py).
Templates do not contain a running workspace and do not select a provider image.
An E2B `templateID:build_UUID` remains private operator packaging configuration.
Each referencing Session obtains its own Environment through the same initialization
and five-operation SandboxProvider path as inline configuration.

## Supported batch

- Create, retrieve, update, delete and list under `/v1/agents/environments/templates`.
  Every operation requires project authentication and `OpenAI-Beta: agents=v1`.
  CRUD/list works without an execution deployment.
- Optional nullable name, preserved verbatim, with a local 1–256 Unicode character
  bound. Network supports `enabled`, `disabled` and exact-host `restricted`; omitted/null create network
  defaults to the pinned enabled policy. Update omission preserves; supplied name
  or network replaces, with null clearing name or resetting network.
- Empty/null installation fields retain empty defaults. Responses contain safe
  metadata and never `env`, `setup_commands` or inline file data. Initial files are
  supported as described below, together with inline/referenced Skills, env, ordered setup and system/npm/Python packages; remaining populated installations reject explicitly.
- Listing uses `after`, `limit` (1–100, default 20), and `order` (default `desc`).
  Creation timestamp plus ID supplies stable local ordering. Missing/foreign IDs
  and cursors return the same not-found result. No compute is allocated by CRUD.
- Session `environment_template_id` resolves under the caller's tenant. Omitted
  network inherits; enabled can narrow to restricted or disabled. Restricted can
  narrow to an exact-host subset or disabled; disabled cannot widen. Effective
  configuration is frozen without passing the template ID to execution.
- Updating/deleting a template does not change existing Sessions. Creation retries
  recover recorded caller intent before template lookup, including after deletion;
  changed intent conflicts. This is the existing local retry policy, not a claim of
  complete upstream idempotency semantics.

```python
from openai import OpenAI

client = OpenAI(base_url="https://your-core.example/v1", api_key="your-project-key")
template = client.beta.agents.environments.templates.create(
    name="Python workspace", network={"access": "disabled"},
    env={"APP_MODE": "analysis"}, packages={"python": ["packaging==26.0"]},
    setup_commands=[{"command": "mkdir -p /workspace/outputs"}]
)
session = client.beta.agents.sessions.create(
    agent={"model": "your-configured-model"},
    environment={"type": "openai_hosted", "environment_template_id": template.id},
    input="Create /workspace/outputs/report.txt containing the result of 6 * 7.",
)
# Inspect Session/Turn/Items and retrieve published Artifacts after completion.
# Delete the Session to reclaim its Environment; template deletion is independent.
```

## Initial files

Both inline hosted configuration and reusable templates accept `files` entries with
an absolute destination inside `/workspace`: `inline` with standard-base64 `data`, or
`file_id` referencing a project-owned Files API upload. The guide's limits are 50
initial files, 5 MiB per inline file, 10 MiB total inline content, and 50 MiB per
referenced file. Session/Template JSON requests allow 16 MiB for the base64 envelope.
Paths must be canonical, distinct and stay within the workspace; symlinks are not
followed. A failed install never starts native execution.

Configure `AGENTS_API_CREDENTIAL_KEY_FILE` with the existing execution-service
base64 32-byte encryption key. Template writes and Session resolution need it;
ordinary metadata reads do not. Template inline metadata contains type/path/size,
while references contain type/path/file_id. Sessions receive fresh file IDs and
sizes for both variants. Initialization keeps file data out of ordinary configuration,
resource responses, lifecycle events and command arguments. Templates keep references; each Session authorizes and
freezes its own encrypted source bytes. Later source deletion cannot change them.

Template `files` omission preserves on update; null/[] clears. In a referencing
Session, omission and null inherit, while a supplied list replaces the entire file
set, including `[]` clearing it. Paths are not merged between the two sources.
The effective list retains the existing validation and tenant-owned source checks.

Core initializes both paths with the same trusted file installer through Provider
RunCommand. Daemon authentication remains available, but native preparation and live
Files wait for all writes. Each file gets a two-minute transfer budget; the batch has
a thirty-minute local budget and shares maintenance scans with other allocations.
These are local operational limits, not verified upstream timing. Initial input
retains its existing five-minute admission deadline; large installations can use an
idle Session and wait for connected status before submitting input.

Uncertain writes and Core restart during initialization fail the new Environment and
reclaim it; they do not replay partial installation. After completion, reconnect and
native-history recovery preserve user modifications instead of reinstalling files.
Current Core-hosted Docker and all three harnesses use this lifecycle. The Provider
API remains five operations; public Templates are never E2B image templates and
remain hosted-only. E2B now uses user-managed Runtime enrollment through the official
SDK. Historical Core-managed E2B evidence below retains its original scope and does
not qualify that new chain.

## Skills and versioned references

Both templates and standalone hosted configuration accept project-owned Skill
references and inline Skill ZIPs. Upload a directory through the pinned SDK, then
reference its default version from a template:

```python
skill = client.skills.create(files=[
    ("report/SKILL.md", b"---\nname: report\ndescription: Create the report.\n---\nFollow the report procedure.", "text/markdown"),
])
template = client.beta.agents.environments.templates.create(
    skills=[{"type": "skill_reference", "skill_id": skill.id}]
)
session = client.beta.agents.sessions.create(
    agent={"model": "your-configured-model"},
    environment={"type": "openai_hosted", "environment_template_id": template.id},
    input="Use the report Skill.",
)
```

Core exposes the pinned `/v1/skills` resource, version and content operations using
ordinary project bearer authentication. No Agents beta header is required on those
resource routes. Metadata reads do not decrypt or fetch bundles. Content is encrypted
with its tenant, Skill and immutable version identity. ZIP uploads use `files` and
directory uploads use repeated `files[]`; the fixed SDK directory form above works.
SDK 3.13.0 drops a single FileTypes tuple during multipart extraction before sending
it. Use raw HTTP for a single ZIP with this fixed client; Core does not synthesize
missing bytes or alter the pinned SDK.

Templates preserve reference selectors: omission or null selects default at Session
creation, `"latest"` selects latest, and a positive version string selects that
version. A Session freezes tenant-authorized bytes and concrete version metadata
in its creation transaction. Later source deletion, default changes or template
updates cannot change that Session or its committed creation retry. A supplied
Session Skill list replaces the template list; omission inherits. Template
responses include `version: null` for an unresolved default selector; resolved
Session references retain a concrete version string. Null list overrides remain
unqualified and reject. See [resource selector qualification](resource-selector-semantics.md).
References return type/skill_id/version/name/description in Session metadata,
while template responses retain unresolved selectors. Confidential bundle content
never appears in these metadata responses. The common Runtime installation path
receives frozen files and descriptive metadata, without source or template IDs.

Inline Skill ZIPs use the same initializer:

```python
import base64
from pathlib import Path

skill = {
    "type": "inline", "name": "report", "description": "Create the report.",
    "source": {"type": "base64", "media_type": "application/zip",
               "data": base64.b64encode(Path("report.zip").read_bytes()).decode()},
}
template = client.beta.agents.environments.templates.create(skills=[skill])
```

Each archive contains one top-level folder with `SKILL.md` and optional supporting
files. The manifest name/description must match the request. Portable descriptive
frontmatter supports `name`, `description`, `license`, `compatibility` and string
`metadata`; native hooks, permission controls and subagent directives reject.
Local limits are 50 Skills, 5 MiB compressed and 20 MiB expanded per archive,
10 MiB compressed and 50 MiB expanded in total, and 1,000 entries per archive.
Regular files only: path traversal, links, duplicate destinations, special files
and invalid manifests reject. Content is inert during installation; executable
files retain their executable bit. These operational limits are not claims about
upstream limits.

Inline metadata contains only type/name/description. Archive content stays in encrypted,
resource-bound template and Session snapshots. Updates replace supplied `skills`;
omission preserves and null/[] clears. Existing Sessions retain their frozen
content after template update/deletion.

The shared initializer installs Skills under
`/environment/initialization/capabilities/skills/<name>` before setup and native execution.
Setup and native tools can read that tree but cannot write it; completed recovery
never reinstalls it. The public descriptor requests capability installation; the common daemon resolves protected metadata and paths for native preparation.
Codex registers native extra roots, Claude creates its own explicit Skill plugin
envelope, and MiniMax points its native user-global catalog at the shared root.
MiniMax retains disabled unrestricted built-in tools and uses its existing
isolated workspace tool worker. No Provider or model/tool loop is added.

Codex nested `SKILL.md` discovery, `agents/openai.yaml` native dependency
configuration and Claude inline/fenced shell preprocessing are not qualified in this batch and explicitly fail adapter
preparation. Other files are not interpreted as a public plugin installation.
The initial skill-only Plugin and capability-directory batch below extends installation;
environment-origin MCP has a separate qualification boundary described below. Native built-in Skill visibility
is not evidence of exact public tool-set parity. Qualification probes alone do not
establish complete public support; record real service acceptance separately.

## Skill-only Plugins and capability directories

A hosted
`plugins` entry uses the pinned inline ZIP source with type/name/description and
`.codex-plugin/plugin.json` inside its single archive root. The manifest declares
Skill directories with `skills`; complete package-relative resources are retained.
The same parser, template resolution and encrypted Session snapshot serve inline
and template requests. Only type/name/description appears in public Plugin metadata.
Template Plugin lists inherit on omission and replace when supplied. Template
updates accept null/empty clearing; explicit null Session overrides remain an
unconfirmed semantic and reject.

`capability_directories` currently accepts clean absolute paths within `/workspace`.
Initial files and setup can populate them. The shared initializer snapshots these
directories after setup, then publishes one protected installed manifest. Recovery
uses those installed bytes even if the source directory changes or is removed.
This timing and workspace restriction are local implementation choices, not claims
about unspecified upstream behavior. A supplied Session directory list replaces
the template list; omitted lists inherit. Missing, overlapping duplicate Skill
names, unsupported manifests and nonregular files reject initialization without
publishing completion. Directory-discovered Skills do not become fabricated
inline entries in public `skills` or `plugins` metadata.

The common daemon resolves the installed manifest before executable preparation;
read-only Files operations retain their existing minimal requirements. Native
adapters consume Runtime-owned Skill and package roots. Codex uses explicit roots,
MiniMax uses its native registry and Claude uses controlled envelopes with explicit
roots into a hardlinked content tree. Native component configuration is never
passed wholesale to a harness. Provider APIs and the native execution loops are
unchanged. No new installation/recovery lifecycle or framework is introduced.

The original accepted batch supported skill-only packages and inert empty MCP maps.
Environment-origin MCP uses the separate transport path below. Archives retain the shared 5 MiB compressed/20 MiB expanded/
1,000-entry limits. Plugin lists are limited to 50 entries and 10 MiB compressed;
combined installed capabilities are limited to 50 Skills and 50 MiB. Limits are
implementation bounds. Native shell preprocessing, dependency activation and
nested discovery retain the existing adapter restrictions.

Real standalone Docker acceptance on 2026-09-21 passed with the fixed official
SDK and raw HTTP against current Core/daemon/adapters on qualified native images:
Codex/Kimi (258.08s), Claude/Kimi (187.81s) and MiniMax Code/MiniMax (250.23s).
Each exercised two Plugin Skills with package-relative resources and one directory
Skill generated during setup, public Files/Artifacts and tenant rejection, retained
native history after Core/Runtime restart and source/template deletion, no repeated
initialization, cancellation with stopped effects and stable duplicate cancellation.
Operators verified nonempty private canaries and actual daemon credentials before
native scripts proved the contents unreadable; canary hashes survived cold recovery.
All owned execution resources were cleaned. The complete Core-only `make check`
passed, including dedicated real PostgreSQL checks and native packaging.

Evidence is under `~/.parsar/remediation/20260921/template-plugins/`:
`acceptance-summary.json`, `native-final-{codex,claude,mcode}.json`,
`runtime-builds.json` and `full-check-attempt2-result.json`. The shared reproducible
fixture is `services/agents-api/tests/official_environment_plugins.py`; operator
runners reuse existing standalone acceptance and private model configuration.
A user-authorized reused-context GPT-6 Astra high independent review of all 60
changed files found no material actionable findings. Those historical results do
not qualify Plugin MCP. Portable root `plugin.json` applicability and the unconfirmed
semantics above remain gaps;
these results do not establish complete Environment Templates or protocol compatibility.

## Environment-origin MCP Plugins

MCP-only and combined Skill/MCP packages use the same encrypted template/Session
snapshot and installation as Skill-only Plugins. The fixed format remains
`.codex-plugin/plugin.json` with `mcpServers: "./.mcp.json"`, or an omitted path
using the root `.mcp.json`. The file contains `mcpServers` keyed by server name.
An exact capability-directory Plugin root activates its MCP declarations; selecting
a parent directory discovers Skills without activating every nested MCP server.

The common parser accepts HTTP `url`, `bearer_token_env_var`, literal `http_headers`,
and stdio `command`, `args`, selected `env_vars`, package-relative `cwd`. It does not
resolve credentials. Runtime reloads frozen installed packages and resolves selected
values only from initialized caller env. A missing value fails instead of using a
model or daemon variable. Public `env_http_headers` remains unsupported; an adapter
may privately use native reference fields without changing public literal values.
Caller-owned env remains available to caller code under the normal env contract.

Stdio executes through the existing Runtime sandbox and a fixed static helper,
which applies selected env/cwd and directly execs the package command. A small
single-threaded launcher monitors the native parent process with Linux pidfds;
bwrap retains its parent-death protection against that stable launcher. This avoids
killing MCP servers when Codex recycles a spawning thread. Native MCP owns the
transport; the launcher does not parse or forward protocol messages. Dependencies must be installed in the ordinary
Environment initialization flow. The current implemented transport boundaries are:

| Adapter | Environment MCP implementation |
| --- | --- |
| Codex | Stdio; HTTP with literal headers and HTTPS bearer references |
| Claude Code | Stdio; anonymous HTTP or HTTPS bearer, without literal custom headers |
| MiniMax Code | Stdio; HTTP explicitly rejected pending safe native qualification |

All current Environment MCP execution requires enabled network. Restricted/disabled
HTTP and hosted stdio under a restricted/disabled policy are not qualified. Claude
literal headers are rejected because the pinned client expands them again and
forwards custom headers across origins. MiniMax ACP does not enable the native
custom-header redirect protection. Duplicate global server identities are rejected;
official duplicate namespace and required/optional connection-failure semantics
remain unconfirmed. These are implementation limits, not changes to the upstream
protocol or claims of equal optional feature sets.

### Docker qualification (2026-09-21)

The fixed OpenAI SDK 3.13.0 and raw HTTP passed against independent Core,
PostgreSQL and Docker Runtimes using real Kimi K3 (Codex/Claude) and MiniMax-M2.7
(MiniMax Code). Each stdio run exercised MCP-only, combined Skill/MCP and exact
capability-directory packages, selected env/cwd, Files/Artifacts, tenant checks
and positive private credential/history canaries. Removing mutable sources and
the template, then restarting Core and Runtime, retained installed tools and native
history without replaying setup. Public cancellation stopped owned tool effects;
duplicate cancellation remained stable. SDK/raw Items and live event ordering
were compared without replacing native failed/incomplete tool observations.

Separate real Kimi HTTPS runs covered inline/template configuration and cold
continuation: Codex literal headers plus bearer, and Claude anonymous plus bearer.
Codex preserved same-origin credentials and rejected cross-origin redirects.
Private CA trust and hostname validation remained enabled. These runs used the
same Core/daemon/adapters before the final stdio-only launcher correction and
Codex shell hook fix; affected paths were then qualified separately on final images.

Evidence is under `~/.parsar/remediation/20260921/template-plugin-mcp/`:

| Profile | Passed public run |
| --- | --- |
| Codex stdio | `docker/codex/plugin-d1anzd48/result.json` |
| Claude stdio | `docker/claude/plugin-l05txyye/result.json` |
| MiniMax stdio | `docker/mcode/plugin-ttnduvtn/result.json` |
| Codex HTTPS | `final-codex-http/run-8tldu8v2/result.json` |
| Claude HTTPS | `final-claude-http/run-67b8vj1o/result.json` |

Core binary SHA-256: `4b0b3360bdc776e715d9239c3b9564cd7760dd4cef6a42d1a5b82db565f84912`.
Daemon: `d6ac4eeb7657884ab3704bcdc74456044e8e702f2f89b2d30486f29ea5fe083c`.
Per-run records retain exact Runtime image IDs and cleanup results. The final
`make-check-final.log` passed; OpenAPI regeneration and focused Go/Claude tests
also passed. Real Linux process checks cover idle creator-thread exit, native and
wrapper exit, active-call cleanup and the reproduced pre-exec orphan window.
`services/agents-api/deploy/runtime/initialize_stdio_test.py` retains that OS
regression; it requires a disposable Linux packaged Runtime, not a model fixture.
The Codex environment hook additionally passed 30 actual sh/Bash cases after its
Bash-specific `eval --` failed under native `/bin/sh`.

The reusable public fixture is
`services/agents-api/tests/official_environment_plugin_mcp.py`. Mechanism probes
and failed attempts remain separate evidence. This batch does not qualify new
Plugin MCP paths on E2B, service-origin hosted MCP, OAuth, unlisted transports or
complete upstream protocol compatibility.

### Composed initialization acceptance

`services/agents-api/tests/official_environment_composition.py` combines the
existing public fixtures in one enabled-network configuration: inline/referenced
files, caller env, system/npm/Python packages, ordered setup, an uploaded Skill
reference, Skill/MCP Plugins and exact capability-directory roots. Pass the same
configuration through a Template reference or inline hosted Session; Core resolves
both through the shared initialization flow.

Use a real standalone service, fixed official SDK/raw HTTP and native model API.
The operator supplies package-network access and creates the nonempty private
canaries required by `official_environment_plugin_mcp.py`. The uploaded Skill and
each selected MCP server must actually use the installed dependencies; compare the
complete expected Files/Artifacts, public metadata, Items and live event sequence.
The fixture is not a synthetic model or a replacement execution loop.

After the first successful Turn, change the Skill default and Template, delete
source resources and mutable capability directories, then retry the original
Session creation and restart Core/Runtime. Verify frozen bytes/configuration,
owned native history, retained user edits and a setup count of one. Finally cancel
an MCP call with observed ongoing effects and verify terminal Items, stopped
effects, stable duplicate cancellation and owned resource cleanup. A successful
initial Turn alone does not pass this composed workflow.

The final Docker composition passed on 2026-09-21 with SDK 3.13.0/raw HTTP,
independent Core/PostgreSQL, Kimi K3 (Codex/Claude) and MiniMax-M2.7. Core and the
unchanged Codex/MiniMax images are from main `8eb3c089`; the Claude image includes
the native-prefix correction described under System packages. All four public
runs completed with zero cleanup errors:

| Profile | Result under `~/.parsar/remediation/20260921/template-composed-acceptance/` |
| --- | --- |
| Codex template | `docker/codex/composition-template-f19l27kz/result.json` |
| Codex inline | `docker/codex/composition-inline-dhioacmy/result.json` |
| MiniMax template | `docker/mcode/composition-template-9txsyiut/result.json` |
| Claude template | `docker/claude/composition-template-fmros4h_/result.json` |

`acceptance-summary.json` records exact images, binary/result hashes and checks.
Claude's final image is
`sha256:287e7634d932c20edc0895d2095ae79c59cc4e82d2e74f6d4996b4d35f091458`.
Its separate real native regression, `claude-prefix-regression/run-6bd24d52`,
verified cross-call cwd, quoting/exit status, installed jq, private credential/history
isolation and restricted-network HTTP/HTTPS allows, domain/subdomain/redirect denials
and direct-IP/proxy-free denial. This supplements the public runs; it does not
replace them. The final `make-check-candidate2.log` passed, including the prefix
regression and 115 Claude SDK tests. An unchanged opt-in packaged scratch/large-output
probe was skipped; live execution checks ran separately.

Failed attempts remain evidence. A deterministic Claude MCP startup failure led
to the prefix correction. A later workspace permission rejection on the final
image remains unexplained because its original native input was not retained;
the successful repeat used explicit foreground/sandbox instructions and did not
change production authorization or acceptance assertions. It does not establish
a fix for that separate rejection. Diagnostic images and the superseded Bash-input
prototype are excluded from final qualification. These results close the supported
Templates workflow, not all upstream semantics, transports or Provider combinations.

## Packaged Runtime initialization contract

Template handlers and stores resolve public configuration without choosing a
harness, native path or compute backend. The common initialization lifecycle uses
the following existing Linux Runtime packaging requirements through Provider
`RunCommand`; these are private deployment requirements, not public Template fields.

- `/workspace` is the public workspace. `/environment/workspace` names the same
  storage for trusted initialization; `/environment/staging` is private staging.
- `/usr/bin/python3 -I -S` runs the trusted, fd-anchored initial-file installer.
  It invokes the existing `/usr/local/bin/agents-api-codex-write` atomic writer.
  That executable is a shared filesystem helper packaged for every harness; its
  historical name does not select Codex or invoke native Codex tools.
- Confidential content travels on bounded stdin. Successful initialization needs
  the writer's versioned completion receipt and confirmed process exit. Unknown
  effects use the existing allocation cleanup path rather than replay.
- Provider implementations preserve argv, stdin, exit status and allocation
  ownership. They do not interpret public templates. Runtime adapters own native
  configuration; initialization must not consume a harness's private history,
  model credentials or native tool protocol.

New hosted harnesses reuse these helpers and paths; new Providers deploy the same
Runtime contract. Neither addition should change template validation, storage or
resolution. Extend this contract only for an accepted initialization requirement.
The trusted `/usr/local/bin/agents-api-runtime-initialize` receives a bounded
version-1 JSON operation on stdin. It configures read-only tool env under
`/environment/initialization`, installs packages under `/environment/packages`,
and runs ordered commands through distro bubblewrap. The fixed mount/process map
excludes daemon credentials, native history and staging. User values are applied
inside isolation, never to the launcher. Receipt and process exit must both confirm
completion; child output is discarded because it can contain secrets.

Runtime receives a `tool_environment` execution flag, without template identity or
provider information. Adapters validate the common files and apply them in their
native tool sandbox: Claude uses its native Bash hook, Codex its managed Bash hook,
and MiniMax its isolated native-tool worker. Native transports remain unchanged.
Files reads do not require initialized tool configuration. Docker setup requires
the existing nested-sandbox deployment profile for every harness; E2B supplies
the same Runtime layout and kernel isolation.

Codex 0.153.4 can execute an original command when a native hook process fails.
The adapter verifies the required trusted managed hook before preparation and
stops the Turn on an observed failed hook. Earlier command effects may already
exist; this is not an atomic hook-failure prevention guarantee.

## System packages

`packages.system` accepts package names for the Runtime's Debian apt repositories,
in both templates and inline hosted configuration. Real apt/dpkg installs packages
and runs package scripts before npm/Python dependencies and setup commands. Template
updates replace the package object; omission preserves it and null clears it.
Referencing Sessions freeze the existing template configuration.

Each Runtime image supplies a seed built before daemon, harness and credential
installation. The common initializer extracts it into
`/environment/packages/system` under the unprivileged Runtime identity. Matching
package databases and base tools are included; private Runtime files and native
history are absent. Installation uses its own process/filesystem view. Package
output is not exposed in public diagnostics. A failed or uncertain installation
fails the Environment through the existing lifecycle and is not replayed.

Setup and native shell tools enter this installed root read-only, with the same
workspace and adapter-owned temporary storage. Trusted launchers stay outside the
package-controlled root. Codex uses its managed hook, Claude its full-shell prefix,
and MiniMax Code its existing tool worker; native execution and cancellation retain
their existing owners. Core carries only the required initialized-tool condition.
Files operations retain their existing authorization and initialization boundary.
The Claude image includes an adapter-specific prefix dispatcher: its exact Runtime
MCP entry starts directly, while complete native Bash commands enter the tool root
unchanged. This preserves native sandboxing and cwd receipts without wrapping MCP
inside a second tool root or introducing Core engine branches.

This is a single-UID tool environment, not a full operating-system service manager.
Packages requiring additional Unix identities, privileged operations or background
system services may fail explicitly. There is no apt mirror, package cache, arbitrary
root installation or package retry mechanism. Existing operation and initialization
time budgets apply. New harnesses implement the same Runtime contract rather than
adding template-specific business logic.

## Restricted network policy

Template and inline configuration share Core validation, persistence and resolution.
`restricted` requires 1–100 exact ASCII hostnames; subdomains and redirect destinations
need their own entries. Unsupported host forms (wildcards, URL/port syntax, IP literals,
Unicode and trailing dots) reject explicitly. This is a qualified subset, not a
claim of complete upstream hostname normalization or TLS routing semantics.
Public reads preserve supplied spelling, order and duplicates. Effective native
comparison uses a separate lowercase, deduplicated copy; updates cannot change
existing Session snapshots or retry intent.

Provider bootstrap and preparation carry the same frozen policy. Runtime rejects
mismatches and missing hosted execution policy. Read-only workspace access retains
its existing minimal prerequisites. Core owns no native proxy configuration:
Codex uses its managed network ceiling, while Claude and MiniMax use native sandbox
allowlists. Provisioning remains a separate phase before runtime restrictions.
Current qualification evidence must cover real Docker native execution, permitted
and denied hosts, credential isolation, Files/Artifacts, cancellation and retained
policy on recovery; resource tests alone do not establish execution compatibility.

## Explicit gaps and evidence boundaries

Templates and inline initialization share Plugin, capability-directory and Skill
reference installation. Unsupported native activation and unqualified protocol
semantics remain explicit gaps as described above. The separate live Files API remains
available after initialization. Unsupported requests reject without echoing payloads.

The [hosted guide](https://developers.openai.com/api/docs/guides/agents-api/environments/openai-hosted)
clarifies that configured env values are readable by Agent code, files/packages
precede setup commands, nonzero setup prevents start, and runtime-reserved env names
must reject. The shared initialization batch implements those fields with encrypted snapshots
and the existing readiness gate. Public reads show packages but omit env/commands.
Template updates replace each supplied field; omission preserves it and null clears
it. Referencing Sessions use the composition rules below; these differ from
Template.update replacement rules.

Files and resolved Skills are installed first, followed by system, npm/Python packages and ordered commands;
the default cwd is `/workspace`. One command or package operation has the existing
two-minute local budget, within the thirty-minute initialization budget. No command
is retried after unknown effects. Completed setup never runs on reconnect.
Package dependencies are available to native tools across working directories.
System packages use the isolated tool root described above.

The [update Reference](https://developers.openai.com/api/reference/python/resources/beta/subresources/agents/subresources/environments/subresources/templates/methods/update)
defines runtime network as post-setup and packages as preceding that policy.
Initialization therefore uses its isolated provisioning network; native tools
apply the requested enabled/disabled/restricted policy afterward. Allowing setup internet is
an implementation inference from that phase boundary, not an explicit upstream
guarantee. Env values are intentionally readable by Agent code; they must not
appear automatically in public metadata or initialization diagnostics.

The [current Template reference](https://developers.openai.com/api/reference/python/resources/beta/subresources/agents/subresources/environments/subresources/templates)
mentions different GA/beta defaults; this service retains `agents=v1` and the
[fixed baseline](upstream.json), whose omitted network is enabled. Exact upstream
errors, no-op timestamps, concurrent pagination and referenced Session null-network
override semantics remain unverified. The last case explicitly rejects in this
batch rather than guessing inheritance. This batch is not full protocol compatibility.

## Template and inline configuration composition

The pinned Session description applies the template before inline configuration.
Owned official API probes on 2026-09-23 establish the following narrower behavior:

| Session field | Omitted or null | Non-null inline value |
|---|---|---|
| `env` | Inherit template keys | Overlay by key; inline value wins, `{}` preserves all keys |
| `setup_commands` | Inherit template sequence | Replace the sequence; `[]` clears it |
| `files` | Inherit template file set | Replace the complete set; `[]` clears it |
| `packages` | Inherit all managers | Resolve Python/npm/system independently; `{}` inherits all |
| Individual package manager | Inherit its template list | Replace that list; `[]` clears it |

Core performs this composition once, before the existing encrypted Session snapshot
transaction. Env values, command bodies and inline bytes remain absent from public
metadata. Effective package/file metadata reflects the selected inputs. Caller
intent still distinguishes omission, null and explicit fields for the local creation
retry policy; composition does not rewrite it. Later template/source changes do not
rewrite committed initialization. Existing per-source and effective-size validation,
network narrowing, Skill/Plugin selection and source authorization remain in place.
No harness or Provider participates in the merge.

The official evidence used SDK 3.13.0, upstream `d7c41ef`, and `agents=v1`:
86 authenticated calls, nine owned Sessions, four lifetime Templates (including two
cleaned setup-script assertion failures), and six completed real `gpt-6-astra` Turns.
Four exit-zero command outputs confirmed env values and command replacement/order
for omitted, populated, empty and null inputs. Two further outputs confirmed full
file replacement and null inheritance, and an initialized empty listing confirmed
`files=[]`. The omitted-files case reached the bounded readiness limit, so its
inheritance proof is metadata-only. Package composition is also metadata evidence;
these official probes do not independently establish installed package versions.
All owned resources have successful public DELETE receipts; physical upstream
sandbox destruction was not independently observed.

Private evidence: `~/.parsar/remediation/20260923/template-inline-composition/`,
subdirectories `official-env-setup` and `official-files-packages`. Reports retain
fixed-source snapshots, raw status/body/request IDs, command-output proofs,
accounting, cleanup and credential scans. This covers the observed fixtures rather
than all possible combinations. Null network and null Skill/Plugin/directory list
selection remain outside this batch. No new protocol version is introduced.

### Core checks for composition (2026-09-23)

`TestTemplateCompositionOfficialClientPostgres` uses the fixed strict SDK and raw
HTTP against real Core handlers and PostgreSQL. Six accepted cases and seven
rejected creation keys cover effective metadata, confidential frozen bytes and
ordered commands, tenant/source isolation, combined setup-size rejection without
partial records, and same-intent retries after template and source deletion.
A second Store/handler verifies reopened persistence; it is not an OS process
restart and does not run a model. API tests additionally cover raw caller intent,
non-mutating composition and retained field validation.

The integrated server gate ran `make -o check-web check` at `5589df0`; this includes
real PostgreSQL tests, byte-for-byte sqlc generation, builds, native bridge checks
and Rust tests/format/Clippy. A fresh `make check-web` ran locally against the same
unchanged production diff: 287 client, 583 Web and 76 browser tests passed. Together
they cover every required `make check` target. `make openapi` regenerated the
Session description. The optional MiniMax packaged-native scratch/large-output
probe was skipped because its optional profile variables were unset; this batch
does not add MiniMax, Claude or E2B native qualification.

### Real Codex Docker composition acceptance (2026-09-23)

The exact `8019ac5` production build ran independently with Core, PostgreSQL and
Docker Runtime against the real Kimi API. Three completed native model Turns prove:

- A populated-inline Session observed env key precedence, whole-file replacement,
  ordered replacement commands, Python `packaging==26.0`, and inherited npm/system
  tools. Its prompt named only the read operation, not the expected canary values.
- A separate inheritance Session observed null env/files/commands inheritance,
  retained npm/system tools and absence of the explicitly cleared Python installation
  directory. After template mutation/deletion and an actual Core process restart,
  identical-key creation recovered its existing identity/configuration. A second
  native Turn returned the exact same values and append trace: initialization did
  not run again. The populated Session itself was not resumed in this run.

Five owned Core Sessions were created over the acceptance attempts. The first two
failed before any model Turn because the reused private Skill-only runner omitted
Docker `nested_sandbox: true`, already required by the documented initialization
profile. Correcting that operator setting resolved the proc-mount failure without
production changes. In the corrected pair, the populated Session completed its
native command, but the private runner then required an optional assistant `phase`
and unwrapped JSON. The original message instead contained matching fenced JSON
without phase. An offline check preserved and verified the original native output
and message bytes; no model call was repeated. One new inheritance Session completed
the remaining two Turns. These failed assertions and operator diagnostics are
retained alongside successful evidence, not counted as extra successful runs.

Private evidence is under the same batch's `live/` directory and records source,
binary/image hashes, operator-setting changes, exact native output and cleanup.
This is Codex/Docker qualification for the described composition paths, not renewed
qualification of other harnesses or Providers, every package manager combination,
or complete Template/Agents API semantics.

## Verification

### Restricted-network Docker acceptance (2026-09-21)

The fixed SDK 3.13.0 and raw HTTP passed restricted-policy CRUD, tenant isolation,
template narrowing and immutable creation-retry checks against the standalone Core.
Current Core/daemon builds with Codex 0.153.4, Claude Code and MiniMax Code passed
real Kimi/MiniMax execution, initialized system tools and Skills, Files/Artifacts,
credential isolation, cancellation with observed descendant cleanup, and retained
workspace/native history after separate Core and Runtime restarts. Codex exercised
both template and inline configuration; Claude and MiniMax exercised templates.

Separate real-model network runs on all three Docker profiles verified HTTP and
certificate-checked HTTPS to allowed sites, rejection of an unlisted host and
subdomain, rejection after an allowed site's redirect, and failure of direct-IP or
proxy-free access. Allowed HTTPS, host rejection and direct-bypass checks repeated
after Core/Runtime restart with the frozen policy and retained conversation history.
The checks use actual tool effects, native command Items where available and exact
transport connection counts, not the model's assessment. All accepted runs cleaned
their owned containers, volumes and test transport.

The test host lacked direct DNS/TCP egress. A task-only network-namespace route and
transparent sidecar carried unchanged HTTP/TLS bytes to real sites through the
existing outlet; host routes, Runtime capabilities, native policy and certificate
validation stayed unchanged. This qualifies native enforcement through that test
outlet, not production direct egress or DNS. An earlier Codex probe incorrectly
required a complete result file after native denial; the corrected probe requires
the corresponding native rejection when the tool is interrupted. One initial
MiniMax run failed before its first tool call; an independent rerun passed, without
a Core change or an established root cause for that failure.

Focused policy/API/adapter/PostgreSQL tests, the real Codex managed-process lifecycle
test and the complete Core `make check` passed. Optional Docker fault fixtures were
not enabled; real public Docker runs cover the accepted paths. The first full-check
invocation lacked the server's OpenSSL development paths; the corrected invocation
passed with a fresh database. Evidence and failed attempts remain under
`~/.parsar/remediation/20260921/template-network-native/` and the Feishu task record.
Core SHA-256: `0dc40384192fc75c4be9896072dfe0089c30a02e44804faca7a14c7d8525efa3`.
E2B probes remain mechanism evidence only; this batch does not qualify official
E2B self-hosted onboarding or complete upstream network semantics.

### Resource and initialization checks

The E2B fixture and opt-in flags described below are historical evidence for the
retired Core-managed deployment, retained in Git history at `d03e1d25`. Current
user-managed E2B uses the shared daemon enrollment path and does not resolve hosted
Templates. These historical tests do not qualify the replacement deployment.

`official_environment_templates.py` checks all five fixed-SDK operations plus raw
HTTP, exact safe response shapes, field replacement/defaults, pagination, tenant
isolation and rejected confidential canaries. `official_e2b_v1.py` opts in with
private `verify_environment_templates: true`; it creates its actual native/model
Sessions from public templates, verifies frozen snapshots and creation retries
after update/delete, then reuses the existing execution, Files/Artifacts, isolation,
cancellation and crash/history-recovery assertions. Its disabled-network Session
inherits that policy from another template. Runtime and provider packaging were
unchanged in the original metadata-only batch. Database integration tests cover persistence and concurrent field updates;
API tests cover parsing and caller-intent distinctions.

`official_environment_initial_files.py` and the `verify_initial_files: true` option
together with `verify_environment_templates: true` in the real E2B runner add
both-source/template/inline metadata, source-deletion,
foreign-tenant and actual first-native-read checks. Existing Files/Artifacts,
cancel/crash/history checks then verify that initialization did not change the
execution loop or overwrite later user modifications. Controlled PostgreSQL lifecycle
tests separately exercise interrupted installation, readiness and maintenance fairness.
A test's presence is not a passing acceptance result; retain actual run evidence.

### Accepted initial-file profiles (2026-09-20)

The batch passed fixed SDK 3.13.0/raw HTTP acceptance with real models on Docker
and E2B for Codex, Claude Code and MiniMax Code. Both template and inline paths
verified initial native reads, Files/Artifacts, tenant and credential isolation,
source/template deletion followed by creation retry, cancellation, and preserved
workspace changes/native history after Core and Runtime restarts. E2B also verified
Core interruption during initialization: no native execution, no replay and owned
resource reclamation. All six completed runs reported clean resource cleanup.

Separate real Provider checks covered Docker binary stdin/backpressure and E2B
50 MiB stdin. The real shared installer verified empty, binary, nested and 50 MiB
files, rejected symlink destinations, and preserved outside bytes. PostgreSQL/race
suites and `make check` passed. The optional native build probe skipped by the
default gate is not counted as real acceptance. Runtime images were the retained
qualified builds; Core was built from this batch. E2B runs preceded the final
readiness guard and store-interface cleanup, which received targeted regression;
The six-profile matrix preceded final creation-intent size and canonical-identity
corrections. Real HTTP/PostgreSQL regression accepted a 1 MiB file and two 5 MiB
inline files with retries, and verified canonical template/file encryption bindings.
A further rebuilt standalone Docker/Codex run passed a 5 MiB initial file with
real model reads, Artifacts, cancellation and retained history in 101.23 seconds.
The original Docker matrix used Core SHA-256
`31973b17dd96106743e581c400555e3a4b036ad8cb3e68b51530a2b56023abe3`.

Docker MiniMax Code passed with the real MiniMax API at its standard HTTPS origin
through the test network relay. Earlier Kimi/MiniMax connection timeouts remain
recorded with unknown cause, as does a Docker reconnect failure under a different
Core/Runtime restart order. They are not claimed as fixed. Sanitized run results,
checks, build hashes and failed attempts are retained under the private
`environment-template-files` acceptance directory and the linked task record.

### Accepted env/setup and npm/Python batch

`official_environment_setup.py` adds fixed-client/raw-response assertions for
confidential snapshots, safe package metadata, real registry dependencies, ordered
setup, native visibility across cwd and the post-setup network boundary. Runtime
mechanism tests cover private files/processes, immutable configuration, child
cleanup and failure receipts. The batch passed real-model template and inline acceptance on newly built Docker
Runtimes for Codex, Claude Code and MiniMax Code, plus Codex on a newly built E2B
template. Each verified actual npm/PyPI installs, ordered setup, native env and
dependency visibility across working directories, Files/Artifacts, cancellation,
post-setup disabled networking and recovery without repeating initialization. E2B
also verified daemon/history/process/envd isolation and separate Core/Runtime
crashes with exact native history and no automatic input replay.

The shared initializer additionally passed actual Docker isolation probes for all
three profiles and E2B registry installation. Docker nonzero setup and missing cwd
failed before native Turns and reclaimed the Environment. PostgreSQL/race checks
cover encrypted owner/field-bound snapshots, readiness and uncertain-install cleanup.
A rebuilt standalone Core passed real fixed-SDK/raw-HTTP retries with changed, added
and removed inline env/setup under a saved Agent; unchanged retries still recover
after Agent deletion. Only this creation-identity regression required the final
Core rebuild; the completed model matrix preceded that isolated hash correction.

One MiniMax inline post-restart model request reported an upstream timeout after
100 seconds. The affected inline rerun passed in 214.75 seconds, with no production
transport changes; this does not establish or fix the timeout cause. All completed
runs confirmed owned resource cleanup. The three-harness-by-two-Provider matrix
was not repeated: shared E2B initialization and the changed native adapter paths
were covered separately. System packages were outside that batch; their current
qualification is recorded separately. Unconfirmed reference overrides remain gaps,
and native Codex hook failure retains the limitation stated above.
Private sanitized run/check/build evidence is retained under
`~/.parsar/remediation/20260920/environment-template-setup/` and the linked board.
These results do not establish complete Template or Agents API compatibility.


### Accepted inline-Skill profiles (2026-09-20)

`official_environment_skills.py` supplies fixed-client/raw-HTTP checks and a
native-discovered Skill whose helper produces an unpredictable Artifact, checks
private credentials/staging, and attempts to modify its own installed manifest.
Standalone Core, dedicated PostgreSQL and freshly packaged Docker Runtimes passed
with Codex 0.153.4/Kimi, Claude SDK 0.3.269 (native 2.1.269)/Kimi, and MiniMax Code
0.4.12/MiniMax-M3. Codex covered template and inline configuration; Claude and
MiniMax covered the template path through the same initializer. All verified safe
metadata, foreign-tenant rejection, frozen snapshots after template clear/delete,
creation retry, native Skill execution, Files/Artifacts, cancellation, and owned
history/workspace recovery after Core and Runtime restart. Cleanup completed.
The Core binary SHA-256 was
`85be1bc26ca6c03617ba74bf092485656dda9311bb636d507be6576a2f087f16`.

The shared initializer also passed on a real Docker container and E2B VM, including
binary/executable content, read-only Skill access from setup, duplicate/path
rejection and private-state isolation. This batch did not repeat the E2B model
matrix: Provider code is unchanged, while its shared initialization boundary was
exercised in a real VM. PostgreSQL/API/archive tests, Claude SDK tests/build,
`make openapi`, `make sqlc-generate` and `make check` passed. The default gate's
optional native build probe remains skipped and is not counted as live acceptance.

Initial integration failed safely because the proposed Skill parent was root-owned;
using the existing Runtime initialization directory resolved that packaging
boundary without broadening permissions. The earlier MiniMax/Kimi native timeout
and probe-only unrestricted-tool configuration failure remain recorded. The latter
passed after restoring the unchanged production tool settings. Docker builds reused
qualified base images after registry DNS failure; current daemon, adapter and
initializer artifacts were copied using the repository packaging recipe. Raw
receipts retain their inherited historical manifest fields; accompanying source,
Core and image hashes identify the actual candidates. Evidence is retained under
`~/.parsar/remediation/20260920/environment-template-skills` and the linked board
record. This profile does not establish complete upstream Skill semantics.

### System-package qualification (2026-09-20)

The batch passed standalone Docker acceptance with current-source Core/daemon and
newly packaged Codex, Claude Code and MiniMax Code Runtimes. Fixed SDK 3.13.0 and
raw HTTP exercised public templates; Codex also exercised inline configuration.
Actual Kimi/MiniMax requests verified jq, compiler/libpq linkage, dependent
npm/Python packages, ordered setup, native visibility, read-only installed roots,
Skill/credential protection, Files/Artifacts, public cancellation and retained
workspace/native history after Core and Runtime restart. Cancellation checks
observed tool identities disappear before sandbox teardown. All three completed
runs reported clean resource cleanup. Template omission, replacement, null/empty
values and atomic invalid-input rejection received additional real HTTP checks.

The Core SHA-256 was
`9466a8419fd0e4ad8cd4fb1786ef131c2cc504513bf77642fca43ce07b8114a6`.
The Codex template/inline run took 599.72 seconds; Claude and MiniMax template
runs took 271.69 and 357.34 seconds. Real initialization mechanism checks separately
covered isolated package scripts and compilation. Focused Go/SDK tests, OpenAPI
generation and `make check` passed. The optional native build probe skipped by
the default gate is not counted as real acceptance.

E2B finalization rewrites `/usr/local` permissions. Its trusted bootstrap must
restore the common system-tool launcher's packaged `0555` mode before native
preparation; root ownership alone does not satisfy that Runtime receipt check.
The first qualified Codex E2B template passed real Kimi template and inline acceptance in
576.41 seconds, including final seed/launcher protection, actual package/setup
visibility, Files/Artifacts, credential/history/process/envd isolation, public
cancellation, separate Core/Runtime crashes, continued owned history without
input or initialization replay, preserved user files, and disabled native-tool
networking. Cleanup completed without fallback errors. This run uses the updated
daemon with the bounded discovery adjustment described below. The immutable build
is `1b60xhq0j13fnr5zipkg:7d11189a-b2bd-4690-bc3f-da0792439f91`. The full
three-harness E2B matrix was not repeated: shared initialization and the changed
native adapter paths were covered separately.

Independent review then identified a missing native cwd alias: the installed tool
root exposed `/workspace`, while Codex retained `/environment/workspace`. Both
now mount the same authorized workspace. A rebuilt Docker Runtime passed actual
system/npm/Python initialization and entry from the default directory and its
subdirectory in 106.30 seconds, including private-state isolation and read-only
tools. The earlier model runs selected `/workspace` and do not prove this fix.
The rebuilt E2B template
`1b60xhq0j13fnr5zipkg:e6437586-927e-4683-99fe-632b51a974fd` then passed the
real Kimi template/inline loop in 457.91 seconds. Native command Items and actual
effects verified the default directory and subdirectory; the same run passed
Files/Artifacts, private-state isolation, cancellation, Core/Runtime recovery,
preserved history and user modifications, and disabled tool networking. Cleanup
reported no errors. The final mount-only correction received this actual regression
and Python source checks; the two full `make check` runs precede it.

Failed attempts are retained: early admission incorrectly required the private
initialization receipt; execution preparation now owns that check. Test-only proxy
configuration and simultaneous package installation attempts failed before the
sequential accepted runs, without extending production budgets. E2B cold discovery
once killed `codex --version`; unchanged discovery subsequently passed, but a later cold deployment repeated
the failure with no observed OOM. The shared CLI availability probe now allows
15 seconds instead of five; no retry or Provider-specific startup path is added.
The precise initial paging/contention cause remains unconfirmed. Docker execution
results above precede this isolated startup-budget adjustment. The E2B launcher-mode mismatch failed preparation before any
native input was applied. The subsequent native isolation fixture assumed
`sudo` existed; the real tool transcript showed `FileNotFoundError`. The fixture
now records an absent privilege command explicitly while retaining all authority
and private-state checks. Interactive PTY behavior and packages needing additional
Unix identities or privileged services are not qualified by these results.

Sanitized results, image/source hashes, full checks and failed evidence are retained
under `~/.parsar/remediation/20260920/environment-template-capabilities` and the board.
Early Docker result manifests contain inherited installer archive fields; those
fields do not qualify a new installer archive. Current binary and image hashes
identify the tested deployment. These checks do not establish complete upstream
Template or Agents API compatibility.

## Reference batch validation and limits

Resource operations passed fixed SDK/raw HTTP and real PostgreSQL checks. The
shared reference path passed real Docker execution on all three qualified profiles:

| Harness | Real model | Complete driver result |
| --- | --- | --- |
| Codex | Kimi K3 | Passed, 308.80 seconds |
| Claude Code | Kimi K3 | Passed, 211.68 seconds |
| MiniMax Code | MiniMax M2.7 | Passed, 162.71 seconds |

Each run covers SDK upload, unresolved template intent, concrete Session metadata,
native Skill supporting files, public Files/Artifacts and tenant checks, source
and template deletion, committed retry, and cold Core/Runtime continuation without
reinstalling or replaying the original Turn. All report zero cleanup errors. The
current Core uses the previously qualified Runtime images and native adapters;
this batch does not change their execution architecture or qualify E2B references.

MiniMax's initial attempts failed because the test host's SOCKS route exceeded the
native TLS connection deadline. A temporary operator SSH byte relay restored normal
TLS latency, retaining native certificate validation and the same model/credentials.
One subsequent response recalled the exact random history value but omitted its
fixed prefix. Clarifying the test prompt to request the complete literal token,
without supplying its random value again, passed the unchanged exact assertion and
remaining checks. Failed evidence is retained; no Core output repair or native
connection-timeout change was made.

`make sqlc-generate`, `make openapi` and the standalone `make check` passed; the
full gate took 484.22 seconds. The optional MiniMax packaged-native scratch/large-output
probe was skipped because its profile/artifact variables were unset. Unchanged
native-profile qualification and the real reference workflows are separate evidence.
A user-authorized reused-context GPT-6 Astra high reviewer inspected the entire
52-file diff and original acceptance records, with no material actionable findings.
This was not a fresh-context blind review. These checks do not establish complete
upstream Skill, Template or Agents API compatibility.

Sanitized results and operator drivers are retained on `zju_a100_2` under
`~/.parsar/remediation/20260921/template-skill-references/`, with a local evidence
index under `~/.parsar/remediation/20260921/template-capabilities-design/`.

The current upload profile accepts at most 500 regular files, 5 MiB compressed
and 20 MiB expanded per bundle. These are qualified implementation limits, not
published protocol maxima. [File resource qualification](file-resource-semantics.md)
records default-selected unversioned content and descriptive metadata, default
deletion rejection with multiple versions, and nondefault latest pointer fallback.
Core updates the default pointer and top-level name/description atomically, while
concrete version bytes and previously frozen Sessions remain immutable. Last-version
deletion, version-number reuse and complete errors/visibility timing remain gaps.
