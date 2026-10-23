import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";

export type MessageEvent =
  | { type: "delta"; delta: string; item_id: string }
  | { type: "output_message"; message: { id: string; status: "in_progress" | "completed"; phase?: "final_answer"; text?: string } }
  | { type: "reasoning"; reasoning: { id: string; status: "in_progress" | "completed"; delta?: string } };

type ActiveMessage = {
  id: string;
  blocks: Map<number, string>;
  block?: number;
  observed: boolean;
  failed: boolean;
  thinking: Set<number>;
};

export class MessageObserver {
  private active?: ActiveMessage;
  // Native thinking blocks are reported as reasoning only when the Agent requested summaries.
  constructor(private readonly reasoning = false) {}

  consume(message: SDKMessage): MessageEvent[] {
    if ((message.type !== "stream_event" && message.type !== "assistant") ||
        message.parent_tool_use_id !== null) return [];
    if (message.type === "assistant") {
      if (message.error) {
        if (this.active) this.active.failed = true;
        return [];
      }
      const text = message.message.content.filter(block => block.type === "text");
      if (!text.length) return [];
      const active = this.active;
      if (!active || message.message.id !== active.id || active.block === undefined ||
          !active.blocks.has(active.block)) throw new Error("unmatched native text snapshot");
      // The SDK delivers one completed content block, before content_block_stop.
      active.blocks.set(active.block, text.map(block => block.text).join(""));
      return [];
    }
    const event = message.event;
    if (event.type === "message_start") {
      if (!event.message.id) throw new Error("missing native message identity");
      this.active = { id: event.message.id, blocks: new Map(), observed: false, failed: false, thinking: new Set() };
      return [];
    }
    const active = this.active;
    if (!active) throw new Error("native message start is missing");
    if (event.type === "content_block_start") {
      active.block = event.index;
      if (event.content_block.type === "thinking" && this.reasoning) {
        active.thinking.add(event.index);
        const id = `${active.id}:${event.index}`;
        return [{ type: "reasoning", reasoning: { id, status: "in_progress", ...(event.content_block.thinking ? { delta: event.content_block.thinking } : {}) } }];
      }
      if (event.content_block.type !== "text") return [];
      active.blocks.set(event.index, event.content_block.text);
      const events: MessageEvent[] = [];
      if (!active.observed) {
        active.observed = true;
        events.push({ type: "output_message", message: { id: active.id, status: "in_progress" } });
      }
      if (event.content_block.text) events.push({ type: "delta", item_id: active.id, delta: event.content_block.text });
      return events;
    }
    if (event.type === "content_block_delta" && event.delta.type === "text_delta") {
      const previous = active.blocks.get(event.index);
      if (previous === undefined || active.block !== event.index) throw new Error("native text block start is missing");
      active.blocks.set(event.index, previous + event.delta.text);
      return event.delta.text ? [{ type: "delta", item_id: active.id, delta: event.delta.text }] : [];
    }
    if (event.type === "content_block_delta" && event.delta.type === "thinking_delta" && active.thinking.has(event.index)) {
      return event.delta.thinking ? [{ type: "reasoning", reasoning: { id: `${active.id}:${event.index}`, status: "in_progress", delta: event.delta.thinking } }] : [];
    }
    if (event.type === "content_block_stop") {
      active.block = undefined;
      if (active.thinking.delete(event.index)) return [{ type: "reasoning", reasoning: { id: `${active.id}:${event.index}`, status: "completed" } }];
    }
    if (event.type === "message_stop") {
      this.active = undefined;
      if (active.observed && !active.failed) {
        const text = [...active.blocks].sort(([a], [b]) => a - b).map(([, text]) => text).join("");
        return [{ type: "output_message", message: { id: active.id, status: "completed", text } }];
      }
    }
    return [];
  }
}
