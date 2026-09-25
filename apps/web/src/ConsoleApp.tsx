import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { AppearanceMenu } from "./components/AppearanceMenu";
import { ConsoleSidebar } from "./components/ConsoleSidebar";
import { FirstProjectSetup } from "./features/api-keys/FirstProjectSetup";
import { ProjectsPage } from "./features/api-keys/ProjectsPage";
import { AgentsPage } from "./features/agents/AgentsPage";
import { ConsoleAccountMenu, useConsoleAccount } from "./features/first-run/ConsoleAccess";
import { OnboardingLayout } from "./features/onboarding/OnboardingLayout";
import { TemplatesPage } from "./features/environment-templates/TemplatesPage";
import { FilesPage } from "./features/files/FilesPage";
import { AgentMetricsPage } from "./features/metrics/AgentMetricsPage";
import { CoreMetricsPage } from "./features/metrics/CoreMetricsPage";
import { SandboxMetricsPage } from "./features/metrics/SandboxMetricsPage";
import { OverviewPage } from "./features/overview/OverviewPage";
import { SandboxManagerView } from "./features/sandbox/SandboxManagerView";
import { SessionLogPage } from "./features/sessions/SessionLogPage";
import { SessionPage } from "./features/sessions/SessionPage";
import { SkillsPage } from "./features/skills/SkillsPage";
import { SystemPage } from "./features/system/SystemPage";
import { VaultsPage } from "./features/vaults/VaultsPage";
import { ConsoleNavigationContext, hashWithParams, routeParamsFromHash, type RouteParams } from "./lib/console-navigation";
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

function readLocation(): { view: ConsoleView; params: RouteParams } {
  const hash = typeof window === "undefined" ? "" : window.location.hash;
  return { view: consoleViewFromHash(hash), params: routeParamsFromHash(hash) };
}

function ConsolePage({ view }: { view: ConsoleView }) {
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
    case "system": return <SystemPage />;
  }
}

function ConsoleShell() {
  const { t } = useTranslation("navigation");
  const { state } = useProjects();
  const account = useConsoleAccount();
  const [location, setLocation] = useState(readLocation);
  const [setupDone, setSetupDone] = useState(false);
  // Once first-run setup has started it stays until it finishes: a background
  // re-read of the projects (which now include the new one) must not replace it
  // while the first key is on screen.
  const needsSetup = state.status === "ready" && state.projects.length === 0;
  const [setupStarted, setSetupStarted] = useState(false);
  useEffect(() => { if (needsSetup) setSetupStarted(true); }, [needsSetup]);

  useEffect(() => {
    const sync = () => setLocation(readLocation());
    window.addEventListener("hashchange", sync);
    window.addEventListener("popstate", sync);
    return () => {
      window.removeEventListener("hashchange", sync);
      window.removeEventListener("popstate", sync);
    };
  }, []);

  const navigate = useCallback((view: ConsoleView, params: RouteParams = {}) => {
    const hash = hashWithParams(consoleHashForView(view), params);
    if (window.location.hash !== hash) {
      window.history.pushState(null, "", hash || window.location.pathname + window.location.search);
    }
    setLocation({ view, params });
    document.getElementById("main-content")?.focus({ preventScroll: true });
  }, []);

  const navigation = useMemo(() => ({ ...location, navigate }), [location, navigate]);

  const prefetch = useCallback((view: ConsoleView) => {
    const spec = prefetchable[view];
    if (!spec) return;
    for (const project of state.projects) void queryClient.prefetchQuery(collectionQuery(spec, project.id));
  }, [state.projects]);

  // First run: an administrator with no project yet creates the first one and its key.
  // Just after the administrator account was created, the projects are still
  // loading: keep the onboarding stage rather than flashing the console.
  if (account?.fresh && !setupDone && !setupStarted && state.status === "loading" && state.projects.length === 0) {
    return (
      <OnboardingLayout scene="project" step="project" controls={<><ConsoleAccountMenu /><AppearanceMenu /></>}>
        <p className="onboarding-preparing" role="status">{t("preparing", { ns: "onboarding" })}</p>
      </OnboardingLayout>
    );
  }

  if ((needsSetup || setupStarted) && !setupDone) {
    return <FirstProjectSetup onDone={() => setSetupDone(true)} />;
  }

  return (
    <ConsoleNavigationContext.Provider value={navigation}>
      <div className="app-shell">
        <a className="skip-link" href="#main-content">{t("skipToContent")}</a>
        <ConsoleSidebar active={consoleNavParent(location.view)} onSelect={(view) => navigate(view)} onIntent={prefetch} />
        <main className="app-main" id="main-content" tabIndex={-1}>
          <div className="page-transition" key={`${location.view}:${location.params.project ?? ""}:${location.params.id ?? ""}`}>
            <ConsolePage view={location.view} />
          </div>
        </main>
      </div>
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
