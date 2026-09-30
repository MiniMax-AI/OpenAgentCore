import type { ModelProviderView } from "@agents-core-web/agents-client";

/** Harnesses by their product names, the same in every language. */
export { coreHarnessNames as harnessNames } from "@agents-core-web/agents-client";

/** Provider protocols by their product names, the same in every language. */
export const protocolNames: Record<ModelProviderView["protocol"], string> = { anthropic: "Anthropic Messages", responses: "OpenAI Responses", chat_completions: "OpenAI Chat Completions" };
