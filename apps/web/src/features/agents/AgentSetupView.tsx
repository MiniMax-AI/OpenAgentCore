import { Check, ChevronRight, Circle, Code2, MessageSquare, Trash2 } from "lucide-react";
import { useState } from "react";

import type { CoreHarnessKind, CreateAgentInput, SavedAgent, UpdateAgentInput } from "@agents-core-web/agents-client";

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
  const preview = buildAgentRequestPreview(values, baseUrl, agentId);
  return (
    <section className="agent-request-preview" aria-labelledby="agent-request-preview-title">
      <header>
        <Code2 size={15} strokeWidth={1.5} aria-hidden="true" />
        <div>
          <h2 id="agent-request-preview-title">Request preview</h2>
          <p>Uses placeholders only. The connected Core credential is never read into this preview.</p>
        </div>
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
  return (
    <section className="agent-saved-summary" aria-labelledby="agent-saved-summary-title">
      <h2 id="agent-saved-summary-title">Saved definition</h2>
      <dl>
        <div><dt>Agent ID</dt><dd><code>{agent.id}</code></dd></div>
        <div><dt>Updated</dt><dd><time dateTime={new Date(agent.updated_at * 1000).toISOString()}>{new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(agent.updated_at * 1000))}</time></dd></div>
        <div><dt>Multi-agent</dt><dd>{agent.multi_agent.enabled ? "Enabled · saved configuration" : "Disabled"}</dd></div>
      </dl>
    </section>
  );
}

function SetupGuide({ saved }: { saved: boolean }) {
  const steps = [
    ["Define an Agent", "Choose a startup-enabled harness, model, and instructions; the Web keeps generation settings on the current Session-safe profile.", true],
    ["Save the definition", "Core becomes the durable source of truth for the saved Agent.", saved],
    ["Start a Session", "Choose a supported Environment profile, create an idle Session, and subscribe before sending input.", false],
    ["Exchange events", "A real Turn still requires a compatible worker, executor, model, and provider.", false],
  ] as const;
  return (
    <section className="agent-setup-guide" aria-labelledby="agent-setup-guide-title">
      <h2 id="agent-setup-guide-title">Get started creating an Agent</h2>
      <ol>
        {steps.map(([title, description, complete]) => (
          <li className={complete ? "complete" : ""} key={title}>
            {complete ? <Check size={13} aria-hidden="true" /> : <Circle size={10} aria-hidden="true" />}
            <div><strong>{title}</strong><span>{description}</span></div>
          </li>
        ))}
      </ol>
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
  initialValues,
  knownModels,
  vaultCatalog = null,
  onBack,
  onCreate,
  onDeleteRequest,
  onStartSession,
  onUpdate,
}: {
  actionError: string | null;
  agent?: SavedAgent;
  baseUrl: string;
  busy: boolean;
  defaultHarness?: CoreHarnessKind;
  enabledHarnesses?: readonly CoreHarnessKind[] | null;
  initialValues?: AgentFormValues;
  knownModels: string[];
  vaultCatalog?: VaultCatalog | null;
  onBack: () => void;
  onCreate: (input: CreateAgentInput) => Promise<SavedAgent | undefined>;
  onDeleteRequest?: () => void;
  onStartSession: (agentId: string) => void;
  onUpdate?: (agentId: string, input: UpdateAgentInput) => Promise<SavedAgent | undefined>;
}) {
  const isEditing = Boolean(agent);
  const [draft, setDraft] = useState<AgentFormValues>(() => {
    const values = agent ? valuesFromAgent(agent, vaultCatalog) : initialValues ?? valuesFromAgent(undefined, vaultCatalog);
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
        setSaveNotice("Agent definition updated.");
      }
      return;
    }
    // This form has no loaded Agent, so its Tool drafts are only controlled
    // create profiles; the cast keeps that lifecycle distinction explicit.
    const created = await onCreate(input as CreateAgentInput);
    if (created) {
      setSavedAgent(created);
      setSaveNotice(`Agent definition saved as ${created.id}.`);
    }
  };

  return (
    <section className="page-section agent-setup-page">
      <header className="agent-setup-header">
        <div className="agent-setup-breadcrumb" aria-label="Breadcrumb">
          <button type="button" onClick={onBack} disabled={busy}>Agents</button>
          <ChevronRight size={14} aria-hidden="true" />
          <h1>{savedAgent?.name || initialValues?.name || "New Agent"}</h1>
        </div>
        <div className="agent-setup-tabs" role="tablist" aria-label="Agent setup sections">
          <button type="button" role="tab" aria-selected="true">Setup</button>
          <button type="button" role="tab" aria-selected="false" disabled title="Session history is available from the Sessions product view">Sessions</button>
        </div>
      </header>

      <div className="agent-setup-layout">
        <section className="agent-setup-editor" aria-label="Agent definition">
          {actionError ? (
            <div className="agent-action-error" role="alert">
              <strong>Request failed</strong><span>{actionError}</span>
            </div>
          ) : null}
          {saveNotice ? (
            <div className="notice success agent-created-notice" role="status">
              <Check size={14} aria-hidden="true" />
              {saveNotice} This does not prove execution readiness.
            </div>
          ) : null}
          {sessionBlocker ? (
            <div className="notice warning" id="created-agent-session-blocker" role="note">
              Start Session is unavailable. {sessionBlocker}
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
            <button className="button outline" type="button" onClick={onBack} disabled={busy}>Back to Agents</button>
            {isEditing && onDeleteRequest ? (
              <button className="button danger" type="button" data-agent-delete="true" onClick={onDeleteRequest} disabled={busy}>
                <Trash2 size={14} strokeWidth={1.5} aria-hidden="true" /> Delete Agent
              </button>
            ) : null}
            <button className="button primary" type="submit" form={formId} disabled={busy || (!isEditing && Boolean(savedAgent))}>
              {busy ? "Saving…" : isEditing ? "Save changes" : savedAgent ? "Agent saved" : "Save Agent definition"}
            </button>
            <button
              className="button primary agent-start-session"
              type="button"
              disabled={busy || !savedAgent || Boolean(sessionBlocker)}
              aria-describedby={sessionBlocker ? "created-agent-session-blocker" : undefined}
              onClick={() => savedAgent && onStartSession(savedAgent.id)}
            >
              <MessageSquare size={14} strokeWidth={1.5} aria-hidden="true" />
              Start Session
            </button>
          </footer>
        </section>

        <aside className="agent-setup-aside">
          <AgentRequestPreview agentId={isEditing ? savedAgent?.id ?? agent?.id : undefined} baseUrl={baseUrl} values={draft} />
          {isEditing && savedAgent ? <SavedDefinitionSummary agent={savedAgent} /> : null}
          <SetupGuide saved={Boolean(savedAgent)} />
        </aside>
      </div>
    </section>
  );
}
