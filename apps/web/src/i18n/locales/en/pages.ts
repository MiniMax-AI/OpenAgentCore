export const pages = {
  agents: { title: "Agents", subtitle: "Saved Agents in each project, with their model, Harness, tools and usage." },
  sessions: { title: "Sessions", subtitle: "Conversations", recover: "Recover durable state" },
  templates: { title: "Environment templates", subtitle: "Reusable configuration for managed Sessions." },
  vaults: { title: "Vaults", subtitle: "Core-owned static bearer credentials for exact HTTPS MCP destinations.", refresh: "Refresh Vaults" },
} as const;
