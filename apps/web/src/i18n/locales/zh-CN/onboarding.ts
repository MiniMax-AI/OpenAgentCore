export const onboarding = {
  stage: {
    login: { title: "一个 Core，承载所有 Agent。", body: "连接你的机器，运行你的 Agent，在一个控制台里看清每一个 Session。" },
    orbit: "Core 和它管理的一切：Agent、Session、Skills、Vault、文件、环境模板和机器",
  },
  tour: {
    label: "认识控制台",
    eyebrow: "认识控制台 · {{n}} / {{total}}",
    skip: "跳过",
    back: "上一步",
    next: "下一步",
    done: "返回控制台",
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
          "查看任意资产，或安全地删除。",
        ],
      },
      platform: {
        name: "平台",
        title: "部署本身。",
        points: [
          "项目与 key：创建项目，签发只显示一次的项目 API Key，撤销或归档。",
          "节点：一条命令接入你的机器，随时看容量。",
          "系统：所有项目共用的沙箱配置。",
        ],
      },
    },
  },
} as const;
