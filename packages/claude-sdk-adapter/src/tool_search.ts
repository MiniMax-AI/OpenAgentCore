// The native harness owns discovery, schema loading and its model/provider policy.
// Reject known conflicting modes, without copying its dynamic policy evaluator.
export function toolSearchEnvironment(env: NodeJS.ProcessEnv, model: string): NodeJS.ProcessEnv {
  const enabled = (value: string | undefined) => !!value && !["0", "false"].includes(value.toLowerCase());
  if (["CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
       "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"].some(key => enabled(env[key])) ||
      (env.ENABLE_TOOL_SEARCH && env.ENABLE_TOOL_SEARCH !== "true") ||
      ["claude-3-haiku", "claude-3-5-haiku"].some(name => model.toLowerCase().includes(name))) {
    throw new Error("unqualified native tool search configuration");
  }
  return { ...env, ENABLE_TOOL_SEARCH: "true" };
}
