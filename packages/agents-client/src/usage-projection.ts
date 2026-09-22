import type { AgentSession } from "./types";
import { isRecord, exactFields, isNonnegativeInteger } from "./response-projection";

const tokenUsageFields = new Set([
  "input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details",
]);
const inputTokenDetailsFields = new Set(["cached_tokens"]);
const outputTokenDetailsFields = new Set(["reasoning_tokens"]);

export function projectTokenUsage(value: unknown, invalid: () => never): AgentSession["usage"] {
  if (value === null) return null;
  if (!isRecord(value) || !exactFields(value, tokenUsageFields)) return invalid();
  const inputDetails = value.input_tokens_details;
  const outputDetails = value.output_tokens_details;
  if (
    !isNonnegativeInteger(value.input_tokens) ||
    !isNonnegativeInteger(value.output_tokens) ||
    !isNonnegativeInteger(value.total_tokens) ||
    !isRecord(inputDetails) || !exactFields(inputDetails, inputTokenDetailsFields) ||
    !isNonnegativeInteger(inputDetails.cached_tokens) ||
    !isRecord(outputDetails) || !exactFields(outputDetails, outputTokenDetailsFields) ||
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
