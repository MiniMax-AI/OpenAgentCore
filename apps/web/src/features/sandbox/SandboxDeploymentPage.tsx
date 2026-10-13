import { useEffect, useRef, useState, type ReactNode } from "react";
import { deploymentContract, type InitializeSandboxDeployment, type UpdateSandboxDeployment, type SandboxDeployment, type StartSandboxReset } from "@oac/agents-client";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { TableSkeleton } from "../../components/Skeleton";
import { HelpTip, RefreshButton } from "../../components/console-ui";
import { ErrorDialog } from "../../components/ErrorDialog";
import { ErrorState } from "../../components/ErrorState";
import { InstallationNotice } from "../../components/InstallationNotice";
import { useFailureToast, useToast } from "../../components/Toast";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { sandboxConfigurationRejection, sandboxRequestError, sandboxWriteUncertain } from "../../lib/sandbox-labels";
import { sandboxAdmin } from "./sandbox-queries";
import { useSandboxPageState } from "./use-sandbox-page-state";
import { sandboxWriteOwnershipQuery } from "./sandbox-write-ownership";
import { writeSandboxDeployment } from "./sandbox-deployment-write";
import { SandboxSetupWizard } from "./SandboxSetupWizard";
import { SandboxDeploymentSettings } from "./SandboxDeploymentSettings";
import { SandboxResetControls } from "./SandboxResetControls";
import "./SandboxManagerView.css";
import { SettingsTabs } from "../settings/SettingsTabs";
import { SandboxCapacitySettings } from "./SandboxCapacitySettings";

function DeploymentHeader({ actions }: { actions?: ReactNode }) {
  const { t } = useTranslation("sandboxNavigation");
  const { t: tNav } = useTranslation("navigation");
  return <header className="page-header">
    <div className="console-page-heading">
      <h1>{tNav("settings.sandbox")}</h1><HelpTip>{t("help")}</HelpTip>
    </div>
    {actions ? <div className="page-actions">{actions}</div> : null}
  </header>;
}

/** Settings owns deployment changes, lifecycle policy and node capacity. */
export function SandboxDeploymentPage() {
  const { i18n } = useTranslation("sandbox");
  return <section className="page-section console-page sandbox-manager sandbox-manager-page" lang={i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en"}>
    <DeploymentConfiguration />
  </section>;
}

function DeploymentConfiguration() {
  const { t, i18n } = useTranslation("sandbox");
  const { t: tNavigation } = useTranslation("sandboxNavigation");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const { navigate } = useConsoleNavigation();
  const queryClient = useQueryClient();
  const { deploymentQuery, snapshot, installation, loading, busy, setupNeedsRefresh, confirmed, fresh, refresh, configurationKey } = useSandboxPageState();
  const error: unknown = deploymentQuery.error;
  const toast = useToast();
  const [writeFailure, setWriteFailure] = useState<{ error: unknown; open: boolean; editing: boolean } | null>(null);
  const lifetime = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController(); lifetime.current = controller;
    return () => { controller.abort(); lifetime.current = null; };
  }, []);
  const refreshByUser = () => {
    void refresh().then((result) => {
      if (!result.isError && queryClient.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase === "idle") setWriteFailure(null);
      if (result.isError && result.data) toast.show(t("Refresh failed; showing the last loaded state."), { tone: "error", detail: sandboxRequestError(result.error, locale), key: "sandbox-read" });
    });
  };
  useFailureToast(snapshot && deploymentQuery.isError ? sandboxRequestError(deploymentQuery.error, locale) : null, t("Refresh failed; showing the last loaded state."), "sandbox-read");

  // The shared mutation owner outlives this page. Leaving only suppresses local UI effects.
  async function changeDeployment(operation: (signal: AbortSignal) => Promise<SandboxDeployment>, fromWizard = false, editing = false): Promise<boolean> {
    const controller = lifetime.current;
    if (!controller || busy || loading || setupNeedsRefresh || !fresh) return false;
    setWriteFailure(null);
    try {
      const deployment = await writeSandboxDeployment(queryClient, operation);
      return deployment !== null && !controller.signal.aborted;
    } catch (error) {
      if (!controller.signal.aborted) {
        if (fromWizard && sandboxConfigurationRejection(error) !== null) throw error;
        if (sandboxWriteUncertain(error)) setWriteFailure({ error, open: true, editing });
        else toast.show(t("Core rejected the sandbox change"), { tone: "error", detail: sandboxRequestError(error, locale), key: "sandbox-write" });
      }
    }
    return false;
  }
  async function initialize(input: InitializeSandboxDeployment) {
    if (await changeDeployment((signal) => sandboxAdmin.initializeDeployment(input, { signal }), true) && deploymentContract.providers[input.provider].mode === "nodes" && !installation.data?.local_only) navigate("settings", { id: "sandbox" }, "add-node");
  }
  async function update(input: UpdateSandboxDeployment) {
    return changeDeployment((signal) => sandboxAdmin.updateDeployment({ ...input, expected_generation: snapshot!.deployment.generation }, { signal }), true, true);
  }
  const startReset = (input: StartSandboxReset) => changeDeployment((signal) => sandboxAdmin.startReset(input, { signal }));
  const cancelReset = (expectedGeneration: number) => changeDeployment((signal) => sandboxAdmin.cancelReset(expectedGeneration, { signal }));
  const unknownWrite = writeFailure?.open ? <div className="form-stack" role="alert">
    <p>{sandboxRequestError(writeFailure.error, locale)}</p>
    <p>{t("Refresh sandbox state to confirm whether the change was saved before submitting again.")}</p>
  </div> : null;
  const configured = snapshot?.deployment.provider || snapshot?.deployment.reset ? snapshot.deployment : null;
  const disabled = busy || loading || !fresh || setupNeedsRefresh;
  const reset = configured ? <SandboxResetControls key={`reset:${configurationKey}`} deployment={configured} disabled={disabled} stale={!confirmed} onStart={startReset} onCancel={cancelReset} /> : null;
  const sections = [
    configured ? <SandboxDeploymentSettings key={configurationKey} deployment={configured} fresh={confirmed} disabled={disabled} onUpdate={update} onRefresh={refreshByUser} refreshing={loading} pending={busy} writeFailure={writeFailure?.editing ? unknownWrite : null} /> : null,
    <SandboxCapacitySettings key="capacity" />,
  ];

  return <>
    <DeploymentHeader actions={<>
      {snapshot?.deployment.mode === "nodes" ? <button type="button" className="button outline" onClick={() => navigate("nodes")}>{tNavigation("nodes")}</button> : null}
      <RefreshButton onClick={refreshByUser} refreshing={loading} disabled={busy} label={t("Refresh sandbox state")} />
    </>} />
    <SettingsTabs active="sandbox" />
    <div className="console-page-body sandbox-content settings-body">
      <InstallationNotice installation={installation.data} />
      {!snapshot && (loading || deploymentQuery.isPending) ? <TableSkeleton label={t("Loading sandbox state…")} rows={3} columns={2} /> : null}
      {snapshot && deploymentQuery.isError ? <p role="alert" className="sandbox-error">{t("Refresh failed; showing the last loaded state.")}</p> : null}
      {setupNeedsRefresh ? <p role="alert" className="sandbox-error">{t("Refresh sandbox state to confirm whether the change was saved before submitting again.")}</p> : null}
      {busy ? <span role="status">{t("Saving sandbox change…")}</span> : null}
      {!snapshot && error !== null ? <ErrorState title={t("Sandbox state couldn't be read")} detail={sandboxRequestError(error, locale)} onRetry={refresh} /> : null}
      {snapshot && !configured ? <SandboxSetupWizard key={configurationKey} coreUrl={snapshot.deployment.core_url} expectedGeneration={snapshot.deployment.generation} disabled={busy || loading || setupNeedsRefresh || error !== null} onSubmit={initialize} /> : null}
      {configured?.reset ? [reset, ...sections] : [...sections, reset]}
    </div>
    <ErrorDialog action={{ label: t("Refresh sandbox state"), onClick: refreshByUser }} open={Boolean(writeFailure?.open && !writeFailure.editing)} title={t("Couldn't confirm the sandbox change")} onClose={() => setWriteFailure((failure) => failure && { ...failure, open: false })}>
      {unknownWrite}
    </ErrorDialog>
  </>;
}
