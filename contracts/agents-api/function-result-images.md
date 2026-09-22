# Function-result image coverage

The pinned official function result accepts a string or ordered text/image content,
independently of success. Our qualified Claude subset is successful inline PNG/JPEG
on `environment:none` and Core-managed Docker `openai_hosted`. Error images,
unqualified placements and remote URLs reject before
batch persistence, without consuming the pending call. These are implementation
gaps, not narrower official types. MiniMax public functions remain unqualified.

Core retains the original ordered output, error field presence and retry identity.
It passes the existing neutral `FunctionResultPayload` through Runtime. Profile
validation sees placement and success; adapters own native conversion. Only actual
image-result delivery requires `function_result_images` from the selected Runtime.
Ordinary function declarations and text results do not acquire an image requirement.
A Runtime refusal after durable admission fails execution without fabricating
application; the original result remains available for recovery queries.

Claude uses native MCP text/image blocks. A live root tool result acknowledges the
once-only pending call only with matching Session/call identity, success, exact text,
block count/order and a native base64 image at every image position. Replay,
synthetic and subagent records cannot acknowledge it. Native image resizing or
re-encoding is allowed; this is incorporation into native history, not unchanged
bytes/pixels or a guarantee that the provider has already consumed the image.
Public Items preserve the caller's bytes. Native decode failure, text fallback or
missing images fails receipt validation. The existing uncertain-delivery timeout
and cancellation behavior remain unchanged; no replay mechanism is added.

Codex's existing result acknowledgement follows a successful transport write. It
must not be described as a native consumption receipt. Real model image use and
native completion provide separate execution evidence. The submitted result remains
durable; process loss between write and native consumption is still unqualified
and tracked as `FUNCTION-RECEIPT-NATIVE-001`.

## Validation

`TestNativeFunctionImagePublicExecution` and `tests/official_function_images.py`
exercise the same independent Core/PostgreSQL/daemon/native path with each selected
real model and the pinned SDK 3.13.0 plus raw HTTP. The workflow covers mixed
text/PNG/text, a 6000x2100 PNG requiring native preprocessing, image-only JPEG,
failed text, pending-call cancellation and cold daemon continuation with unchanged
native Session identity. The real answer must read visual information absent from
the tool description and text content. Recovery reads must retain original content.
Retries admit one result; changed retries conflict; foreign tenants cannot read or
submit it. Claude invalid/remote/error image batches leave the call and history
untouched, then a valid result succeeds on that same pending call.

Direct native feasibility probes separately establish Claude's successful image
preprocessing and lossy error-image path. They do not replace public acceptance.
Controlled tests cover malformed/missing/reordered receipts, wrong identity,
unsupported placement, operation-specific Runtime support and batch atomicity.
The bundled JPEG fixture has yellow, blue, red and green vertical bands; it contains
no metadata or credentials. PNG markers are generated with randomized band order.

The [Docker workspace workflow](message-input.md#docker-workspace-acceptance)
uses the same function-result contract alongside native file tools, public
Files/Artifacts and cold Core/Runtime continuation. It checks Claude's rejected
error/remote result directly on an outstanding call before accepting a valid
image on that same call; no mixed-message rejection substitutes for this check.

The accepted combinations and run evidence are recorded in the task board. This
coverage does not qualify self-hosted/user-managed image results, all native image limits, provider
parity, arbitrary managed output rewrites, crash recovery or full Agents API
compatibility. No downloader, image converter or second tool loop belongs in Core.
