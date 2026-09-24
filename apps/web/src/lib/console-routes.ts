/**
 * Console information architecture: every signed-in page, its hash route and
 * its navigation group. Monitor leads; Resources holds the objects callers use
 * by ID; Platform holds deployment-level settings; the Playground holds
 * debugging tools.
 */
export type ConsoleView =
  | "overview"
  | "agent-metrics"
  | "sandbox-metrics"
  | "sessions"
  | "templates"
  | "skills"
  | "files"
  | "vaults"
  | "nodes"
  | "api-keys"
  | "workbench"
  | "playground"
  | "builder";

export type ConsoleNavGroup = "monitor" | "resources" | "platform" | "playground";

export const consoleNavGroups: ReadonlyArray<{ id: ConsoleNavGroup; views: readonly ConsoleView[] }> = [
  { id: "monitor", views: ["overview", "agent-metrics", "sandbox-metrics", "sessions"] },
  { id: "resources", views: ["builder", "skills", "templates", "vaults", "files"] },
  { id: "platform", views: ["nodes", "api-keys"] },
  { id: "playground", views: ["workbench"] },
];

/**
 * Secondary pages reached from a navigation entry: routable, highlighted as
 * their parent, not listed in the sidebar.
 */
export const consoleSecondaryViews: Readonly<Partial<Record<ConsoleView, ConsoleView>>> = {
  // The Session console opens from a Session log row.
  playground: "sessions",
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
  // Platform facts moved to the Overview's deployment panel.
  system: "overview",
  // Agents are listed, created and edited in one Resources page.
  agents: "builder",
};

export interface ConsoleViewAvailability {
  templates: boolean;
}

export function consoleViewFromHash(hash: string, availability: ConsoleViewAvailability): ConsoleView {
  const candidate = hash.startsWith("#") ? hash.slice(1) : hash;
  const view = legacyHashes[candidate] ?? (routedViews.has(candidate) ? candidate as ConsoleView : "overview");
  if (view === "templates" && !availability.templates) return "overview";
  return view;
}

export function consoleHashForView(view: ConsoleView): string {
  return view === "overview" ? "" : `#${view}`;
}
