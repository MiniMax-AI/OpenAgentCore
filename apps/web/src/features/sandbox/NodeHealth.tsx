import { SandboxDiagnostic } from "./SandboxDiagnostic";
import type { SandboxNode } from "@agents-core-web/agents-client";
function bytes(value: number | null): string {
  if (value === null) return "Unavailable";
  return `${(value / 1024 ** 3).toLocaleString(undefined, { maximumFractionDigits: 1 })} GiB`;
}
export function NodeHealth({ node }: { node: SandboxNode }) {
  return <div>
    <strong>{node.online ? "Online" : "Offline"} · {!node.online ? "Provider status unconfirmed" : node.provider_ready ? "Provider ready" : "Provider unavailable"}</strong>
    <SandboxDiagnostic diagnostic={!node.online ? "node_unavailable" : !node.provider_ready ? "provider_unavailable" : node.diagnostic} />
    <small>Last seen: {node.last_seen_at ? new Date(node.last_seen_at).toLocaleString() : "Never"}</small>
    {node.online ? <small>{node.cpu_count ?? "Unavailable"} CPUs · {bytes(node.available_memory_bytes)} memory free · {bytes(node.available_disk_bytes)} disk free</small> : <small>Host metrics unavailable (stale heartbeat)</small>}
  </div>;
}
