import { useState } from "react";
import type { SandboxDeployment } from "@agents-core-web/agents-client";
import { sandboxCoreOrigin } from "./core-origin";
import { enrollmentCommand } from "./enrollment-command";

export function NodeEnrollment({ deployment, initialCoreUrl, busy, enrollment, onEnroll, onClear }: {
  deployment: SandboxDeployment;
  initialCoreUrl: string;
  busy: boolean;
  enrollment: { token: string; expires_at: string } | null;
  onEnroll: () => Promise<void>;
  onClear: () => void;
}) {
  const [commandUrl, setCommandUrl] = useState(initialCoreUrl);
  const coreUrl = sandboxCoreOrigin(deployment.core_url || commandUrl);
  return <section className="form-stack sandbox-enrollment" aria-labelledby="sandbox-enrollment-heading"><h2 id="sandbox-enrollment-heading">Add node</h2>
    <ol className="sandbox-steps">
      <li>Install <code>parsar-sandbox-node</code> from the same Core release on a Linux host. {deployment.provider === "docker" ? "Prepare its local Docker Unix socket and pinned runtime image." : "Prepare KVM access and the qualified microsandbox runtime, helper and firmware."}</li>
      <li>Create a private <code>/etc/parsar/sandbox-node.json</code> provider configuration with provider <code>{deployment.provider}</code> and installation ID <code>{deployment.installation_id}</code>. Use this host’s own backend paths and image. <a href="https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host" target="_blank" rel="noreferrer">Node configuration guide</a></li>
      <li>Generate the command below. Adjust its absolute paths, node name and capacity, then execute it on that host. Keep its private state directory on persistent storage.</li>
      <li>Run the node under the host’s service supervisor. Refresh sandbox state here and check that the node is online and its provider is ready.</li>
    </ol>
    <label className="field"><span>Core URL reachable from the node</span><input type="url" placeholder="https://core.example" value={deployment.core_url || commandUrl} onChange={(event) => setCommandUrl(event.target.value)} readOnly={Boolean(deployment.core_url)} disabled={busy || Boolean(enrollment)} /></label>
    <p>This Core origin must also be reachable from sandbox guests. Every added node uses this deployment’s {deployment.provider} provider.</p>
    {!enrollment ? <button type="button" className="button primary" disabled={busy || !coreUrl} onClick={() => void onEnroll()}>Generate enrollment command</button> : <>
      <p>One-time enrollment token expires {new Date(enrollment.expires_at).toLocaleString()}. Save the command now; it is cleared when you leave this page.</p>
      <label className="field"><span>One-time enrollment command</span><textarea readOnly rows={12} value={coreUrl ? enrollmentCommand(enrollment.token, coreUrl) : ""} onFocus={(event) => event.target.select()} spellCheck={false} /></label>
      <button type="button" className="button" onClick={onClear}>Clear enrollment command</button>
    </>}
  </section>;
}
