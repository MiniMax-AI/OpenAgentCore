import type { SDKAssistantMessageError, SDKMessage } from "@anthropic-ai/claude-agent-sdk";

const codes: Record<SDKAssistantMessageError, string | undefined> = {
  authentication_failed: "authentication_error", oauth_org_not_allowed: "authentication_error",
  account_on_hold: "authentication_error", verification_required: "authentication_error",
  cloud_credential_error: "authentication_error", billing_error: "usage_limit_exceeded",
  rate_limit: "rate_limit_exceeded", overloaded: "server_overloaded",
  invalid_request: "invalid_request", model_not_found: "resource_not_found", server_error: "server_error",
  unknown: undefined, max_output_tokens: undefined,
};

// Candidates are scoped to uncompleted, Core-submitted input UUIDs, not a query's
// last error text. A matching native error result alone commits a classification.
export class NativeFailure {
  private readonly candidates = new Map<string, string>();

  observe(message: SDKMessage, sessionID: string, pending: readonly string[]): string | undefined {
    if (!sessionID || message.session_id !== sessionID || !pending.length ||
        ("isReplay" in message && message.isReplay) || ("isSynthetic" in message && message.isSynthetic)) return;
    if (message.type === "assistant" && message.parent_tool_use_id === null) {
      const code = typeof message.error !== "string" || !Object.hasOwn(codes, message.error) ? undefined : codes[message.error];
      for (const id of pending) {
        this.candidates.delete(id);
        if (code) this.candidates.set(id, code);
      }
    } else if (message.type === "result") {
      const candidate = this.candidates.get(pending[0]!);
      const same = pending.every(id => this.candidates.get(id) === candidate);
      for (const id of pending) this.candidates.delete(id);
      if (same && message.subtype === "error_during_execution") return candidate;
      // Claude Code also reports a request that got no HTTP response (DNS, refused or
      // reset connection, timeout) as server_error; only a provider 5xx has a status.
      if (same && message.subtype === "success" && message.is_error)
        return candidate === "server_error" && typeof message.api_error_status !== "number" ? "connection_failed" : candidate;
    }
  }
}
