import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { type SandboxNode } from "@oac/agents-client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Plus, Server, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, HelpTip, RefreshButton } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { useFailureToast, useToast } from "../../components/Toast";
import { useConsoleIntent, useConsoleNavigation } from "../../lib/console-navigation";
import { InstallationNotice } from "../../components/InstallationNotice";
import { installationQuery } from "../../lib/installation";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import type { SandboxConsoleConfig } from "./console-config";
import { sandboxAdmin, sandboxConsoleConfigQuery } from "./sandbox-queries";
import { useSandboxPageState } from "./use-sandbox-page-state";
import { NodeList, onOldAddress } from "./NodeList";
import { NodeDetail } from "./NodeDetail";
import { NodeEnrollment } from "./NodeEnrollment";
import { NodeEditDialog } from "./NodeEditDialog";
import { sandboxSize } from "./deployment-specification";
import { NodeCleanupDialog, type NodeCleanup } from "./NodeCleanupDialog";
import "./SandboxManagerView.css";

/** Nodes owns the node list, each node's capacity and host figures, Add node, Edit node and removal; deployment configuration lives in Settings. */
export function SandboxManagerView() {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const { data: config, isError, isFetching, refetch } = useQuery(sandboxConsoleConfigQuery);
  return <section className="page-section console-page sandbox-manager sandbox-manager-page" lang={locale}>
    {config ? <SandboxManager consoleConfig={config} /> : <>
      <NodesPageHeader />
      <div className="console-page-body">{isError
        ? <><p role="alert">{t("The console configuration could not be read. Refresh to try again.")}</p><button type="button" className="button outline" disabled={isFetching} onClick={() => { void refetch(); }}>{t("Refresh sandbox state")}</button></>
        : <p role="status">{t("Connecting to this console's Core…")}</p>}</div>
    </>}
  </section>;
}

/** Node list and node detail share one header. */
function NodesPageHeader({ title, count, back, actions, headingRef }: { title?: ReactNode; count?: number; back?: () => void; actions?: ReactNode; headingRef?: RefObject<HTMLHeadingElement | null> }) {
  const { t } = useTranslation("sandbox");
  return <header className="page-header">
    <div className="console-page-heading">
      {back ? <button type="button" className="icon-button ghost back-button" aria-label={t("Back")} title={t("Back")} onClick={back}><ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" /></button> : null}
      <h1 ref={headingRef} tabIndex={headingRef ? -1 : undefined}>{title ?? t("Nodes")}</h1>
      {count === undefined ? null : <span className="heading-count">{count}</span>}
      {back ? null : <HelpTip>{t("Your hosts for running sandboxes.")}</HelpTip>}
    </div>
    {actions ? <div className="page-actions">{actions}</div> : null}
  </header>;
}

function SandboxManager({ consoleConfig }: { consoleConfig: SandboxConsoleConfig }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const { params, navigate, back: goBack } = useConsoleNavigation();
  const client = sandboxAdmin;
  const queryClient = useQueryClient();
  const { deploymentQuery, query, compatible, inventoryOlder, inventoryLoading, snapshot, installation, loading, busy, setupNeedsRefresh, confirmed, fresh, refetch, refresh } = useSandboxPageState({ poll: true });
  const { t: tSandboxNav } = useTranslation("sandboxNavigation");
  const localOnly = installation.data?.local_only === true;
  const { t: tCommon } = useTranslation("common");
  const error: unknown = deploymentQuery.error;
  const [removeTarget, setRemoveTarget] = useState<SandboxNode | null>(null);
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  // After a removal, the host's uninstall command; it stays for the closing animation.
  const [cleanup, setCleanup] = useState<{ node: NodeCleanup; open: boolean } | null>(null);
  // Where focus goes once a dialog about a node that is gone closes.
  const heading = useRef<HTMLHeadingElement>(null);
  const toast = useToast();
  // A user-requested refresh reports failure while retaining the last observation.
  const refreshByUser = () => {
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
        // The host still runs the node's service until it is uninstalled there; the dialog reads the installation for the command.
        if (consoleConfig.node_installer && snapshot) {
          const { deployment } = snapshot;
          setCleanup({ node: {
            name: target.name || target.id, installationId: deployment.installation_id, scriptDigest: consoleConfig.node_installer_sha256,
            oldAddress: onOldAddress(target, deployment.core_url) ? target.core_url : null,
          }, open: true });
        }
      }
    } catch (error) {
      // Keep the dialog open with Core's reason; the refreshed list shows what actually happened.
      if (!controller.signal.aborted) { setRemoveError(sandboxRequestError(error, locale)); refresh(); }
    } finally { if (!controller.signal.aborted) setRemoving(false); }
  }

  const nodesConfirmed = confirmed && compatible && !inventoryOlder && !query.isError && !snapshot?.nodesError;
  const nodes = snapshot?.nodes ?? [];
  const allocations = snapshot?.allocations ?? [];
  const hostedNodes = snapshot?.deployment.mode === "nodes";
  // Nodes share the deployment's Provider, which declares whether sandboxes suspend.
  const suspends = Boolean(snapshot?.deployment.suspension);
  // A node enrolled with another address than Core's current one (config.json's public_url) gets no new sandboxes until it is added again.
  const staleNodes = hostedNodes && snapshot ? nodes.filter((node) => onOldAddress(node, snapshot.deployment.core_url)).map((node) => node.name || node.id) : [];
  const selected = params.id ? nodes.find((node) => node.id === params.id) : undefined;
  const editTarget = editId ? nodes.find((node) => node.id === editId) ?? null : null;
  const reset = Boolean(snapshot?.deployment.reset);
  const addDisabled = busy || loading || !fresh || !nodesConfirmed || setupNeedsRefresh || reset || localOnly;

  const { refetch: refetchInventory } = query;
  const refreshNodes = useCallback(async () => {
    const result = await refetchInventory();
    return result.isSuccess && !result.data.nodesError ? result.data.nodes : null;
  }, [refetchInventory]);
  // Getting started and Overview ask for Add node on arrival. A request the first settled read
  // cannot serve (no own-machines deployment, active reset, a failed read) is dropped, so the
  // dialog never opens later on its own.
  const addNodeReadiness = loading || busy || installation.isPending || inventoryLoading ? "wait"
    : !snapshot ? (deploymentQuery.isError ? "unavailable" : "wait")
    : hostedNodes && fresh && nodesConfirmed && !reset && !localOnly ? "ready" : "unavailable";
  useConsoleIntent("add-node", addNodeReadiness, () => setAdding(true));
  const enrollment = hostedNodes && snapshot ? <NodeEnrollment key={snapshot.deployment.generation} client={client} consoleConfig={consoleConfig} deployment={snapshot.deployment} nodes={nodes} open={adding} fresh={nodesConfirmed && !reset} onClose={() => setAdding(false)} onRefresh={refreshNodes} /> : null;
  const editDialog = <NodeEditDialog
    key={editTarget?.id ?? "closed"}
    client={client}
    node={editTarget}
    size={snapshot ? sandboxSize(snapshot.deployment) : null}
    suspends={suspends}
    onClose={() => setEditId(null)}
    onSaved={() => {
      const saved = editTarget;
      setEditId(null);
      toast.show(t("Node saved"), { tone: "success" });
      refresh();
      // Overview and Sandbox metrics read the fleet, and this page the node's host detail, separately.
      void queryClient.invalidateQueries({ queryKey: ["sandbox-fleet"] });
      if (saved) void queryClient.invalidateQueries({ queryKey: ["sandbox-node", saved.id] });
    }}
  />;
  const refreshButton = <RefreshButton onClick={refreshByUser} refreshing={loading} disabled={busy || removing} label={t("Refresh sandbox state")} />;
  const readFailure = error !== null ? sandboxRequestError(error, locale) : null;
  // A failed refresh keeps the last state on screen and says so in a toast, once while the failure lasts.
  useFailureToast(snapshot && deploymentQuery.isError ? sandboxRequestError(deploymentQuery.error, locale) : null, t("Refresh failed; showing the last loaded state."), "sandbox-read");
  const status = <>
    <InstallationNotice installation={installation.data} />
    {!snapshot && (loading || deploymentQuery.isPending) ? <p role="status">{t("Loading sandbox state…")}</p> : null}
    {inventoryOlder ? <p role="status" className="sandbox-notice">{t("Node observations are from an earlier configuration generation. Refreshing does not change their owned sandboxes.")}</p> : null}
    {snapshot?.nodesError ? <ErrorState title={t("Node state could not be read")} detail={sandboxRequestError(snapshot.nodesError, locale)} onRetry={refresh} /> : null}
    {snapshot && deploymentQuery.isError ? <p role="alert" className="sandbox-error">{t("Refresh failed; showing the last loaded state.")}</p> : null}
    {setupNeedsRefresh ? <p role="alert" className="sandbox-error">{t("Refresh sandbox state to confirm whether the change was saved before submitting again.")}</p> : null}
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
  const cleanupDialog = <NodeCleanupDialog cleanup={cleanup?.node ?? null} open={cleanup?.open ?? false} onClose={() => {
    setCleanup((current) => current && { ...current, open: false });
    // The removed node's button is gone, so focus returns to the page, after the dialog restores its own.
    window.requestAnimationFrame(() => heading.current?.focus());
  }} />;
  if (params.id && hostedNodes) {
    const back = () => goBack("nodes");
    return <>
      <NodesPageHeader
        headingRef={heading}
        back={back}
        title={selected ? selected.name || selected.id : params.id}
        actions={<>
          {refreshButton}
          {selected ? <button type="button" className="button outline" disabled={busy || removing || !nodesConfirmed} onClick={() => setEditId(selected.id)}>{t("Edit node")}</button> : null}
          {selected ? <button type="button" className="button danger" disabled={busy || removing || !nodesConfirmed} onClick={() => askRemove(selected)}><Trash2 size={14} aria-hidden="true" />{t("Remove node")}</button> : null}
        </>}
      />
      <div className="console-page-body sandbox-content">
        {status}
        {selected ? <NodeDetail node={selected} allocations={allocations} coreUrl={snapshot?.deployment.core_url ?? ""} targetGeneration={snapshot?.deployment.generation} stale={!nodesConfirmed} suspension={snapshot?.deployment.suspension ?? null} /> : snapshot && nodesConfirmed && !loading ? (
          <EmptyState icon={Server} title={t("Node not found")} hint={t("This node is not registered. It may have been removed.")} action={<button type="button" className="button outline" onClick={back}>{t("Back")}</button>} />
        ) : null}
      </div>
      {dialog}
      {cleanupDialog}
      {editDialog}
    </>;
  }

  const actions = <>
    {localOnly && hostedNodes ? <span id="add-node-blocked" className="muted">{tCommon("installationNotice.addBlocked")}</span> : null}
    {hostedNodes ? <button className="button outline" type="button" onClick={() => navigate("settings", { id: "sandbox" })}>{tSandboxNav("open")}</button> : null}
    {consoleConfig.node_installer && snapshot ? <button className="button outline" type="button" onClick={() => setCleanup({ node: {
      name: null, installationId: snapshot.deployment.installation_id, scriptDigest: consoleConfig.node_installer_sha256, oldAddress: null,
    }, open: true })}>{t("Clean up a host")}</button> : null}
    {refreshButton}
    {hostedNodes && snapshot ? <button type="button" className="button primary" disabled={addDisabled} aria-describedby={localOnly ? "add-node-blocked" : undefined} onClick={() => setAdding(true)}><Plus size={16} />{t("Add node")}</button> : null}
  </>;
  return <>
    <NodesPageHeader headingRef={heading} count={hostedNodes && nodesConfirmed ? nodes.length : undefined} actions={actions} />
    <div className="console-page-body sandbox-content">
      {status}
      {snapshot && !hostedNodes ? <EmptyState icon={Server} title={tSandboxNav(snapshot.deployment.mode === "direct" ? "cloud" : "unconfigured")} action={<button className="button outline" type="button" onClick={() => navigate("settings", { id: "sandbox" })}>{tSandboxNav("open")}</button>} /> : null}
      {snapshot?.deployment.reset && hostedNodes ? <p role="status">{tSandboxNav("reset")}</p> : null}
      {snapshot?.deployment.provider ? <>
        {staleNodes.length ? <p className="sandbox-notice sandbox-address-warning" role="status">{staleNodes.length === 1
          ? t("{{name}} is still bound to an old Core address. Remove it and add it again.", { name: staleNodes[0] })
          : t("{{count}} nodes are still bound to an old Core address: {{names}}. Remove them and add them again.", { count: staleNodes.length, names: new Intl.ListFormat(i18n.resolvedLanguage, { type: "conjunction" }).format(staleNodes) })}</p> : null}
        {hostedNodes && nodes.some((node) => node.active > node.max_active || node.retained > node.max_retained) ? <p role="status" className="sandbox-notice">{t("Current usage exceeds a saved limit. Existing sandboxes are preserved; new placements wait for capacity.")}</p> : null}
        {hostedNodes ? nodes.length
          ? <NodeList nodes={nodes} allocations={allocations} coreUrl={snapshot.deployment.core_url} stale={!nodesConfirmed} disabled={busy || loading || removing || !nodesConfirmed} suspends={suspends} onOpen={(node) => navigate("nodes", { id: node.id })} onEdit={(node) => setEditId(node.id)} onRemove={askRemove} />
          : nodesConfirmed ? <EmptyState icon={Server} title={t("Add your first node")} hint={t("No nodes yet. Add a node to provide hosted capacity.")} /> : null
        : null}
      </> : null}
    </div>
    {dialog}
    {cleanupDialog}
    {enrollment}
    {editDialog}
  </>;
}
