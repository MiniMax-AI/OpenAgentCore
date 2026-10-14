import type { SandboxNode } from "@oac/agents-client";
import { ChevronRight, Cloud, Network, Server } from "lucide-react";
import { useTranslation } from "react-i18next";

import { StatusDot, type Tone } from "../../components/console-ui";
import { formatInteger } from "../../lib/format";
import { nodeHealth, type NodeHealth } from "../fleet/fleet-model";

/** Nodes shown in the overview; the rest are counted and listed on the Nodes page. */
export const FLEET_LIMIT = 4;

const healthTone: Record<NodeHealth, Tone> = { available: "ok", degraded: "warning", offline: "danger" };
const healthRank: Record<NodeHealth, number> = { offline: 0, degraded: 1, available: 2 };

/** The nodes to show: all of them, or the unhealthy ones first when they do not fit. */
export function overviewNodes(nodes: readonly SandboxNode[]): SandboxNode[] {
  if (nodes.length <= FLEET_LIMIT) return [...nodes];
  return nodes
    .map((node, index) => ({ node, index }))
    .sort((a, b) => healthRank[nodeHealth(a.node)] - healthRank[nodeHealth(b.node)] || a.index - b.index)
    .slice(0, FLEET_LIMIT)
    .map(({ node }) => node);
}

/** E2B's cloud in place of machines: what Core holds there. */
export interface CloudHost {
  running: number;
  pending: number;
  template: string | null;
}

/** A compact fleet inventory: Core, then E2B's cloud or the nodes; each row opens its page. */
export function FleetOverview({ nodes, cloud, coreLabel, coreTone, onOpenNode, onOpenCloud, onOpenCore }: {
  nodes: readonly SandboxNode[];
  /** A direct deployment: Core links to the Provider's cloud instead of to machines. */
  cloud?: CloudHost | null;
  coreLabel: string;
  coreTone: Tone;
  onOpenNode: (node: SandboxNode) => void;
  onOpenCloud: () => void;
  onOpenCore: () => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const shown = cloud ? [] : overviewNodes(nodes);
  return (
    <div className="fleet-inventory">
      <button type="button" className="fleet-row fleet-core-row" aria-label={`${t("fleet.core")}, ${coreLabel}`} onClick={onOpenCore}>
        <Network size={18} strokeWidth={1.75} aria-hidden="true" />
        <span className="fleet-row-info"><strong>{t("fleet.core")}</strong><StatusDot tone={coreTone} label={coreLabel} /></span>
        <ChevronRight size={14} aria-hidden="true" />
      </button>
      {cloud ? (
        <button type="button" className="fleet-row" aria-label={t("fleet.cloud.open", { running: formatInteger(cloud.running, locale) })} onClick={onOpenCloud}>
          <Cloud size={18} strokeWidth={1.75} aria-hidden="true" />
          <span className="fleet-row-info"><strong>{t("fleet.cloud.name")}</strong><StatusDot tone="ok" label={t("fleet.cloud.running", { count: cloud.running })} /></span>
          <ChevronRight size={14} aria-hidden="true" />
        </button>
      ) : null}
      {shown.map((node) => {
        const health = nodeHealth(node);
        const name = node.name || node.id;
        const state = t(`nodeHealth.${health}`);
        const slots = `${formatInteger(node.active, locale)} / ${formatInteger(node.max_active, locale)}`;
        return (
          <button key={node.id} type="button" className="fleet-row fleet-node-row" aria-label={t("fleet.open", { name, state, slots })} onClick={() => onOpenNode(node)}>
            <Server size={18} strokeWidth={1.75} aria-hidden="true" />
            <span className="fleet-row-info"><strong>{name}</strong><StatusDot tone={healthTone[health]} label={state} /></span>
            <span className="fleet-node-slots">{slots}</span>
            <ChevronRight size={14} aria-hidden="true" />
          </button>
        );
      })}
    </div>
  );
}
