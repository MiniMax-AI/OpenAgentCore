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

Owned official probes and Core acceptance records are under `~/.parsar/remediation/20260923/template-null-selection/`. The network probe distinguishes disabled and populated restricted policies, narrowing, inline defaults and Template resets. The capability probe uses distinct nonempty lists, avoiding an ambiguous empty-default comparison. Final request counts, real execution and required-check results are recorded after completion; this file does not yet claim batch acceptance.

This batch does not widen hostname syntax, change native capability support, requalify every harness/Provider combination, or claim full protocol compatibility. Provisioning-delete retries and private test assertion failures remain in the evidence rather than being counted as successful first attempts.
