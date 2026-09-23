import { describe, expect, it } from "vitest";

import {
  createSessionInitialInputDraft,
  projectSessionInitialInput,
  sessionInitialInputError,
  sessionInitialInputDraftReducer,
  type SessionInitialInputDraft,
} from "./session-initial-input";

function messageDraft(): SessionInitialInputDraft {
  return {
    mode: "messages",
    text: "inactive text draft",
    messages: [
      {
        id: "message-a",
        parts: [
          { id: "part-a1", text: "  first part\n" },
          { id: "part-a2", text: "second part" },
        ],
      },
      {
        id: "message-b",
        parts: [{ id: "part-b1", text: "third part" }],
      },
    ],
  };
}

describe("Session initial input projection", () => {
  it("keeps Text mode compatible with the existing exact-string semantics", () => {
    expect(projectSessionInitialInput(createSessionInitialInputDraft(" \t\n\u0085 "))).toEqual({
      ok: true,
      input: undefined,
    });

    const exact = "  Keep both edges.\n";
    expect(projectSessionInitialInput(createSessionInitialInputDraft(exact))).toEqual({
      ok: true,
      input: exact,
    });
  });

  it("preserves message grouping, part order, and exact meaningful text", () => {
    expect(projectSessionInitialInput(messageDraft())).toEqual({
      ok: true,
      input: [
        {
          type: "message",
          role: "user",
          content: [
            { type: "input_text", text: "  first part\n" },
            { type: "input_text", text: "second part" },
          ],
        },
        {
          type: "message",
          role: "user",
          content: [{ type: "input_text", text: "third part" }],
        },
      ],
    });
  });

  it("rejects an empty message array, a message without parts, and an all-blank message", () => {
    expect(projectSessionInitialInput({
      mode: "messages",
      text: "",
      messages: [],
    })).toEqual({ ok: false, error: "Add at least one user message." });

    expect(projectSessionInitialInput({
      mode: "messages",
      text: "",
      messages: [{ id: "message", parts: [] }],
    })).toEqual({
      ok: false,
      error: "User message 1 needs at least one text part.",
    });

    expect(projectSessionInitialInput({
      mode: "messages",
      text: "",
      messages: [{
        id: "message",
        parts: [
          { id: "valid", text: "valid" },
          { id: "blank", text: " \t\n\u0085 " },
        ],
      }],
    }).ok).toBe(true);

    expect(projectSessionInitialInput({
      mode: "messages",
      text: "",
      messages: [{
        id: "message",
        parts: [
          { id: "blank-1", text: " " },
          { id: "blank-2", text: "\t\n\u0085" },
        ],
      }],
    })).toEqual({
      ok: false,
      error: "User message 1 needs nonblank text across its parts.",
    });
  });

  it("returns a detached request value and never mutates the draft", () => {
    const draft = messageDraft();
    const before = structuredClone(draft);
    const projection = projectSessionInitialInput(draft);

    expect(draft).toEqual(before);
    expect(projection.ok).toBe(true);
    if (!projection.ok || !Array.isArray(projection.input)) return;
    projection.input[0]!.content[0]!.text = "changed output";
    expect(draft).toEqual(before);
  });
});

describe("Session initial input admission", () => {
  it.each([undefined, null, "", " \t\n\u0085", []])("rejects missing or empty none input: %j", (input) => {
    expect(sessionInitialInputError(input, "none")).not.toBeNull();
  });

  it.each(["self_hosted", "openai_hosted"])("permits missing or null input for %s", (environmentType) => {
    expect(sessionInitialInputError(undefined, environmentType)).toBeNull();
    expect(sessionInitialInputError(null, environmentType)).toBeNull();
  });

  it("validates the submitted Web profile without changing text or ordering", () => {
    const projected = projectSessionInitialInput(messageDraft());
    expect(projected.ok && sessionInitialInputError(projected.input, "none")).toBeNull();
    expect(sessionInitialInputError("  Explain this code.\n", "none")).toBeNull();
    for (const input of [0, {}, [{ role: "assistant", content: [{ type: "input_text", text: "text" }] }],
      [{ role: "user", content: [] }], [{ type: null, role: "user", content: [{ type: "input_text", text: "text" }] }],
      [{ role: "user", content: [{ type: "input_text", text: " \u0085" }] }],
      [{ role: "user", content: [{ type: "input_image", image_url: "https://example.test/image.png" }] }]]) {
      expect(sessionInitialInputError(input, "none")).not.toBeNull();
    }
  });
});

describe("Session initial input draft reducer", () => {
  it("seeds the first message from exact Text content and retains both drafts across switches", () => {
    const initial = createSessionInitialInputDraft("  exact seed\n");
    const messages = sessionInitialInputDraftReducer(initial, {
      type: "set-mode",
      mode: "messages",
      seed: { messageId: "seed-message", partId: "seed-part" },
    });
    expect(messages).toEqual({
      mode: "messages",
      text: "  exact seed\n",
      messages: [{
        id: "seed-message",
        parts: [{ id: "seed-part", text: "  exact seed\n" }],
      }],
    });

    const editedMessages = sessionInitialInputDraftReducer(messages, {
      type: "update-part",
      messageId: "seed-message",
      partId: "seed-part",
      value: "array-only edit",
    });
    const text = sessionInitialInputDraftReducer(editedMessages, { type: "set-mode", mode: "text" });
    const returned = sessionInitialInputDraftReducer(text, {
      type: "set-mode",
      mode: "messages",
      seed: { messageId: "ignored-message", partId: "ignored-part" },
    });

    expect(text.text).toBe("  exact seed\n");
    expect(text.messages[0]?.parts[0]?.text).toBe("array-only edit");
    expect(returned.messages).toBe(text.messages);
    expect(returned.messages[0]?.id).toBe("seed-message");
  });

  it("adds, moves, and removes ordered messages without mutating the source", () => {
    const source = messageDraft();
    const before = structuredClone(source);
    const added = sessionInitialInputDraftReducer(source, {
      type: "add-message",
      message: { id: "message-c", parts: [{ id: "part-c1", text: "fourth" }] },
    });
    const moved = sessionInitialInputDraftReducer(added, {
      type: "move-message",
      messageId: "message-c",
      direction: -1,
    });
    const removed = sessionInitialInputDraftReducer(moved, {
      type: "remove-message",
      messageId: "message-a",
    });

    expect(source).toEqual(before);
    expect(added.messages.map((message) => message.id)).toEqual([
      "message-a", "message-b", "message-c",
    ]);
    expect(moved.messages.map((message) => message.id)).toEqual([
      "message-a", "message-c", "message-b",
    ]);
    expect(removed.messages.map((message) => message.id)).toEqual([
      "message-c", "message-b",
    ]);
  });

  it("adds, updates, moves, and removes ordered parts without mutating siblings", () => {
    const source = messageDraft();
    const sibling = source.messages[1];
    const added = sessionInitialInputDraftReducer(source, {
      type: "add-part",
      messageId: "message-a",
      part: { id: "part-a3", text: "fourth part" },
    });
    const moved = sessionInitialInputDraftReducer(added, {
      type: "move-part",
      messageId: "message-a",
      partId: "part-a3",
      direction: -1,
    });
    const updated = sessionInitialInputDraftReducer(moved, {
      type: "update-part",
      messageId: "message-a",
      partId: "part-a3",
      value: "updated fourth",
    });
    const removed = sessionInitialInputDraftReducer(updated, {
      type: "remove-part",
      messageId: "message-a",
      partId: "part-a1",
    });

    expect(source.messages[0]?.parts.map((part) => part.id)).toEqual(["part-a1", "part-a2"]);
    expect(added.messages[1]).toBe(sibling);
    expect(moved.messages[0]?.parts.map((part) => part.id)).toEqual([
      "part-a1", "part-a3", "part-a2",
    ]);
    expect(removed.messages[0]?.parts).toEqual([
      { id: "part-a3", text: "updated fourth" },
      { id: "part-a2", text: "second part" },
    ]);
  });

  it("keeps the editor's one-message and one-part minimum invariant", () => {
    const one: SessionInitialInputDraft = {
      mode: "messages",
      text: "",
      messages: [{ id: "message", parts: [{ id: "part", text: "value" }] }],
    };

    expect(sessionInitialInputDraftReducer(one, {
      type: "remove-message",
      messageId: "message",
    })).toBe(one);
    expect(sessionInitialInputDraftReducer(one, {
      type: "remove-part",
      messageId: "message",
      partId: "part",
    })).toEqual(one);
  });
});
