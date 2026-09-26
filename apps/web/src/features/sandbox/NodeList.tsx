import type { SandboxAllocation, SandboxNode } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";

import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { NameCell, RowActions } from "../../components/list-ui";
import { formatDateTime, formatRelative } from "../../lib/format";
import type { MessageKey } from "../../lib/locale-strings";
import { nodeProviderDiagnostic } from "../../lib/sandbox-diagnostic";
import { DiagnosticTip } from "../fleet/DiagnosticTip";
import { nodeHealth, suspendedSandboxes } from "../fleet/fleet-model";

export type NodeState = "unconfirmed" | "old_address" | "offline" | "degraded" | "attention" | "available";

/**
 * Whether a node enrolled with another Core address than the deployment's
 * `coreUrl`. An empty address is unknown, not old: a node Core did not enroll,
 * such as a file-managed local one, reports none.
 */
export function onOldAddress(node: SandboxNode, coreUrl: string): boolean {
  return Boolean(node.core_url && coreUrl && node.core_url !== coreUrl);
}

/**
 * One status per node: stale data first; then a node enrolled with another
 * address than the deployment's `coreUrl`, which gets no new sandboxes until it
 * is removed and added again; then reachability, then anything reported to look at.
 */
export function nodeState(node: SandboxNode, allocations: readonly SandboxAllocation[], stale: boolean, coreUrl: string): NodeState {
  if (stale) return "unconfirmed";
  if (onOldAddress(node, coreUrl)) return "old_address";
  const health = nodeHealth(node);
  if (health !== "available") return health;
  const attention = node.cleanup_pending > 0 || allocations.some((allocation) => allocation.node_id === node.id && allocation.diagnostic);
  return attention ? "attention" : "available";
}

const stateTone: Record<NodeState, Tone> = { unconfirmed: "neutral", old_address: "warning", offline: "danger", degraded: "warning", attention: "warning", available: "ok" };
const stateLabel: Record<NodeState, MessageKey> = {
  unconfirmed: "Status unconfirmed",
  old_address: "Old address",
  offline: "Offline",
  degraded: "Provider unavailable",
  attention: "Needs attention",
  available: "Available",
};

export function NodeStatus({ state }: { state: NodeState }) {
  const { t } = useTranslation("sandbox");
  return <StatusDot tone={stateTone[state]} label={t(stateLabel[state])} />;
}

/** What to do about a node on an old address, under its status. */
export function OldAddressHint() {
  const { t } = useTranslation("sandbox");
  return <span className="node-status-hint">{t("Remove and add again")}</span>;
}

export function seconds(value: string | null): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : Math.floor(parsed / 1000);
}

export function NodeList({ nodes, allocations, coreUrl, stale, disabled, suspends = false, onOpen, onRemove }: {
  nodes: readonly SandboxNode[];
  /** The deployment's address; a node enrolled with another one is on an old address. */
  coreUrl: string;
  /** microsandbox: sandboxes sleep as snapshots, so the list also shows suspended counts. */
  suspends?: boolean;
  allocations: readonly SandboxAllocation[];
  stale: boolean;
  disabled: boolean;
  onOpen: (node: SandboxNode) => void;
  onRemove: (node: SandboxNode) => void;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1000);
  return (
    <div className="table-frame">
      <table className="data-table nodes-table" aria-label={t("Sandbox nodes")}>
        <thead>
          <tr>
            <th scope="col">{t("Node")}</th>
            <th scope="col">{t("Status")}</th>
            <th scope="col" className="numeric">{t("Active / limit")}</th>
            {suspends ? <th scope="col" className="numeric"><span className="column-help">{t("Suspended")}<HelpTip>{t("Suspended sandboxes keep their state as a snapshot on the node and resume on the Session's next Turn. They count toward the retained limit, not the active one.")}</HelpTip></span></th> : null}
            <th scope="col" className="numeric">{t("Last seen")}</th>
            <th scope="col">{t("Added")}</th>
            <th scope="col"><span className="visually-hidden">{t("Actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((node) => {
            const name = node.name || node.id;
            const state = nodeState(node, allocations, stale, coreUrl);
            // A degraded node names the reason its provider is not ready.
            const diagnostic = state === "degraded" ? nodeProviderDiagnostic(node) : "";
            return (
              <tr key={node.id}>
                <th scope="row">
                  <NameCell name={node.name} id={node.id} onOpen={() => onOpen(node)} openLabel={t("Open {{name}}", { name })} idLabel={t("Node ID")} />
                </th>
                <td>
                  <span className="status-with-help"><NodeStatus state={state} />{diagnostic ? <DiagnosticTip code={diagnostic} /> : null}</span>
                  {state === "old_address" ? <OldAddressHint /> : null}
                </td>
                <td className="numeric">{node.active} / {node.max_active}</td>
                {suspends ? <td className="numeric">{suspendedSandboxes(node)}</td> : null}
                <td className="numeric" title={node.last_seen_at ? formatDateTime(seconds(node.last_seen_at), locale) : undefined}>
                  {node.last_seen_at ? formatRelative(seconds(node.last_seen_at), now, locale) : t("Never")}
                </td>
                <td className="nodes-nowrap">{formatDateTime(seconds(node.created_at), locale)}</td>
                <td className="actions-cell">
                  <RowActions>
                    <button className="text-action danger" type="button" disabled={disabled} aria-label={t("Remove {{name}}", { name })} onClick={() => onRemove(node)}>
                      {t("Remove")}
                    </button>
                  </RowActions>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
