import type { system as english } from "../en/system";
type TranslationShape<T> = { [K in keyof T]: T[K] extends string ? string : TranslationShape<T[K]> };
export const system: TranslationShape<typeof english> = {
  help: "所有项目共用的沙箱配置：沙箱在哪里运行、每个多大、运行什么。修改请到“节点”页面。",
  refresh: "刷新系统配置",
  loading: "正在加载…",
  sandbox: {
    title: "沙箱",
    deploymentFailed: "无法加载沙箱部署。",
    unconfigured: "还没有设置沙箱",
    setUp: "去“节点”页面设置",
    runsOn: "运行在",
    e2b: "E2B 云端",
    ownMachines: "自有机器 · {{provider}}",
    each: "每个沙箱",
    size: "{{count}} 核 · {{memory}}", size_one: "{{count}} 核 · {{memory}}", size_other: "{{count}} 核 · {{memory}}",
    disks: "根盘 {{root}} · 数据盘 {{data}}（/environment）",
    runtime: "Runtime",
    runtimeHelp: "每个节点运行的 Runtime 版本；节点只接受这一个。",
    e2bTemplate: "E2B 模板",
    coreOrigin: "Core 地址",
    coreOriginHelp: "节点和沙箱访问 Core 使用的地址。",
    maintenance: "维护模式",
    maintenanceHelp: "开启时不再分配新沙箱。",
  },
  values: {
    on: "开启",
    off: "关闭",
  },
};
