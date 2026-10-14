export const pages = {
  agents: { title: "Agent", subtitle: "各项目保存的 Agent，及其模型、Harness、工具和用量。" },
  sessions: { title: "Session", subtitle: "对话", recover: "恢复持久状态" },
  templates: { title: "Environment 模板", subtitle: "用于托管 Session 的可复用配置。" },
  vaults: { title: "Vault", subtitle: "由 Core 管理、仅用于精确 HTTPS MCP 目标的静态 bearer Credential。", refresh: "刷新 Vault" },
} as const;
