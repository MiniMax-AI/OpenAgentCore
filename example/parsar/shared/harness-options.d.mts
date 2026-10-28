export type HarnessKind = "codex" | "claude_sdk" | "mcode";
export type ExecutionOptionField =
  | "reasoning_effort"
  | "reasoning_summary"
  | "web_search"
  | "verbosity"
  | "service_tier";
export const harnessOptions: Record<
  HarnessKind,
  { label: string; protocol: "responses" | "anthropic" } & Record<
    ExecutionOptionField,
    string[]
  >
>;
export const executionOptionFields: ExecutionOptionField[];
export function unsupportedOption(
  agent: { harness: string } & Partial<Record<ExecutionOptionField, string>>,
): ExecutionOptionField | "harness" | undefined;
