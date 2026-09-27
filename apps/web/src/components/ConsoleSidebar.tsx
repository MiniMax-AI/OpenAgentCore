import {
  Activity,
  Bot,
  Cloud,
  Cpu,
  FileText,
  FolderKanban,
  Layers3,
  LayoutDashboard,
  ListChecks,
  ListTree,
  Network,
  Puzzle,
  Server,
  Settings2,
  Vault,
  type LucideIcon,
} from "lucide-react";
import * as m from "motion/react-m";
import { useTranslation } from "react-i18next";

import { consoleNavGroups, type ConsoleView } from "../lib/console-routes";
import { useSandboxProvider } from "../features/sandbox/sandbox-queries";
import { AppearanceMenu } from "./AppearanceMenu";
import { ConsoleAccountMenu } from "../features/first-run/ConsoleAccess";

const viewIcons: Record<ConsoleView, LucideIcon> = {
  overview: LayoutDashboard,
  "core-metrics": Network,
  "agent-metrics": Activity,
  "sandbox-metrics": Cpu,
  sessions: ListTree,
  session: ListTree,
  agents: Bot,
  templates: Layers3,
  skills: Puzzle,
  files: FileText,
  vaults: Vault,
  projects: FolderKanban,
  nodes: Server,
  system: Settings2,
};

export function ConsoleSidebar({
  active,
  onSelect,
  onIntent,
  onGettingStarted,
}: {
  active: ConsoleView | null;
  onSelect: (view: ConsoleView) => void;
  /** Hover or focus on an item: read its page's data before the click. */
  onIntent?: (view: ConsoleView) => void;
  /** Opens the Overview's Getting started, even after it was hidden. */
  onGettingStarted: () => void;
}) {
  const { t } = useTranslation("navigation");
  const provider = useSandboxProvider();
  return (
    <aside className="app-sidebar">
      <div className="brand-lockup">
        <span className="brand-mark-frame">
          <img className="brand-mark brand-mark-light" src="/oac-mark-light.png" width="18" height="18" alt="" aria-hidden="true" />
          <img className="brand-mark brand-mark-dark" src="/oac-mark-dark.png" width="18" height="18" alt="" aria-hidden="true" />
        </span>
        <span className="brand-name">OpenAgentCore</span>
        <span className="brand-product">{t("console")}</span>
      </div>

      <nav className="main-nav console-nav" aria-label={t("mainNavigation")}>
        {consoleNavGroups.map((group) => (
          <div className="nav-group" key={group.id} role="group" aria-labelledby={`nav-group-${group.id}`}>
            <p className="nav-label" id={`nav-group-${group.id}`}>
              {t(`groups.${group.id}`)}
            </p>
            {group.views.map((view) => {
              // An E2B deployment has no machines: its Nodes entry is the sandbox backend.
              const cloud = view === "nodes" && provider === "e2b";
              const Icon = cloud ? Cloud : viewIcons[view];
              const label = cloud ? t("views.sandboxBackend") : t(`views.${view}`);
              return (
                <button
                  type="button"
                  key={view}
                  className={active === view ? "active" : undefined}
                  aria-current={active === view ? "page" : undefined}
                  aria-label={label}
                  title={label}
                  onClick={() => onSelect(view)}
                  onPointerEnter={() => onIntent?.(view)}
                  onFocus={() => onIntent?.(view)}
                >
                  {active === view ? <m.span className="nav-active-chip" layoutId="console-nav-active" aria-hidden="true" /> : null}
                  <Icon size={15} strokeWidth={1.5} aria-hidden="true" />
                  <span>{label}</span>
                </button>
              );
            })}
          </div>
        ))}
      </nav>

      <div className="sidebar-footer">
        <div className="main-nav sidebar-help">
          <button type="button" aria-label={t("showGettingStarted")} title={t("showGettingStarted")} onClick={onGettingStarted}>
            <ListChecks size={15} strokeWidth={1.5} aria-hidden="true" />
            <span>{t("showGettingStarted")}</span>
          </button>
        </div>
        <div className="sidebar-account">
          <ConsoleAccountMenu />
          <AppearanceMenu />
        </div>
      </div>
    </aside>
  );
}
