export const pages = {
  dashboard: {
    agents: "Agents", sessions: "Sessions", runtime: "Runtime", connectionSettings: "Connection settings",
    backend: { label: "Agent Core backend is not ready. Open Docker startup guide", title: "Agent Core backend is not ready", detail: "Web is running, but its local `/v1` proxy cannot reach a ready Core{{failure}}. Start the Docker backend, then test the connection.", openGuide: "Open startup guide", networkFailure: "network failure", collectionFailed: "Collection request failed." },
  },
  agents: { title: "Agents", subtitle: "Saved Agents in this project. Callers create Sessions with an Agent ID; here you can create, edit and try them." },
  sessions: { title: "Sessions", subtitle: "Conversations", recover: "Recover durable state" },
  templates: { title: "Environment Templates", subtitle: "Reusable configuration for managed Sessions.", newTemplate: "New Template" },
  vaults: { title: "Vaults", subtitle: "Core-owned static bearer credentials for exact HTTPS MCP destinations.", refresh: "Refresh Vaults", create: "New Vault" },
  sandbox: { title: "Hosted Sandbox Manager", subtitle: "Deployment provider, runtime nodes and Session allocations.", disconnect: "Disconnect admin" },
} as const;
