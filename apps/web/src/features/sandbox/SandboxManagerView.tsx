import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import type { InitializeSandboxDeployment, SandboxDeployment, SandboxNode } from "@agents-core-web/agents-client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Pencil, Plus, Server, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, RefreshButton } from "../../components/console-ui";
import { ErrorDialog } from "../../components/ErrorDialog";
import { ErrorState } from "../../components/ErrorState";
import { useFailureToast, useToast } from "../../components/Toast";
import { useConsoleIntent, useConsoleNavigation } from "../../lib/console-navigation";
import { installationQuery } from "../../lib/installation";
import { sandboxConfigurationRejection, sandboxRequestError } from "../../lib/sandbox-labels";
import type { SandboxConsoleConfig } from "./console-config";
import { sandboxAdmin, sandboxConsoleConfigQuery, sandboxDeploymentQuery, sandboxScope, sandboxSnapshotQuery, type SandboxSnapshot } from "./sandbox-queries";
import { SandboxSetupWizard } from "./SandboxSetupWizard";
import { SandboxDeploymentSettings } from "./SandboxDeploymentSettings";
import { NodeEnrollment } from "./NodeEnrollment";
import { NodeList } from "./NodeList";
import { NodeDetail } from "./NodeDetail";
import { NodeEditDialog } from "./NodeEditDialog";
import "./SandboxManagerView.css";

/** Nodes: the deployment provider, the node list and one node's detail (`#nodes?id=…`). */
export function SandboxManagerView() {
  const { i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  return <section className="page-section console-page sandbox-manager sandbox-manager-page" lang={locale}>
    <SandboxAccess />
  </section>;
}

/** The page header; an E2B deployment has no machines, so the page is its sandbox backend. */
function NodesPageHeader({ title, count, back, actions, cloud = false }: { title?: ReactNode; count?: number; back?: () => void; actions?: ReactNode; cloud?: boolean }) {
  const { t } = useTranslation("sandbox");
  return <header className="page-header">
    <div className="console-page-heading">
      {back ? <button type="button" className="icon-button ghost back-button" aria-label={t("Back")} title={t("Back")} onClick={back}><ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" /></button> : null}
      <h1>{title ?? t(cloud ? "Sandbox backend" : "Nodes")}</h1>
      {count === undefined ? null : <span className="heading-count">{count}</span>}
      {back ? null : <HelpTip>{t(cloud ? "E2B runs this deployment's sandboxes in its cloud. There are no machines to add." : "Your hosts for running sandboxes.")}</HelpTip>}
    </div>
    {actions ? <div className="page-actions">{actions}</div> : null}
  </header>;
}

function SandboxAccess() {
  const { t } = useTranslation("sandbox");
  const { data: config, isPending: checking, isFetching, isError, refetch } = useQuery(sandboxConsoleConfigQuery);
  if (isError && config === undefined) return <><NodesPageHeader /><div className="console-page-body"><p role="alert">{t("The console configuration could not be read. Refresh to try again.")}</p><button type="button" className="button outline" disabled={isFetching} onClick={() => { void refetch(); }}>{t("Refresh sandbox state")}</button></div></>;
  if (checking) return <><NodesPageHeader /><div className="console-page-body"><p role="status">{t("Connecting to this console's Core…")}</p></div></>;
  if (!config?.sandbox_admin) return <><NodesPageHeader /><div className="console-page-body"><p role="alert">{t("Sandbox administration is not configured on this console. Ask the deployment administrator to configure access.")}</p><button type="button" className="button outline" disabled={isFetching} onClick={() => { void refetch(); }}>{t("Refresh sandbox state")}</button></div></>;
  return <SandboxManager consoleConfig={config} />;
}

function SandboxManager({ consoleConfig }: { consoleConfig: SandboxConsoleConfig }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const { params, navigate, back: goBack } = useConsoleNavigation();
  const client = sandboxAdmin;
  const queryClient = useQueryClient();
  const query = useQuery(sandboxSnapshotQuery);
  const snapshot: SandboxSnapshot | null = query.data ?? null;
  const loading = query.isFetching;
  // As before a reload clears the last error, a running read hides it.
  const error: unknown = query.isFetching ? null : query.error;
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const [removeTarget, setRemoveTarget] = useState<SandboxNode | null>(null);
  const [editTarget, setEditTarget] = useState<SandboxNode | null>(null);
  // The Add node dialog; it stays mounted with the page so its command survives closing.
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  // When a deployment write last had an uncertain outcome; only a read begun after it confirms the state again.
  const [uncertainSince, setUncertainSince] = useState<number | null>(null);
  const setupNeedsRefresh = uncertainSince !== null && !(snapshot && snapshot.readAt > uncertainSince);
  // The state on screen is Core's last successful read, with no uncertain write since.
  const confirmed = snapshot !== null && !query.isError && !setupNeedsRefresh;
  // Writes additionally wait for any read in flight.
  const fresh = confirmed && !loading;
  // A failed write opens a dialog with Core's reason; the error stays for the closing animation.
  const [writeFailure, setWriteFailure] = useState<{ error: unknown; open: boolean } | null>(null);
  const { refetch } = query;
  const refresh = useCallback(() => {
    // Each refresh starts a new read (cancelling one in flight) and resets the forms, as a reload did.
    setRevision((value) => value + 1);
    // The wizard reads the address and config file from the installation.
    void queryClient.invalidateQueries({ queryKey: installationQuery.queryKey });
    // Settles when the read does; the enrollment dialog waits for it before calling a command expired.
    return refetch();
  }, [queryClient, refetch]);
  const toast = useToast();
  // A refresh the administrator asks for reports its failure even while an earlier one is still unconfirmed;
  // the enrollment dialog's own repeated refreshes do not.
  const refreshByUser = () => {
    setRevision((value) => value + 1);
    void queryClient.invalidateQueries({ queryKey: installationQuery.queryKey });
    void refetch().then((result) => {
      if (result.isError && result.data) toast.show(t("Refresh failed; showing the last loaded state."), { tone: "error", detail: sandboxRequestError(result.error, locale), key: "sandbox-read" });
    });
  };
  const lifetime = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController(); lifetime.current = controller;
    return () => { controller.abort(); lifetime.current = null; };
  }, []);
  /** Whether Core confirmed the change. `fromWizard`: a configuration Core rejects is thrown back to the wizard that sent it. */
  async function changeDeployment(operation: (signal: AbortSignal) => Promise<SandboxDeployment>, fromWizard = false): Promise<boolean> {
    const controller = lifetime.current;
    if (!controller || busy || loading || setupNeedsRefresh || !fresh) return false;
    setBusy(true); setWriteFailure(null);
    try {
      const deployment = await operation(controller.signal);
      if (!controller.signal.aborted) {
        // Core's confirmed response replaces the deployment; nodes of an older generation no longer apply.
        queryClient.setQueryData(sandboxSnapshotQuery.queryKey, (current): SandboxSnapshot => {
          const same = current !== undefined && deployment.generation === current.deployment.generation;
          return { deployment, nodes: same ? current.nodes : [], allocations: same ? current.allocations : [], readAt: current?.readAt ?? performance.now() };
        });
        // A later visit reads the whole snapshot again; other pages re-read the deployment now.
        void queryClient.invalidateQueries({ queryKey: sandboxSnapshotQuery.queryKey, refetchType: "none" });
        void queryClient.invalidateQueries({ queryKey: sandboxDeploymentQuery.queryKey });
        // Overview and Sandbox metrics read the fleet separately and lay out by provider.
        void queryClient.invalidateQueries({ queryKey: ["sandbox-fleet"] });
        return true;
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        // Core rejected the configuration and saved nothing: the wizard explains why.
        if (fromWizard && sandboxConfigurationRejection(error) !== null) throw error;
        setUncertainSince(performance.now()); setWriteFailure({ error, open: true });
        // Nothing re-reads on its own: the operator refreshes to confirm. A later visit reads again.
        void queryClient.invalidateQueries({ queryKey: sandboxScope, refetchType: "none" });
      }
    } finally { if (!controller.signal.aborted) setBusy(false); }
    return false;
  }
  /** First setup; own machines continue straight to adding the first node. */
  async function initialize(input: InitializeSandboxDeployment) {
    if (await changeDeployment((signal) => client.initializeDeployment(input, { signal }), true) && input.provider !== "e2b") setAdding(true);
  }
  async function update(input: InitializeSandboxDeployment) {
    await changeDeployment((signal) => client.updateDeployment({ ...input, expected_generation: snapshot!.deployment.generation }, { signal }), true);
  }
  async function maintenance(maintenance: boolean) {
    await changeDeployment((signal) => client.setMaintenance({ maintenance, expected_generation: snapshot!.deployment.generation }, { signal }));
  }
  const writeDialog = <ErrorDialog
    open={writeFailure?.open ?? false}
    title={t("Couldn't confirm the sandbox change")}
    action={{ label: t("Refresh sandbox state"), onClick: refreshByUser }}
    onClose={() => setWriteFailure((failure) => failure && { ...failure, open: false })}
  >
    <p>{writeFailure ? sandboxRequestError(writeFailure.error, locale) : null}</p>
    <p>{t("Refresh sandbox state to confirm whether the change was saved before submitting again.")}</p>
  </ErrorDialog>;
  const askRemove = (node: SandboxNode) => { setRemoveError(null); setRemoveTarget(node); };
  async function remove() {
    const controller = lifetime.current;
    const target = removeTarget;
    if (!controller || !target || removing) return;
    setRemoving(true); setRemoveError(null);
    try {
      const result = await client.removeNode(target.id, { signal: controller.signal });
      if (!result.deleted || result.id !== target.id) throw new Error("removal_unconfirmed");
      if (!controller.signal.aborted) {
        setRemoveTarget(null);
        if (params.id === target.id) navigate("nodes");
        refresh();
      }
    } catch (error) {
      // Keep the dialog open with Core's reason; the refreshed list shows what actually happened.
      if (!controller.signal.aborted) { setRemoveError(sandboxRequestError(error, locale)); refresh(); }
    } finally { if (!controller.signal.aborted) setRemoving(false); }
  }

  const nodes = snapshot?.nodes ?? [];
  const allocations = snapshot?.allocations ?? [];
  const hostedNodes = Boolean(snapshot?.deployment.provider && snapshot.deployment.provider !== "e2b");
  // Getting started asks for Add node on arrival. A request the first settled read cannot
  // serve (no own-machines deployment, maintenance, a failed read) is dropped, so the
  // dialog never opens later on its own.
  const addNodeReadiness = loading || busy ? "wait"
    : !snapshot ? (query.isError ? "unavailable" : "wait")
    : hostedNodes && fresh && !snapshot.deployment.maintenance ? "ready" : "unavailable";
  useConsoleIntent("add-node", addNodeReadiness, () => setAdding(true));
  // A node enrolled with another address than Core's current one (config.json's public_url) gets no new sandboxes until it is added again.
  const staleNodes = hostedNodes && snapshot ? nodes.filter((node) => node.core_url !== snapshot.deployment.core_url).map((node) => node.name || node.id) : [];
  const selected = params.id ? nodes.find((node) => node.id === params.id) : undefined;
  const refreshButton = <RefreshButton onClick={refreshByUser} refreshing={loading} disabled={busy || removing} label={t("Refresh sandbox state")} />;
  const readFailure = error !== null ? sandboxRequestError(error, locale) : null;
  // A failed refresh keeps the last state on screen and says so in a toast, once while the failure lasts.
  useFailureToast(snapshot && query.isError ? sandboxRequestError(query.error, locale) : null, t("Refresh failed; showing the last loaded state."), "sandbox-read");
  const status = <>
    {!snapshot && (loading || query.isPending) ? <p role="status">{t("Loading sandbox state…")}</p> : null}
    {busy ? <span role="status">{t("Saving sandbox change…")}</span> : null}
    {!snapshot && readFailure ? <ErrorState title={t("Sandbox state couldn't be read")} detail={readFailure} onRetry={refresh} /> : null}
  </>;
  const removeName = removeTarget ? removeTarget.name || removeTarget.id : "";
  const dialog = <ConfirmDialog
    open={removeTarget !== null}
    title={t("Remove node")}
    confirmLabel={t("Confirm removal")}
    busyLabel={t("Removing…")}
    busy={removing}
    error={removeError}
    onConfirm={() => void remove()}
    onClose={() => { if (!removing) { setRemoveTarget(null); setRemoveError(null); } }}
  >
    <p>{t("{{name}} will be removed from this deployment.", { name: removeName })}</p>
    <p>{t("Core rejects removal while allocations or retained resources remain.")}</p>
  </ConfirmDialog>;
  // Rendered first in both the list and a node's page, so an open command outlives the navigation.
  const enrollment = hostedNodes && snapshot ? <NodeEnrollment key={snapshot.deployment.generation} client={client} consoleConfig={consoleConfig} deployment={snapshot.deployment} nodes={snapshot.nodes} open={adding} fresh={confirmed} onClose={() => setAdding(false)} onRefresh={refresh} /> : null;

  if (params.id && hostedNodes) {
    const back = () => goBack("nodes");
    return <>
      {enrollment}
      <NodesPageHeader
        back={back}
        title={selected ? selected.name || selected.id : params.id}
        actions={<>
          {refreshButton}
          {selected ? <button type="button" className="button outline" disabled={busy || removing} onClick={() => setEditTarget(selected)}><Pencil size={14} aria-hidden="true" />{t("Edit node")}</button> : null}
          {selected ? <button type="button" className="button danger" disabled={busy || removing} onClick={() => askRemove(selected)}><Trash2 size={14} aria-hidden="true" />{t("Remove node")}</button> : null}
        </>}
      />
      <div className="console-page-body sandbox-content">
        {status}
        {selected ? <NodeDetail node={selected} allocations={allocations} stale={!confirmed} suspension={snapshot?.deployment.suspension ?? null} /> : snapshot && !loading ? (
          <EmptyState icon={Server} title={t("Node not found")} hint={t("This node is not registered. It may have been removed.")} action={<button type="button" className="button outline" onClick={back}>{t("Back")}</button>} />
        ) : null}
      </div>
      {dialog}
      {writeDialog}
      <NodeEditDialog
        key={editTarget?.id ?? "closed"}
        client={client}
        node={editTarget}
        onClose={() => setEditTarget(null)}
        onSaved={() => {
          const saved = editTarget;
          setEditTarget(null);
          toast.show(t("Node saved"), { tone: "success" });
          refresh();
          // Other pages read the fleet and the node's detail separately.
          void queryClient.invalidateQueries({ queryKey: ["sandbox-fleet"] });
          if (saved) void queryClient.invalidateQueries({ queryKey: ["sandbox-node", saved.id] });
        }}
      />
    </>;
  }

  const actions = <>
    {refreshButton}
    {hostedNodes && snapshot ? <button type="button" className="button primary" disabled={busy || loading || !fresh || snapshot.deployment.maintenance} onClick={() => setAdding(true)}><Plus size={16} />{t("Add node")}</button> : null}
  </>;
  return <>
    {enrollment}
    <NodesPageHeader count={hostedNodes ? nodes.length : undefined} actions={actions} cloud={snapshot?.deployment.provider === "e2b"} />
    <div className="console-page-body sandbox-content">
      {status}
      {snapshot && !snapshot.deployment.provider ? <SandboxSetupWizard key={revision} coreUrl={snapshot.deployment.core_url} disabled={busy || loading || setupNeedsRefresh || error !== null} onSubmit={initialize} /> : null}
      {snapshot?.deployment.provider ? <>
        {snapshot.deployment.maintenance ? <p className="sandbox-maintenance" role="status">{t("Maintenance is enabled. New sandbox placement is paused.")}</p> : null}
        {staleNodes.length ? <p className="sandbox-maintenance sandbox-address-warning" role="status">{staleNodes.length === 1
          ? t("{{name}} is still bound to an old Core address. Remove it and add it again.", { name: staleNodes[0] })
          : t("{{count}} nodes are still bound to an old Core address: {{names}}. Remove them and add them again.", { count: staleNodes.length, names: new Intl.ListFormat(i18n.resolvedLanguage, { type: "conjunction" }).format(staleNodes) })}</p> : null}
        <SandboxDeploymentSettings key={`${snapshot.deployment.generation}:${snapshot.deployment.maintenance}:${revision}`} deployment={snapshot.deployment} fresh={confirmed} disabled={busy || loading || !fresh || setupNeedsRefresh} onMaintenance={maintenance} onUpdate={update} onRefresh={refresh} />
        {hostedNodes ? <section aria-label={t("Sandbox nodes")}>
          {nodes.length
            ? <NodeList nodes={nodes} allocations={allocations} stale={!confirmed} disabled={busy || loading || removing} suspends={snapshot.deployment.provider === "microsandbox"} onOpen={(node) => navigate("nodes", { id: node.id })} onRemove={askRemove} />
            : <EmptyState icon={Server} title={t("Add your first node")} hint={t("No nodes registered. Add a node to provide hosted capacity.")} />}
        </section> : null}
      </> : null}
    </div>
    {dialog}
    {writeDialog}
  </>;
}
