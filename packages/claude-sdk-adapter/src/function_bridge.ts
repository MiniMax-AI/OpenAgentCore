import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { isDeepStrictEqual } from "node:util";
import type { FunctionCall, FunctionHandler } from "./functions.js";
import { imageSource, nativeContent, parseInputContent, type InputContent } from "./message_input.js";

export type FunctionResult = {
  type: "function_result";
  call_id: string;
  delivery_id: string;
  success: boolean;
  content: InputContent[];
};
export type FunctionEvent =
  | { type: "function_call"; call: { call_id: string; name: string; arguments: Record<string, unknown> } }
  | { type: "function_applied"; call_id: string; delivery_id: string };
type Pending = {
  resolve: (result: CallToolResult) => void;
  reject: (error: Error) => void;
  cleanup: () => void;
  result?: FunctionResult;
};

export class FunctionBridge {
  private readonly pending = new Map<string, Pending>();
  private readonly seen = new Set<string>();
  constructor(private readonly emit: (event: FunctionEvent) => Promise<void>) {}

  readonly invoke: FunctionHandler = async (call: FunctionCall, signal: AbortSignal) => {
    signal.throwIfAborted();
    if (this.seen.has(call.id)) throw new Error("Repeated native call identity.");
    this.seen.add(call.id);
    const waiting = new Promise<CallToolResult>((resolve, reject) => {
      const stop = () => {
        this.pending.delete(call.id);
        reject(new Error("Native function call aborted."));
      };
      signal.addEventListener("abort", stop, { once: true });
      this.pending.set(call.id, { resolve, reject, cleanup: () => signal.removeEventListener("abort", stop) });
    });
    const [, result] = await Promise.all([
      this.emit({ type: "function_call", call: { call_id: call.id, name: call.name, arguments: call.arguments } }),
      waiting,
    ]);
    return result;
  };

  submit(line: string): void {
    if (Buffer.byteLength(line) > 1024 * 1024) throw new Error("Invalid bridge input.");
    const value: unknown = JSON.parse(line);
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid function result.");
    const result = value as FunctionResult;
    if (Object.keys(result).some(key => !["type", "call_id", "delivery_id", "success", "content"].includes(key)) ||
        result.type !== "function_result" || typeof result.call_id !== "string" || !result.call_id ||
        typeof result.delivery_id !== "string" || !result.delivery_id || typeof result.success !== "boolean") {
      throw new Error("Invalid function result.");
    }
    parseInputContent(result.content);
    if (!result.success && result.content.some(part => part.type === "input_image")) {
      throw new Error("Native error results cannot retain images.");
    }
    const pending = this.pending.get(result.call_id);
    if (!pending || pending.result) throw new Error("Function result is not pending.");
    pending.result = result;
    pending.resolve({ content: result.content.map(part => {
      if (part.type === "input_text") return { type: "text" as const, text: part.text };
      const source = imageSource(part.image_url);
      return { type: "image" as const, mimeType: source.media_type, data: source.data };
    }), isError: !result.success });
  }

  async consume(message: SDKMessage, sessionID: string): Promise<void> {
    if (message.type !== "user" || message.parent_tool_use_id !== null || message.session_id !== sessionID ||
        message.isSynthetic || ("isReplay" in message && message.isReplay) || !Array.isArray(message.message.content)) return;
    for (const block of message.message.content) {
      if (block.type !== "tool_result") continue;
      const pending = this.pending.get(block.tool_use_id);
      if (!pending) continue;
      const result = pending.result;
      if (!result) throw new Error("Native response preceded host result.");
      // Native error results join MCP text blocks; retain the original parts in the host.
      const matches = result.success ? matchesNativeContent(result.content, block.content) :
        block.content === result.content.map(part => part.type === "input_text" ? part.text : "").join("\n");
      if (!!block.is_error !== !result.success || !matches) {
        throw new Error("Native function response differs from submitted result.");
      }
      await this.emit({ type: "function_applied", call_id: result.call_id, delivery_id: result.delivery_id });
      pending.cleanup();
      this.pending.delete(result.call_id);
    }
  }

  assertComplete(): void {
    if (this.pending.size) throw new Error("Native function results are unconfirmed.");
  }

  close(): void {
    for (const pending of this.pending.values()) {
      pending.cleanup();
      pending.reject(new Error("Execution ended before the function completed."));
    }
    this.pending.clear();
  }
}

// A matching root call confirms incorporation into native history. Native image
// decoding/resizing may change bytes; preserve text and ordered image positions.
function matchesNativeContent(content: InputContent[], actual: unknown): boolean {
  const expected = nativeContent({ content });
  if (!Array.isArray(expected) || !Array.isArray(actual) || actual.length !== expected.length) return false;
  return expected.every((part, index) => {
    const received = actual[index];
    if (part.type !== "image") return isDeepStrictEqual(part, received);
    if (!received || received.type !== "image" || received.source?.type !== "base64" ||
        typeof received.source.media_type !== "string" || typeof received.source.data !== "string") return false;
    try {
      imageSource(`data:${received.source.media_type};base64,${received.source.data}`);
      return true;
    } catch { return false; }
  });
}
