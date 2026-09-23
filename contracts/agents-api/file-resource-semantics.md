# File and Skill resource semantics

The fixed baseline remains SDK 3.13.0, commit `d7c41ef`, and `agents=v1` for Beta
resources. General Files and Skills do not require the Beta header. This batch
aligns specific resource operations; it does not establish full compatibility.

## Official observations

The 2026-09-23 probes used the fixed client and raw HTTP against the official API.
Evidence is retained under `~/.parsar/remediation/20260923/file-resource-semantics/`
in `official-files/` and `official-skills/`. They made 56 requests in total, owned
three tiny Files and two Skills with three ZIP uploads, and created no Sessions or
model calls. Every owned resource received a successful public delete response;
physical erasure was not independently observed. Requests were not automatically
retried.

| Operation | Observed behavior and Core rule |
| --- | --- |
| Files upload/retrieve | `user_data` returns 200 with processed status, null expires_at and status_details; the existing Core shape matches |
| Files content | Both sampled `user_data` and `assistants` uploads reject direct download with 400, invalid_request_error, null code and param; Core applies this to its supported user_data uploads after tenant lookup |
| Files list purpose | Nine candidate values reach missing-cursor lookup; unknown and uppercase values reject first with 400 and param purpose |
| Skill content | Unversioned content follows default, even when latest differs; exact-version ZIPs retain their respective manifests and bytes |
| Skill descriptive metadata | Switching default changes top-level name and description, preserving ID and created_at; Core updates all three fields atomically |
| Skill default deletion | With two versions, deleting default rejects with 400, invalid_request_error, invalid_value and param version |
| Skill latest deletion | Nondefault latest deletion returns 200 and parent latest falls back to the surviving version |

The qualified list values are `user_data`, `assistants`, `batch`, `fine-tune`,
`vision`, `evals`, `assistants_output`, `batch_output` and `fine-tune-results`.
These observations establish validation before a missing cursor, not successful
filtering for every purpose. Omitted and explicitly empty purpose also reached
cursor lookup. Core then retained its exact empty filter; the later [list query tolerance](list-query-semantics.md#list-query-tolerance--september-23-2026)
batch treats an explicit empty purpose as omitted, as a successful official page showed. Accepting a list filter does not enable
uploads, processing or jobs for that purpose. Core still uploads user_data only.
The fixed request/response `evals` union discrepancy is unchanged.

The Files probe stopped its first phase when the fixed SDK raised BadRequestError
for content that the runner expected to download. Raw denials were preserved,
owned resources were cleaned, and an independent missing-cursor query phase used
the remaining request budget without recreating files. Positive official filtering
was therefore not observed. The extra official `detail` error member, complete
error messages, other purposes, expiration and Uploads remain separate gaps.

The Skill probe observed an acknowledged version deletion followed about two
seconds later by a list and exact GET that still exposed that version, while the
parent latest pointer had already changed. It stopped and cleaned up. Core does
not emulate this inconsistent visibility. Fresh/reduced sole-version deletion was
not reached in this probe; the later [sole-version deletion](#sole-version-deletion--september-23-2026)
batch records that observation.
Upload `default:true` was not separately probed: updating descriptive metadata
there is the same pointer-consistency rule, covered by Core tests rather than a
new official wire claim. No newer schema or integer selector form was adopted.

## Implementation and acceptance boundary

Tenant authorization precedes public source download rejection. Missing and foreign
IDs retain indistinguishable 404 responses; the denial never opens source bytes.
Environment initialization and file_id copies still consume their authorized
internal source snapshot. Skill and Artifact downloads keep their existing rules.
Core Web no longer offers a source download action that the API rejects; the SDK
retains the official content operation and propagates its error.

Skill metadata updates use the owning row lock and validated version metadata in
the existing transaction. A data-only migration corrects existing stale parent
metadata from the matching tenant and default version before the updated service
serves requests. It changes no identifiers, pointers, encrypted contents or Session
snapshots. No decrypt-on-read, cache, Runtime interface or lifecycle owner is added.
Nondefault uploads leave parent
metadata unchanged. Exact version bytes remain encrypted and immutable; previously
frozen Session metadata/content and retry intent remain independent of later source
changes.

`official_file_resource_semantics.py` is the joint strict-client/raw-HTTP acceptance
against actual Core and isolated PostgreSQL. Existing source snapshot/copy and
Skill reference tests retain the internal read and frozen-input regressions.
The resource acceptance does not run daemon or model execution; historical native
qualification is separate.

### Batch validation (2026-09-23)

The final service source `be47803` passed targeted API and actual PostgreSQL
resource/migration/reference regressions (15.977 seconds for the Store package),
including strict SDK 3.13.0/raw HTTP. The data correction preserved version rows,
other resource fields and frozen Session configuration/setup bytes; reapplying it
did not rewrite correct rows. Reconstructed Store/handler reads validate persisted
data, not a full service process restart.

All required `make check` targets passed: the server ran `make -o check-web check`
on `zju_a100_2`, paired with a fresh local `make check-web` on `13139f5`. The latter
adds only a browser-test focus synchronization to the service source. Client
287, Web unit 583, Core doctor 63 and browser 76 checks passed. sqlc generation
and byte comparison, OpenAPI generation, Go/build/native adapter/Rust gates passed.
The optional 512 MiB source streaming and packaged MiniMax scratch/large-output
profiles were not enabled. No new native model/Provider combination was qualified.

Failures remain evidence: an initial Web type check caught a stale callback after
removing the download action; it was removed. One Chrome context setup timed out.
The existing Vault lifecycle browser test twice raced the dialog's scheduled
initial focus and filled the URL into Name, before any credential was created.
A bounded isolated run passed, but the full-suite repeat reproduced it. Reusing
the neighboring test's initial-focus wait corrected that test synchronization;
the final full Web gate passed without weakening assertions or changing credential
business behavior. Private logs and original artifacts remain under the evidence
root above. These results do not close the remaining protocol gaps.

## Sole-version deletion — September 23, 2026

Evidence: campaign scan 1, `~/.parsar/remediation/20260923/campaign-scan-1/skills-files-templates/findings.json`
SFT-01 to SFT-04, with raw records in the adjacent `official-ledger.jsonl` (labels
`s1-*`, `s2-*`, `s3-*`). The scan owned three Skills and created no Sessions or
model calls.

| # | Case | Official observation | Core rule |
| --- | --- | --- | --- |
| V1 | Delete the only remaining version, which is also the default | 200 `{"id": "skillver_…", "object": "skill.version.deleted", "deleted": true, "version": "1"}`; retrieve and versions.list then return 404 (SFT-01) | Same body; the Skill is deleted in the same transaction. The reduced case (delete v2, then v1 is the only version) applies the same rule; official evidence covers only a fresh single-version Skill |
| V2 | Delete the default while another version is visible | 400 invalid_request_error, invalid_value, param version (SFT-04) | Unchanged |
| V3 | Delete a nondefault or latest version | 200; latest falls back | Unchanged |
| V4 | Foreign or missing Skill or version | 404 | Unchanged, indistinguishable |

`DeleteSkillVersion` keeps the owning Skill row lock. When the target is the
default, it deletes the Skill only if no other version row exists, through the
same cascade as `skills.delete`, so every encrypted version row is removed in the
same commit; otherwise the 400 remains. Uploads take the same lock: an upload
committed first makes the default undeletable, and a deletion committed first
makes the later upload return 404. Frozen Session installations keep their own
snapshot, and Templates keep their stored reference intent, exactly as after
`skills.delete`. No schema, query or numbering change is involved.

Recorded decisions:

- **SFT-02, number reuse: intentional difference.** After the latest nondefault
  version 2 was deleted, the next official upload was numbered "2" again (one
  sample, so max+1 and latest+1 are indistinguishable). Core keeps immutable,
  monotonically increasing numbers because exact Template and Session selectors
  reference numbers; reusing one could re-point a stored exact selector to other
  bytes and make a frozen Session's concrete version ambiguous. A sole-version
  deletion removes the Skill, so numbering never restarts within a Skill.
- **SFT-03, upstream anomaly: never emulated.** Deleting default version 1 about
  four seconds after an acknowledged version 2 upload returned 200 and removed the
  whole Skill, including version 2. The same request with version 2 visible
  returned 400 (SFT-04). Core serializes both operations on the Skill row, so a
  version deletion never removes an acknowledged upload.

Core acceptance: real-PostgreSQL store tests prove atomic Skill removal without
orphaned version rows, unchanged frozen Session contents, creation retry and
Template intent, and both lock orders of a concurrent upload; a real HTTP test
covers V1 to V4 across two tenants, and `official_skills.py` checks V1 with the
pinned SDK and raw HTTP. None of these run a model.
