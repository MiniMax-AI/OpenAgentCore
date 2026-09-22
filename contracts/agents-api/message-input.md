# Ordered message input

The pinned SDK defines user messages as ordered `input_text` and `input_image`
parts. Core retains message boundaries, content order and the supplied image
reference in input persistence and user Items. Session creation and subsequent
events share validation and atomic admission.

## Supported profile

Codex and Claude SDK support inline PNG/JPEG data URIs on `environment:none` and
Core-managed Docker `openai_hosted`, for initial, prepared and active input. Use a real vision-capable model.
The existing 1 MiB HTTP and 512 KiB durable input limits still apply. A successful
events response acknowledges persistence, not native consumption. Active input
advances its durable receipt only after the adapter confirms application.

```python
import base64
from pathlib import Path

image_url = "data:image/png;base64," + base64.b64encode(
    Path("example.png").read_bytes()
).decode()
session = client.beta.agents.sessions.create(
    agent={"model": model},
    environment={"type": "none"},
    input=[{"role": "user", "content": [
        {"type": "input_text", "text": "Describe this image."},
        {"type": "input_image", "image_url": image_url},
    ]}],
)
```

Query Session/Turn/Items to recover results. SSE remains live-only; reconnecting
does not replay inputs or recreate completed Turns. The request contains no
image `detail` or file-ID extension.

## Runtime boundary

Private wire 0.5.0 uses `MessageInput` for initial requests, prepared start and
active steering. Each message contains ordered `InputContent` parts, also reused
by function-result content. Core does not download, transcode or repair media.
The separate Claude bridge reports protocol 2; daemon readiness rejects protocol 1.
Adapters own native encoding and native application-receipt mapping. Existing
text-only adapters reject image parts instead of dropping them.

Codex converts content to its native flat input list and inserts blank-line
separators between public messages. Public message boundaries remain durable;
independent native message boundaries are not claimed. Claude uses native image
blocks and a UUID for each native user message; one public active input is applied
only after every message in its batch is consumed. Its native 64-message bound is
checked before any part of a batch enters the iterator.

Public profile qualification and Runtime advertisement are separate. Admission
checks the registered image profile for this placement; device selection and
delivery check actual image support. Text-only operations retain existing offline
admission. Adding an adapter must implement the shared contract and qualify the
public operation, without adding engine-name branches to Core.

## Validation and remaining gaps

`TestNativeMessageImagePublicExecution` runs the pinned SDK and raw HTTP against
Core, a dedicated PostgreSQL database, the real daemon and a native harness.
Set `PARSAR_MESSAGE_IMAGE_ENGINE` to `codex` or `claude_sdk`, provide private real
provider options via `PARSAR_MESSAGE_IMAGE_REAL_OPTIONS`, and use the existing
`PARSAR_NATIVE_DAEMON_BIN`, `PARSAR_NATIVE_PROOF_DIR` and
`PARSAR_OFFICIAL_SDK_PYTHON` fixture settings. The test never supplies model responses.

Real Kimi K3 acceptance on 2026-09-22 used randomized four-color PNGs whose answers
were absent from the input text. Both adapters passed initial ordered input,
active replacement with a different image, retry deduplication, exact retained
user Items, native receipts, cold daemon history continuation, cancellation and
ordinary text continuation, malformed-batch atomic rejection and tenant isolation.
Evidence is under `zju_a100_2:~/.parsar/remediation/20260922/message-image-input/`:
`public-codex-final.log` / `message-image-public-2718944397/public.json` and
`public-claude_sdk-final.log` / `message-image-public-700202073/public.json`.
After final lifecycle and bridge-version changes, both complete public chains
passed again: `public-codex-reviewed.log` (59.416s) and
`public-claude-reviewed.log` (79.525s). MiniMax ordinary text, active steering,
receipt, cancellation and cold continuation passed in
`public-mcode-network-fixed.log` (48.963s). The first MiniMax attempt hit a stale
hosted-fixture assertion; a subsequent attempt failed because its test network
relay was absent. Both failures are retained, and no production model behavior
was changed to obtain the passing result.

The final required gate was split by host: server `make -o check-web check`
passed, and local Node22/pnpm10.30.3 `make check-web` passed all 73 browser tests.
An earlier complete server `make check` stopped at its missing Chrome executable.
The dedicated PostgreSQL cancellation/deletion interleaving regression passed
five repetitions, and shared input/dispatch race checks passed. These checks do
not qualify workspace images or additional native/provider combinations.
Native-only probes are feasibility evidence, not public qualification.

## Docker workspace acceptance

`tests/official_workspace_images_native.py` exposes `verify_workspace_images`
for an operator-owned standalone deployment. Supply the fixed SDK clients for
two tenants, their raw HTTP transport, a real vision model, the selected harness,
a cold Core/Runtime restart callback and a private evidence path. It creates and
deletes its own hosted Sessions; it never supplies model responses or credentials.

The common workflow covers initial text/PNG/text input, prepared image-only JPEG
followed by a separate message, active PNG input while a function waits, and a
6000x2100 PNG function result. The model must read the band order from image pixels
and write the corresponding bytes with native tools. Files listing and immutable
Artifact downloads verify those bytes. SDK and HTTP Items must retain the original
ordered input/results. The same workflow checks retry/conflict, unsupported-input
non-mutation, tenant and same-tenant Session isolation, cold history continuation,
pending cancellation and ordinary text after cancellation.

Real Kimi K3 acceptance on 2026-09-22 passed this workflow for Codex 0.153.4 and
Claude SDK 0.3.269/native 2.1.269. Evidence is retained under
`zju_a100_2:~/.parsar/remediation/20260922/workspace-images/`:
`codex/public-run-_lr5uiy_/` (152.78s) and
`claude_sdk/public-run-sb65p9e9/` (197.84s). Each run completed seven public Turns,
including one cancelled Turn, and cleaned up both owned hosted Sessions. Initial
Codex attempts exposed two acceptance-script errors: treating an earlier idle
event as the submitted Turn's completion and sending a scalar message to the
array-only events endpoint in a conflict probe. Both failures are retained;
production lifecycle behavior was not changed to obtain passing results.

Here `openai_hosted` means the Core-managed Docker deployment, with daemon, native
harness, tools and workspace in one sandbox. Image admission uses Environment type
and the common operation-specific Runtime support; it adds no provider-name branch,
media downloader, file permission or preparation lifecycle. Docker evidence does
not qualify other providers or user-managed deployment. Both adapters now require
native function-result confirmation as described in the
[receipt coverage](function-result-images.md). This does not imply crash-safe
exactly-once tool effects; the original workspace acceptance predates Codex's
native receipt qualification.

## Remaining gaps

Self-hosted/user-managed image workflows, MiniMax Code image input, remote HTTP(S) image URLs,
other media types and full upstream error/default semantics remain unqualified.
MiniMax's fixed ACP advertises `image:false`; its adapter rejects images. These are
implementation gaps, not changes to the official protocol. JPEG parsing/conversion
has deterministic coverage; the original `none` message fixtures are PNG, and
the hosted workflow also exercises JPEG. Empty
messages, local payload limits and native batch-size parity need upstream evidence.
Function-result image support has its own [coverage record](function-result-images.md). No full protocol
compatibility or support for arbitrary vision-model/provider combinations is claimed.
