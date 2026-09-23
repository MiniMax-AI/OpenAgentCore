# Template-reference null selection

This batch retains SDK 3.13.0, upstream `d7c41efee1b0802b79f3f88a678ef2052b06e9ce` and `agents=v1`. It changes shared Core configuration selection only; Provider and Runtime receive the existing frozen effective configuration.

## Qualified selection rules

| Operation / field | Omitted | Null | Non-null |
| --- | --- | --- | --- |
| Referencing Session network | Inherit complete policy | Inherit complete policy | Must narrow template authority |
| Referencing Session Skills, Plugins, capability directories | Inherit list | Inherit list | Replace list; empty clears |
| Inline hosted Session network | Enabled default | Enabled default | Existing policy validation |
| Template resource update network | Preserve | Reset to enabled | Replace saved policy |
| Template resource update capability lists | Preserve | Clear | Replace saved list |

Resolve only the selected tenant-owned sources, using the existing encrypted initialization transaction. Keep unresolved caller intent distinct from the frozen snapshot. Template mutation/deletion cannot alter existing Sessions or same-intent creation retries. No new installer, native path, harness branch or compatibility reader is introduced.

Official Session capability-directory responses can include automatically derived Skill/Plugin installation directories in addition to caller-selected paths. Core continues to return caller paths and keeps Runtime-owned installation locations private. The selection matrix does not qualify complete public directory projection parity; copying official internal paths would not establish usable Core paths.

## Evidence and limits

Owned official probes and Core acceptance records are under
`~/.parsar/remediation/20260923/template-null-selection/`. Together they made
82 HTTP requests and created six Templates and eleven Sessions. All seventeen
owned resources have successful public DELETE receipts; physical upstream
destruction was not independently observed. Credential scans passed.

- `official-network/REPORT.md`: 46 requests, four Templates, six Sessions, zero
  Turns. Disabled and populated restricted policies distinguish inheritance from
  an enabled default. Narrowing succeeds; broadening rejects. Inline null and
  Template create/update null produce enabled policy. Existing Sessions preserve
  their policies after Template reset. Four provisioning-time DELETE conflicts
  each succeeded on one later bounded cleanup attempt. This does not requalify
  native network enforcement.
- `official-capabilities/REPORT.md`: 36 requests, two Templates, five Sessions,
  one real official `gpt-6-astra` Turn. Four distinct request cases establish
  list selection. The null case additionally has three completed, exit-zero
  native commands reading unknown markers from inherited Skill, Plugin Skill
  and caller directory contents. Empty/replacement native bytes were not newly
  qualified against the official service.

The first capability attempt stopped before any model call because its test
incorrectly equated caller directories with the entire official Session projection.
Its original script, raw results and cleanup remain. The bounded continuation
separated caller-selected paths from observed derived paths; it did not change
the requests or hide a model failure. Official Environment reads contain Skill
and Plugin metadata but no capability-directory field; do not equate them with
the richer Session environment projection. Mixed individual-field official null
cases and all native/provider combinations remain unqualified.

## Core verification

`TestTemplateNullSelectionOfficialClientPostgres` and
`official_template_null_selection.py` exercise the actual HTTP handler, isolated
PostgreSQL, pinned strict SDK and raw HTTP. Eleven successful selections cover
combined and mixed fields, disabled/restricted inheritance, narrowing, inline null,
Template resets and exclusion of an unselected foreign Skill. Eight rejected
creations leave no Session, Environment or initialization residue. Foreign and
missing template lookups remain indistinguishable; direct foreign snapshot reads
reject. Encrypted archive digests and unrelated initial file/env contents remain
frozen after source/default/template mutation and deletion and reopening the Store.
Changed retry intent conflicts. This test does not execute a native model.

The first acceptance assertion incorrectly expected Session-only directory/network
fields on the Environment resource. The one-line test correction uses its pinned
projection; the original failure remains. Independent PostgreSQL acceptance passed
in 1.74 seconds. The complete server gate then passed at `65f9898`, including
228.891 seconds of Store tests, sqlc byte comparison, Go/build/native package and
Rust checks. No database query or migration changed.

Fresh `make check-web` passed with Node22 and pinned pnpm10.30.3: 63 doctor,
287 client, 583 Web unit and 76 browser cases. Its initial dependency setup used
the app's pnpm11 fallback and stopped before tests; the tool-generated workspace
placeholder was removed and the unchanged repository was tested with its pinned
toolchain. No dependency or build-policy change was made. `make openapi` and
`git diff --check` passed. Optional 512 MiB/source streaming and packaged MiniMax
scratch/large-output live profiles were not enabled.

The first Codex/Docker attempt initialized three Sessions. Its Kimi null-selection
Turn read the three inherited capability markers through native commands. The next
empty-selection Turn failed on provider HTTP 429 before a native command or reported
usage; replacement and restart checks were not submitted. These records are retained.

An OpenAI-provider continuation was stopped when the user clarified that its key
was restricted to official Agents API probes. One submitted Core Turn was cancelled;
its stored usage was 10,576 input and 61 output tokens. This attempt is not acceptance.
Its owned resources and temporary credential copies were removed. Subsequent Core
model verification uses only the authorized Kimi or MiniMax providers.

MiniMax-M3 completed the empty and replacement Turns. Each produced one completed,
exit-zero native command whose unmodified JSON output exactly matched the selected
capabilities, excluded markers, inherited confidential env and single setup trace.
The original runner still failed because the replacement assistant answer omitted
one trailing newline from that trace. Both native proofs and the mismatching answer
remain preserved; Core and model output were not changed. Initialization acceptance
uses the actual command output, with the assistant answer retained as an observation.

The separate one-Session null continuation passed two MiniMax-M3 Turns. After the
first native proof, the test changed the source Skill version and Template, deleted
both resources, restarted Core, and retried the original creation key. The retry
returned the same Session; its second native command read the original markers,
confidential env and unchanged one-line setup trace. Initialization was not replayed.

All Core native execution used production source `f86223f`; the final server gate
adds only the corrected resource-test assertion and documentation. The null
continuation reused the verified Core/daemon binaries and base image. Rebuilding
the deleted wrapper image changed its image ID; base identity, in-image daemon
hash, Dockerfile and runtime configuration established the same content provenance.
No native feature, model response or production behavior was changed for acceptance.
Reports in `live/attempt-minimax/` and `live/attempt-minimax-null/` retain the original
failures, command proofs, source hashes and owned-resource cleanup records.

This batch does not widen hostname syntax, change native capability support, requalify every harness/Provider combination, or claim full protocol compatibility. Provisioning-delete retries and private test assertion failures remain in the evidence rather than being counted as successful first attempts.
