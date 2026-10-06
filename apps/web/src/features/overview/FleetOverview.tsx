import type { SandboxNode } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Cloud, Network, Server } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ConsolePopover } from "../../components/console-popover";
import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { formatBytes, formatDateTime, formatDuration, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { nodeProviderDiagnostic } from "../../lib/sandbox-diagnostic";
import { DiagnosticTip } from "../fleet/DiagnosticTip";
import { nodeHealth, suspendedSandboxes, type NodeHealth } from "../fleet/fleet-model";
import { coreMetricsQuery } from "../metrics/metrics-queries";
import { NodeRolloutStatus } from "../sandbox/NodeRolloutStatus";
import { seconds } from "../sandbox/NodeList";

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

/** A compact fleet inventory with on-demand operational details. */
export function FleetOverview({ nodes, cloud, coreLabel, coreTone, stale, onOpenNode, onOpenBackend, onOpenSandboxMetrics, onOpenCoreMetrics }: {
  nodes: readonly SandboxNode[];
  /** An E2B deployment: Core links to E2B's cloud instead of to machines. */
  cloud?: CloudHost | null;
  onOpenBackend?: () => void;
  coreLabel: string;
  coreTone: Tone;
  /** Older generation or failed refresh: retain observations without implying live traffic. */
  stale: boolean;
  onOpenNode: (node: SandboxNode) => void;
  onOpenSandboxMetrics: () => void;
  onOpenCoreMetrics: () => void;
}) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const shown = cloud ? [] : overviewNodes(nodes);
  return (
    <div className="fleet-inventory">
      <ConsolePopover
        trigger={(
          <button type="button" className="fleet-row fleet-core-row" aria-label={`${t("fleet.core")}, ${coreLabel}`}>
            <Network size={18} strokeWidth={1.75} aria-hidden="true" />
            <span className="fleet-row-info"><strong>{t("fleet.core")}</strong><StatusDot tone={coreTone} label={coreLabel} /></span>
            <ChevronRight size={14} aria-hidden="true" />
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
              className="fleet-row"
              aria-label={t("fleet.cloud.open", { running: formatInteger(cloud.running, locale) })}
            >
              <Cloud size={18} strokeWidth={1.75} aria-hidden="true" />
              <span className="fleet-row-info"><strong>{t("fleet.cloud.name")}</strong><StatusDot tone="ok" label={t("fleet.cloud.running", { count: cloud.running })} /></span>
              <ChevronRight size={14} aria-hidden="true" />
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
      {shown.map((node) => {
        const health = nodeHealth(node);
        const name = node.name || node.id;
        const state = t(`nodeHealth.${health}`);
        const slots = `${formatInteger(node.active, locale)} / ${formatInteger(node.max_active, locale)}`;
        return (
          <ConsolePopover
            key={node.id}
            side="left"
            trigger={(
              <button
                type="button"
                className="fleet-row fleet-node-row"
                aria-label={t("fleet.open", { name, state, slots })}
              >
                <Server size={18} strokeWidth={1.75} aria-hidden="true" />
                <span className="fleet-row-info"><strong>{name}</strong><StatusDot tone={healthTone[health]} label={state} /></span>
                <span className="fleet-node-slots">{slots}</span>
                <ChevronRight size={14} aria-hidden="true" />
              </button>
            )}
            title={name}
            meta={node.name ? <code>{node.id}</code> : undefined}
            actions={<>
              <button className="text-action" type="button" onClick={onOpenSandboxMetrics}>{t("fleet.openSandboxMetrics")}</button>
              <button className="text-action" type="button" onClick={() => onOpenNode(node)}>{t("fleet.openNode")}</button>
            </>}
          >
            <NodeGlance node={node} health={health} stale={stale} />
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

/** A node at a glance: reachability (with the reason a degraded provider is not ready), sandbox slots and what the node has left. */
function NodeGlance({ node, health, stale }: { node: SandboxNode; health: NodeHealth; stale: boolean }) {
  const { t, i18n } = useTranslation("overview");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  const seen = seconds(node.last_seen_at);
  const count = (value: number) => formatInteger(value, locale);
  const diagnostic = health === "degraded" ? nodeProviderDiagnostic(node) : "";
  return (
    <Facts>
      <Fact label={t("fleet.facts.status")}>
        <span className="status-with-help">
          <StatusDot tone={healthTone[health]} label={t(`nodeHealth.${health}`)} />
          {diagnostic ? <DiagnosticTip code={diagnostic} /> : null}
        </span>
      </Fact>
      <Fact label={t("fleet.targetPreparation")}><NodeRolloutStatus node={node} stale={stale} /></Fact>
      <Fact label={t("fleet.servingGeneration")}><span className="status-with-help">{node.rollout.ready_generation ?? MISSING}<HelpTip>{t("fleet.servingGenerationHelp")}</HelpTip></span></Fact>
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
