---
title: "Message content"
---

User messages and function results share one content model: an ordered list of `input_text` and `input_image` parts. Core stores message boundaries, part order and image references exactly as sent and returns them unchanged in user Items. It never downloads, transcodes or repairs media. Session creation `input` and `events.create` messages share validation and admission; [Sessions, events and history](./sessions-events.md#send-input) covers admission, request limits and errors.

## Messages

A message has `role: "user"`, an optional `type: "message"` and a non-empty `content` array. Event `input` is an array of messages; Session creation `input` may also be a string, which becomes one text message. An explicit null or empty message `type`, a string `content` or a string event `input` is invalid.

A message is valid when it has an image or at least one non-empty text part. Core never trims text. These requests return 400 `invalid_request` and write nothing:

- an empty `input` string, `input` array or `content` array;
- a message whose text parts are all empty and that has no image.

An empty text part beside other content, such as `["", "text"]`, is accepted and stored as sent.

## Images

An `input_image` part carries `image_url` as an inline data URI: `data:image/png;base64,…` or `data:image/jpeg;base64,…`. The base64 must be canonical and the decoded image must match the declared type. Core accepts no remote URL, `file_id` or `detail`.

| Harness | Message images |
| --- | --- |
| Codex | Accepted on every placement the harness supports: `none`, `openai_hosted` and `self_hosted` |
| Claude Code | Accepted on `none`, `openai_hosted` and `self_hosted` |
| MiniMax Code | Rejected |

Admission checks the harness before anything is written; an image the harness cannot take returns 400. The Runtime must also report message image support: Core binds a Session whose input carries images only to such a Runtime, and delivery to a Runtime without it fails. Use a model that accepts images.

## Whitespace-only text

Whitespace-only text such as `"   "` or `"\n\t"` is valid content and is stored and returned verbatim. Whether a harness can run it is declared in its engine profile:

| Harness | A message with no image and no non-whitespace text |
| --- | --- |
| Codex | Admitted and delivered unchanged |
| Claude Code | 400 `unsupported_or_invalid_configuration` |
| MiniMax Code | 400 `unsupported_or_invalid_configuration` |

The rejection applies to Session creation (including streaming and `self_hosted` creation) and to `events.create`, before any write, reservation or Turn, so a running Turn is never disturbed. Whitespace beside non-whitespace text in the same message is admitted for every harness. Whitespace is the union of Go `unicode.IsSpace` and ECMAScript `String.prototype.trim`, for example U+0085 and U+FEFF; Core admission and the Claude bridge use the same set.

## Function results

An `agent.session.input.tool_result` event carries `success`, an optional nullable `error` string and an optional nullable `output`: a string or an ordered array of `input_text` and `input_image` parts.

- Core stores the result as submitted, including which of `output` and `error` were present, and uses it for retry identity. Public Items always carry both fields ([Item rules](./sessions-events.md#turns-and-items)).
- The Runtime receives one ordered content list: the `output` parts, then the `error` text as a final text part. This conversion never changes the stored result.
- A result with images needs a Runtime that reports function-result image support; only image-bearing results check it. When the Runtime refuses a result after admission, the Turn fails without a confirmed application, and the stored result stays readable.

| Harness | Function results |
| --- | --- |
| Codex | Text and ordered text/image output. Core checks only that each part is well formed and passes image references to the harness unchanged |
| Claude Code | Text output. Images only in successful results and only as inline PNG or JPEG; an image in a failed result or a remote reference returns 400 before anything is stored, and the pending call stays open. The harness may resize or re-encode images in its own history; public Items keep the submitted bytes |
| MiniMax Code | No public functions |

### Application receipts

Admission (202) does not mean the harness used the result. The pending call clears when the adapter confirms native application:

- **Claude Code** confirms with a live root native tool result that matches the Session, call ID, success flag, exact text, block count and order, with a native image at every image position. Replayed, synthetic and Subagent records do not confirm it.
- **Codex** confirms with the live root dynamic-tool `item/completed` observation that matches thread, Turn, call, function name, status, success flag and the exact ordered content. A write to the harness alone does not confirm it.

Both adapters wait at most 10 seconds for a receipt. A timeout or native release without confirmation leaves application uncertain. Confirmation means the harness recorded the result, not that the model provider consumed it. Core never replays a result automatically; the submission stays stored for recovery reads.

## Runtime boundary

The Core–Runtime wire carries messages as `MessageInput` for initial input, prepared start and steering, with the same ordered `InputContent` parts that function results use ([Core–Runtime protocol](../../docs/runtime-protocol.md)). Admission checks the harness's declared profile; binding and delivery check what the Runtime reports. Adapters own native encoding and application receipts. A text-only adapter rejects image parts instead of dropping them.

- **Codex** flattens a batch into its native input list with a blank-line separator between public messages. Public message boundaries stay in Core's storage; the native history does not keep them.
- **Claude Code** sends native image blocks and a UUID per native user message. One public input is applied only after every message in its batch is consumed. Within one native Turn the bridge accepts at most 64 user messages, including the opening prompt; it rejects a steering batch that would exceed the bound before submitting any part of it, which ends the running Turn. The daemon requires bridge protocol 3.

Native message and function-result image checks are listed under [qualify the adapter](./harness-onboarding.md#qualify-the-adapter).
