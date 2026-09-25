import { AgentCoreError, createIdempotencyKey, type Vault } from "@agents-core-web/agents-client";
import { ArrowRight } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { ConsoleSelect } from "../../components/console-select";
import { HelpTip } from "../../components/console-ui";
import { CopyableId } from "../../components/list-ui";
import { Modal } from "../../components/Modal";
import type { ConsoleView } from "../../lib/console-routes";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { projectClient, readAllPages, useProjects } from "../../lib/projects";
import { queryClient } from "../../lib/queries";
import "./copy.css";
import { type CopyableResourceType, copyAsset, type CopyResult, type Project } from "../../lib/admin-view";

export interface CopySource {
  type: CopyableResourceType;
  id: string;
  name: string;
  project: Project;
}

/** Where to look at a copy once it exists. */
const viewFor: Record<CopyableResourceType, ConsoleView> = {
  agent: "agents",
  skill: "skills",
  environment_template: "templates",
  file: "files",
  vault: "vaults",
  credential: "vaults",
};

type Outcome =
  | { kind: "idle" }
  | { kind: "copying" }
  | { kind: "done"; result: CopyResult }
  | { kind: "failed"; message: string; uncertain: boolean };

/**
 * Copies one asset into another project. Core does the work in one
 * transaction; a retry reuses the same Idempotency-Key so it never duplicates.
 */
export function CopyDialog({ source, onClose }: { source: CopySource | null; onClose: () => void }) {
  const { t } = useTranslation();
  const { state } = useProjects();
  const { navigate } = useConsoleNavigation();
  const [targetId, setTargetId] = useState("");
  const [includeDependencies, setIncludeDependencies] = useState(true);
  const [vaults, setVaults] = useState<Vault[] | null>(null);
  const [vaultId, setVaultId] = useState("");
  const [outcome, setOutcome] = useState<Outcome>({ kind: "idle" });
  const idempotency = useRef(createIdempotencyKey());

  const targets = useMemo(
    () => state.projects.filter((project) => project.status === "active" && project.id !== source?.project.id),
    [source?.project.id, state.projects],
  );
  const target = targets.find((project) => project.id === targetId);
  const needsVault = source?.type === "credential";

  // A new source or target is a new request: forget the previous outcome and key.
  useEffect(() => {
    setOutcome({ kind: "idle" });
    idempotency.current = createIdempotencyKey();
  }, [source?.id, targetId, includeDependencies, vaultId]);
  useEffect(() => {
    setTargetId("");
    setVaultId("");
    setIncludeDependencies(true);
  }, [source?.id]);
  useEffect(() => {
    if (!needsVault || !targetId) { setVaults(null); return; }
    const controller = new AbortController();
    setVaults(null);
    setVaultId("");
    readAllPages((after) => projectClient(targetId).listVaults({ after, limit: 100, signal: controller.signal })).then(
      setVaults,
      () => { if (!controller.signal.aborted) setVaults([]); },
    );
    return () => controller.abort();
  }, [needsVault, targetId]);

  const submit = async () => {
    if (!source || !target || outcome.kind === "copying" || (needsVault && !vaultId)) return;
    setOutcome({ kind: "copying" });
    try {
      const result = await copyAsset({
        source_project_id: source.project.id,
        target_project_id: target.id,
        resource_type: source.type,
        resource_id: source.id,
        include_dependencies: includeDependencies,
        ...(needsVault ? { target_vault_id: vaultId } : {}),
      }, { idempotencyKey: idempotency.current });
      setOutcome({ kind: "done", result });
      // The target project now holds new assets: re-read its cached lists.
      void queryClient.invalidateQueries({ predicate: (query) => query.queryKey[0] === "collection" && query.queryKey.at(-1) === target.id });
    } catch (error) {
      // A 4xx is a definite rejection; anything else may have committed.
      const definite = error instanceof AgentCoreError && error.status >= 400 && error.status < 500;
      setOutcome({ kind: "failed", message: error instanceof Error ? error.message : String(error), uncertain: !definite });
    }
  };

  const typeLabel = (type: string) => t(`copy.types.${type as CopyableResourceType}`, { defaultValue: type });
  const busy = outcome.kind === "copying";
  const done = outcome.kind === "done";

  return (
    <Modal
      open={source !== null}
      title={t("copy.title")}
      onClose={() => { if (!busy) onClose(); }}
      footer={done ? (
        <>
          <button type="button" className="button outline" onClick={onClose}>{t("copy.close")}</button>
          {target && source ? (
            <button type="button" className="button primary" onClick={() => { onClose(); navigate(viewFor[source.type], { project: target.id }); }}>
              {t("copy.open", { name: target.name })}
            </button>
          ) : null}
        </>
      ) : (
        <>
          <button type="button" className="button outline" disabled={busy} onClick={onClose}>{t("actions.cancel")}</button>
          <button type="button" className="button primary" disabled={busy || !target || (needsVault && !vaultId)} onClick={() => void submit()}>
            {busy ? t("copy.copying") : target ? t("copy.submitTo", { name: target.name }) : t("copy.submit")}
          </button>
        </>
      )}
    >
      {source ? (
        <div className="copy-dialog">
          <div className="copy-item">
            <span className="copy-item-type">{typeLabel(source.type)}</span>
            <strong className="copy-item-name" title={source.name}>{source.name}</strong>
            <CopyableId id={source.id} compact />
          </div>
          {done ? <CopyResultView result={outcome.result} target={target?.name ?? ""} typeLabel={typeLabel} /> : (
            <>
              <div className="copy-route">
                <div className="copy-route-end">
                  <span className="copy-route-label">{t("copy.from")}</span>
                  <span className="copy-route-project" title={source.project.name}>{source.project.name}</span>
                </div>
                <ArrowRight className="copy-route-arrow" size={16} strokeWidth={1.6} aria-hidden="true" />
                <div className="copy-route-end">
                  <span className="copy-route-label">{t("copy.to")}<HelpTip>{t("copy.help")}</HelpTip></span>
                  {targets.length ? (
                    <ConsoleSelect
                      label={t("copy.target")}
                      value={targetId}
                      onChange={setTargetId}
                      disabled={busy}
                      className="w-full max-w-none"
                      options={[{ value: "", label: t("copy.chooseTarget") }, ...targets.map((project) => ({ value: project.id, label: project.name }))]}
                    />
                  ) : <span className="copy-empty">{t("copy.noTargets")}</span>}
                </div>
              </div>
              {needsVault && targetId ? (
                <div className="copy-route-end">
                  <span className="copy-route-label">{t("copy.targetVault")}</span>
                  {vaults !== null && !vaults.length ? <p className="copy-empty">{t("copy.noVaults")}</p> : (
                    <ConsoleSelect
                      label={t("copy.targetVault")}
                      value={vaultId}
                      onChange={setVaultId}
                      disabled={busy || vaults === null}
                      className="w-full max-w-none"
                      options={[{ value: "", label: vaults === null ? "…" : t("copy.chooseVault") }, ...(vaults ?? []).map((vault) => ({ value: vault.id, label: vault.name ?? vault.id }))]}
                    />
                  )}
                </div>
              ) : null}
              {source.type !== "file" && source.type !== "credential" ? (
                <label className="copy-check">
                  <input type="checkbox" checked={includeDependencies} disabled={busy} onChange={(event) => setIncludeDependencies(event.target.checked)} />
                  <span className="field-label-row">{t("copy.includeDependencies")}<HelpTip>{t("copy.dependenciesHelp")}</HelpTip></span>
                </label>
              ) : null}
              {outcome.kind === "failed" ? (
                <p className="copy-error" role="alert">{outcome.uncertain ? t("copy.uncertain") : t("copy.failed")} {outcome.message}</p>
              ) : null}
            </>
          )}
        </div>
      ) : null}
    </Modal>
  );
}

function CopyResultView({ result, target, typeLabel }: { result: CopyResult; target: string; typeLabel: (type: string) => string }) {
  const { t } = useTranslation();
  return (
    <div className="copy-result" role="status">
      <p className="copy-result-title">{target ? t("copy.doneTo", { name: target }) : t("copy.done")}</p>
      {result.mappings.length ? (
        <div className="table-frame">
          <table className="data-table data-table-compact" aria-label={t("copy.mapped")}>
            <tbody>
              {result.mappings.map((mapping) => (
                <tr key={`${mapping.type}:${mapping.source_id}`}>
                  <td>{typeLabel(mapping.type)}</td>
                  <td><code title={mapping.source_id}>{mapping.source_id.length > 14 ? `${mapping.source_id.slice(0, 6)}…${mapping.source_id.slice(-4)}` : mapping.source_id}</code></td>
                  <td aria-hidden="true">→</td>
                  <td><CopyableId id={mapping.target_id} compact /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {result.skipped.length ? (
        <>
          <p className="copy-result-title">{t("copy.skipped")}</p>
          <div className="table-frame">
            <table className="data-table data-table-compact" aria-label={t("copy.skipped")}>
              <tbody>
                {result.skipped.map((entry) => (
                  <tr key={`${entry.type}:${entry.source_id}`}>
                    <td>{typeLabel(entry.type)}</td>
                    <td><code title={entry.source_id}>{entry.source_id.length > 14 ? `${entry.source_id.slice(0, 6)}…${entry.source_id.slice(-4)}` : entry.source_id}</code></td>
                    <td>{entry.reason}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : null}
    </div>
  );
}
