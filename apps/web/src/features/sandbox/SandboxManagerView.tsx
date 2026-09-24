import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { SandboxAdminClient, type InitializeSandboxDeployment, type SandboxAllocation, type SandboxDeployment, type SandboxNode } from "@agents-core-web/agents-client";
import { RefreshCw, Server } from "lucide-react";
import { useTranslation } from "react-i18next";
import { HelpTip, RefreshButton } from "../../components/console-ui";
import { isLocalProxyBaseUrl } from "../../lib/connection";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { SandboxTopology } from "./SandboxTopology";
import { SandboxNodeCard } from "./SandboxNodeCard";
import { sandboxConsoleConfig, type SandboxConsoleConfig } from "./console-config";
import { SandboxSetup } from "./SandboxSetup";
import { NodeEnrollment } from "./NodeEnrollment";
import "./SandboxManagerView.css";

export function SandboxManagerView({ coreBaseUrl, presentation = "manager" }: { coreBaseUrl: string; presentation?: "manager" | "home" }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const manager = presentation === "manager";
  return <section className={manager ? "page-section console-page sandbox-manager sandbox-manager-page" : "sandbox-manager"} lang={locale}>
    {isLocalProxyBaseUrl(coreBaseUrl) ? <SandboxAccess key={coreBaseUrl} presentation={presentation} /> : <>
      {manager ? <NodesPageHeader /> : null}
      <div className={manager ? "console-page-body" : undefined}><p role="status">{t("Sandbox management is available through the signed-in console connection. Switch the Core connection to /v1 to manage this deployment.")}</p></div>
    </>}
  </section>;
}

function NodesPageHeader({ count, actions }: { count?: number; actions?: ReactNode }) {
  const { t } = useTranslation("sandbox");
  return <header className="page-header">
    <div className="console-page-heading"><h1>{t("Nodes")}</h1>{count === undefined ? null : <span className="heading-count">{count}</span>}<HelpTip>{t("Your hosts for running sandboxes.")}</HelpTip></div>
    {actions ? <div className="page-actions">{actions}</div> : null}
  </header>;
}

function SandboxAccess({ presentation }: { presentation: "manager" | "home" }) {
  const { t } = useTranslation("sandbox");
  const [config, setConfig] = useState<SandboxConsoleConfig | null>(null);
  const [checking, setChecking] = useState(true);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setChecking(true);
    void sandboxConsoleConfig(controller.signal).then((value) => {
      if (!controller.signal.aborted) { setConfig(value); setChecking(false); }
    });
    return () => controller.abort();
  }, [revision]);
  const manager = presentation === "manager";
  if (checking) return <>{manager ? <NodesPageHeader /> : null}<div className={manager ? "console-page-body" : undefined}><p role="status">{t("Connecting to this console's Core…")}</p></div></>;
  if (!config?.sandbox_admin) return <>{manager ? <NodesPageHeader /> : null}<div className={manager ? "console-page-body" : undefined}><p role="alert">{t("Sandbox administration is not configured on this console. Ask the deployment administrator to configure access.")}</p><button type="button" className="button outline" onClick={() => setRevision((value) => value + 1)}>{t("Refresh sandbox state")}</button></div></>;
  return <SandboxManager consoleConfig={config} presentation={presentation} />;
}

function SandboxManager({ consoleConfig, presentation }: { consoleConfig: SandboxConsoleConfig; presentation: "manager" | "home" }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const client = useMemo(() => new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" }), []);
  const [snapshot, setSnapshot] = useState<{ deployment: SandboxDeployment; nodes: SandboxNode[]; allocations: SandboxAllocation[] } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [fresh, setFresh] = useState(false);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selectedNode = snapshot?.nodes.find((node) => node.id === selectedId);
  const [removeId, setRemoveId] = useState<string | null>(null);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const revealNode = useCallback((id: string) => { setSelectedId(id); setRemoveId(null); }, []);
  const initialCoreUrl = window.location.origin;
  const [setupNeedsRefresh, setSetupNeedsRefresh] = useState(false);
  const lifetime = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController(); lifetime.current = controller;
    return () => { controller.abort(); lifetime.current = null; };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError(null);
    void (async () => {
      const [deployment, nodes] = await Promise.all([client.retrieveDeployment({ signal: controller.signal }), client.listNodes({ signal: controller.signal })]);
      const allocations = await Promise.all(nodes.data.map((node) => client.listAllocations(node.id, { signal: controller.signal })));
      if (!controller.signal.aborted) {
        setSnapshot({ deployment, nodes: nodes.data, allocations: allocations.flatMap((page) => page.data) });
        setSetupNeedsRefresh(false); setFresh(true);
      }
    })().catch((error) => { if (!controller.signal.aborted) { setError(error); setFresh(false); } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [client, revision]);
  async function initialize(input: InitializeSandboxDeployment) {
    const controller = lifetime.current;
    if (!controller || busy || loading || setupNeedsRefresh) return;
    setBusy(true); setError(null);
    try {
      const deployment = await client.initializeDeployment(input, { signal: controller.signal });
      if (!controller.signal.aborted) setSnapshot({ deployment, nodes: [], allocations: [] });
    } catch (error) {
      if (!controller.signal.aborted) { setSetupNeedsRefresh(true); setError(error); }
    } finally { if (!controller.signal.aborted) setBusy(false); }
  }
  async function remove() {
    const controller = lifetime.current;
    if (!controller || !removeId || busy) return;
    setBusy(true); setError(null);
    try {
      const result = await client.removeNode(removeId, { signal: controller.signal });
      if (!result.deleted || result.id !== removeId) throw new Error("removal_unconfirmed");
      if (!controller.signal.aborted) { setRemoveId(null); setRevision((v) => v + 1); }
    } catch (error) { if (!controller.signal.aborted) setError(error); }
    finally { if (!controller.signal.aborted) setBusy(false); }
  }
  const manager = presentation === "manager";
  const actions = <>
    {manager
      ? <RefreshButton onClick={refresh} refreshing={loading} disabled={busy} label={t("Refresh sandbox state")} />
      : <button type="button" className="icon-button sandbox-refresh" disabled={loading || busy} aria-label={t("Refresh sandbox state")} title={t("Refresh sandbox state")} onClick={refresh}><RefreshCw size={17} /></button>}
    {snapshot?.deployment.provider ? <NodeEnrollment client={client} consoleConfig={consoleConfig} deployment={snapshot.deployment} nodes={snapshot.nodes} disabled={busy || loading || error !== null} fresh={fresh} onRefresh={refresh} onConnected={revealNode} /> : null}
  </>;
  return <>{manager ? <NodesPageHeader count={snapshot?.nodes.length} actions={actions} /> : null}<div className={manager ? "console-page-body sandbox-content" : "sandbox-content form-stack"}>
    {manager ? null : <header className="sandbox-heading"><div><h2>{t("Node network")}</h2></div><div className="sandbox-actions">{actions}</div></header>}
    {loading && !snapshot ? <p role="status">{t("Loading sandbox state…")}</p> : null}
    {busy ? <span role="status">{t("Saving sandbox change…")}</span> : null}
    {error !== null ? <p role="alert" className="sandbox-error">{sandboxRequestError(error, locale)}{setupNeedsRefresh ? ` ${t("Refresh sandbox state to confirm whether setup was saved before submitting again.")}` : ""}{snapshot ? ` ${t("Previously loaded state is shown below.")}` : ""}</p> : null}
    {snapshot && !snapshot.deployment.provider ? <SandboxSetup key={revision} initialCoreUrl={initialCoreUrl} disabled={busy || loading || setupNeedsRefresh || error !== null} onInitialize={initialize} /> : null}
    {presentation === "home" && snapshot && !snapshot.deployment.provider ? <SandboxTopology nodes={snapshot.nodes} allocations={snapshot.allocations} stale={!fresh} selectedId={selectedId} onSelect={revealNode} /> : null}
    {snapshot?.deployment.provider ? <>
      {snapshot.deployment.maintenance ? <p className="sandbox-maintenance" role="status">{t("Maintenance is enabled. New sandbox placement is paused.")}</p> : null}
      <section aria-label={t("Nodes")}>
        {snapshot.nodes.length || presentation === "home" ? <SandboxTopology nodes={snapshot.nodes} allocations={snapshot.allocations} stale={!fresh} selectedId={selectedId} onSelect={revealNode} /> : <div className="sandbox-empty"><Server size={32} strokeWidth={1.25} /><h3>{t("Add your first node")}</h3><p>{t("No nodes registered. Add a node to provide hosted capacity.")}</p></div>}
        {selectedNode ? <div id="sandbox-selected-node" className="sandbox-selected-node"><SandboxNodeCard key={selectedNode.id} node={selectedNode} allocations={snapshot.allocations.filter((allocation) => allocation.node_id === selectedNode.id)} stale={!fresh} disabled={busy || loading} confirming={removeId === selectedNode.id} onRemove={() => setRemoveId(selectedNode.id)} onConfirm={() => void remove()} onCancel={() => setRemoveId(null)} /></div> : null}
      </section>
    </> : null}
  </div></>;
}
