import { sandboxNavigation as enSandboxNavigation } from "./locales/en/sandbox-navigation";
import { sandboxNavigation as zhSandboxNavigation } from "./locales/zh-CN/sandbox-navigation";
import { diagnostics as enDiagnostics } from "./locales/en/diagnostics";
import { diagnostics as zhDiagnostics } from "./locales/zh-CN/diagnostics";
import { common as enCommon } from "./locales/en/common";
import { navigation as enNavigation } from "./locales/en/navigation";
import { pages as enPages } from "./locales/en/pages";
import { agents as enAgents } from "./locales/en/agents";
import { templates as enTemplates } from "./locales/en/templates";
import { vaults as enVaults } from "./locales/en/vaults";
import { files as enFiles } from "./locales/en/files";
import { dashboard as enDashboard } from "./locales/en/dashboard";
import { sessions as enSessions } from "./locales/en/sessions";
import { overview as enOverview } from "./locales/en/overview";
import { metrics as enMetrics } from "./locales/en/metrics";
import { skills as enSkills } from "./locales/en/skills";
import { keys as enKeys } from "./locales/en/keys";
import { system as enSystem } from "./locales/en/system";
import { onboarding as enOnboarding } from "./locales/en/onboarding";
import { common as zhCNCommon } from "./locales/zh-CN/common";
import { navigation as zhCNNavigation } from "./locales/zh-CN/navigation";
import { pages as zhCNPages } from "./locales/zh-CN/pages";
import { agents as zhCNAgents } from "./locales/zh-CN/agents";
import { templates as zhCNTemplates } from "./locales/zh-CN/templates";
import { vaults as zhCNVaults } from "./locales/zh-CN/vaults";
import { files as zhCNFiles } from "./locales/zh-CN/files";
import { dashboard as zhCNDashboard } from "./locales/zh-CN/dashboard";
import { sessions as zhCNSessions } from "./locales/zh-CN/sessions";
import { overview as zhCNOverview } from "./locales/zh-CN/overview";
import { metrics as zhCNMetrics } from "./locales/zh-CN/metrics";
import { skills as zhCNSkills } from "./locales/zh-CN/skills";
import { keys as zhCNKeys } from "./locales/zh-CN/keys";
import { system as zhCNSystem } from "./locales/zh-CN/system";
import { onboarding as zhCNOnboarding } from "./locales/zh-CN/onboarding";
import { firstRun as zhCNFirstRun } from "./locales/zh-CN/first-run";
import { sandbox as zhCNSandbox } from "./locales/zh-CN/sandbox";

const enSandbox = Object.fromEntries(
  Object.keys(zhCNSandbox).map((key) => [key, key]),
) as { [K in keyof typeof zhCNSandbox]: K };

const enFirstRun = Object.fromEntries(
  Object.keys(zhCNFirstRun).map((key) => [key, key]),
) as { [K in keyof typeof zhCNFirstRun]: K };

export const defaultNamespace = "common";

export const resources = {
  en: {
    common: enCommon,
    navigation: enNavigation,
    pages: enPages,
    agents: enAgents,
    templates: enTemplates,
    vaults: enVaults,
    files: enFiles,
    dashboard: enDashboard,
    sessions: enSessions,
    diagnostics: enDiagnostics,
    overview: enOverview,
    metrics: enMetrics,
    skills: enSkills,
    keys: enKeys,
    system: enSystem,
    sandbox: enSandbox,
    sandboxNavigation: enSandboxNavigation,
    firstRun: enFirstRun,
    onboarding: enOnboarding,
  },
  "zh-CN": {
    common: zhCNCommon,
    navigation: zhCNNavigation,
    pages: zhCNPages,
    agents: zhCNAgents,
    templates: zhCNTemplates,
    vaults: zhCNVaults,
    files: zhCNFiles,
    dashboard: zhCNDashboard,
    sessions: zhCNSessions,
    diagnostics: zhDiagnostics,
    overview: zhCNOverview,
    metrics: zhCNMetrics,
    skills: zhCNSkills,
    keys: zhCNKeys,
    system: zhCNSystem,
    sandbox: zhCNSandbox,
    sandboxNavigation: zhSandboxNavigation,
    firstRun: zhCNFirstRun,
    onboarding: zhCNOnboarding,
  },
} as const;
