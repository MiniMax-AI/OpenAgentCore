import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";

type Action = { type: "search"; query: string } | { type: "open_page"; url: string };
type Observation = { kind: "web_search"; status: "in_progress" | "completed" | "failed" | "incomplete"; action: Action };
export type SearchEvent = {
  type: "search_observation"; session_id: string; id: string;
  stage: "before" | "after"; observation: Observation;
};

// Projects the native WebSearch and WebFetch calls of the root thread as web search Items.
export class SearchObserver {
  private readonly calls = new Map<string, Observation>();

  *consume(message: SDKMessage, sessionID: string): Generator<SearchEvent, void> {
    if ((message.type !== "assistant" && message.type !== "user") || message.parent_tool_use_id !== null ||
        ("isSynthetic" in message && message.isSynthetic) || ("isReplay" in message && message.isReplay)) return;
    const content = message.message.content;
    if (!Array.isArray(content)) return;
    if (message.type === "assistant") {
      if (message.error) return;
      for (const block of content) {
        if (block.type !== "tool_use" || this.calls.has(block.id)) continue;
        const input = block.input as Record<string, unknown> | null;
        let action: Action | undefined;
        if (block.name === "WebSearch" && typeof input?.query === "string") action = { type: "search", query: input.query };
        else if (block.name === "WebFetch" && typeof input?.url === "string") action = { type: "open_page", url: input.url };
        if (!action) continue;
        const observation: Observation = { kind: "web_search", status: "in_progress", action };
        this.calls.set(block.id, observation);
        yield this.event(sessionID, block.id, "before", observation);
      }
    } else {
      for (const block of content) {
        if (block.type !== "tool_result") continue;
        const call = this.calls.get(block.tool_use_id);
        if (!call || call.status !== "in_progress") continue;
        call.status = block.is_error ? "failed" : "completed";
        yield this.event(sessionID, block.tool_use_id, "after", call);
      }
    }
  }

  assertComplete(): void {
    if ([...this.calls.values()].some(call => call.status === "in_progress")) throw new Error("unconfirmed search result");
  }

  close(sessionID: string): SearchEvent[] {
    const events: SearchEvent[] = [];
    for (const [id, call] of this.calls) {
      if (call.status !== "in_progress") continue;
      call.status = "incomplete";
      events.push(this.event(sessionID, id, "after", call));
    }
    return events;
  }

  private event(sessionID: string, id: string, stage: "before" | "after", observation: Observation): SearchEvent {
    return { type: "search_observation", session_id: sessionID, id, stage, observation: { ...observation } };
  }
}
