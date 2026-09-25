import type { SandboxNode } from "@agents-core-web/agents-client";
import { useQuery } from "@tanstack/react-query";
import { Cloud, Network } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ConsolePopover } from "../../components/console-popover";
import { StatusDot, type Tone } from "../../components/console-ui";
import { formatBytes, formatDateTime, formatDuration, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { nodeHealth, suspendedSandboxes, type NodeHealth } from "../fleet/fleet-model";
import { coreMetricsQuery } from "../metrics/metrics-queries";
import { seconds } from "../sandbox/NodeList";

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

/** E2B's cloud in place of machines: what Core holds there. */
export interface CloudHost {
  running: number;
  pending: number;
  template: string | null;
}

/**
 * Core in the middle, sandbox nodes left and right of it. Lines are solid and
 * animated for connected nodes and dashed for offline ones. Core and each node
 * open a popover with a glance at their state and the pages that go deeper.
 */
export function FleetTopology({ nodes, cloud, coreLabel, coreTone, stale, onOpenNode, onOpenBackend, onOpenSandboxMetrics, onOpenCoreMetrics }: {
  nodes: readonly SandboxNode[];
  /** An E2B deployment: Core links to E2B's cloud instead of to machines. */
  cloud?: CloudHost | null;
  onOpenBackend?: () => void;
  coreLabel: string;
  coreTone: Tone;
  /** The last refresh failed: keep the picture, stop implying live traffic. */
  stale: boolean;
  onOpenNode: (node: SandboxNode) => void;
  onOpenSandboxMetrics: () => void;
  onOpenCoreMetrics: () => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const shown = cloud ? [] : topologyNodes(nodes);
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
        {cloud ? (
          <g className="fleet-link fleet-link-available">
            <path d={`M 500 ${middle} L 700 ${middle}`} className="fleet-link-edge" />
            <path d={`M 500 ${middle} L 700 ${middle}`} className="fleet-link-flow" />
          </g>
        ) : null}
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
      <ConsolePopover
        trigger={(
          <button type="button" className="fleet-core" aria-label={`${t("fleet.core")}, ${coreLabel}`}>
            <Network size={20} strokeWidth={1.4} aria-hidden="true" />
            <strong>{t("fleet.core")}</strong>
            <StatusDot tone={coreTone} label={coreLabel} />
          </button>
        )}
        title={t("fleet.core")}
        actions={<button className="text-action" type="button" onClick={onOpenCoreMetrics}>{t("fleet.openCoreMetrics")}</button>}
      >
        <CoreGlance label={coreLabel} tone={coreTone} />
      </ConsolePopover>
      {cloud ? (
        <ConsolePopover
          side="left"
          trigger={(
            <button
              type="button"
              className="fleet-node fleet-node-right"
              style={{ top: middle }}
              aria-label={t("fleet.cloud.open", { running: formatInteger(cloud.running, locale) })}
            >
              <strong className="fleet-node-name"><Cloud size={14} strokeWidth={1.5} aria-hidden="true" />{t("fleet.cloud.name")}</strong>
              <span className="fleet-node-foot">
                <StatusDot tone="ok" label={t("fleet.cloud.running", { count: cloud.running })} />
              </span>
            </button>
          )}
          title={t("fleet.cloud.name")}
          actions={<>
            <button className="text-action" type="button" onClick={onOpenSandboxMetrics}>{t("fleet.openSandboxMetrics")}</button>
            {onOpenBackend ? <button className="text-action" type="button" onClick={onOpenBackend}>{t("fleet.cloud.openBackend")}</button> : null}
          </>}
        >
          <Facts>
            <Fact label={t("fleet.cloud.runningLabel")}>{formatInteger(cloud.running, locale)}</Fact>
            <Fact label={t("fleet.cloud.pending")}>{formatInteger(cloud.pending, locale)}</Fact>
            <Fact label={t("fleet.cloud.template")}>{cloud.template ? <code>{cloud.template}</code> : MISSING}</Fact>
          </Facts>
        </ConsolePopover>
      ) : null}
      {placed.map(({ node, left, y, health }) => {
        const name = node.name || node.id;
        const state = t(`nodeHealth.${health}`);
        const slots = `${formatInteger(node.active, locale)} / ${formatInteger(node.max_active, locale)}`;
        return (
          <ConsolePopover
            key={node.id}
            side={left ? "right" : "left"}
            trigger={(
              <button
                type="button"
                className={left ? "fleet-node fleet-node-left" : "fleet-node fleet-node-right"}
                style={{ top: y }}
                aria-label={t("fleet.open", { name, state, slots })}
              >
                <strong className="fleet-node-name">{name}</strong>
                <span className="fleet-node-foot">
                  <StatusDot tone={healthTone[health]} label={state} />
                  <span className="fleet-node-slots">{slots}</span>
                </span>
              </button>
            )}
            title={name}
            meta={node.name ? <code>{node.id}</code> : undefined}
            actions={<>
              <button className="text-action" type="button" onClick={onOpenSandboxMetrics}>{t("fleet.openSandboxMetrics")}</button>
              <button className="text-action" type="button" onClick={() => onOpenNode(node)}>{t("fleet.openNode")}</button>
            </>}
          >
            <NodeGlance node={node} health={health} />
          </ConsolePopover>
        );
      })}
    </div>
  );
}

function Facts({ children }: { children: ReactNode }) {
  return <dl className="console-popover-facts">{children}</dl>;
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}

/** A node at a glance: reachability, sandbox slots and what the node has left. */
function NodeGlance({ node, health }: { node: SandboxNode; health: NodeHealth }) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  const seen = seconds(node.last_seen_at);
  const count = (value: number) => formatInteger(value, locale);
  return (
    <Facts>
      <Fact label={t("fleet.facts.status")}><StatusDot tone={healthTone[health]} label={t(`nodeHealth.${health}`)} /></Fact>
      <Fact label={t("fleet.facts.lastSeen")}><span title={seen === null ? undefined : formatDateTime(seen, locale)}>{seen === null ? t("fleet.facts.never") : formatRelative(seen, now, locale)}</span></Fact>
      <Fact label={t("fleet.facts.active")}>{count(node.active)}<span className="kpi-unit">/ {count(node.max_active)}</span></Fact>
      {node.provider === "microsandbox" ? <Fact label={t("fleet.facts.suspended")}>{count(suspendedSandboxes(node))}</Fact> : null}
      <Fact label={t("fleet.facts.cpu")}>{node.cpu_count === null ? MISSING : t("fleet.facts.cores", { count: node.cpu_count })}</Fact>
      <Fact label={t("fleet.facts.memory")}>{formatBytes(node.available_memory_bytes)}</Fact>
      <Fact label={t("fleet.facts.disk")}>{formatBytes(node.available_disk_bytes)}</Fact>
    </Facts>
  );
}

/**
 * Core at a glance: its status, and — once Core reports its own metrics — its
 * uptime, execution slots, Turn queue, connected daemons and database latency.
 */
function CoreGlance({ label, tone }: { label: string; tone: Tone }) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const query = useQuery({ ...coreMetricsQuery("1h"), retry: false });
  const metrics = query.data ?? null;
  const count = (value: number | null) => (value === null ? MISSING : formatInteger(value, locale));
  const started = seconds(metrics?.service.started_at ?? null);
  const now = Math.floor(Date.now() / 1000);
  return (
    <Facts>
      <Fact label={t("fleet.facts.status")}><StatusDot tone={tone} label={label} /></Fact>
      {metrics ? <>
        <Fact label={t("fleet.facts.uptime")}>{started === null ? MISSING : formatDuration(Math.max(0, now - started))}</Fact>
        <Fact label={t("fleet.facts.slots")}>
          {count(metrics.execution.slots_in_use)}
          {metrics.execution.slots_total === null ? null : <span className="kpi-unit">/ {count(metrics.execution.slots_total)}</span>}
        </Fact>
        <Fact label={t("fleet.facts.queued")}>{count(metrics.execution.queued_turns)}</Fact>
        <Fact label={t("fleet.facts.daemons")}>{count(metrics.execution.connected_daemons)}</Fact>
        <Fact label={t("fleet.facts.database")}>{metrics.database.ping_ms.p95 === null ? MISSING : formatDuration(metrics.database.ping_ms.p95 / 1000)}</Fact>
      </> : null}
    </Facts>
  );
}
