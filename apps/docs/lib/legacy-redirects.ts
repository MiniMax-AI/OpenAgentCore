/**
 * Slugs retired when the manual was rewritten around the administrator-API
 * baseline. Keys are the previous slugs without a locale prefix; values are the
 * surviving page that now covers the topic, or `/` for a topic that was folded
 * into an overview.
 *
 * `middleware.ts` answers these with a permanent redirect and carries the
 * reader's language across, so `/zh/api-keys` lands on
 * `/zh/bootstrap-projects-keys` instead of the English page.
 *
 * `legacy-redirects.test.ts` asserts every target is a page that still exists
 * in every language, and that no retired slug is still published — so this map
 * cannot silently rot as the manual changes.
 */
export const LEGACY_TARGETS: Record<string, string> = {
  // Folded into the overview page; no single successor page.
  "project-map": "/",
  "console-tour": "/",
  glossary: "/",
  "local-development": "/",
  "locales-themes": "/",

  // Renamed, or folded into a surviving page.
  "execution-flow": "/execution-model",
  "install-core": "/install",
  "configure-core": "/configure",
  "console-admin": "/admin-api",
  credentials: "/configure",
  "api-keys": "/bootstrap-projects-keys",
  connection: "/configure",
  verification: "/install",

  // Agent and Session topics.
  agents: "/agents-and-tools",
  "create-agent": "/agents-and-tools",
  "agent-tools": "/agents-and-tools",
  vaults: "/agents-and-tools",
  "start-session": "/sessions",
  conversation: "/sessions",

  // Operations topics.
  dashboard: "/observability",
  metrics: "/observability",
  trace: "/observability",
  system: "/hosted-providers",

  // Environment, file and provider topics.
  environment: "/environments-and-files",
  "environment-templates": "/environments-and-files",
  files: "/environments-and-files",
  "sandbox-nodes": "/hosted-providers",
  "hosted-sandbox-manager": "/hosted-providers",
  "connect-daemon": "/self-hosted-execution",
  "connect-executor": "/self-hosted-execution",

  // The public Agent API replaced three separate API pages.
  "agents-api": "/public-api",
  "agents-api-sessions": "/public-api",
  "agents-api-errors": "/public-api",
}

/**
 * Resolve a retired path to its successor. `rest` is the path with any locale
 * prefix already removed; surrounding slashes are ignored, and a live page or
 * the root resolves to `undefined` so the caller can carry on routing.
 */
export function legacyTarget(rest: string): string | undefined {
  const key = rest.replace(/^\/+/, "").replace(/\/+$/, "")
  return key ? LEGACY_TARGETS[key] : undefined
}