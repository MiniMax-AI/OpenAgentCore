import type { SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";

export type InputContent = { type: "input_text"; text: string } | { type: "input_image"; image_url: string };
export type MessageInput = { content: InputContent[] }[];

// This is the common Runtime representation; native image blocks stay here.
export function parseMessageInput(value: unknown): MessageInput {
  if (!Array.isArray(value) || !value.length) throw new Error("Invalid user messages.");
  for (const message of value) {
    if (!message || typeof message !== "object" || Object.keys(message).some(key => key !== "content") ||
        !Array.isArray(message.content) || !message.content.length) throw new Error("Invalid user message.");
    const content = parseInputContent(message.content);
    const meaningful = content.some(part => part.type === "input_image" || Boolean(part.text.trim()));
    if (!meaningful) throw new Error("Empty user message.");
  }
  return value as MessageInput;
}

// Function results share content validation but permit empty arrays and empty text.
export function parseInputContent(content: unknown): InputContent[] {
  if (!Array.isArray(content)) throw new Error("Invalid input content.");
  for (const part of content) {
    if (!part || typeof part !== "object") throw new Error("Invalid user content.");
    if (part.type === "input_text" && typeof part.text === "string" && Object.keys(part).every(key => key === "type" || key === "text")) {
      continue;
    } else if (part.type === "input_image" && typeof part.image_url === "string" &&
        Object.keys(part).every(key => key === "type" || key === "image_url")) {
      imageSource(part.image_url);
    } else throw new Error("Invalid user content.");
  }
  return content as InputContent[];
}

export function imageSource(url: string): { type: "base64"; media_type: "image/png" | "image/jpeg"; data: string } {
  const match = /^data:(image\/png|image\/jpeg);base64,([A-Za-z0-9+/]+={0,2})$/.exec(url);
  if (!match || Buffer.from(match[2]!, "base64").toString("base64") !== match[2]) throw new Error("Unsupported image reference.");
  return { type: "base64", media_type: match[1] as "image/png" | "image/jpeg", data: match[2]! };
}

export function nativeContent(message: MessageInput[number]): SDKUserMessage["message"]["content"] {
  return message.content.map(part => part.type === "input_text"
    ? { type: "text" as const, text: part.text }
    : { type: "image" as const, source: imageSource(part.image_url) });
}

export function textInput(text: string): MessageInput { return [{ content: [{ type: "input_text", text }] }]; }
