// The native harness owns discovery, schema loading and its model/provider policy.
// ENABLE_TOOL_SEARCH=true is the official mapping of tool_search.
export function toolSearchEnvironment(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  return { ...env, ENABLE_TOOL_SEARCH: "true" };
}
