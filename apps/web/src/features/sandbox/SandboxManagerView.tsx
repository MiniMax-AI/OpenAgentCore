import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { SandboxAdminClient, type InitializeSandboxDeployment, type SandboxAllocation, type SandboxDeployment, type SandboxNode } from "@agents-core-web/agents-client";
import { sandboxAdminBaseUrl } from "./SandboxContext";
import { SandboxDiagnostic } from "./SandboxDiagnostic";
import { NodeHealth } from "./NodeHealth";
import { isLocalProxyBaseUrl } from "../../lib/connection";
import { sandboxConsoleConfig, type SandboxConsoleConfig } from "./console-config";
import { SandboxSetup } from "./SandboxSetup";
import { NodeEnrollment } from "./NodeEnrollment";
import "./SandboxManagerView.css";

function message(error: unknown): string {
  return error instanceof Error ? error.message : "The sandbox request failed.";
}

export function SandboxManagerView({ coreBaseUrl }: { coreBaseUrl: string }) {
  return <SandboxAccess key={coreBaseUrl} coreBaseUrl={coreBaseUrl} />;
}

function SandboxAccess({ coreBaseUrl }: { coreBaseUrl: string }) {
  const [draft, setDraft] = useState("");
  const [credential, setCredential] = useState("");
  const [consoleConfig, setConsoleConfig] = useState<SandboxConsoleConfig | null>(null);
  const [checkingConsole, setCheckingConsole] = useState(isLocalProxyBaseUrl(coreBaseUrl));
  useEffect(() => {
    if (!isLocalProxyBaseUrl(coreBaseUrl)) return;
    const controller = new AbortController();
    void sandboxConsoleConfig(controller.signal).then((config) => {
      if (!controller.signal.aborted) { setConsoleConfig(config); setCheckingConsole(false); }
    });
    return () => controller.abort();
  }, [coreBaseUrl]);
  function connect(event: FormEvent) {
    event.preventDefault();
    if (draft.trim()) { setCredential(draft.trim()); setDraft(""); }
  }
  return <section className="sandbox-manager">
    <header className="sandbox-heading"><div><h1>Hosted Sandbox Manager</h1><p>Deployment provider, runtime nodes and Session allocations.</p></div>
      {credential ? <button type="button" className="button" onClick={() => setCredential("")}>Disconnect admin</button> : null}
    </header>
    {checkingConsole ? <p role="status">Connecting to this console’s Core…</p> : !credential && !consoleConfig?.sandbox_admin ? <form className="form-stack sandbox-access" onSubmit={connect}>
      <h2>Deployment administrator access</h2>
      <p>Enter the separate deployment admin key. It stays in memory until you leave this page or disconnect.</p>
      <label className="field"><span>Deployment admin key</span><input type="password" autoComplete="off" value={draft} onChange={(event) => setDraft(event.target.value)} required /></label>
      <button type="submit" className="button primary" disabled={!draft.trim()}>Connect admin</button>
    </form> : <SandboxManager key={`${coreBaseUrl}:${credential}`} coreBaseUrl={coreBaseUrl} credential={credential} consoleConfig={consoleConfig} />}
  </section>;
}

function SandboxManager({ coreBaseUrl, credential, consoleConfig }: { coreBaseUrl: string; credential: string; consoleConfig: SandboxConsoleConfig | null }) {
  const client = useMemo(() => new SandboxAdminClient({ baseUrl: sandboxAdminBaseUrl(coreBaseUrl), token: credential, fetch: (input, init) => fetch(input, { ...init, credentials: "include" }) }), [coreBaseUrl, credential]);
  const [snapshot, setSnapshot] = useState<{ deployment: SandboxDeployment; nodes: SandboxNode[]; allocations: SandboxAllocation[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const [removeId, setRemoveId] = useState<string | null>(null);
  const [enrollment, setEnrollment] = useState<{ token: string; expires_at: string } | null>(null);
  const initialCoreUrl = coreBaseUrl.startsWith("http") ? coreBaseUrl.replace(/\/v1\/?$/, "") : consoleConfig?.sandbox_admin ? window.location.origin : "";
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
    })().catch((error) => { if (!controller.signal.aborted) setError(message(error)); })
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
      if (!controller.signal.aborted) {
        setSetupNeedsRefresh(true);
        setError(`${message(error)} Refresh sandbox state to confirm whether setup was saved before submitting again.`);
      }
    } finally { if (!controller.signal.aborted) setBusy(false); }
  }
  async function enroll() {
    const controller = lifetime.current;
    if (!controller || busy) return;
    setBusy(true); setError(null); setEnrollment(null);
    try {
      const result = await client.createEnrollment({ signal: controller.signal });
      if (!controller.signal.aborted) setEnrollment(result);
    } catch (error) { if (!controller.signal.aborted) setError(message(error)); }
    finally { if (!controller.signal.aborted) setBusy(false); }
  }
  async function remove() {
    const controller = lifetime.current;
    if (!controller || !removeId || busy) return;
    setBusy(true); setError(null);
    try {
      const result = await client.removeNode(removeId, { signal: controller.signal });
      if (!result.deleted || result.id !== removeId) throw new Error("Core did not confirm node removal. Refresh to check its state.");
      if (!controller.signal.aborted) { setRemoveId(null); setRevision((v) => v + 1); }
    } catch (error) { if (!controller.signal.aborted) setError(message(error)); }
    finally { if (!controller.signal.aborted) setBusy(false); }
  }
  return <div className="form-stack">
    <div className="sandbox-toolbar"><button type="button" className="button" disabled={loading || busy} onClick={() => setRevision((v) => v + 1)}>Refresh sandbox state</button>{loading ? <span role="status">Loading sandbox state…</span> : busy ? <span role="status">Saving sandbox change…</span> : null}</div>
    {error ? <p role="alert" className="sandbox-error">{error}{snapshot ? " Previously loaded state is shown below." : ""}</p> : null}
    {snapshot && !snapshot.deployment.provider ? <SandboxSetup automaticInstall={consoleConfig?.node_installer} key={revision} initialCoreUrl={initialCoreUrl} disabled={busy || loading || setupNeedsRefresh || Boolean(error)} onInitialize={initialize} /> : null}
    {snapshot?.deployment.provider ? <>
      <dl className="sandbox-summary">
        <div><dt>Provider</dt><dd>{snapshot.deployment.provider === "docker" ? "Docker" : "microsandbox"}</dd></div>
        <div><dt>Maintenance</dt><dd>{snapshot.deployment.maintenance ? "Enabled" : "Off"}</dd></div>
        <div><dt>Installation</dt><dd><code>{snapshot.deployment.installation_id}</code></dd></div>
      </dl>
      <section aria-labelledby="sandbox-nodes-heading"><h2 id="sandbox-nodes-heading">Nodes</h2><p>Local nodes run on the Core server. Capacity and counts are reported by Core.</p>
        {snapshot.nodes.length ? <div className="sandbox-table-scroll" tabIndex={0} role="region" aria-label="Sandbox nodes"><table><thead><tr><th>Node</th><th>Health</th><th>Active / limit</th><th>Reserved</th><th>Retained / limit</th><th>Running / snapshots</th><th>Cleanup pending</th><th>Actions</th></tr></thead><tbody>
          {snapshot.nodes.map((node) => <tr key={node.id}><td><strong>{node.name}</strong><small>{node.id}</small></td><td><NodeHealth node={node} /></td><td>{node.active} / {node.max_active}</td><td>{node.reserved}</td><td>{node.retained} / {node.max_retained}</td><td>{node.running} / {node.snapshots}</td><td>{node.cleanup_pending}</td><td><button type="button" className="button" disabled={busy || loading} aria-label={`Remove ${node.name}`} onClick={() => setRemoveId(node.id)}>Remove</button></td></tr>)}
        </tbody></table></div> : <p>No nodes registered. Add a node to provide hosted capacity.</p>}
        {removeId ? <div className="sandbox-confirm" role="group" aria-label="Confirm node removal"><p>Remove node <code>{removeId}</code>? Core rejects removal while allocations or retained resources remain.</p><button type="button" className="button danger" disabled={busy} onClick={() => void remove()}>Confirm removal</button> <button type="button" className="button" disabled={busy} onClick={() => setRemoveId(null)}>Cancel removal</button></div> : null}
      </section>
      <section aria-labelledby="sandbox-allocations-heading"><h2 id="sandbox-allocations-heading">Allocations</h2>
        {snapshot.allocations.length ? <div className="sandbox-table-scroll" tabIndex={0} role="region" aria-label="Sandbox allocations"><table><thead><tr><th>Session</th><th>Node</th><th>Recorded state</th><th>Recorded compute</th><th>Health</th></tr></thead><tbody>
          {snapshot.allocations.map((allocation) => <tr key={allocation.id}><td><code>{allocation.session_id}</code></td><td>{snapshot.nodes.find((node) => node.id === allocation.node_id)?.name ?? allocation.node_id}</td><td>{allocation.state}</td><td>{allocation.compute_phase}</td><td><SandboxDiagnostic diagnostic={allocation.diagnostic} />{!allocation.diagnostic ? "No reported issue" : null}</td></tr>)}
        </tbody></table></div> : <p>No sandbox allocations.</p>}
      </section>
      <NodeEnrollment consoleConfig={consoleConfig} deployment={snapshot.deployment} initialCoreUrl={initialCoreUrl} busy={busy || loading || Boolean(error)} enrollment={enrollment} onEnroll={enroll} onClear={() => setEnrollment(null)} />
    </> : null}
  </div>;
}
