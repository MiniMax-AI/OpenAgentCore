import { useEffect, useMemo, useRef, useState } from "react";
import { SandboxAdminClient, type InitializeSandboxDeployment, type SandboxAllocation, type SandboxDeployment, type SandboxNode } from "@agents-core-web/agents-client";
import { isLocalProxyBaseUrl } from "../../lib/connection";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxRequestError, sandboxStateLabel } from "../../lib/sandbox-labels";
import { SandboxDiagnostic } from "./SandboxDiagnostic";
import { NodeHealth } from "./NodeHealth";
import { sandboxConsoleConfig, type SandboxConsoleConfig } from "./console-config";
import { SandboxSetup } from "./SandboxSetup";
import { NodeEnrollment } from "./NodeEnrollment";
import "./SandboxManagerView.css";

export function SandboxManagerView({ coreBaseUrl }: { coreBaseUrl: string }) {
  const { t, locale } = useLocale();
  return <section className="sandbox-manager" lang={locale}>
    <header className="sandbox-heading"><div><h1>{t("Hosted Sandbox Manager")}</h1><p>{t("Deployment provider, runtime nodes and Session allocations.")}</p></div></header>
    {isLocalProxyBaseUrl(coreBaseUrl) ? <SandboxAccess key={coreBaseUrl} /> : <p role="status">{t("Sandbox management is available through the signed-in console connection. Switch the Core connection to /v1 to manage this deployment.")}</p>}
  </section>;
}

function SandboxAccess() {
  const { t } = useLocale();
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
  if (checking) return <p role="status">{t("Connecting to this console's Core…")}</p>;
  if (!config?.sandbox_admin) return <div><p role="alert">{t("Sandbox administration is not configured on this console. Ask the deployment administrator to configure access.")}</p><button type="button" className="button" onClick={() => setRevision((value) => value + 1)}>{t("Refresh sandbox state")}</button></div>;
  return <SandboxManager consoleConfig={config} />;
}

function SandboxManager({ consoleConfig }: { consoleConfig: SandboxConsoleConfig }) {
  const { t, locale } = useLocale();
  const client = useMemo(() => new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" }), []);
  const [snapshot, setSnapshot] = useState<{ deployment: SandboxDeployment; nodes: SandboxNode[]; allocations: SandboxAllocation[] } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const [removeId, setRemoveId] = useState<string | null>(null);
  const [enrollment, setEnrollment] = useState<{ token: string; expires_at: string } | null>(null);
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
        setSetupNeedsRefresh(false);
      }
    })().catch((error) => { if (!controller.signal.aborted) setError(error); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [client, revision]);
  useEffect(() => {
    if (!enrollment || busy) return;
    const interval = window.setInterval(() => setRevision((value) => value + 1), 3000);
    return () => window.clearInterval(interval);
  }, [enrollment, busy]);
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
  async function enroll() {
    const controller = lifetime.current;
    if (!controller || busy) return;
    setBusy(true); setError(null); setEnrollment(null);
    try {
      const result = await client.createEnrollment({ signal: controller.signal });
      if (!controller.signal.aborted) setEnrollment(result);
    } catch (error) { if (!controller.signal.aborted) setError(error); }
    finally { if (!controller.signal.aborted) setBusy(false); }
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
  return <div className="form-stack">
    <div className="sandbox-toolbar"><button type="button" className="button" disabled={loading || busy} onClick={() => setRevision((v) => v + 1)}>{t("Refresh sandbox state")}</button>{loading ? <span role="status">{t("Loading sandbox state…")}</span> : busy ? <span role="status">{t("Saving sandbox change…")}</span> : null}</div>
    {error !== null ? <p role="alert" className="sandbox-error">{sandboxRequestError(error, locale)}{setupNeedsRefresh ? ` ${t("Refresh sandbox state to confirm whether setup was saved before submitting again.")}` : ""}{snapshot ? ` ${t("Previously loaded state is shown below.")}` : ""}</p> : null}
    {snapshot && !snapshot.deployment.provider ? <SandboxSetup automaticInstall={consoleConfig.node_installer} key={revision} initialCoreUrl={initialCoreUrl} disabled={busy || loading || setupNeedsRefresh || error !== null} onInitialize={initialize} /> : null}
    {snapshot?.deployment.provider ? <>
      <dl className="sandbox-summary">
        <div><dt>{t("Provider")}</dt><dd>{snapshot.deployment.provider === "docker" ? "Docker" : "microsandbox"}</dd></div>
        <div><dt>{t("Maintenance")}</dt><dd>{snapshot.deployment.maintenance ? t("Enabled") : t("Off")}</dd></div>
        <div><dt>{t("Installation")}</dt><dd><code>{snapshot.deployment.installation_id}</code></dd></div>
      </dl>
      <section aria-labelledby="sandbox-nodes-heading"><h2 id="sandbox-nodes-heading">{t("Nodes")}</h2><p>{t("Local nodes run on the Core server. Capacity and counts are reported by Core.")}</p>
        {snapshot.nodes.length ? <div className="sandbox-table-scroll" tabIndex={0} role="region" aria-label={t("Sandbox nodes")}><table><thead><tr><th>{t("Node")}</th><th>{t("Health")}</th><th>{t("Active / limit")}</th><th>{t("Reserved")}</th><th>{t("Retained / limit")}</th><th>{t("Running / snapshots")}</th><th>{t("Cleanup pending")}</th><th>{t("Actions")}</th></tr></thead><tbody>
          {snapshot.nodes.map((node) => <tr key={node.id}><td><strong>{node.name}</strong><small>{node.id}</small></td><td><NodeHealth node={node} /></td><td>{node.active} / {node.max_active}</td><td>{node.reserved}</td><td>{node.retained} / {node.max_retained}</td><td>{node.running} / {node.snapshots}</td><td>{node.cleanup_pending}</td><td><button type="button" className="button" disabled={busy || loading} aria-label={`${t("Remove")} ${node.name}`} onClick={() => setRemoveId(node.id)}>{t("Remove")}</button></td></tr>)}
        </tbody></table></div> : <p>{t("No nodes registered. Add a node to provide hosted capacity.")}</p>}
        {removeId ? <div className="sandbox-confirm" role="group" aria-label={t("Confirm node removal")}><p>{t("Remove node")} <code>{removeId}</code>? {t("Core rejects removal while allocations or retained resources remain.")}</p><button type="button" className="button danger" disabled={busy} onClick={() => void remove()}>{t("Confirm removal")}</button> <button type="button" className="button" disabled={busy} onClick={() => setRemoveId(null)}>{t("Cancel removal")}</button></div> : null}
      </section>
      <section aria-labelledby="sandbox-allocations-heading"><h2 id="sandbox-allocations-heading">{t("Allocations")}</h2>
        {snapshot.allocations.length ? <div className="sandbox-table-scroll" tabIndex={0} role="region" aria-label={t("Sandbox allocations")}><table><thead><tr><th>{t("Session")}</th><th>{t("Node")}</th><th>{t("Recorded state")}</th><th>{t("Recorded compute")}</th><th>{t("Health")}</th></tr></thead><tbody>
          {snapshot.allocations.map((allocation) => <tr key={allocation.id}><td><code>{allocation.session_id}</code></td><td>{snapshot.nodes.find((node) => node.id === allocation.node_id)?.name ?? allocation.node_id}</td><td>{sandboxStateLabel(allocation.state, locale)}</td><td>{sandboxStateLabel(allocation.compute_phase, locale)}</td><td><SandboxDiagnostic diagnostic={allocation.diagnostic} />{!allocation.diagnostic ? t("No reported issue") : null}</td></tr>)}
        </tbody></table></div> : <p>{t("No sandbox allocations.")}</p>}
      </section>
      <NodeEnrollment consoleConfig={consoleConfig} deployment={snapshot.deployment} initialCoreUrl={initialCoreUrl} busy={busy || loading || error !== null} enrollment={enrollment} onEnroll={enroll} onClear={() => setEnrollment(null)} />
    </> : null}
  </div>;
}
