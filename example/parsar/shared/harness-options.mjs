import { harnessCatalog } from "./harness-catalog.mjs";

// Execution options each Harness admits, as Core's Harness declarations state
// them. An empty value means the Harness default and is never sent. Parsar
// connects a Provider with the Harness's first declared protocol.
export const harnessOptions = Object.fromEntries(
  Object.entries(harnessCatalog).map(([kind, declared]) => [kind, { ...declared, protocol: declared.protocols[0] }]),
);

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
