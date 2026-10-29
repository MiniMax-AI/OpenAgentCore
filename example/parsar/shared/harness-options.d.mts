import type { CoreHarnessKind, ModelProviderProtocol } from "@oac/agents-client";

export type HarnessKind = CoreHarnessKind;
export type ExecutionOptionField =
  | "reasoning_effort"
  | "reasoning_summary"
  | "web_search"
  | "verbosity"
  | "service_tier";
export const harnessOptions: Record<
  HarnessKind,
  {
    label: string;
    protocol: ModelProviderProtocol;
    protocols: ModelProviderProtocol[];
    message_images: boolean;
    mcp_http_required: boolean;
  } & Record<ExecutionOptionField, string[]>
>;
export const executionOptionFields: ExecutionOptionField[];
export function unsupportedOption(
  agent: { harness: string } & Partial<Record<ExecutionOptionField, string>>,
): ExecutionOptionField | "harness" | undefined;
