export const pages = {
  dashboard: {
    agents: "Agent", sessions: "Session", runtime: "运行时", connectionSettings: "连接设置",
    backend: { label: "OpenAgentCore 后端未就绪。打开 Docker 启动指南", title: "OpenAgentCore 后端未就绪", detail: "Web 正在运行，但本地 /v1 代理无法连接已就绪的 Core{{failure}}。请启动 Docker 后端，然后测试连接。", openGuide: "打开启动指南", networkFailure: "网络故障", collectionFailed: "数据收集请求失败。" },
  },
  agents: { title: "Agent", subtitle: "各项目保存的 Agent，及其模型、执行框架、工具和用量。" },
  sessions: { title: "Session", subtitle: "对话", recover: "恢复持久状态" },
  templates: { title: "环境模板", subtitle: "用于托管 Session 的可复用配置。", newTemplate: "新建模板" },
  vaults: { title: "Vault", subtitle: "由 Core 管理、仅用于精确 HTTPS MCP 目标的静态 bearer Credential。", refresh: "刷新 Vault", create: "新建 Vault" },
} as const;
