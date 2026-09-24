import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConsoleSidebar } from "./components/ConsoleSidebar";
import { FirstKeySetup } from "./features/api-keys/FirstKeySetup";
import { ApiKeysPage } from "./features/api-keys/ApiKeysPage";
import { AgentsPage } from "./features/agents/AgentsPage";
import { TemplatesPage } from "./features/environment-templates/TemplatesPage";
import { FilesPage } from "./features/files/FilesPage";
import { AgentMetricsPage } from "./features/metrics/AgentMetricsPage";
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
import { KeySpacesProvider, useKeySpaces } from "./lib/key-spaces";

/** Sandbox administration stays on the paired console's `/core/v1/sandbox` routes. */
const SANDBOX_BASE = "/v1";

function readLocation(): { view: ConsoleView; params: RouteParams } {
  const hash = typeof window === "undefined" ? "" : window.location.hash;
  return { view: consoleViewFromHash(hash), params: routeParamsFromHash(hash) };
}

function ConsolePage({ view }: { view: ConsoleView }) {
  switch (view) {
    case "overview": return <OverviewPage />;
    case "agent-metrics": return <AgentMetricsPage />;
    case "sandbox-metrics": return <SandboxMetricsPage />;
    case "sessions": return <SessionLogPage />;
    case "session": return <SessionPage />;
    case "agents": return <AgentsPage />;
    case "templates": return <TemplatesPage />;
    case "skills": return <SkillsPage />;
    case "files": return <FilesPage />;
    case "vaults": return <VaultsPage />;
    case "api-keys": return <ApiKeysPage />;
    case "nodes": return <SandboxManagerView coreBaseUrl={SANDBOX_BASE} />;
    case "system": return <SystemPage />;
  }
}

function ConsoleShell() {
  const { t } = useTranslation("navigation");
  const { state } = useKeySpaces();
  const [location, setLocation] = useState(readLocation);
  const [setupDone, setSetupDone] = useState(false);

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

  // First run: an administrator who has no API key yet creates the first one.
  if (state.status === "ready" && state.spaces.length === 0 && !setupDone) {
    return <FirstKeySetup onDone={() => setSetupDone(true)} />;
  }

  return (
    <ConsoleNavigationContext.Provider value={navigation}>
      <div className="app-shell">
        <a className="skip-link" href="#main-content">{t("skipToContent")}</a>
        <ConsoleSidebar active={consoleNavParent(location.view)} onSelect={(view) => navigate(view)} />
        <main className="app-main" id="main-content" tabIndex={-1}>
          <div className="page-transition" key={`${location.view}:${location.params.space ?? ""}:${location.params.id ?? ""}`}>
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
    <KeySpacesProvider>
      <ConsoleShell />
    </KeySpacesProvider>
  );
}
