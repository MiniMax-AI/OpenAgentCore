import type { TokenUsage } from "@oac/agents-client";

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function safeNonNegativeInteger(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

/** A complete, safe token usage object, or null when any figure is missing or unsafe. */
export function canonicalUsage(value: unknown): TokenUsage | null {
  const usage = record(value);
  const inputDetails = record(usage?.input_tokens_details);
  const outputDetails = record(usage?.output_tokens_details);
  if (!usage || !inputDetails || !outputDetails) return null;
  if (
    safeNonNegativeInteger(usage.input_tokens) === null ||
    safeNonNegativeInteger(usage.output_tokens) === null ||
    safeNonNegativeInteger(usage.total_tokens) === null ||
    safeNonNegativeInteger(inputDetails.cached_tokens) === null ||
    safeNonNegativeInteger(outputDetails.reasoning_tokens) === null
  ) return null;
  return value as TokenUsage;
}

export function formatDashboardTokens(value: number | null, locale = "en-US"): string {
  if (value === null) return "Unavailable";
  return value.toLocaleString(locale);
}
