export type HarnessKind = "codex" | "claude_sdk" | "mcode";
export type ProfileOptionField =
  | "reasoning_effort"
  | "reasoning_summary"
  | "web_search"
  | "verbosity"
  | "service_tier";
export const harnessOptions: Record<
  HarnessKind,
  { label: string; protocol: "responses" | "anthropic" } & Record<
    ProfileOptionField,
    string[]
  >
>;
export const profileOptionFields: ProfileOptionField[];
export function unsupportedOption(
  profile: { harness: string } & Partial<Record<ProfileOptionField, string>>,
): ProfileOptionField | "harness" | undefined;
