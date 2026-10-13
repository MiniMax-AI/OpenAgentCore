import type { AgentSession } from "./types";
import { isRecord, exactFields, isNonnegativeInteger } from "./response-projection";
import { inputTokensDetailsResourceFields, outputTokensDetailsResourceFields, tokenUsageResourceFields } from "./generated/public-api";

export function projectTokenUsage(value: unknown, invalid: () => never): AgentSession["usage"] {
  if (value === null) return null;
  if (!isRecord(value) || !exactFields(value, tokenUsageResourceFields)) return invalid();
  const inputDetails = value.input_tokens_details;
  const outputDetails = value.output_tokens_details;
  if (
    !isNonnegativeInteger(value.input_tokens) ||
    !isNonnegativeInteger(value.output_tokens) ||
    !isNonnegativeInteger(value.total_tokens) ||
    !isRecord(inputDetails) || !exactFields(inputDetails, inputTokensDetailsResourceFields) ||
    !isNonnegativeInteger(inputDetails.cached_tokens) ||
    !isRecord(outputDetails) || !exactFields(outputDetails, outputTokensDetailsResourceFields) ||
    !isNonnegativeInteger(outputDetails.reasoning_tokens)
  ) return invalid();
  return {
    input_tokens: value.input_tokens,
    output_tokens: value.output_tokens,
    total_tokens: value.total_tokens,
    input_tokens_details: { cached_tokens: inputDetails.cached_tokens },
    output_tokens_details: { reasoning_tokens: outputDetails.reasoning_tokens },
  };
}
