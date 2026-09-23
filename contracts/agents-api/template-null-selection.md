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

Core implementation checks and real execution results are recorded after completion;
this record does not yet claim batch acceptance.

This batch does not widen hostname syntax, change native capability support, requalify every harness/Provider combination, or claim full protocol compatibility. Provisioning-delete retries and private test assertion failures remain in the evidence rather than being counted as successful first attempts.
