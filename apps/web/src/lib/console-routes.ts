/**
 * Console information architecture: every signed-in page, its hash route and
 * its navigation group. The console is a management tool over the Web API:
 * Monitor leads; Resources shows each API key's assets; Platform holds keys,
 * nodes and deployment configuration. There is no Agents API playground.
 */
export type ConsoleView =
  | "overview"
  | "agent-metrics"
  | "sandbox-metrics"
  | "sessions"
  | "session"
  | "agents"
  | "templates"
  | "skills"
  | "files"
  | "vaults"
  | "api-keys"
  | "nodes"
  | "system";

export type ConsoleNavGroup = "monitor" | "resources" | "platform";

export const consoleNavGroups: ReadonlyArray<{ id: ConsoleNavGroup; views: readonly ConsoleView[] }> = [
  { id: "monitor", views: ["overview", "agent-metrics", "sandbox-metrics", "sessions"] },
  { id: "resources", views: ["agents", "templates", "skills", "files", "vaults"] },
  { id: "platform", views: ["api-keys", "nodes", "system"] },
];

/**
 * Secondary pages reached from a navigation entry: routable, highlighted as
 * their parent, not listed in the sidebar.
 */
export const consoleSecondaryViews: Readonly<Partial<Record<ConsoleView, ConsoleView>>> = {
  // One Session's read-only history opens from a Session log row.
  session: "sessions",
};

export function consoleNavParent(view: ConsoleView): ConsoleView {
  return consoleSecondaryViews[view] ?? view;
}

const routedViews = new Set<string>([
  ...consoleNavGroups.flatMap((group) => group.views),
  ...Object.keys(consoleSecondaryViews),
]);

/** Earlier console releases used these hashes; keep bookmarks working. */
const legacyHashes: Readonly<Record<string, ConsoleView>> = {
  dashboard: "overview",
  sandbox: "nodes",
  builder: "agents",
  playground: "sessions",
  workbench: "overview",
};

export function consoleViewFromHash(hash: string): ConsoleView {
  const candidate = hash.startsWith("#") ? hash.slice(1).split("?")[0]! : hash;
  return legacyHashes[candidate] ?? (routedViews.has(candidate) ? candidate as ConsoleView : "overview");
}

export function consoleHashForView(view: ConsoleView): string {
  return view === "overview" ? "" : `#${view}`;
}
