import {
  Activity,
  Bot,
  Cpu,
  FileText,
  FolderKanban,
  Layers3,
  LayoutDashboard,
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
}: {
  active: ConsoleView | null;
  onSelect: (view: ConsoleView) => void;
  /** Hover or focus on an item: read its page's data before the click. */
  onIntent?: (view: ConsoleView) => void;
}) {
  const { t } = useTranslation("navigation");
  return (
    <aside className="app-sidebar">
      <div className="brand-lockup">
        <span className="brand-mark-frame">
          <img className="brand-mark brand-mark-light" src="/parsar-mark-light.png" width="18" height="18" alt="" aria-hidden="true" />
          <img className="brand-mark brand-mark-dark" src="/parsar-mark-dark.png" width="18" height="18" alt="" aria-hidden="true" />
        </span>
        <span className="brand-name">Parsar Core</span>
        <span className="brand-product">{t("console")}</span>
      </div>

      <nav className="main-nav console-nav" aria-label={t("mainNavigation")}>
        {consoleNavGroups.map((group) => (
          <div className="nav-group" key={group.id} role="group" aria-labelledby={`nav-group-${group.id}`}>
            <p className="nav-label" id={`nav-group-${group.id}`}>
              {t(`groups.${group.id}`)}
            </p>
            {group.views.map((view) => {
              const Icon = viewIcons[view];
              return (
                <button
                  type="button"
                  key={view}
                  className={active === view ? "active" : undefined}
                  aria-current={active === view ? "page" : undefined}
                  aria-label={t(`views.${view}`)}
                  title={t(`views.${view}`)}
                  onClick={() => onSelect(view)}
                  onPointerEnter={() => onIntent?.(view)}
                  onFocus={() => onIntent?.(view)}
                >
                  {active === view ? <m.span className="nav-active-chip" layoutId="console-nav-active" aria-hidden="true" /> : null}
                  <Icon size={15} strokeWidth={1.5} aria-hidden="true" />
                  <span>{t(`views.${view}`)}</span>
                </button>
              );
            })}
          </div>
        ))}
      </nav>

      <div className="sidebar-footer">
        <div className="sidebar-account">
          <ConsoleAccountMenu />
          <AppearanceMenu />
        </div>
      </div>
    </aside>
  );
}
