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
  | { type: "function_call"; call: { call_id: string; name: string; arguments: Record<string, unknown>; native_id?: string; turn_id?: string } }
  | { type: "function_withdrawn"; withdrawn: { call_id: string; native_id: string; turn_id: string } }
  | { type: "function_applied"; call_id: string; delivery_id: string };
type Pending = {
  resolve: (result: CallToolResult) => void;
  reject: (error: Error) => void;
  cleanup: () => void;
  result?: FunctionResult;
  aborted?: boolean;
  child?: FunctionCall["child"];
};
// Native stopped waiting for the call, so no result can be applied.
class Abandoned extends Error {}

export class FunctionBridge {
  private readonly pending = new Map<string, Pending>();
  private readonly seen = new Set<string>();
  private readonly withdrawn = new Set<string>();
  constructor(private readonly emit: (event: FunctionEvent) => Promise<void>) {}

  readonly invoke: FunctionHandler = async (call: FunctionCall, signal: AbortSignal) => {
    signal.throwIfAborted();
    if (this.seen.has(call.id)) throw new Error("Repeated native call identity.");
    this.seen.add(call.id);
    const waiting = new Promise<CallToolResult>((resolve, reject) => {
      const stop = () => {
        const pending = this.pending.get(call.id);
        if (pending?.result) pending.aborted = true;
        else this.pending.delete(call.id);
        reject(new Abandoned("Native function call aborted."));
      };
      signal.addEventListener("abort", stop, { once: true });
      this.pending.set(call.id, { resolve, reject, cleanup: () => signal.removeEventListener("abort", stop), child: call.child });
    });
    try {
      const [, result] = await Promise.all([
        this.emit({ type: "function_call", call: { call_id: call.id, name: call.name, arguments: call.arguments, ...call.child } }),
        waiting,
      ]);
      return result;
    } catch (error) {
      if (call.child && error instanceof Abandoned) {
        this.withdrawn.add(call.id);
        await this.emit({ type: "function_withdrawn", withdrawn: { call_id: call.id, ...call.child } });
      }
      throw error;
    }
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
    // The host may send a result before it reads the withdrawal; native no longer waits for it.
    if (!pending && this.withdrawn.has(result.call_id)) return;
    if (!pending || pending.result) throw new Error("Function result is not pending.");
    pending.result = result;
    pending.resolve({ content: result.content.map(part => {
      if (part.type === "input_text") return { type: "text" as const, text: part.text };
      const source = imageSource(part.image_url);
      return { type: "image" as const, mimeType: source.media_type, data: source.data };
    }), isError: !result.success });
  }

  async consume(message: SDKMessage, sessionID: string, cancelled = false): Promise<void> {
    if (message.type !== "user" || message.session_id !== sessionID ||
        message.isSynthetic || ("isReplay" in message && message.isReplay) || !Array.isArray(message.message.content)) return;
    for (const block of message.message.content) {
      if (block.type !== "tool_result") continue;
      const pending = this.pending.get(block.tool_use_id);
      if (!pending || pending.aborted || (message.parent_tool_use_id !== null) !== !!pending.child) continue;
      const result = pending.result;
      if (!result) {
        if (!cancelled || !block.is_error) throw new Error("Native response preceded host result.");
        this.cancelUnanswered(block.tool_use_id);
        continue;
      }
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

  // Only a native cancellation result authorizes abandoning unanswered calls.
  // Submitted results retain their receipt obligation, including after abort.
  cancelUnanswered(callID?: string): void {
    for (const [id, pending] of this.pending) {
      if (pending.result || (callID !== undefined && id !== callID)) continue;
      pending.cleanup();
      this.pending.delete(id);
      pending.reject(new Abandoned("Native function call cancelled."));
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
// processing may change bytes or replace an unprocessable image with a text note;
// preserve text and ordered image positions.
function matchesNativeContent(content: InputContent[], actual: unknown): boolean {
  const expected = nativeContent({ content });
  if (!Array.isArray(expected) || !Array.isArray(actual) || actual.length !== expected.length) return false;
  return expected.every((part, index) => part.type === "image"
    ? actual[index]?.type === "image" || actual[index]?.type === "text"
    : isDeepStrictEqual(part, actual[index]));
}
