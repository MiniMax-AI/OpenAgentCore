export const pages = {
  dashboard: {
    agents: "Agent", sessions: "Session", runtime: "运行时", connectionSettings: "连接设置",
    backend: { label: "Agent Core 后端未就绪。打开 Docker 启动指南", title: "Agent Core 后端未就绪", detail: "Web 正在运行，但本地 /v1 代理无法连接已就绪的 Core{{failure}}。请启动 Docker 后端，然后测试连接。", openGuide: "打开启动指南", networkFailure: "网络故障", collectionFailed: "数据收集请求失败。" },
  },
  agents: { title: "Agent", subtitle: "这个项目里保存的 Agent。调用方可以用 Agent ID 创建 Session；在这里可以新建、编辑和试运行。" },
  sessions: { title: "Session", subtitle: "对话", recover: "恢复持久状态" },
  templates: { title: "环境模板", subtitle: "用于托管 Session 的可复用配置。", newTemplate: "新建模板" },
  vaults: { title: "Vault", subtitle: "由 Core 管理、仅用于精确 HTTPS MCP 目标的静态 bearer Credential。", refresh: "刷新 Vault", create: "新建 Vault" },
  sandbox: { title: "托管沙箱管理", subtitle: "管理部署 provider、运行时节点和 Session 分配。", disconnect: "断开管理员连接" },
} as const;
