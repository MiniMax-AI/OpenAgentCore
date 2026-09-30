# Shared Environment preparation qualification

This record covers the shared preparation change based on Core
`5cac06c13eccee954575a359df9b014d728f4d14`. The current behavioral contract is
[Environments](environments.md); this record does not broaden Harness capabilities.

## Native execution

Every passing row used the pinned official Python SDK, a real Core and PostgreSQL,
the daemon and the native Harness against MiniMax-M2.7. Model credentials were
supplied explicitly by each Session and excluded from retained public evidence.

| Environment | Harness | Verified |
| --- | --- | --- |
| Linux user machine | Codex 0.153.4 | Uploaded Skill script, Plugin stdio MCP, initial file, setup, two Turns and daemon restart |
| Linux user machine | Claude SDK 0.3.269 | Same workload, two Turns and daemon restart |
| Linux user machine | MiniMax Code 0.4.12 | Same workload and declared workspace, two Turns and daemon restart |
| Docker managed Linux | Codex 0.153.4 | Same preparation input and workload, two Turns and container restart |
| Native macOS arm64 | Codex 0.153.4 | Same workload, physical workspace, two Turns and daemon restart |

Skill execution wrote a marker loaded from the installed archive; a separate MCP
call returned another marker. Tests checked the resulting workspace files and
hashes of the installed capability tree independently of model text. Editing the
Template after preparation left existing Sessions unchanged; new Sessions read the
edited setup command. No allocation was fabricated for user-managed machines.

Additional Linux Codex runs exercised an explicit operator tool-environment file:
local defaults, a Session override, preserving the source, editing the source and
reconnecting with the original values. Separate empty-configuration and initial-file-only
Sessions also retained their original tool variables after restart.

## Contract and regression coverage

Focused tests cover the shared extension and Template parser, encrypted frozen
resources and tenant access, authenticated preparation without an allocation,
missing Harnesses, failed or unknown steps, revoked or stale bindings, recovery
without replay, independent progress on healthy nodes and unchanged typed client
requests. Runtime tests cover local configuration freezing, missing snapshot
rejection, capability integrity and the existing Harness adapters.

Required repository checks and native CI results are recorded on the change's PR.
The remote browser checks use cached Chromium instead of the local Google Chrome
channel; they run the same acceptance cases. No large build runs on the Mac.

## Limits

These probes do not establish complete upstream parity or qualify new image,
structured-output, tool-discovery or public HTTP MCP combinations. Those require
separate adapter changes and qualification. The shared setup/package mechanisms
retain their contract tests; these live probes do not download npm/Python packages.
Windows requires its native CI job; no Windows manual execution was performed.
No Claude installation was performed on the Mac. E2B and microsandbox were not
requalified by this change.

An initial managed probe used a cached image with retired Codex requirements;
native shell execution failed. The passing managed row used a clean image without
those requirements. An initial MiniMax probe exposed a wrong native working
directory; the passing row includes the separately merged workspace fix. Neither
failed attempt is counted as acceptance.
