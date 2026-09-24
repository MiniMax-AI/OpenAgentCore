import type { SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";

import { HelpTip, Meter, StatusDot } from "../../components/console-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import type { OwnedRuntimeObservation } from "../../lib/admin-view";
import { formatBytes, formatCores, formatInteger, formatRelative, MISSING } from "../../lib/format";
import type { CapacitySummary } from "../fleet/fleet-model";
import { sandboxAttention, type SandboxAttention } from "./sandbox-attention";
import type { HostedRuntimeUsage } from "./sandbox-runtime";

const ATTENTION_SHOWN = 3;

/** One segment per node, sized by its limit; the fill is its running sandboxes. */
function CapacityBar({ nodes }: { nodes: readonly SandboxNode[] }) {
  const { t } = useTranslation("metrics");
  if (!nodes.length) return <span className="capacity-bar capacity-bar-empty" aria-hidden="true" />;
  return (
    <span className="capacity-bar" role="img" aria-label={nodes.map((node) => node.online
      ? t("sandbox.summary.segment", { name: node.name || node.id, active: node.active, max: node.max_active })
      : t("sandbox.summary.segmentOffline", { name: node.name || node.id })).join(", ")}
    >
      {nodes.map((node) => {
        const ratio = node.max_active > 0 ? Math.min(1, node.active / node.max_active) : 0;
        const title = node.online
          ? t("sandbox.summary.segment", { name: node.name || node.id, active: node.active, max: node.max_active })
          : t("sandbox.summary.segmentOffline", { name: node.name || node.id });
        return (
          <span key={node.id} className={node.online ? "capacity-segment" : "capacity-segment capacity-segment-offline"} style={{ flexGrow: Math.max(1, node.max_active) }} title={title}>
            {node.online ? <span className="capacity-segment-fill" style={{ width: `${ratio * 100}%` }} /> : null}
          </span>
        );
      })}
    </span>
  );
}

function attentionText(item: SandboxAttention, t: ReturnType<typeof useTranslation<"metrics">>["t"]): string {
  switch (item.kind) {
    case "offline": return t("sandbox.summary.offline", { name: item.node.name || item.node.id });
    case "degraded": return t("sandbox.summary.degraded", { name: item.node.name || item.node.id });
    case "cleanup": return t("sandbox.summary.cleanup", { name: item.node.name || item.node.id, n: item.count });
    case "allocation": return t("sandbox.summary.allocation", { name: item.node.name || item.node.id, n: item.count });
    case "unobservable": return t("sandbox.summary.unobservable", { n: item.count });
  }
}

/**
 * The page's answer in one band: how full the sandbox slots are, what the
 * hosted sandboxes use, and what needs attention. Figures are large only where
 * they are the answer; units and limits stay small.
 */
export function SandboxSummary({ capacity, nodes, allocations, observations, usage, fleetMessage }: {
  capacity: CapacitySummary | null;
  nodes: readonly SandboxNode[];
  allocations: readonly SandboxAllocation[];
  observations: readonly OwnedRuntimeObservation[] | null;
  usage: HostedRuntimeUsage | null;
  /** Why fleet figures are missing (unconfigured, failed, loading). */
  fleetMessage: string;
}) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const now = Math.floor(Date.now() / 1000);
  const items = capacity ? sandboxAttention(nodes, allocations, observations ?? []) : null;
  const shown = items?.slice(0, ATTENTION_SHOWN) ?? [];
  const cpuKnown = usage && usage.cpuUsageCores !== null;
  const memoryKnown = usage && usage.memoryUsageBytes !== null;

  return (
    <section className="sandbox-summary" aria-label={t("sandbox.summary.label")}>
      <div className="summary-zone">
        <header className="summary-label">{t("sandbox.summary.capacity")}<HelpTip>{t("sandbox.summary.capacityHelp")}</HelpTip></header>
        <p className="summary-figure">
          <strong>{capacity ? formatInteger(capacity.active, locale) : MISSING}</strong>
          {capacity ? <span>/ {formatInteger(capacity.maxActive, locale)}</span> : null}
        </p>
        <CapacityBar nodes={nodes} />
        <p className="summary-sub">{capacity ? t("sandbox.summary.nodesOnline", { online: capacity.online, total: capacity.nodes }) : fleetMessage}</p>
      </div>

      <div className="summary-zone">
        <header className="summary-label">{t("sandbox.summary.resources")}<HelpTip>{t("sandbox.summary.resourcesHelp")}</HelpTip></header>
        <dl className="summary-resources">
          <div>
            <dt>{t("sandbox.cpu")}</dt>
            <dd>
              <Meter value={cpuKnown ? usage.cpuUsageCores : null} limit={usage?.cpuCapacityCores ?? null} label={t("sandbox.cpu")} />
              <span>{cpuKnown ? <><strong>{formatCores(usage.cpuUsageCores, locale)}</strong> / {t("sandbox.cores", { value: formatCores(usage.cpuCapacityCores, locale) })}</> : MISSING}</span>
            </dd>
          </div>
          <div>
            <dt>{t("sandbox.memory")}</dt>
            <dd>
              <Meter value={memoryKnown ? usage.memoryUsageBytes : null} limit={usage?.memoryLimitBytes ?? null} label={t("sandbox.memory")} />
              <span>{memoryKnown ? <><strong>{formatBytes(usage.memoryUsageBytes)}</strong> / {formatBytes(usage.memoryLimitBytes)}</> : MISSING}</span>
            </dd>
          </div>
        </dl>
        <p className="summary-sub">{usage ? t("sandbox.summary.runtimes", { active: formatInteger(usage.active, locale), sleeping: formatInteger(usage.sleeping, locale) }) : MISSING}</p>
      </div>

      <div className="summary-zone">
        <header className="summary-label">
          {t("sandbox.summary.attention")}
          {items?.length ? <span className="pill pill-orange">{items.length}</span> : null}
          <HelpTip>{t("sandbox.summary.attentionHelp")}</HelpTip>
        </header>
        {items === null ? <p className="summary-sub">{fleetMessage}</p> : items.length ? (
          <ul className="summary-attention">
            {shown.map((item, index) => {
              const text = attentionText(item, t);
              const node = "node" in item ? item.node : null;
              return (
                <li key={`${item.kind}:${node?.id ?? index}`}>
                  <button type="button" className="summary-attention-row" onClick={() => (node ? navigate("nodes", { id: node.id }) : navigate("sandbox-metrics"))} disabled={!node}>
                    <StatusDot tone={item.tone} label={text} />
                    {item.kind === "offline" ? <span className="summary-attention-time">{formatRelative(item.lastSeen, now, locale)}</span> : null}
                  </button>
                </li>
              );
            })}
            {items.length > shown.length ? <li className="summary-sub">{t("sandbox.summary.more", { n: items.length - shown.length })}</li> : null}
          </ul>
        ) : <p className="summary-clear"><StatusDot tone="ok" label={t("sandbox.summary.allClear")} /></p>}
      </div>
    </section>
  );
}
