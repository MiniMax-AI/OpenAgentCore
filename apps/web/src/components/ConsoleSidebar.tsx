import {
  Activity,
  Code2,
  Cpu,
  FileText,
  KeyRound,
  Layers3,
  LayoutDashboard,
  ListTree,
  MessagesSquare,
  Puzzle,
  Server,
  Settings2,
  Sparkles,
  Vault,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { consoleNavGroups, type ConsoleView } from "../lib/console-routes";
import type { CoreConnectionState } from "../lib/connection";
import { AppearanceMenu } from "./AppearanceMenu";
import { StatusIcon } from "./StatusIcon";
import { ConsoleAccountMenu } from "../features/first-run/ConsoleAccess";

const viewIcons: Record<ConsoleView, LucideIcon> = {
  overview: LayoutDashboard,
  "agent-metrics": Activity,
  "sandbox-metrics": Cpu,
  sessions: ListTree,
  templates: Layers3,
  skills: Puzzle,
  files: FileText,
  vaults: Vault,
  nodes: Server,
  "api-keys": KeyRound,
  workbench: Code2,
  playground: MessagesSquare,
  builder: Wrench,
};

export function ConsoleSidebar({
  active,
  hidden,
  coreState,
  coreLabel,
  showIntroduction,
  onSelect,
  onIntroduction,
  onConfigureCore,
}: {
  active: ConsoleView | null;
  /** Views this console cannot offer, such as Vaults on a Core without them. */
  hidden: ReadonlySet<ConsoleView>;
  coreState: CoreConnectionState;
  coreLabel: string;
  /** The first-run introduction can be replayed from the Playground group. */
  showIntroduction: boolean;
  onSelect: (view: ConsoleView) => void;
  onIntroduction: () => void;
  onConfigureCore: () => void;
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
        {consoleNavGroups.map((group) => {
          const views = group.views.filter((view) => !hidden.has(view));
          if (!views.length && !(group.id === "playground" && showIntroduction)) return null;
          return (
            <div className="nav-group" key={group.id} role="group" aria-labelledby={`nav-group-${group.id}`}>
              <p className="nav-label" id={`nav-group-${group.id}`}>
                {t(`groups.${group.id}`)}
              </p>
              {views.map((view) => {
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
                  >
                    <Icon size={15} strokeWidth={1.5} aria-hidden="true" />
                    <span>{t(`views.${view}`)}</span>
                  </button>
                );
              })}
              {group.id === "playground" && showIntroduction ? (
                <button type="button" onClick={onIntroduction} aria-label={t("gettingStarted")} title={t("gettingStarted")}>
                  <Sparkles size={15} strokeWidth={1.5} aria-hidden="true" />
                  <span>{t("gettingStarted")}</span>
                </button>
              ) : null}
            </div>
          );
        })}
      </nav>

      <div className="sidebar-footer">
        <button className="core-switcher" type="button" onClick={onConfigureCore} aria-label={t("configureCore")}>
          <StatusIcon
            status={coreState === "ready" ? "completed" : coreState === "failed" ? "failed" : "running"}
            title={t("coreState", { state: coreState })}
          />
          <span>
            <strong>{t("coreApi")}</strong>
            <small>{coreState === "connecting" ? t("coreConnecting") : coreState === "ready" ? coreLabel : t("coreFailed")}</small>
          </span>
          <Settings2 size={14} strokeWidth={1.5} aria-hidden="true" />
        </button>
        <div className="sidebar-account">
          <ConsoleAccountMenu />
          <AppearanceMenu />
        </div>
      </div>
    </aside>
  );
}
