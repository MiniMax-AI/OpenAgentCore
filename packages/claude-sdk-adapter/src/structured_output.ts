import type { SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import type { MessageEvent } from "./messages.js";

// StructuredOutput is the native terminal tool. Its acknowledged tool-use identity
// owns the final JSON message; the parent assistant may already own ordinary prose.
export class StructuredOutput {
  private readonly calls = new Set<string>();
  private accepted?: string;

  consume(message: SDKMessage, session: string): void {
    if (!session || message.session_id !== session ||
        !("parent_tool_use_id" in message) || message.parent_tool_use_id !== null ||
        ("isReplay" in message && message.isReplay) || ("isSynthetic" in message && message.isSynthetic)) return;
    if (message.type === "assistant") {
      for (const block of message.message.content) {
        if (block.type !== "tool_use" || block.name !== "StructuredOutput") continue;
        if (!block.id || this.calls.has(block.id)) throw new Error("invalid structured output identity");
        this.calls.add(block.id);
      }
    } else if (message.type === "user" && Array.isArray(message.message.content)) {
      for (const block of message.message.content) {
        if (block.type !== "tool_result" || !this.calls.delete(block.tool_use_id)) continue;
        if (!block.is_error) this.accepted = block.tool_use_id;
      }
    }
  }

  complete(message: SDKMessage): MessageEvent {
    if (message.type !== "result" || message.subtype !== "success" || message.is_error ||
        message.structured_output === undefined || !this.accepted || this.calls.size ||
        typeof message.result !== "string") throw new Error("unconfirmed structured output");
    // Preserve the SDK's final string. Reserializing structured_output can round
    // JSON numbers and would replace the native validated result.
    JSON.parse(message.result);
    const id = this.accepted;
    this.accepted = undefined;
    return { type: "output_message", message: { id, status: "completed", phase: "final_answer", text: message.result } };
  }
}
