import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode } from "@agents-core-web/agents-client";
import { isValidDirectCoreBaseUrl } from "../../lib/connection";
import { sandboxAdminBaseUrl } from "./SandboxContext";
import { SandboxDiagnostic } from "./SandboxDiagnostic";
import { NodeHealth } from "./NodeHealth";
import { enrollmentCommand } from "./enrollment-command";
import "./SandboxManagerView.css";

function message(error: unknown): string {
  return error instanceof Error ? error.message : "The sandbox request failed.";
}

export function SandboxManagerView({ coreBaseUrl }: { coreBaseUrl: string }) {
  const [draft, setDraft] = useState("");
  const [credential, setCredential] = useState("");
  function connect(event: FormEvent) {
    event.preventDefault();
    if (draft.trim()) { setCredential(draft.trim()); setDraft(""); }
  }
  return <section className="sandbox-manager">
    <header className="sandbox-heading"><div><h1>Hosted Sandbox Manager</h1><p>Deployment provider, runtime nodes and Session allocations.</p></div>
      {credential ? <button type="button" className="button" onClick={() => setCredential("")}>Disconnect admin</button> : null}
    </header>
    {!credential ? <form className="form-stack sandbox-access" onSubmit={connect}>
      <h2>Deployment administrator access</h2>
      <p>Enter the separate deployment admin key. It stays in memory until you leave this page or disconnect.</p>
      <label className="field"><span>Deployment admin key</span><input type="password" autoComplete="off" value={draft} onChange={(event) => setDraft(event.target.value)} required /></label>
      <button type="submit" className="button primary" disabled={!draft.trim()}>Connect admin</button>
    </form> : <SandboxManager key={`${coreBaseUrl}:${credential}`} coreBaseUrl={coreBaseUrl} credential={credential} />}
  </section>;
}

function SandboxManager({ coreBaseUrl, credential }: { coreBaseUrl: string; credential: string }) {
  const client = useMemo(() => new SandboxAdminClient({ baseUrl: sandboxAdminBaseUrl(coreBaseUrl), token: credential }), [coreBaseUrl, credential]);
  const [snapshot, setSnapshot] = useState<{ deployment: SandboxDeployment; nodes: SandboxNode[]; allocations: SandboxAllocation[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const [removeId, setRemoveId] = useState<string | null>(null);
  const [enrollment, setEnrollment] = useState<{ token: string; expires_at: string } | null>(null);
  const [coreUrl, setCoreUrl] = useState(() => coreBaseUrl.startsWith("http") ? coreBaseUrl.replace(/\/v1\/?$/, "") : "");
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
      if (!controller.signal.aborted) setSnapshot({ deployment, nodes: nodes.data, allocations: allocations.flatMap((page) => page.data) });
    })().catch((error) => { if (!controller.signal.aborted) setError(message(error)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [client, revision]);
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
    <div className="sandbox-toolbar"><button type="button" className="button" disabled={loading || busy} onClick={() => setRevision((v) => v + 1)}>Refresh sandbox state</button>{loading ? <span role="status">Loading sandbox state…</span> : null}</div>
    {error ? <p role="alert" className="sandbox-error">{error}{snapshot ? " Previously loaded state is shown below." : ""}</p> : null}
    {snapshot ? <>
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
      <section className="form-stack sandbox-enrollment" aria-labelledby="sandbox-enrollment-heading"><h2 id="sandbox-enrollment-heading">Add node</h2>
        <p>Install parsar-sandbox-node and prepare its {snapshot.deployment.provider} provider configuration on the target host. Adjust the absolute paths, node name and capacity in the command before running it.</p>
        <label className="field"><span>Core URL reachable from the node</span><input type="url" placeholder="https://core.example" value={coreUrl} onChange={(event) => setCoreUrl(event.target.value)} disabled={busy || Boolean(enrollment)} /></label>
        {!enrollment ? <button type="button" className="button primary" disabled={busy || !isValidDirectCoreBaseUrl(coreUrl)} onClick={() => void enroll()}>Generate enrollment command</button> : <>
          <p>One-time enrollment token expires {new Date(enrollment.expires_at).toLocaleString()}. Save the command now; it is cleared when you leave this page.</p>
          <label className="field"><span>One-time enrollment command</span><textarea readOnly rows={12} value={enrollmentCommand(enrollment.token, coreUrl.trim())} onFocus={(event) => event.target.select()} spellCheck={false} /></label>
          <button type="button" className="button" onClick={() => setEnrollment(null)}>Clear enrollment command</button>
        </>}
      </section>
    </> : null}
  </div>;
}
