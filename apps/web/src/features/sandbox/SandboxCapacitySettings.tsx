import { useCallback, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useConsoleIntent, useConsoleNavigation } from "../../lib/console-navigation";
import { Section } from "../../components/console-ui";
import { useToast } from "../../components/Toast";
import { sandboxRequestError, sandboxWriteUncertain } from "../../lib/sandbox-labels";
import { sandboxAdmin, sandboxConsoleConfigQuery } from "./sandbox-queries";
import { useSandboxPageState } from "./use-sandbox-page-state";
import { NodeEnrollment } from "./NodeEnrollment";
import { NodeEditDialog } from "./NodeEditDialog";
import { SuspensionSettings } from "./SandboxLifecycle";
import { sandboxSize } from "./deployment-specification";
import { writeSandboxDeployment } from "./sandbox-deployment-write";

export function SandboxCapacitySettings() {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const configQuery = useQuery(sandboxConsoleConfigQuery);
  const consoleConfig = configQuery.data;
  const { deploymentQuery, query, compatible, inventoryOlder, inventoryLoading, snapshot, installation, loading, busy, setupNeedsRefresh, confirmed, fresh, refresh } = useSandboxPageState({ poll: true });
  const queryClient = useQueryClient();
  const toast = useToast();
  const client = sandboxAdmin;
  const { params, navigate } = useConsoleNavigation();
  const [adding, setAdding] = useState(false);
  const { t: tCommon } = useTranslation("common");
  const localOnly = installation.data?.local_only === true;
  const nodesConfirmed = confirmed && compatible && !inventoryOlder && !query.isError && !snapshot?.nodesError;
  const hostedNodes = snapshot?.deployment.mode === "nodes";
  const suspends = Boolean(snapshot?.deployment.suspension);
  const editTarget = params.node ? snapshot?.nodes.find((node) => node.id === params.node) ?? null : null;
  const closeEdit = () => navigate("settings", { id: "sandbox" });

  const { refetch: refetchInventory } = query;
  const refreshNodes = useCallback(async () => {
    const result = await refetchInventory();
    return result.isSuccess && !result.data.nodesError ? result.data.nodes : null;
  }, [refetchInventory]);
  // Getting started asks for Add node on arrival. A request the first settled read cannot
  // serve (no own-machines deployment, active reset, a failed read) is dropped, so the
  // dialog never opens later on its own.
  const addNodeReadiness = loading || busy || installation.isPending || inventoryLoading ? "wait"
    : !snapshot ? (deploymentQuery.isError ? "unavailable" : "wait")
    : hostedNodes && fresh && nodesConfirmed && !snapshot.deployment.reset && !localOnly ? "ready" : "unavailable";
  useConsoleIntent("add-node", addNodeReadiness, () => setAdding(true));
  // Enrollment and capacity editing have one owner in Settings.
  const enrollment = hostedNodes && snapshot && consoleConfig ? <NodeEnrollment key={snapshot.deployment.generation} client={client} consoleConfig={consoleConfig} deployment={snapshot.deployment} nodes={snapshot.nodes} open={adding} fresh={nodesConfirmed && !snapshot.deployment.reset} onClose={() => setAdding(false)} onRefresh={refreshNodes} /> : null;

  const editDialog = <NodeEditDialog
        key={editTarget?.id ?? "closed"}
        client={client}
        node={editTarget}
        size={snapshot ? sandboxSize(snapshot.deployment) : null}
        suspends={suspends}
        onClose={closeEdit}
        onSaved={() => {
          const saved = editTarget;
          closeEdit();
          toast.show(t("Node saved"), { tone: "success" });
          refresh();
          // Other pages read the fleet and the node's detail separately.
          void queryClient.invalidateQueries({ queryKey: ["sandbox-fleet"] });
          if (saved) void queryClient.invalidateQueries({ queryKey: ["sandbox-node", saved.id] });
        }}
      />;

  async function saveIdle(seconds: number) {
    if (busy || loading || !fresh || setupNeedsRefresh) return false;
    try {
      return await writeSandboxDeployment(queryClient, (signal) => client.updateSuspension({ idle_seconds: seconds }, { signal })) !== null;
    } catch (error) {
      toast.show(t(sandboxWriteUncertain(error) ? "Couldn't confirm the sandbox change" : "Core rejected the sandbox change"), { tone: "error", detail: sandboxRequestError(error, locale), key: "sandbox-write" });
      return false;
    }
  }

  const disabled = busy || loading || !fresh || !nodesConfirmed || setupNeedsRefresh || Boolean(snapshot?.deployment.reset);
  if (!snapshot?.deployment.provider) return null;
  const nodes = snapshot.nodes;
  return <>
    <SuspensionSettings deployment={snapshot.deployment} disabled={busy || loading || !fresh || setupNeedsRefresh || Boolean(snapshot.deployment.reset)} onSave={saveIdle} />
    {hostedNodes ? <Section headingId="settings-node-capacity" title={t("Sandbox nodes")} actions={<button className="button outline" type="button" disabled={disabled || localOnly || !consoleConfig} aria-describedby={localOnly ? "settings-add-node-blocked" : undefined} onClick={() => setAdding(true)}>{t("Add node")}</button>}>
      {configQuery.isError ? <p role="alert">{t("The console configuration could not be read. Refresh to try again.")} <button type="button" className="text-action" onClick={() => void configQuery.refetch()}>{t("Try again")}</button></p> : null}
      {snapshot.nodesError ? <p role="alert">{sandboxRequestError(snapshot.nodesError, locale)}</p> : null}
      {localOnly ? <p role="status" id="settings-add-node-blocked">{tCommon("installationNotice.addBlocked")}</p> : null}
      {nodes.some((node) => node.active > node.max_active || node.retained > node.max_retained) ? <p role="status">{t("Current usage exceeds a saved limit. Existing sandboxes are preserved; new placements wait for capacity.")}</p> : null}
      <div className="settings-table"><table className="data-table" aria-label={t("Sandbox nodes")}><thead><tr><th scope="col">{t("Node")}</th><th scope="col" className="numeric">{t("Active / limit")}</th>{suspends ? <th scope="col" className="numeric">{t("Retained / limit")}</th> : null}<th scope="col" className="settings-table-action">{t("Actions")}</th></tr></thead><tbody>
        {nodes.map((node) => <tr key={node.id} id={`capacity-${node.id}`}><th scope="row">{node.name || node.id}</th><td className="numeric">{node.active} / {node.max_active}</td>{suspends ? <td className="numeric">{node.retained} / {node.max_retained}</td> : null}<td className="settings-table-action"><button type="button" className="text-action" disabled={disabled} onClick={() => navigate("settings", { id: "sandbox", node: node.id })}>{t("Edit node")}</button></td></tr>)}
      </tbody></table></div>
    </Section> : null}
    {enrollment}{editDialog}
  </>;
}
