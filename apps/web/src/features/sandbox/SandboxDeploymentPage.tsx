import { useEffect, useRef, useState, type ReactNode } from "react";
import type { InitializeSandboxDeployment, UpdateSandboxDeployment, SandboxDeployment, StartSandboxReset } from "@oac/agents-client";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
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
import { SandboxPageAccess } from "./SandboxPageAccess";
import { useSandboxPageState } from "./use-sandbox-page-state";
import { sandboxWriteOwnershipQuery } from "./sandbox-write-ownership";
import { writeSandboxDeployment } from "./sandbox-deployment-write";
import { SandboxSetupWizard } from "./SandboxSetupWizard";
import { SandboxDeploymentSettings } from "./SandboxDeploymentSettings";
import "./SandboxManagerView.css";

function DeploymentHeader({ actions }: { actions?: ReactNode }) {
  const { t } = useTranslation("sandboxNavigation");
  const { navigate } = useConsoleNavigation();
  return <header className="page-header">
    <div className="console-page-heading">
      <button type="button" className="icon-button ghost back-button" aria-label={t("back")} title={t("back")} onClick={() => navigate("system")}><ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" /></button>
      <h1>{t("title")}</h1><HelpTip>{t("help")}</HelpTip>
    </div>
    {actions ? <div className="page-actions">{actions}</div> : null}
  </header>;
}

/** System's secondary page is the sole owner of deployment configuration controls. */
export function SandboxDeploymentPage() {
  const { i18n } = useTranslation("sandbox");
  return <section className="page-section console-page sandbox-manager sandbox-manager-page" lang={i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en"}>
    <SandboxPageAccess header={<DeploymentHeader />}>{() => <DeploymentConfiguration />}</SandboxPageAccess>
  </section>;
}

function DeploymentConfiguration() {
  const { t, i18n } = useTranslation("sandbox");
  const { t: tNavigation } = useTranslation("sandboxNavigation");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
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
    if (await changeDeployment((signal) => sandboxAdmin.initializeDeployment(input, { signal }), true) && input.provider !== "e2b" && !installation.data?.local_only) navigate("nodes", {}, "add-node");
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

  return <>
    <DeploymentHeader actions={<>
      {snapshot?.deployment.provider && snapshot.deployment.provider !== "e2b" ? <button type="button" className="button outline" onClick={() => navigate("nodes")}>{tNavigation("nodes")}</button> : null}
      <RefreshButton onClick={refreshByUser} refreshing={loading} disabled={busy} label={t("Refresh sandbox state")} />
    </>} />
    <div className="console-page-body sandbox-content">
      <InstallationNotice installation={installation.data} />
      {!snapshot && (loading || deploymentQuery.isPending) ? <TableSkeleton label={t("Loading sandbox state…")} rows={3} columns={2} /> : null}
      {snapshot && deploymentQuery.isError ? <p role="alert" className="sandbox-error">{t("Refresh failed; showing the last loaded state.")}</p> : null}
      {setupNeedsRefresh ? <p role="alert" className="sandbox-error">{t("Refresh sandbox state to confirm whether the change was saved before submitting again.")}</p> : null}
      {busy ? <span role="status">{t("Saving sandbox change…")}</span> : null}
      {!snapshot && error !== null ? <ErrorState title={t("Sandbox state couldn't be read")} detail={sandboxRequestError(error, locale)} onRetry={refresh} /> : null}
      {snapshot && !snapshot.deployment.provider && !snapshot.deployment.reset ? <SandboxSetupWizard key={configurationKey} coreUrl={snapshot.deployment.core_url} expectedGeneration={snapshot.deployment.generation} disabled={busy || loading || setupNeedsRefresh || error !== null} onSubmit={initialize} /> : null}
      {snapshot?.deployment.provider || snapshot?.deployment.reset ? <SandboxDeploymentSettings key={configurationKey} deployment={snapshot.deployment} fresh={confirmed} disabled={busy || loading || !fresh || setupNeedsRefresh} onReset={startReset} onCancelReset={cancelReset} onUpdate={update} onRefresh={refreshByUser} refreshing={loading} pending={busy} writeFailure={writeFailure?.editing ? unknownWrite : null} /> : null}
    </div>
    <ErrorDialog action={{ label: t("Refresh sandbox state"), onClick: refreshByUser }} open={Boolean(writeFailure?.open && !writeFailure.editing)} title={t("Couldn't confirm the sandbox change")} onClose={() => setWriteFailure((failure) => failure && { ...failure, open: false })}>
      {unknownWrite}
    </ErrorDialog>
  </>;
}
