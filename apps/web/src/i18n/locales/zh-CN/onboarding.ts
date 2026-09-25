export const onboarding = {
  steps: {
    label: "设置进度",
    account: "管理员",
    project: "项目和 key",
    tour: "认识控制台",
  },
  stage: {
    account: { title: "一个 Core，承载所有 Agent。", body: "连接你的机器，运行你的 Agent，在一个控制台里看清每一个 Session。" },
    login: { title: "欢迎回到你的 Core。", body: "你的 Agent、Session 和机器，都还在原处。" },
    project: { title: "项目承载所有工作。", body: "Agent、Session、Skills、文件和 Vault 都归属某个项目；应用用项目的 API key 访问它们。" },
    orbit: "Core 和它管理的一切：Agent、Session、Skills、Vault、文件、环境模板和机器",
  },
  preparing: "正在准备你的控制台…",
  terminal: "在终端里试一下",
  tour: {
    eyebrow: "认识控制台 · {{n}} / {{total}}",
    skip: "跳过",
    back: "上一步",
    next: "下一步",
    enter: "进入控制台",
    shot: "控制台的{{name}}页面",
    chapters: {
      monitor: {
        name: "监控",
        title: "服务健康吗？哪里在出错？",
        points: [
          "概览：服务状态、运行中的 Session、沙箱容量，以及需要你处理的 Session。",
          "Core、Agent、沙箱监控：执行队列、请求与错误、节点和运行时。",
          "Session 日志：每个 Session 的对话、追踪和 Turn，只读。",
        ],
      },
      resources: {
        name: "资源",
        title: "应用创建的一切，都在这里。",
        points: [
          "Agent、环境模板和 Skills，每一项都记录由谁创建。",
          "文件和 Vault；凭据只写入、不回显。",
          "查看、安全删除，或把资产复制到另一个项目。",
        ],
      },
      platform: {
        name: "平台",
        title: "部署本身。",
        points: [
          "项目与 key：创建项目，签发只显示一次的 key，撤销或归档。",
          "节点：一条命令接入你的机器，随时看容量。",
          "系统：Core 的构建版本和启动配置。",
        ],
      },
    },
  },
} as const;
