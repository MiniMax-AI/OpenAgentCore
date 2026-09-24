import { Check, ChevronRight, Code2, MessageSquare, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";

import type { CoreHarnessKind, CreateAgentInput, SavedAgent, UpdateAgentInput } from "@agents-core-web/agents-client";

import { HelpTip } from "../../components/console-ui";
import { buildModelOptionGroups } from "../../lib/model-options";
import type { VaultCatalog } from "../vaults/vault-catalog";
import { AgentForm } from "./AgentForm";
import { buildAgentRequestPreview } from "./agent-preview";
import { type AgentFormSubmitInput, type AgentFormValues, valuesFromAgent } from "./agent-form";
import { sessionAdmissionBlocker } from "./session-admission";

function AgentRequestPreview({
  agentId,
  baseUrl,
  values,
}: {
  agentId?: string;
  baseUrl: string;
  values: AgentFormValues;
}) {
  const { t } = useTranslation("agents");
  const preview = buildAgentRequestPreview(values, baseUrl, agentId);
  return (
    <section className="agent-request-preview" aria-labelledby="agent-request-preview-title">
      <header>
        <Code2 size={15} strokeWidth={1.5} aria-hidden="true" />
        <h2 id="agent-request-preview-title">{t("setup.requestPreview")}</h2>
        <HelpTip>{t("setup.previewHelp")}</HelpTip>
      </header>
      <div className="agent-preview-block">
        <span>curl</span>
        <pre>{preview.curl}</pre>
      </div>
      <div className="agent-preview-block">
        <span>agent.json</span>
        <pre>{preview.json}</pre>
      </div>
    </section>
  );
}

function SavedDefinitionSummary({ agent }: { agent: SavedAgent }) {
  const { t } = useTranslation("agents");
  return (
    <section className="agent-saved-summary" aria-labelledby="agent-saved-summary-title">
      <h2 id="agent-saved-summary-title">{t("setup.savedDefinition")}</h2>
      <dl>
        <div><dt>{t("setup.agentId")}</dt><dd><code>{agent.id}</code></dd></div>
        <div><dt>{t("setup.updated")}</dt><dd><time dateTime={new Date(agent.updated_at * 1000).toISOString()}>{new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(agent.updated_at * 1000))}</time></dd></div>
        <div><dt>{t("setup.multiAgent")}</dt><dd>{agent.multi_agent.enabled ? t("setup.enabledSaved") : t("setup.disabled")}</dd></div>
      </dl>
    </section>
  );
}

export function AgentSetupView({
  actionError,
  agent,
  baseUrl,
  busy,
  defaultHarness,
  enabledHarnesses = null,
  knownModels,
  vaultCatalog = null,
  onBack,
  onCreate,
  onDeleteRequest,
  onStartSession,
  onUpdate,
  usagePanel,
}: {
  actionError: string | null;
  agent?: SavedAgent;
  baseUrl: string;
  busy: boolean;
  defaultHarness?: CoreHarnessKind;
  enabledHarnesses?: readonly CoreHarnessKind[] | null;
  knownModels: string[];
  vaultCatalog?: VaultCatalog | null;
  onBack: () => void;
  onCreate: (input: CreateAgentInput) => Promise<SavedAgent | undefined>;
  onDeleteRequest?: () => void;
  onStartSession: (agentId: string) => void;
  onUpdate?: (agentId: string, input: UpdateAgentInput) => Promise<SavedAgent | undefined>;
  /** Usage statistics for an existing Agent, shown first in the aside. */
  usagePanel?: ReactNode;
}) {
  const { t } = useTranslation("agents");
  const isEditing = Boolean(agent);
  const [draft, setDraft] = useState<AgentFormValues>(() => {
    const values = valuesFromAgent(agent, vaultCatalog);
    const harness = !agent && !values.harness && defaultHarness && enabledHarnesses?.includes(defaultHarness)
      ? defaultHarness
      : values.harness;
    if (values.model) return { ...values, harness };
    const models = buildModelOptionGroups(
      knownModels,
      import.meta.env.VITE_AGENT_MODEL_PRESETS,
      import.meta.env.VITE_AGENT_DEFAULT_MODEL,
    );
    return { ...values, harness, model: models.defaultModel };
  });
  const [savedAgent, setSavedAgent] = useState<SavedAgent | null>(agent ?? null);
  const [formRevision, setFormRevision] = useState(0);
  const [saveNotice, setSaveNotice] = useState<string | null>(null);
  const formId = isEditing ? "edit-agent" : "create-agent";
  const sessionBlocker = savedAgent ? sessionAdmissionBlocker(savedAgent, vaultCatalog) : null;

  const save = async (input: AgentFormSubmitInput) => {
    setSaveNotice(null);
    if (isEditing) {
      if (!agent || !onUpdate) return;
      const updated = await onUpdate(agent.id, input as UpdateAgentInput);
      if (updated) {
        setSavedAgent(updated);
        setDraft(valuesFromAgent(updated, vaultCatalog));
        setFormRevision((current) => current + 1);
        setSaveNotice(t("setup.updatedNotice"));
      }
      return;
    }
    // This form has no loaded Agent, so its Tool drafts are only controlled
    // create profiles; the cast keeps that lifecycle distinction explicit.
    const created = await onCreate(input as CreateAgentInput);
    if (created) {
      setSavedAgent(created);
      setSaveNotice(t("setup.savedNotice", { id: created.id }));
    }
  };

  return (
    <section className="page-section agent-setup-page">
      <header className="agent-setup-header">
        <div className="agent-setup-breadcrumb" aria-label={t("setup.breadcrumb")}>
          <button type="button" onClick={onBack} disabled={busy}>{t("catalog.listLabel")}</button>
          <ChevronRight size={14} aria-hidden="true" />
          <h1>{savedAgent?.name || t("setup.newAgent")}</h1>
        </div>
        <div className="agent-setup-tabs" role="tablist" aria-label={t("setup.sections")}>
          <button type="button" role="tab" aria-selected="true">{t("setup.setup")}</button>
          <button type="button" role="tab" aria-selected="false" disabled title={t("setup.sessionsTitle")}>{t("setup.sessions")}</button>
        </div>
      </header>

      <div className="agent-setup-layout">
        <section className="agent-setup-editor" aria-label={t("setup.definition")}>
          {actionError ? (
            <div className="agent-action-error" role="alert">
              <strong>{t("requestFailure")}</strong><span>{actionError}</span>
            </div>
          ) : null}
          {saveNotice ? (
            <div className="notice success agent-created-notice" role="status">
              <Check size={14} aria-hidden="true" />
              {saveNotice} {t("setup.readinessBoundary")}
            </div>
          ) : null}
          {sessionBlocker ? (
            <div className="notice warning" id="created-agent-session-blocker" role="note">
              {t("setup.startUnavailable", { reason: sessionBlocker })}
            </div>
          ) : null}
          <AgentForm
            key={isEditing && savedAgent ? `${savedAgent.id}:${savedAgent.updated_at}:${formRevision}` : "create"}
            agent={isEditing ? savedAgent ?? agent : undefined}
            defaultHarness={defaultHarness}
            disabled={busy || (!isEditing && Boolean(savedAgent))}
            enabledHarnesses={enabledHarnesses}
            formId={formId}
            initialValues={isEditing ? undefined : draft}
            knownModels={knownModels}
            vaultCatalog={vaultCatalog}
            onDraftChange={setDraft}
            onSubmit={save}
          />
          <footer className="agent-setup-actions">
            <button className="button outline" type="button" onClick={onBack} disabled={busy}>{t("setup.back")}</button>
            {isEditing && onDeleteRequest ? (
              <button className="button danger" type="button" data-agent-delete="true" onClick={onDeleteRequest} disabled={busy}>
                <Trash2 size={14} strokeWidth={1.5} aria-hidden="true" /> {t("setup.delete")}
              </button>
            ) : null}
            <button className="button primary" type="submit" form={formId} disabled={busy || (!isEditing && Boolean(savedAgent))}>
              {busy ? t("setup.saving") : isEditing ? t("setup.saveChanges") : savedAgent ? t("setup.saved") : t("setup.saveDefinition")}
            </button>
            <button
              className="button primary agent-start-session"
              type="button"
              disabled={busy || !savedAgent || Boolean(sessionBlocker)}
              aria-describedby={sessionBlocker ? "created-agent-session-blocker" : undefined}
              onClick={() => savedAgent && onStartSession(savedAgent.id)}
            >
              <MessageSquare size={14} strokeWidth={1.5} aria-hidden="true" />
              {t("setup.startSession")}
            </button>
          </footer>
        </section>

        <aside className="agent-setup-aside">
          {isEditing ? usagePanel : null}
          <AgentRequestPreview agentId={isEditing ? savedAgent?.id ?? agent?.id : undefined} baseUrl={baseUrl} values={draft} />
          {isEditing && savedAgent ? <SavedDefinitionSummary agent={savedAgent} /> : null}
        </aside>
      </div>
    </section>
  );
}
