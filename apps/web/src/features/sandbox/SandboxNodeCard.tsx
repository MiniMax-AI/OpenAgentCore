import { Server } from "lucide-react";
import type { SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxNodeStatus, sandboxStateLabel } from "../../lib/sandbox-labels";
import { NodeHealth } from "./NodeHealth";
import { SandboxDiagnostic } from "./SandboxDiagnostic";

export function SandboxNodeCard({ node, allocations, stale, disabled, confirming, onRemove, onConfirm, onCancel }: {
  node: SandboxNode;
  allocations: SandboxAllocation[];
  stale: boolean;
  disabled: boolean;
  confirming: boolean;
  onRemove: () => void;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const { t, locale } = useLocale();
  const ready = !stale && node.online && node.provider_ready;
  const status = sandboxNodeStatus(node, stale, locale);
  const memory = node.available_memory_bytes === null ? t("Unavailable") : `${(node.available_memory_bytes / 1024 ** 3).toLocaleString(locale, { maximumFractionDigits: 1 })} GiB`;
  return <article className="sandbox-node-card">
    <div className="sandbox-node-top"><span className="sandbox-node-icon"><Server size={20} strokeWidth={1.5} /></span><h3>{node.name}</h3><span className={`sandbox-node-status ${ready ? "ready" : "unavailable"}`}><span className="sandbox-status-dot" />{status}</span></div>
    <dl className="sandbox-node-capacity"><div><dt>{t("Sandboxes / capacity")}</dt><dd>{node.active}<span> / {node.max_active}</span></dd></div><div><dt>{t("Running")}</dt><dd>{stale || !node.online ? "—" : node.running}</dd></div></dl>
    <p className="sandbox-node-resources">{stale || !node.online ? t("Host metrics unavailable (stale heartbeat)") : `${node.cpu_count ?? t("Unavailable")} ${t("CPUs")} · ${memory} ${t("memory free")}`}</p>
    {node.cleanup_pending > 0 ? <p className="sandbox-node-warning">{t("Cleanup pending")}: {node.cleanup_pending}</p> : null}
    {node.diagnostic && node.online ? <SandboxDiagnostic diagnostic={node.diagnostic} /> : null}
    {allocations.some((allocation) => allocation.diagnostic) ? <p className="sandbox-node-warning">{t("Sandbox resources need attention")}</p> : null}
    <details className="sandbox-node-details" open><summary>{t("Node details")}</summary><div className="sandbox-node-detail-body form-stack">
      <dl className="sandbox-summary"><div><dt>{t("Node ID")}</dt><dd><code>{node.id}</code></dd></div><div><dt>{t("Retained / limit")}</dt><dd>{node.retained} / {node.max_retained}</dd></div><div><dt>{t("Snapshots")}</dt><dd>{node.snapshots}</dd></div><div><dt>{t("Reserved")}</dt><dd>{node.reserved}</dd></div><div><dt>{t("Cleanup pending")}</dt><dd>{node.cleanup_pending}</dd></div></dl>
      {stale ? <p>{t("Previously loaded state is shown below.")}</p> : <NodeHealth node={node} />}
      <section aria-label={t("Sandbox allocations")}><h4>{t("Allocations")}</h4>{allocations.length ? <div className="sandbox-allocation-list">{allocations.map((allocation) => <div key={allocation.id} className="sandbox-allocation"><code>{allocation.session_id}</code><span>{sandboxStateLabel(allocation.state, locale)} · {sandboxStateLabel(allocation.compute_phase, locale)}</span><SandboxDiagnostic diagnostic={allocation.diagnostic} />{!allocation.diagnostic ? <small>{t("No reported issue")}</small> : null}</div>)}</div> : <p>{t("No sandbox allocations.")}</p>}</section>
      {confirming ? <div className="sandbox-confirm" role="group" aria-label={t("Confirm node removal")}><p>{t("Remove node")} {node.name}? {t("Core rejects removal while allocations or retained resources remain.")}</p><div className="sandbox-actions"><button type="button" className="button outline danger" disabled={disabled} onClick={onConfirm}>{t("Confirm removal")}</button><button type="button" className="button outline" disabled={disabled} onClick={onCancel}>{t("Cancel removal")}</button></div></div> : <button type="button" className="button outline danger sandbox-remove" disabled={disabled} aria-label={`${t("Remove")} ${node.name}`} onClick={onRemove}>{t("Remove node")}</button>}
    </div></details>
  </article>;
}
