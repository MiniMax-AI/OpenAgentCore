import { useEffect, useState } from "react";
import { AgentCoreError, type SandboxPlacement } from "@agents-core-web/agents-client";
import { SandboxDiagnostic } from "./SandboxDiagnostic";
import { useSandboxClient } from "./SandboxContext";

export function SessionPlacement({ sessionId }: { sessionId: string }) {
  const client = useSandboxClient();
  const [result, setResult] = useState<{ client: typeof client; sessionId: string; placement?: SandboxPlacement; error?: string; absent?: boolean } | null>(null);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!client) return;
    const controller = new AbortController();
    setResult(null);
    void client.retrieveSandboxPlacement(sessionId, { signal: controller.signal }).then((placement) => {
      if (!controller.signal.aborted) setResult({ client, sessionId, placement });
    }).catch((error) => {
      if (!controller.signal.aborted) setResult({ client, sessionId, ...(error instanceof AgentCoreError && error.status === 404 ? { absent: true } : { error: "Sandbox placement could not be loaded." }) });
    });
    return () => controller.abort();
  }, [client, sessionId, revision]);
  if (!client) return null;
  const current = result?.client === client && result.sessionId === sessionId ? result : null;
  return <div><dt>Sandbox node</dt><dd>
    {!current ? <span role="status">Loading placement…</span> : current.placement ? <>
      <strong>{current.placement.node_name}</strong> <code>{current.placement.node_id}</code>
      <div>{current.placement.available ? "Available" : "Unavailable"} · Recorded allocation: {current.placement.state} · Recorded compute: {current.placement.compute_phase}</div>
      <SandboxDiagnostic diagnostic={current.placement.diagnostic} />
      <button type="button" className="button" onClick={() => setRevision((v) => v + 1)}>Refresh placement</button>
    </> : current.absent ? "No hosted placement" : <span role="alert">{current.error} <button type="button" onClick={() => setRevision((v) => v + 1)}>Retry placement</button></span>}
  </dd></div>;
}
