import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { harnessesQuery } from "../system/harness-queries";
import { PageBody, PageHeader, RefreshButton } from "../../components/console-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { SandboxDeploymentPage } from "../sandbox/SandboxDeploymentPage";
import { DefaultModelsSection } from "../system/DefaultModelsSection";
import { SystemPage } from "../system/SystemPage";
import { SettingsTabs } from "./SettingsTabs";
import { WorkspaceStorageSettings } from "./WorkspaceStorageSettings";

export function SettingsPage() {
  const { params } = useConsoleNavigation();
  const { t } = useTranslation("navigation");
  const { t: tSystem } = useTranslation("system");
  const models = useQuery({ ...harnessesQuery, enabled: params.id === "models" });
  if (params.id === "service") return <SystemPage />;
  if (params.id !== "models" && params.id !== "storage") return <SandboxDeploymentPage />;
  const section = params.id;
  return <section className="page-section console-page system-page">
    <PageHeader headingId="settings-heading" title={t(`settings.${section}`)} actions={section === "models" ? <RefreshButton refreshing={models.isFetching} onClick={() => void models.refetch()} label={tSystem("refresh")} /> : undefined} />
    <SettingsTabs active={section} />
    <PageBody>{section === "models" ? <DefaultModelsSection /> : <WorkspaceStorageSettings />}</PageBody>
  </section>;
}
