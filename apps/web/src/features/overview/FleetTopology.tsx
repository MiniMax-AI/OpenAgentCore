import type { SandboxNode } from "@agents-core-web/agents-client";
import { Network } from "lucide-react";
import { useTranslation } from "react-i18next";

import { StatusDot, type Tone } from "../../components/console-ui";
import { formatInteger } from "../../lib/format";
import { nodeHealth, type NodeHealth } from "../fleet/fleet-model";

/** Row pitch of the node columns, in pixels. */
const ROW = 72;
/** Nodes drawn around Core; the rest are counted and listed on the Nodes page. */
export const TOPOLOGY_LIMIT = 8;

const healthTone: Record<NodeHealth, Tone> = { available: "ok", degraded: "warning", offline: "danger" };
const healthRank: Record<NodeHealth, number> = { offline: 0, degraded: 1, available: 2 };

/** The nodes to draw: all of them, or the unhealthy ones first when they do not fit. */
export function topologyNodes(nodes: readonly SandboxNode[]): SandboxNode[] {
  if (nodes.length <= TOPOLOGY_LIMIT) return [...nodes];
  return nodes
    .map((node, index) => ({ node, index }))
    .sort((a, b) => healthRank[nodeHealth(a.node)] - healthRank[nodeHealth(b.node)] || a.index - b.index)
    .slice(0, TOPOLOGY_LIMIT)
    .map(({ node }) => node);
}

/**
 * Core in the middle, sandbox nodes left and right of it. Lines are solid and
 * animated for connected nodes and dashed for offline ones; a node opens its
 * page on the Nodes view.
 */
export function FleetTopology({ nodes, coreLabel, coreTone, stale, onOpen }: {
  nodes: readonly SandboxNode[];
  coreLabel: string;
  coreTone: Tone;
  /** The last refresh failed: keep the picture, stop implying live traffic. */
  stale: boolean;
  onOpen: (node: SandboxNode) => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const shown = topologyNodes(nodes);
  const leftCount = Math.ceil(shown.length / 2);
  const rightCount = shown.length - leftCount;
  const height = Math.max(216, leftCount * ROW + 24);
  const middle = height / 2;
  const placed = shown.map((node, index) => {
    const left = index % 2 === 0;
    const count = left ? leftCount : rightCount;
    const y = middle + (Math.floor(index / 2) - (count - 1) / 2) * ROW;
    return { node, left, y, health: nodeHealth(node) };
  });
  return (
    <div className={stale ? "fleet-topology fleet-topology-stale" : "fleet-topology"} style={{ height }}>
      <svg className="fleet-topology-lines" viewBox={`0 0 1000 ${height}`} preserveAspectRatio="none" aria-hidden="true">
        {placed.map(({ node, left, y, health }) => {
          const end = left ? 300 : 700;
          const bend = left ? 380 : 620;
          const path = `M 500 ${middle} C ${bend} ${middle}, ${bend} ${y}, ${end} ${y}`;
          return (
            <g key={node.id} className={`fleet-link fleet-link-${health}`}>
              <path d={path} className="fleet-link-edge" />
              {health !== "offline" ? <path d={path} className="fleet-link-flow" /> : null}
            </g>
          );
        })}
      </svg>
      <div className="fleet-core">
        <Network size={20} strokeWidth={1.4} aria-hidden="true" />
        <strong>{t("fleet.core")}</strong>
        <StatusDot tone={coreTone} label={coreLabel} />
      </div>
      {placed.map(({ node, left, y, health }) => {
        const name = node.name || node.id;
        const state = t(`nodeHealth.${health}`);
        const slots = `${formatInteger(node.active, locale)} / ${formatInteger(node.max_active, locale)}`;
        return (
          <button
            key={node.id}
            type="button"
            className={left ? "fleet-node fleet-node-left" : "fleet-node fleet-node-right"}
            style={{ top: y }}
            title={node.id}
            aria-label={t("fleet.open", { name, state, slots })}
            onClick={() => onOpen(node)}
          >
            <span className="fleet-node-head">
              <strong>{name}</strong>
              <span className="fleet-node-slots">{slots}</span>
            </span>
            <StatusDot tone={healthTone[health]} label={state} />
          </button>
        );
      })}
    </div>
  );
}
