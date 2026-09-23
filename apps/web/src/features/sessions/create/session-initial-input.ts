import type { InputMessage } from "@agents-core-web/agents-client";

export type SessionInitialInputMode = "text" | "messages";

export interface SessionInitialInputPartDraft {
  id: string;
  text: string;
}

export interface SessionInitialInputMessageDraft {
  id: string;
  parts: SessionInitialInputPartDraft[];
}

/**
 * Text and message-array values intentionally live side by side. Switching the
 * active mode never destroys the inactive draft; only `mode` controls which
 * value is projected into a Session create request.
 */
export interface SessionInitialInputDraft {
  mode: SessionInitialInputMode;
  text: string;
  messages: SessionInitialInputMessageDraft[];
}

export type SessionInitialInputDraftAction =
  | { type: "set-mode"; mode: "text" }
  | {
    type: "set-mode";
    mode: "messages";
    seed?: { messageId: string; partId: string };
  }
  | { type: "set-text"; value: string }
  | { type: "add-message"; message: SessionInitialInputMessageDraft }
  | { type: "remove-message"; messageId: string }
  | { type: "move-message"; messageId: string; direction: -1 | 1 }
  | {
    type: "add-part";
    messageId: string;
    part: SessionInitialInputPartDraft;
  }
  | { type: "update-part"; messageId: string; partId: string; value: string }
  | { type: "remove-part"; messageId: string; partId: string }
  | {
    type: "move-part";
    messageId: string;
    partId: string;
    direction: -1 | 1;
  };

export type SessionInitialInputProjection =
  | { ok: true; input: string | InputMessage[] | undefined }
  | { ok: false; error: string };

const CORE_WHITESPACE_ONLY = /^\p{White_Space}*$/u;

function isBlank(value: string): boolean {
  return CORE_WHITESPACE_ONLY.test(value);
}

function moveById<Item extends { id: string }>(
  items: Item[],
  id: string,
  direction: -1 | 1,
): Item[] {
  const index = items.findIndex((item) => item.id === id);
  const destination = index + direction;
  if (index < 0 || destination < 0 || destination >= items.length) return items;

  const sourceItem = items[index];
  const destinationItem = items[destination];
  if (!sourceItem || !destinationItem) return items;
  const moved = [...items];
  moved[index] = destinationItem;
  moved[destination] = sourceItem;
  return moved;
}

export function createSessionInitialInputDraft(text = ""): SessionInitialInputDraft {
  return { mode: "text", text, messages: [] };
}

export function sessionInitialInputDraftReducer(
  draft: SessionInitialInputDraft,
  action: SessionInitialInputDraftAction,
): SessionInitialInputDraft {
  if (action.type === "set-mode") {
    if (draft.mode === action.mode) return draft;
    if (action.mode === "text") return { ...draft, mode: "text" };
    if (draft.messages.length > 0) return { ...draft, mode: "messages" };

    const seed = action.seed ?? {
      messageId: "session-initial-message-1",
      partId: "session-initial-part-1",
    };
    return {
      ...draft,
      mode: "messages",
      messages: [{
        id: seed.messageId,
        parts: [{ id: seed.partId, text: draft.text }],
      }],
    };
  }

  if (action.type === "set-text") return { ...draft, text: action.value };

  if (action.type === "add-message") {
    if (draft.messages.some((message) => message.id === action.message.id)) return draft;
    return {
      ...draft,
      messages: [
        ...draft.messages,
        {
          ...action.message,
          parts: action.message.parts.map((part) => ({ ...part })),
        },
      ],
    };
  }

  if (action.type === "remove-message") {
    if (draft.messages.length <= 1) return draft;
    return {
      ...draft,
      messages: draft.messages.filter((message) => message.id !== action.messageId),
    };
  }

  if (action.type === "move-message") {
    const messages = moveById(draft.messages, action.messageId, action.direction);
    return messages === draft.messages ? draft : { ...draft, messages };
  }

  return {
    ...draft,
    messages: draft.messages.map((message) => {
      if (message.id !== action.messageId) return message;

      if (action.type === "add-part") {
        if (message.parts.some((part) => part.id === action.part.id)) return message;
        return { ...message, parts: [...message.parts, { ...action.part }] };
      }
      if (action.type === "update-part") {
        return {
          ...message,
          parts: message.parts.map((part) => (
            part.id === action.partId ? { ...part, text: action.value } : part
          )),
        };
      }
      if (action.type === "remove-part") {
        if (message.parts.length <= 1) return message;
        return {
          ...message,
          parts: message.parts.filter((part) => part.id !== action.partId),
        };
      }

      const parts = moveById(message.parts, action.partId, action.direction);
      return parts === message.parts ? message : { ...message, parts };
    }),
  };
}

/** Checks the finite text-only Web profile before Session creation. */
export function sessionInitialInputError(input: unknown, environmentType: string): string | null {
  if (input == null) {
    return environmentType === "none" ? "A first message is required without an Environment." : null;
  }
  if (typeof input === "string") return isBlank(input) ? "Enter a nonblank first message." : null;
  if (!Array.isArray(input) || input.length === 0) return "Add at least one user message.";
  for (const [index, message] of input.entries()) {
    if (
      !message || typeof message !== "object" || Array.isArray(message)
      || Object.keys(message).some((key) => !["type", "role", "content"].includes(key))
      || (Object.hasOwn(message, "type") && message.type !== "message")
      || message.role !== "user" || !Array.isArray(message.content) || message.content.length === 0
      || message.content.some((part: unknown) => (
        !part || typeof part !== "object" || Array.isArray(part)
        || Object.keys(part).some((key) => !["type", "text"].includes(key))
        || !("type" in part) || part.type !== "input_text" || !("text" in part) || typeof part.text !== "string"
      ))
    ) return `User message ${index + 1} must contain supported text parts.`;
    if (isBlank(message.content.map((part: { text: string }) => part.text).join(""))) {
      return `User message ${index + 1} needs nonblank text across its parts.`;
    }
  }
  return null;
}

/**
 * Strictly projects the active draft without trimming, joining, regrouping, or
 * otherwise rewriting meaningful user text.
 */
export function projectSessionInitialInput(
  draft: SessionInitialInputDraft,
): SessionInitialInputProjection {
  if (draft.mode === "text") {
    return { ok: true, input: isBlank(draft.text) ? undefined : draft.text };
  }

  if (draft.messages.length === 0) {
    return { ok: false, error: "Add at least one user message." };
  }

  for (const [messageIndex, message] of draft.messages.entries()) {
    if (message.parts.length === 0) {
      return {
        ok: false,
        error: `User message ${messageIndex + 1} needs at least one text part.`,
      };
    }
    if (isBlank(message.parts.map((part) => part.text).join(""))) {
      return {
        ok: false,
        error: `User message ${messageIndex + 1} needs nonblank text across its parts.`,
      };
    }
  }

  return {
    ok: true,
    input: draft.messages.map((message) => ({
      type: "message",
      role: "user",
      content: message.parts.map((part) => ({ type: "input_text", text: part.text })),
    })),
  };
}
