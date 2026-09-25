import type { CoreHarnessKind, ModelProviderView } from "@agents-core-web/agents-client";

/** Harnesses by their product names, the same in every language. */
export const harnessNames: Record<CoreHarnessKind, string> = { claude_sdk: "Claude SDK", codex: "Codex", mcode: "MiniMax Code" };

/** Provider protocols by their product names, the same in every language. */
export const protocolNames: Record<ModelProviderView["protocol"], string> = { anthropic: "Anthropic Messages", responses: "OpenAI Responses" };
