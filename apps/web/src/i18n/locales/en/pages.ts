export const pages = {
  dashboard: {
    agents: "Agents", sessions: "Sessions", runtime: "Runtime", connectionSettings: "Connection settings",
    backend: { label: "OpenAgentCore backend is not ready. Open Docker startup guide", title: "OpenAgentCore backend is not ready", detail: "Web is running, but its local `/v1` proxy cannot reach a ready Core{{failure}}. Start the Docker backend, then test the connection.", openGuide: "Open startup guide", networkFailure: "network failure", collectionFailed: "Collection request failed." },
  },
  agents: { title: "Agents", subtitle: "Saved Agents in each project, with their model, harness, tools and usage." },
  sessions: { title: "Sessions", subtitle: "Conversations", recover: "Recover durable state" },
  templates: { title: "Environment Templates", subtitle: "Reusable configuration for managed Sessions.", newTemplate: "New Template" },
  vaults: { title: "Vaults", subtitle: "Core-owned static bearer credentials for exact HTTPS MCP destinations.", refresh: "Refresh Vaults", create: "New Vault" },
} as const;
