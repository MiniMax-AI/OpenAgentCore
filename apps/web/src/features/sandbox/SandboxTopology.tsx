import { Network, Server } from "lucide-react";
import type { SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxNodeStatus } from "../../lib/sandbox-labels";
import "./SandboxTopology.css";

export function SandboxTopology({ nodes, allocations, stale, selectedId, onSelect }: {
  nodes: SandboxNode[];
  allocations: SandboxAllocation[];
  stale: boolean;
  selectedId: string | null;
  onSelect: (id: string) => void;
}) {
  const { t, locale } = useLocale();
  const rows = Math.ceil(nodes.length / 2);
  const height = Math.max(460, rows * 168 + 112);
  const positioned = nodes.map((node, index) => {
    const left = index % 2 === 0;
    const count = left ? rows : Math.floor(nodes.length / 2);
    const y = height / 2 + (Math.floor(index / 2) - (count - 1) / 2) * 168;
    const state = stale || !node.online ? "offline" : node.provider_ready ? "online" : "warning";
    return { node, left, y, state };
  });
  return <div className="sandbox-topology-scroll" role="region" aria-label={t("Sandbox nodes")}>
    <div className="sandbox-topology" style={{ height }}>
      <div className="sandbox-topology-caption"><span className="sandbox-topology-eyebrow">{t("Node network")}</span><span>{t("Select a node to inspect it")}</span></div>
      <svg className="sandbox-topology-lines" viewBox={`0 0 1000 ${height}`} preserveAspectRatio="none" aria-hidden="true">
        {positioned.map(({ node, left, y, state }) => {
          const end = left ? 320 : 680;
          const bend = left ? 365 : 635;
          const path = `M 500 ${height / 2} C ${bend} ${height / 2}, ${bend} ${y}, ${end} ${y}`;
          return <g key={node.id} className={`sandbox-topology-connection ${state}`}>
            <path d={path} className="sandbox-topology-edge" />
            {state !== "offline" ? <path d={path} className="sandbox-topology-flow" /> : null}
            <circle cx={end} cy={y} r="3.5" className="sandbox-topology-port" />
          </g>;
        })}
      </svg>
      <div className="sandbox-topology-core" aria-label="Core"><span className="sandbox-core-orbit" /><span className="sandbox-core-orbit outer" /><div className="sandbox-core-body"><Network size={27} strokeWidth={1.35} aria-hidden="true" /><strong>Core</strong></div></div>
      {positioned.map(({ node, left, y, state }) => <button
        key={node.id}
        type="button"
        className={`sandbox-topology-node ${state} ${selectedId === node.id ? "selected" : ""}`}
        style={{ left: left ? "8%" : "68%", top: y }}
        aria-pressed={selectedId === node.id}
        aria-controls={selectedId === node.id ? "sandbox-selected-node" : undefined}
        onClick={() => onSelect(node.id)}
      >
        <span className="sandbox-topology-node-header"><span className="sandbox-topology-node-icon"><Server size={18} strokeWidth={1.5} aria-hidden="true" /></span><strong>{node.name}</strong><span className="sandbox-topology-node-arrow" aria-hidden="true">↗</span></span>
        <span className="sandbox-topology-node-status"><span className="sandbox-status-dot" />{sandboxNodeStatus(node, stale, locale)}</span>
        <span className="sandbox-topology-node-capacity"><span>{t("Sandboxes / capacity")}</span><strong>{node.active}<span> / {node.max_active}</span></strong></span>
        {node.cleanup_pending > 0 || node.diagnostic || allocations.some((allocation) => allocation.node_id === node.id && allocation.diagnostic) ? <span className="sandbox-topology-attention">{t("Needs attention")}</span> : null}
      </button>)}
      <div className="sandbox-topology-legend"><span><i className="online" />{t("Online")}</span><span><i />{t("Offline")}</span></div>
    </div>
  </div>;
}
