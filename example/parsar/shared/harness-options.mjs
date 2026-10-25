// Execution options each Harness admits, mirroring Core's Harness declarations
// (contracts/agents-api/harness-capabilities.md). An empty value means the
// Harness default and is never sent.
export const harnessOptions = {
  codex: {
    label: "Codex",
    protocol: "responses",
    reasoning_effort: ["none", "minimal", "low", "medium", "high", "xhigh", "max"],
    reasoning_summary: ["concise", "detailed", "auto"],
    web_search: ["disabled", "cached", "live"],
    verbosity: ["low", "medium", "high"],
    service_tier: ["default", "flex", "priority"],
  },
  claude_sdk: {
    label: "Claude Code",
    protocol: "anthropic",
    reasoning_effort: ["low", "medium", "high", "xhigh", "max"],
    reasoning_summary: [],
    web_search: ["disabled", "live"],
    verbosity: ["medium"],
    service_tier: [],
  },
  mcode: {
    label: "MiniMax Code",
    protocol: "anthropic",
    reasoning_effort: ["none", "minimal", "low", "medium", "high", "xhigh", "max"],
    reasoning_summary: [],
    web_search: ["disabled"],
    verbosity: ["medium"],
    service_tier: [],
  },
};

export const executionOptionFields = [
  "reasoning_effort",
  "reasoning_summary",
  "web_search",
  "verbosity",
  "service_tier",
];

// Returns the first option the Agent's Harness does not admit.
export function unsupportedOption(agent) {
  const declared = harnessOptions[agent.harness];
  if (!declared) return "harness";
  return executionOptionFields.find(
    (field) => agent[field] && !declared[field].includes(agent[field]),
  );
}
