import { common as enCommon } from "./locales/en/common";
import { navigation as enNavigation } from "./locales/en/navigation";
import { connection as enConnection } from "./locales/en/connection";
import { pages as enPages } from "./locales/en/pages";
import { agents as enAgents } from "./locales/en/agents";
import { templates as enTemplates } from "./locales/en/templates";
import { vaults as enVaults } from "./locales/en/vaults";
import { files as enFiles } from "./locales/en/files";
import { dashboard as enDashboard } from "./locales/en/dashboard";
import { app as enApp } from "./locales/en/app";
import { sessions as enSessions } from "./locales/en/sessions";
import { overview as enOverview } from "./locales/en/overview";
import { metrics as enMetrics } from "./locales/en/metrics";
import { skills as enSkills } from "./locales/en/skills";
import { keys as enKeys } from "./locales/en/keys";
import { system as enSystem } from "./locales/en/system";
import { common as zhCNCommon } from "./locales/zh-CN/common";
import { navigation as zhCNNavigation } from "./locales/zh-CN/navigation";
import { connection as zhCNConnection } from "./locales/zh-CN/connection";
import { pages as zhCNPages } from "./locales/zh-CN/pages";
import { agents as zhCNAgents } from "./locales/zh-CN/agents";
import { templates as zhCNTemplates } from "./locales/zh-CN/templates";
import { vaults as zhCNVaults } from "./locales/zh-CN/vaults";
import { files as zhCNFiles } from "./locales/zh-CN/files";
import { dashboard as zhCNDashboard } from "./locales/zh-CN/dashboard";
import { app as zhCNApp } from "./locales/zh-CN/app";
import { sessions as zhCNSessions } from "./locales/zh-CN/sessions";
import { overview as zhCNOverview } from "./locales/zh-CN/overview";
import { metrics as zhCNMetrics } from "./locales/zh-CN/metrics";
import { skills as zhCNSkills } from "./locales/zh-CN/skills";
import { keys as zhCNKeys } from "./locales/zh-CN/keys";
import { system as zhCNSystem } from "./locales/zh-CN/system";
import { consoleAuthChinese } from "../lib/console-auth-strings";
import { chinese as zhCNSandbox } from "../lib/locale-strings";

const enSandbox = Object.fromEntries(
  Object.keys(zhCNSandbox).map((key) => [key, key]),
) as { [K in keyof typeof zhCNSandbox]: K };

const zhCNFirstRun = {
  ...consoleAuthChinese,
} as const;

const enFirstRun = Object.fromEntries(
  Object.keys(zhCNFirstRun).map((key) => [key, key]),
) as { [K in keyof typeof zhCNFirstRun]: K };

export const defaultNamespace = "common";

export const resources = {
  en: {
    common: enCommon,
    navigation: enNavigation,
    connection: enConnection,
    pages: enPages,
    agents: enAgents,
    templates: enTemplates,
    vaults: enVaults,
    files: enFiles,
    dashboard: enDashboard,
    app: enApp,
    sessions: enSessions,
    overview: enOverview,
    metrics: enMetrics,
    skills: enSkills,
    keys: enKeys,
    system: enSystem,
    sandbox: enSandbox,
    firstRun: enFirstRun,
  },
  "zh-CN": {
    common: zhCNCommon,
    navigation: zhCNNavigation,
    connection: zhCNConnection,
    pages: zhCNPages,
    agents: zhCNAgents,
    templates: zhCNTemplates,
    vaults: zhCNVaults,
    files: zhCNFiles,
    dashboard: zhCNDashboard,
    app: zhCNApp,
    sessions: zhCNSessions,
    overview: zhCNOverview,
    metrics: zhCNMetrics,
    skills: zhCNSkills,
    keys: zhCNKeys,
    system: zhCNSystem,
    sandbox: zhCNSandbox,
    firstRun: zhCNFirstRun,
  },
} as const;
