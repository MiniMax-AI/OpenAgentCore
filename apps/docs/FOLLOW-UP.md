# Feature integration follow-up

This candidate documents the actual maintenance and drain model at source
`75bf4484ba957344ae374354858bbdfe104f5c4f`. It is not final documentation for design 50.
The Core error-envelope foundation is included; the later configuration validators and
diagnostic endpoints are not. Do not replace current procedures with the planned
behavior before the features land.

| Feature dependency | Site pages to reconcile | Source and verification work |
| --- | --- | --- |
| Reset replaces maintenance (design 50 PR-R) | configure, hosted-providers, troubleshooting, admin-api, observability, API reference/core | Regenerate the canonical guides and management contract; describe reset admission, cleanup, failures, archive preconditions and offline-node limits from merged code. Remove retired maintenance instructions only after their replacement exists. |
| Online generations and E2B changes (design 50 PR-G) | configure, hosted-providers, troubleshooting, execution-model, API reference/core | Document actual generation/rollout states, the supported change matrix and existing-sandbox behavior. Verify PUT examples against the final contract. |
| Node update protocol (design 50 PR-N) | hosted-providers, troubleshooting, API reference/machine | Explain the implemented update command, old-node handling and ownership preservation. Do not claim an update command now. |
| Later configuration/error changes (design 40 and remaining installer work) | install, configure, troubleshooting, quickstart | Regenerate current configuration fields and revise diagnostics against merged code; keep secrets out of examples. |
| Final feature assembly | every Chinese reading note, execution-model diagram, all generated references | Review translations against regenerated English, verify source provenance and links, run contract/route/copy/fact checks, typecheck/build, local browser review and the repository gate. |

English guides are rendered from repository sources. Chinese pages are explicitly
labelled reading notes, not complete translations; expand them only against the final
source procedures. API tag pages use the default-language fallback. Release publication,
remote deployment and preview replacement are outside this reconciliation task.

The source Web guide still carries a pre-rename screenshot. This site copies the
approved current Web onboarding Monitor image instead, with its own source hash;
refresh that mapping when the canonical guide image is updated.
