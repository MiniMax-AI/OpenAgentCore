import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConsoleSidebar } from "./components/ConsoleSidebar";
import { ProjectsPage } from "./features/api-keys/ProjectsPage";
import { AgentsPage } from "./features/agents/AgentsPage";
import { TemplatesPage } from "./features/environment-templates/TemplatesPage";
import { FilesPage } from "./features/files/FilesPage";
import { AgentMetricsPage } from "./features/metrics/AgentMetricsPage";
import { CoreMetricsPage } from "./features/metrics/CoreMetricsPage";
import { SandboxMetricsPage } from "./features/metrics/SandboxMetricsPage";
import { ConsoleTourContext, ConsoleTourScreen } from "./features/onboarding/ConsoleTour";
import { withTransition } from "./features/onboarding/view-transition";
import { OverviewPage } from "./features/overview/OverviewPage";
import { SandboxDeploymentPage } from "./features/sandbox/SandboxDeploymentPage";
import { SandboxManagerView } from "./features/sandbox/SandboxManagerView";
import { SessionLogPage } from "./features/sessions/SessionLogPage";
import { SessionPage } from "./features/sessions/SessionPage";
import { SkillsPage } from "./features/skills/SkillsPage";
import { DomainPage } from "./features/system/DomainPage";
import { SystemPage } from "./features/system/SystemPage";
import { VaultsPage } from "./features/vaults/VaultsPage";
import { consoleDepth, ConsoleNavigationContext, useConsoleNavigation, hashWithParams, routeParamsFromHash, type ConsoleIntent, type RouteParams } from "./lib/console-navigation";
import { consoleHashForView, consoleNavParent, consoleViewFromHash, type ConsoleView } from "./lib/console-routes";
import { ProjectsProvider, useProjects } from "./lib/projects";
import { collectionQuery, collections, filesCollection, queryClient, type CollectionSpec } from "./lib/queries";
import { filesPageSize } from "./features/files/file-operations";

/** The collection each resource page lists, read ahead when its nav item is hovered. */
const prefetchable: Partial<Record<ConsoleView, CollectionSpec<unknown>>> = {
  agents: collections.agents,
  templates: collections.templates,
  skills: collections.skills,
  files: filesCollection("desc", filesPageSize),
  vaults: collections.vaults,
  sessions: collections.sessions,
};

function readLocation(): { view: ConsoleView; params: RouteParams; intent: ConsoleIntent | null } {
  const hash = typeof window === "undefined" ? "" : window.location.hash;
  return { view: consoleViewFromHash(hash), params: routeParamsFromHash(hash), intent: null };
}

function ConsolePage({ view }: { view: ConsoleView }) {
  const { params } = useConsoleNavigation();
  switch (view) {
    case "overview": return <OverviewPage />;
    case "core-metrics": return <CoreMetricsPage />;
    case "agent-metrics": return <AgentMetricsPage />;
    case "sandbox-metrics": return <SandboxMetricsPage />;
    case "sessions": return <SessionLogPage />;
    case "session": return <SessionPage />;
    case "agents": return <AgentsPage />;
    case "templates": return <TemplatesPage />;
    case "skills": return <SkillsPage />;
    case "files": return <FilesPage />;
    case "vaults": return <VaultsPage />;
    case "projects": return <ProjectsPage />;
    case "nodes": return <SandboxManagerView />;
    case "system": return params.id === "sandbox" ? <SandboxDeploymentPage /> : params.id === "domain" ? <DomainPage /> : <SystemPage />;
  }
}

function ConsoleShell() {
  const { t } = useTranslation("navigation");
  const { state } = useProjects();
  const [location, setLocation] = useState(readLocation);
  // The console tour takes the place of the shell until it ends; then the
  // control that opened it, or else the page, takes the focus back.
  const [touring, setTouring] = useState(false);
  const tourEnded = useRef(false);
  const openTour = useCallback((from: HTMLElement | null) => withTransition("enter", () => setTouring(true), from), []);
  const endTour = useCallback((from: HTMLElement | null) => withTransition("enter", () => {
    tourEnded.current = true;
    setTouring(false);
  }, from), []);
  useEffect(() => {
    if (touring || !tourEnded.current) return;
    tourEnded.current = false;
    (document.querySelector<HTMLElement>("[data-tour-opener]") ?? document.getElementById("main-content"))?.focus();
  }, [touring]);

  useEffect(() => {
    const sync = () => setLocation(readLocation());
    window.addEventListener("hashchange", sync);
    window.addEventListener("popstate", sync);
    return () => {
      window.removeEventListener("hashchange", sync);
      window.removeEventListener("popstate", sync);
    };
  }, []);

  const navigate = useCallback((view: ConsoleView, params: RouteParams = {}, intent: ConsoleIntent | null = null) => {
    const hash = hashWithParams(consoleHashForView(view), params);
    if (window.location.hash !== hash) {
      // Each entry records how many console pages lie behind it, so `back` knows it can return.
      window.history.pushState({ consoleDepth: consoleDepth() + 1 }, "", hash || window.location.pathname + window.location.search);
    }
    setLocation({ view, params, intent });
    document.getElementById("main-content")?.focus({ preventScroll: true });
  }, []);
  const clearIntent = useCallback(() => setLocation((current) => (current.intent ? { ...current, intent: null } : current)), []);

  const back = useCallback((view: ConsoleView, params: RouteParams = {}) => {
    if (consoleDepth() > 0) window.history.back();
    else navigate(view, params);
  }, [navigate]);

  const navigation = useMemo(() => ({ ...location, navigate, back, clearIntent }), [location, navigate, back, clearIntent]);

  const prefetch = useCallback((view: ConsoleView) => {
    const spec = prefetchable[view];
    if (!spec) return;
    for (const project of state.projects) void queryClient.prefetchQuery(collectionQuery(spec, project.id));
  }, [state.projects]);

  if (touring) return <ConsoleTourScreen onDone={endTour} />;

  return (
    <ConsoleNavigationContext.Provider value={navigation}>
      <ConsoleTourContext.Provider value={openTour}>
        <div className="app-shell">
          <a className="skip-link" href="#main-content">{t("skipToContent")}</a>
          <ConsoleSidebar active={consoleNavParent(location.view)} onSelect={(view) => navigate(view)} onIntent={prefetch} onGettingStarted={() => navigate("overview", {}, "getting-started")} />
          <main className="app-main" id="main-content" tabIndex={-1}>
            <div className="page-transition" key={`${location.view}:${location.params.project ?? ""}:${location.params.id ?? ""}`}>
              <ConsolePage view={location.view} />
            </div>
          </main>
        </div>
      </ConsoleTourContext.Provider>
    </ConsoleNavigationContext.Provider>
  );
}

/** The signed-in management console. It calls only the Web API, never `/v1`. */
export function ConsoleApp() {
  return (
    <ProjectsProvider>
      <ConsoleShell />
    </ProjectsProvider>
  );
}
