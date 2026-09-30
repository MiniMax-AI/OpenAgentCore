import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { type SandboxNode } from "@oac/agents-client";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Pencil, Plus, Server, Trash2 } from "lucide-react";
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
import { sandboxAdmin } from "./sandbox-queries";
import { useSandboxPageState } from "./use-sandbox-page-state";
import { SandboxPageAccess } from "./SandboxPageAccess";
import { NodeEnrollment } from "./NodeEnrollment";
import { NodeList, onOldAddress } from "./NodeList";
import { NodeDetail } from "./NodeDetail";
import { NodeEditDialog } from "./NodeEditDialog";
import { NodeCleanupDialog, type NodeCleanup } from "./NodeCleanupDialog";
import { sandboxSize } from "./deployment-specification";
import "./SandboxManagerView.css";

/** Nodes owns node enrollment, the list and individual node management. */
export function SandboxManagerView() {
  const { i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  return <section className="page-section console-page sandbox-manager sandbox-manager-page" lang={locale}>
    <SandboxPageAccess header={<NodesPageHeader />}>{(config) => <SandboxManager consoleConfig={config} />}</SandboxPageAccess>
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
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const { params, navigate, back: goBack } = useConsoleNavigation();
  const client = sandboxAdmin;
  const queryClient = useQueryClient();
  const { deploymentQuery, query, compatible, inventoryOlder, inventoryLoading, snapshot, installation, loading, busy, setupNeedsRefresh, confirmed, fresh, refetch, refresh } = useSandboxPageState();
  const { t: tSandboxNav } = useTranslation("sandboxNavigation");
  const localOnly = installation.data?.local_only === true;
  const { t: tCommon } = useTranslation("common");
  const error: unknown = deploymentQuery.error;
  const [removeTarget, setRemoveTarget] = useState<SandboxNode | null>(null);
  const [editTarget, setEditTarget] = useState<SandboxNode | null>(null);
  // The Add node dialog; it stays mounted with the page so its command survives closing.
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  // After a removal, the host's uninstall command; it stays for the closing animation.
  const [cleanup, setCleanup] = useState<{ node: NodeCleanup; open: boolean } | null>(null);
  // The enrollment dialog's reads: the node list alone, every few seconds while it waits. It settles
  // when the read does, which the dialog waits for before calling a command expired.
  const { refetch: refetchInventory } = query;
  const refreshNodes = useCallback(async () => {
    const result = await refetchInventory();
    return result.isSuccess && !result.data.nodesError ? result.data.nodes : null;
  }, [refetchInventory]);
  // Where focus goes once a dialog about a node that is gone closes.
  const heading = useRef<HTMLHeadingElement>(null);
  const toast = useToast();
  // A refresh the administrator asks for reports its failure even while an earlier one is still unconfirmed;
  // the enrollment dialog's own repeated refreshes do not.
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
            provider: deployment.provider, oldAddress: onOldAddress(target, deployment.core_url) ? target.core_url : null,
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
  const hostedNodes = Boolean(snapshot?.deployment.provider && snapshot.deployment.provider !== "e2b");
  // Getting started asks for Add node on arrival. A request the first settled read cannot
  // serve (no own-machines deployment, active reset, a failed read) is dropped, so the
  // dialog never opens later on its own.
  const addNodeReadiness = loading || busy || installation.isPending || inventoryLoading ? "wait"
    : !snapshot ? (deploymentQuery.isError ? "unavailable" : "wait")
    : hostedNodes && fresh && nodesConfirmed && !snapshot.deployment.reset && !localOnly ? "ready" : "unavailable";
  useConsoleIntent("add-node", addNodeReadiness, () => setAdding(true));
  // A node enrolled with another address than Core's current one (config.json's public_url) gets no new sandboxes until it is added again.
  const staleNodes = hostedNodes && snapshot ? nodes.filter((node) => onOldAddress(node, snapshot.deployment.core_url)).map((node) => node.name || node.id) : [];
  const selected = params.id ? nodes.find((node) => node.id === params.id) : undefined;
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
  // Rendered first in both the list and a node's page, so an open command outlives the navigation.
  const enrollment = hostedNodes && snapshot ? <NodeEnrollment key={snapshot.deployment.generation} client={client} consoleConfig={consoleConfig} deployment={snapshot.deployment} nodes={snapshot.nodes} open={adding} fresh={nodesConfirmed && !snapshot.deployment.reset} onClose={() => setAdding(false)} onRefresh={refreshNodes} /> : null;

  if (params.id && hostedNodes) {
    const back = () => goBack("nodes");
    return <>
      {enrollment}
      <NodesPageHeader
        headingRef={heading}
        back={back}
        title={selected ? selected.name || selected.id : params.id}
        actions={<>
          {refreshButton}
          {selected ? <button type="button" className="button outline" disabled={busy || removing || !nodesConfirmed} onClick={() => setEditTarget(selected)}><Pencil size={14} aria-hidden="true" />{t("Edit node")}</button> : null}
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
      <NodeEditDialog
        key={editTarget?.id ?? "closed"}
        client={client}
        node={editTarget}
        size={snapshot ? sandboxSize(snapshot.deployment) : null}
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
    {localOnly && hostedNodes ? <span id="add-node-blocked" className="muted">{tCommon("installationNotice.addBlocked")}</span> : null}
    {hostedNodes ? <button className="button outline" type="button" onClick={() => navigate("system", { id: "sandbox" })}>{tSandboxNav("open")}</button> : null}
    {refreshButton}
    {hostedNodes && snapshot ? <button type="button" className="button primary" disabled={busy || loading || !fresh || !nodesConfirmed || Boolean(snapshot.deployment.reset) || localOnly} aria-describedby={localOnly ? "add-node-blocked" : undefined} onClick={() => setAdding(true)}><Plus size={16} />{t("Add node")}</button> : null}
  </>;
  return <>
    {enrollment}
    <NodesPageHeader headingRef={heading} count={hostedNodes && nodesConfirmed ? nodes.length : undefined} actions={actions} />
    <div className="console-page-body sandbox-content">
      {status}
      {snapshot && !hostedNodes ? <EmptyState icon={Server} title={tSandboxNav(snapshot.deployment.provider === "e2b" ? "cloud" : "unconfigured")} action={<button className="button outline" type="button" onClick={() => navigate("system", { id: "sandbox" })}>{tSandboxNav("open")}</button>} /> : null}
      {snapshot?.deployment.reset && hostedNodes ? <p role="status">{tSandboxNav("reset")}</p> : null}
      {snapshot?.deployment.provider ? <>
        {staleNodes.length ? <p className="sandbox-notice sandbox-address-warning" role="status">{staleNodes.length === 1
          ? t("{{name}} is still bound to an old Core address. Remove it and add it again.", { name: staleNodes[0] })
          : t("{{count}} nodes are still bound to an old Core address: {{names}}. Remove them and add them again.", { count: staleNodes.length, names: new Intl.ListFormat(i18n.resolvedLanguage, { type: "conjunction" }).format(staleNodes) })}</p> : null}
        {hostedNodes ? <section aria-label={t("Sandbox nodes")}>
          {nodes.length
            ? <NodeList nodes={nodes} allocations={allocations} coreUrl={snapshot.deployment.core_url} stale={!nodesConfirmed} disabled={busy || loading || removing || !nodesConfirmed} suspends={snapshot.deployment.provider === "microsandbox"} onOpen={(node) => navigate("nodes", { id: node.id })} onRemove={askRemove} />
            : nodesConfirmed ? <EmptyState icon={Server} title={t("Add your first node")} hint={t("No nodes registered. Add a node to provide hosted capacity.")} /> : null}
        </section> : null}
      </> : null}
    </div>
    {dialog}
    {cleanupDialog}
  </>;
}
