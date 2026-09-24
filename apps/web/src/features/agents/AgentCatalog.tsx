import { Bot, Check, Copy, MessageSquare, SearchX } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";

import type { SavedAgent } from "@agents-core-web/agents-client";

import { EmptyState, HelpTip } from "../../components/console-ui";
import { formatDateTime, formatInteger, formatRelative } from "../../lib/format";
import type { VaultCatalog } from "../vaults/vault-catalog";
import { harnessLabel } from "./AgentForm";
import { AgentUsageCells, type AgentUsageModel } from "./AgentUsage";
import { sessionAdmissionBlocker } from "./session-admission";

import "./AgentCatalog.css";

function AgentSessionStartAction({
  agent,
  busy,
  onStart,
  vaultCatalog,
}: {
  agent: SavedAgent;
  busy: boolean;
  onStart: (agentId: string) => void;
  vaultCatalog: VaultCatalog | null;
}) {
  const { t } = useTranslation("agents");
  const blocker = sessionAdmissionBlocker(agent, vaultCatalog);
  const descriptionId = useId();
  const reason = blocker ? t("catalog.sessionUnavailable", { reason: blocker }) : null;
  return (
    <span className="agent-start-action">
      <button
        className="button outline agent-session-start"
        type="button"
        onClick={() => {
          if (!blocker) onStart(agent.id);
        }}
        disabled={busy}
        aria-disabled={blocker ? true : undefined}
        aria-label={t("catalog.startLabel", { name: agent.name || t("catalog.untitled"), id: agent.id })}
        aria-describedby={blocker ? descriptionId : undefined}
      >
        <MessageSquare size={14} strokeWidth={1.5} aria-hidden="true" />
        <span>{blocker ? t("catalog.unavailable") : t("catalog.startSession")}</span>
      </button>
      {reason ? (
        <>
          <span className="visually-hidden" id={descriptionId}>{reason}</span>
          <HelpTip label={t("catalog.unavailableHelp")}>{reason}</HelpTip>
        </>
      ) : null}
    </span>
  );
}

function CopyAgentId({ id }: { id: string }) {
  const { t } = useTranslation("agents");
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1_500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const label = copied ? t("catalog.copied") : t("catalog.copyId", { id });
  return (
    <button
      className="icon-button ghost agent-copy-id"
      type="button"
      aria-label={label}
      title={label}
      onClick={(event) => {
        event.stopPropagation();
        void navigator.clipboard?.writeText(id).then(() => setCopied(true), () => undefined);
      }}
    >
      {copied ? <Check size={13} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.6} aria-hidden="true" />}
    </button>
  );
}

/** Saved Agents as one table row each; a row opens the Agent's setup view. */
export function AgentCatalog({
  agents,
  busy,
  coreReady,
  hasSavedAgents,
  isFiltering,
  openingAgentId,
  usage = null,
  vaultCatalog,
  onClearSearch,
  onCreate,
  onEdit,
  onStartSession,
}: {
  agents: SavedAgent[];
  busy: boolean;
  coreReady: boolean;
  hasSavedAgents: boolean;
  isFiltering: boolean;
  openingAgentId: string | null;
  usage?: AgentUsageModel | null;
  vaultCatalog: VaultCatalog | null;
  onClearSearch: () => void;
  onCreate: () => void;
  onEdit: (agent: SavedAgent) => void;
  onStartSession: (agentId: string) => void;
}) {
  const { t, i18n } = useTranslation("agents");
  const locale = i18n.resolvedLanguage;
  const now = Math.floor(Date.now() / 1_000);

  if (!agents.length) {
    return hasSavedAgents ? (
      <EmptyState
        icon={SearchX}
        title={t("catalog.noMatch")}
        description={t("catalog.trySearch")}
        action={<button className="button outline" type="button" onClick={onClearSearch}>{t("catalog.clearSearch")}</button>}
      />
    ) : (
      <EmptyState
        icon={Bot}
        title={t("catalog.noSaved")}
        description={t("catalog.emptyHelp")}
        action={coreReady ? (
          <button className="button outline" type="button" onClick={onCreate} disabled={busy}>{t("catalog.create")}</button>
        ) : undefined}
      />
    );
  }

  return (
    <div className="table-frame agent-table-frame">
      <table className="data-table agent-table" aria-label={t("catalog.listLabel")} aria-busy={openingAgentId ? true : undefined}>
        <thead>
          <tr>
            <th scope="col">{t("catalog.columns.agent")}</th>
            <th scope="col">{t("catalog.columns.model")}</th>
            <th scope="col">{t("catalog.columns.harness")}</th>
            <th scope="col" className="numeric">{t("catalog.columns.tools")}</th>
            {usage ? (
              <>
                <th scope="col" className="numeric">{t("usage.sessions")}</th>
                <th scope="col" className="numeric">{t("usage.tokens")}</th>
                <th scope="col" className="numeric">
                  <span className="agent-table-help">{t("usage.coverage")}<HelpTip>{t("usage.coverageHelp")}</HelpTip></span>
                </th>
                <th scope="col" className="numeric">{t("usage.lastActive")}</th>
              </>
            ) : null}
            <th scope="col" className="numeric">{t("catalog.columns.updated")}</th>
            <th scope="col"><span className="visually-hidden">{t("catalog.columns.actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {agents.map((agent) => {
            const opening = openingAgentId === agent.id;
            const name = agent.name || t("catalog.untitled");
            const harness = agent.x_agents_core?.harness;
            const open = () => {
              if (!busy && !opening) onEdit(agent);
            };
            return (
              <tr key={agent.id} className="clickable-row" onClick={open} aria-busy={opening || undefined}>
                <th scope="row">
                  <span className="agent-table-name">
                    <button
                      className="table-link"
                      type="button"
                      title={agent.id}
                      onClick={(event) => {
                        event.stopPropagation();
                        open();
                      }}
                      disabled={busy || opening}
                      aria-label={t("catalog.editLabel", { name, id: agent.id })}
                      data-agent-id={agent.id}
                    >
                      <strong className={agent.name ? undefined : "agent-null-value"}>{name}</strong>
                    </button>
                    <CopyAgentId id={agent.id} />
                  </span>
                  {opening ? <span className="agent-table-opening" role="status">{t("catalog.opening")}</span> : null}
                </th>
                <td><code className="agent-table-model" title={agent.model}>{agent.model}</code></td>
                <td className={harness ? undefined : "table-muted"}>{harness ? harnessLabel(harness) : t("catalog.coreDefaultHarness")}</td>
                <td className="numeric">{formatInteger(agent.tools.length, locale)}</td>
                {usage ? <AgentUsageCells usage={usage} agentId={agent.id} /> : null}
                <td className="numeric" title={formatDateTime(agent.updated_at, locale)}>
                  <time dateTime={new Date(agent.updated_at * 1_000).toISOString()}>{formatRelative(agent.updated_at, now, locale)}</time>
                </td>
                <td className="row-actions" onClick={(event) => event.stopPropagation()}>
                  <AgentSessionStartAction agent={agent} busy={busy} onStart={onStartSession} vaultCatalog={vaultCatalog} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
