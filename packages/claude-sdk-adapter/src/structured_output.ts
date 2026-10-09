import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { isDeepStrictEqual } from "node:util";
import type { MessageEvent } from "./messages.js";

type Candidate = { id: string; text: string; snapshot?: string; input?: unknown; receipt?: string; stopped: boolean; status?: "failed" | "accepted" | "published" };

// StructuredOutput is the native terminal tool. Its acknowledged tool-use identity
// owns the final JSON message; the parent assistant may already own ordinary prose.
export class StructuredOutput {
  private readonly calls = new Map<string, Candidate>();
  private readonly messages = new Set<string>();
  private readonly events = new Set<string>();
  private active?: { id: string; blocks: Map<number, Candidate> };
  private session = "";

  consume(message: SDKMessage, session: string): void {
    if (!session || message.session_id !== session ||
        ("isReplay" in message && message.isReplay) || ("isSynthetic" in message && message.isSynthetic)) return;
    const retracted = message.type === "assistant" ? message.supersedes :
      message.type === "system" && message.subtype === "model_refusal_fallback" ? message.retracted_message_uuids : undefined;
    if (retracted?.some(id => [...this.calls.values()].some(call => call.snapshot === id || call.receipt === id))) {
      throw new Error("retracted structured output");
    }
    if (!("parent_tool_use_id" in message) || message.parent_tool_use_id !== null) return;
    this.session = session;
    if (message.type === "stream_event") {
      if (!message.uuid || this.events.has(message.uuid)) throw new Error("invalid structured stream identity");
      this.events.add(message.uuid);
      const event = message.event;
      if (event.type === "message_start") {
        if (!event.message.id || this.messages.has(event.message.id) || this.active?.blocks.size) throw new Error("invalid structured message identity");
        this.messages.add(event.message.id);
        this.active = { id: event.message.id, blocks: new Map() };
      } else if (event.type === "content_block_start" && event.content_block.type === "tool_use" && event.content_block.name === "StructuredOutput") {
        const id = event.content_block.id;
        if (!this.active || !id || this.calls.has(id) || this.active.blocks.has(event.index) ||
            [...this.calls.values()].some(call => call.status === "accepted")) throw new Error("invalid structured output identity");
        const call: Candidate = { id, text: "", stopped: false };
        this.calls.set(id, call);
        this.active.blocks.set(event.index, call);
      } else if (event.type === "content_block_delta" && event.delta.type === "input_json_delta") {
        const call = this.active?.blocks.get(event.index);
        if (call) {
          if (call.snapshot || call.stopped) throw new Error("late structured output input");
          call.text += event.delta.partial_json;
        }
      } else if (event.type === "content_block_stop") {
        const call = this.active?.blocks.get(event.index);
        if (call) {
          if (!call.snapshot || call.stopped) throw new Error("unconfirmed structured output block");
          call.stopped = true;
        }
      } else if (event.type === "message_stop") {
        if (this.active && [...this.active.blocks.values()].some(call => !call.stopped)) throw new Error("incomplete structured output message");
        this.active = undefined;
      }
    } else if (message.type === "assistant") {
      for (const block of message.message.content) {
        if (block.type !== "tool_use" || block.name !== "StructuredOutput") continue;
        const call = this.calls.get(block.id);
        if (!call || !this.active || message.message.id !== this.active.id ||
            ![...this.active.blocks.values()].includes(call) || call.snapshot || !message.uuid || message.error || message.aborted) throw new Error("unmatched structured output snapshot");
        call.snapshot = message.uuid;
        call.input = block.input;
      }
    } else if (message.type === "user" && Array.isArray(message.message.content)) {
      for (const block of message.message.content) {
        if (block.type !== "tool_result") continue;
        const call = this.calls.get(block.tool_use_id);
        if (!call) continue;
        if (!call.snapshot || call.receipt || !message.uuid) throw new Error("invalid structured output receipt");
        // Failed native attempts may contain malformed JSON and must reach the
        // SDK retry loop. Successful inputs must match JSON transport's zero semantics.
        if (!block.is_error && !isDeepStrictEqual(JSON.parse(call.text, (_, value) => value === 0 ? 0 : value), call.input)) {
          throw new Error("unmatched structured output input");
        }
        call.receipt = message.uuid;
        call.status = block.is_error ? "failed" : "accepted";
      }
    }
  }

  complete(message: SDKMessage): MessageEvent {
    const calls = [...this.calls.values()];
    const accepted = calls.filter(call => call.status === "accepted");
    if (message.type !== "result" || message.subtype !== "success" || message.is_error ||
        message.session_id !== this.session || message.structured_output === undefined || this.active?.blocks.size ||
        calls.some(call => !call.stopped || !call.receipt) || accepted.length !== 1 ||
        !isDeepStrictEqual(accepted[0]!.input, message.structured_output)) throw new Error("unconfirmed structured output");
    // Native validation compares binary64 values. Publish only the attributed raw
    // tool input: reserializing the validated object would lose original digits.
    const { id, text } = accepted[0]!;
    accepted[0]!.status = "published";
    return { type: "output_message", message: { id, status: "completed", phase: "final_answer", text } };
  }
}
