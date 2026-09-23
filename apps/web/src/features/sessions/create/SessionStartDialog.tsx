import { useEffect, useId, useRef, useState, type FormEvent } from "react";

import {
  AgentCoreError,
  type AgentEnvironmentInput,
  type CreateEnvironmentTemplateInput,
  type EnvironmentTemplate,
  type InlineAgentInput,
  type OpenAIHostedNetworkAccess,
  type InputMessage,
  type SavedAgent,
} from "@agents-core-web/agents-client";

import { SandboxNodeSelector } from "../../sandbox/SandboxNodeSelector";
import { Modal } from "../../../components/Modal";
import { sessionEnvironmentAdmissionBlocker } from "../../agents/session-admission";
import {
  environmentTemplateLabel,
  hostedNetworkNarrowingBlocker,
  type EnvironmentTemplateCatalog,
} from "../environment/environment-templates";
import { deriveSessionVaultPlan, type VaultCatalog } from "../../vaults/vault-catalog";
import {
  beginSessionCreateAttempt,
  type SessionCreateAttempt,
} from "./session-create-attempt";
import {
  sessionEnvironmentInput,
  type HostedNetworkChoice,
  type SessionEnvironmentType,
} from "./session-environment";
import { SessionInitialInputEditor } from "./SessionInitialInputEditor";
import {
  projectSessionInitialInput,
  sessionInitialInputError,
  sessionInitialInputDraftReducer,
} from "./session-initial-input";
import {
  sessionStartDetailsFromAgent,
  validateInlineSessionAgent,
  validateSessionAgentOverrides,
  validateSessionStartDetails,
  type SessionAgentMode,
  type SessionStartDetailsValues,
} from "./session-start-draft";
import { SessionToolsEditor } from "./SessionToolsEditor";
import "./SessionStartDialog.css";

interface SessionStartCommonInput {
  sandboxNodeId?: string;
  environment: AgentEnvironmentInput;
  idempotencyKey: string;
  input?: string | InputMessage[];
  manualVaultIds: string[];
  metadata: Record<string, string>;
  stream: boolean;
  vaultIds: string[];
}

export type SessionStartInput = SessionStartCommonInput & ({
  agentMode: "inline";
  agent: InlineAgentInput;
  agentId?: never;
} | {
  agentMode: "saved";
  agentId: string;
  agent?: InlineAgentInput;
});

export interface SessionStartDialogProps {
  agents: SavedAgent[];
  disabled?: boolean;
  initialAdvancedOpen?: boolean;
  open: boolean;
  preselectedAgentId?: string | null;
  selfHostedEnabled: boolean;
  openAIHostedEnabled?: boolean;
  environmentTemplates?: EnvironmentTemplateCatalog | null;
  vaultCatalog?: VaultCatalog | null;
  onClose: () => void;
  onCreateEnvironmentTemplate?: (input: CreateEnvironmentTemplateInput) => Promise<EnvironmentTemplate>;
  onSubmit: (input: SessionStartInput) => Promise<void>;
}

export const genericSessionStartError = "Agent Core could not create the Session. Review the Core connection and try again.";

export function safeSessionStartError(error: unknown): string {
  return error instanceof AgentCoreError && error.message.trim()
    ? error.message
    : genericSessionStartError;
}

function firstStartableAgentId(agents: SavedAgent[], preferred?: string | null): string {
  return agents.find((agent) => agent.id === preferred)?.id ?? agents[0]?.id ?? "";
}

function resetAgentOverrideSource(
  values: SessionStartDetailsValues,
  agent: SavedAgent | undefined,
  catalog: VaultCatalog | null,
): SessionStartDetailsValues {
  const source = sessionStartDetailsFromAgent(agent, catalog);
  return {
    ...values,
    overridesEnabled: false,
    overrideModelEnabled: source.overrideModelEnabled,
    overrideModel: source.overrideModel,
    overrideInstructionsEnabled: source.overrideInstructionsEnabled,
    overrideInstructions: source.overrideInstructions,
    overrideTextEnabled: source.overrideTextEnabled,
    overrideTextVerbosity: source.overrideTextVerbosity,
    resetMultiAgent: source.resetMultiAgent,
    resetReasoning: source.resetReasoning,
    resetServiceTier: source.resetServiceTier,
    toolsMode: source.toolsMode,
    overrideTools: source.overrideTools,
  };
}

function sorted(values: readonly string[]): string[] {
  return [...values].sort();
}

function hasHttpMcpTool(agent: SavedAgent): boolean {
  return agent.tools.some((rawTool) => {
    if (rawTool === null || typeof rawTool !== "object" || Array.isArray(rawTool)) return false;
    const tool = rawTool as Record<string, unknown>;
    const transport = tool.transport;
    return tool.type === "mcp"
      && transport !== null
      && typeof transport === "object"
      && !Array.isArray(transport)
      && (transport as Record<string, unknown>).type === "http";
  });
}

export function SessionStartDialog({
  agents,
  disabled = false,
  initialAdvancedOpen = false,
  open,
  preselectedAgentId,
  selfHostedEnabled,
  openAIHostedEnabled = false,
  environmentTemplates = null,
  vaultCatalog = null,
  onClose,
  onCreateEnvironmentTemplate,
  onSubmit,
}: SessionStartDialogProps) {
  const formId = useId();
  const environmentName = useId();
  const agentModeName = useId();
  const initialAgentId = firstStartableAgentId(agents, preselectedAgentId);
  const [agentMode, setAgentMode] = useState<SessionAgentMode>(() => agents.length ? "saved" : "inline");
  const [agentId, setAgentId] = useState(() => initialAgentId);
  const [details, setDetails] = useState<SessionStartDetailsValues>(() => (
    sessionStartDetailsFromAgent(agents.find((agent) => agent.id === initialAgentId), vaultCatalog)
  ));
  const [manualVaultIds, setManualVaultIds] = useState<string[]>([]);
  const [sandboxNodeId, setSandboxNodeId] = useState("");
  const [environmentType, setEnvironmentType] = useState<SessionEnvironmentType>("none");
  const [workspaceDirectory, setWorkspaceDirectory] = useState("");
  const [hostedNetwork, setHostedNetwork] = useState<HostedNetworkChoice>("default");
  const [templateId, setTemplateId] = useState("");
  const [templateFormOpen, setTemplateFormOpen] = useState(false);
  const [templateName, setTemplateName] = useState("");
  const [templateNetwork, setTemplateNetwork] = useState<OpenAIHostedNetworkAccess>("enabled");
  const [templateSaving, setTemplateSaving] = useState(false);
  const [templateError, setTemplateError] = useState<string | null>(null);
  const [advancedOpen, setAdvancedOpen] = useState(initialAdvancedOpen);
  const [metadataError, setMetadataError] = useState<string | null>(null);
  const [agentError, setAgentError] = useState<string | null>(null);
  const [requestError, setRequestError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const attemptRef = useRef<SessionCreateAttempt | null>(null);
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      const nextAgentId = firstStartableAgentId(agents, preselectedAgentId);
      setAgentMode(agents.length ? "saved" : "inline");
      setAgentId(nextAgentId);
      setDetails(sessionStartDetailsFromAgent(
        agents.find((agent) => agent.id === nextAgentId),
        vaultCatalog,
      ));
      setManualVaultIds([]);
      setEnvironmentType("none");
      setSandboxNodeId("");
      setWorkspaceDirectory("");
      setHostedNetwork("default");
      setTemplateId("");
      setTemplateFormOpen(false);
      setTemplateName("");
      setTemplateNetwork("enabled");
      setTemplateError(null);
      setAdvancedOpen(initialAdvancedOpen);
      setMetadataError(null);
      setAgentError(null);
      setRequestError(null);
      attemptRef.current = null;
    }
    wasOpenRef.current = open;
  }, [agents, initialAdvancedOpen, open, preselectedAgentId, vaultCatalog]);

  useEffect(() => {
    if (!selfHostedEnabled && environmentType === "self_hosted") {
      setEnvironmentType("none");
      setSandboxNodeId("");
    }
  }, [environmentType, selfHostedEnabled]);

  useEffect(() => {
    if (!openAIHostedEnabled && environmentType === "openai_hosted") {
      setEnvironmentType("none");
      setSandboxNodeId("");
      setHostedNetwork("default");
      setTemplateId("");
    }
  }, [environmentType, openAIHostedEnabled]);

  const availableTemplates = environmentTemplates?.state === "ready" ? environmentTemplates.templates : [];
  const selectedTemplate = availableTemplates.find((template) => template.id === templateId) ?? null;

  useEffect(() => {
    // A Template that Core no longer lists cannot be referenced by this Session.
    if (templateId && !availableTemplates.some((template) => template.id === templateId)) {
      setTemplateId("");
    }
  }, [availableTemplates, templateId]);

  useEffect(() => {
    if (!open || agentMode !== "saved") return;
    const current = agents.find((agent) => agent.id === agentId);
    if (current) return;
    const nextAgentId = firstStartableAgentId(agents, preselectedAgentId);
    if (!nextAgentId) {
      setAgentMode("inline");
      setAgentId("");
      return;
    }
    setAgentId(nextAgentId);
    setDetails((values) => resetAgentOverrideSource(
      values,
      agents.find((agent) => agent.id === nextAgentId),
      vaultCatalog,
    ));
  }, [agentId, agentMode, agents, open, preselectedAgentId, vaultCatalog]);

  const selectedAgent = agents.find((agent) => agent.id === agentId);
  const agentValidation = agentMode === "inline"
    ? validateInlineSessionAgent(details, vaultCatalog)
    : validateSessionAgentOverrides(details, selectedAgent, vaultCatalog);
  const effectiveAgent = agentValidation.effectiveAgent;
  const environment = sessionEnvironmentInput(
    environmentType,
    workspaceDirectory,
    hostedNetwork,
    environmentType === "openai_hosted" && selectedTemplate ? selectedTemplate.id : null,
  );
  const templateNetworkBlocker = environmentType === "openai_hosted"
    ? hostedNetworkNarrowingBlocker(selectedTemplate, hostedNetwork === "default" ? null : hostedNetwork)
    : null;
  const environmentAdmissionBlocker = effectiveAgent
    ? sessionEnvironmentAdmissionBlocker(effectiveAgent, environmentType)
    : null;
  const hasEffectiveHttpMcp = effectiveAgent ? hasHttpMcpTool(effectiveAgent) : null;
  const applicableManualVaultIds = hasEffectiveHttpMcp === false ? [] : manualVaultIds;
  const vaultPlan = effectiveAgent
    ? deriveSessionVaultPlan(effectiveAgent, vaultCatalog, applicableManualVaultIds)
    : null;
  const showVaultAttachments = Boolean(vaultPlan && (
    vaultPlan.resolutions.length > 0
    || vaultPlan.blocker
    || applicableManualVaultIds.length > 0
  ));
  const initialInput = projectSessionInitialInput(details.initialInput);
  const initialInputError = initialInput.ok ? sessionInitialInputError(initialInput.input, environmentType) : initialInput.error;
  const workspaceError = environmentType === "self_hosted" ? environment.error : null;
  const formDisabled = disabled || submitting;
  const canSubmit = open
    && !formDisabled
    && (agentMode === "inline" || Boolean(agentId))
    && Boolean(effectiveAgent)
    && !agentValidation.overrideError
    && !vaultPlan?.blocker
    && !environmentAdmissionBlocker
    && !templateNetworkBlocker
    && !initialInputError
    && Boolean(environment.input);
  const advancedNeedsAttention = Boolean(
    agentValidation.overrideError
    || vaultPlan?.blocker
    || !initialInput.ok
    || agentError,
  );
  const shouldAutoOpenAdvanced = Boolean(
    (agentMode === "saved" && agentValidation.overrideError)
    || vaultPlan?.blocker
    || !initialInput.ok,
  );

  useEffect(() => {
    if (open && shouldAutoOpenAdvanced) setAdvancedOpen(true);
  }, [open, shouldAutoOpenAdvanced]);

  useEffect(() => {
    if (hasEffectiveHttpMcp === false && manualVaultIds.length > 0) {
      setManualVaultIds([]);
    }
  }, [hasEffectiveHttpMcp, manualVaultIds.length]);

  const updateDetails = <Key extends keyof SessionStartDetailsValues>(
    key: Key,
    value: SessionStartDetailsValues[Key],
  ) => {
    setDetails((current) => ({ ...current, [key]: value }));
    setAgentError(null);
    setRequestError(null);
  };

  const updateInitialText = (value: string) => {
    updateDetails(
      "initialInput",
      sessionInitialInputDraftReducer(details.initialInput, { type: "set-text", value }),
    );
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submittingRef.current || !canSubmit || !environment.input) return;

    const validation = validateSessionStartDetails(
      details,
      agentMode,
      selectedAgent,
      vaultCatalog,
      environment.input.type,
    );
    setMetadataError(validation.metadataError ?? null);
    setAgentError(validation.agentError ?? null);
    if (!validation.request || !validation.effectiveAgent) {
      if (validation.inputError || validation.agentError) {
        setAdvancedOpen(true);
      }
      return;
    }
    if (sessionEnvironmentAdmissionBlocker(validation.effectiveAgent, environment.input.type)) return;
    const submittedManualVaultIds = hasHttpMcpTool(validation.effectiveAgent)
      ? manualVaultIds
      : [];
    const submittedVaultPlan = deriveSessionVaultPlan(
      validation.effectiveAgent,
      vaultCatalog,
      submittedManualVaultIds,
    );
    if (submittedVaultPlan.blocker) {
      setAgentError(submittedVaultPlan.blocker);
      setAdvancedOpen(true);
      return;
    }

    const common = {
      environment: environment.input,
      ...(environment.input.type === "openai_hosted" && sandboxNodeId ? { sandboxNodeId } : {}),
      metadata: validation.request.metadata,
      stream: validation.request.stream,
      vaultIds: submittedVaultPlan.vaultIds,
      manualVaultIds: sorted(submittedManualVaultIds),
      ...(validation.request.input === undefined ? {} : { input: validation.request.input }),
    };
    const draft: Omit<SessionStartInput, "idempotencyKey"> = agentMode === "inline"
      ? {
          ...common,
          agentMode: "inline",
          agent: validation.request.agent as InlineAgentInput,
        }
      : {
          ...common,
          agentMode: "saved",
          agentId,
          ...(validation.request.agent === undefined ? {} : { agent: validation.request.agent }),
        };
    const attempt = beginSessionCreateAttempt(draft, attemptRef.current);
    const retry = attemptRef.current?.fingerprint === attempt.fingerprint;
    attemptRef.current = attempt;
    submittingRef.current = true;
    setSubmitting(true);
    setRequestError(null);
    try {
      // A creation SSE retry has no created event. JSON recovers the same Session ID.
      await onSubmit({ ...draft, stream: retry ? false : draft.stream, idempotencyKey: attempt.idempotencyKey } as SessionStartInput);
      onClose();
    } catch (error) {
      setRequestError(safeSessionStartError(error));
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  const saveTemplate = async () => {
    if (!onCreateEnvironmentTemplate || templateSaving) return;
    const name = templateName.trim();
    if ([...name].length > 256) {
      setTemplateError("A Template name accepts at most 256 characters.");
      return;
    }
    setTemplateSaving(true);
    setTemplateError(null);
    try {
      const template = await onCreateEnvironmentTemplate({
        ...(name ? { name } : {}),
        network: { access: templateNetwork },
      });
      setTemplateId(template.id);
      setTemplateFormOpen(false);
      setTemplateName("");
      if (template.network.access === "disabled" && hostedNetwork === "enabled") setHostedNetwork("default");
    } catch (error) {
      setTemplateError(error instanceof AgentCoreError && error.message.trim()
        ? error.message
        : "Agent Core could not save this Environment Template.");
    } finally {
      setTemplateSaving(false);
    }
  };

  const setMode = (mode: SessionAgentMode) => {
    setAgentMode(mode);
    setManualVaultIds([]);
    setAgentError(null);
    setRequestError(null);
  };
  const toggleManualVault = (vaultId: string, checked: boolean) => {
    setManualVaultIds((current) => checked
      ? sorted([...new Set([...current, vaultId])])
      : current.filter((candidate) => candidate !== vaultId));
    setRequestError(null);
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Create a Session"
      footer={(
        <>
          <button className="button outline" type="button" onClick={onClose}>Cancel</button>
          <button className="button primary" type="submit" form={formId} disabled={!canSubmit}>
            {submitting ? "Creating…" : "Create Session"}
          </button>
        </>
      )}
    >
      <form id={formId} className="form-stack session-start-form" onSubmit={(event) => void submit(event)} noValidate>
        {requestError ? (
          <div className="session-action-error" role="alert">
            <strong>Session wasn’t created</strong>
            <span>{requestError}</span>
            <small>Retrying this unchanged request reuses the original idempotency key.</small>
          </div>
        ) : null}

        {agentMode === "saved" ? (
          <label className="field">
            <span>Agent</span>
            <select
              aria-label="Saved Agent"
              value={agentId}
              onChange={(event) => {
                const nextAgentId = event.target.value;
                setAgentId(nextAgentId);
                setManualVaultIds([]);
                setDetails((values) => resetAgentOverrideSource(
                  values,
                  agents.find((agent) => agent.id === nextAgentId),
                  vaultCatalog,
                ));
                setRequestError(null);
              }}
              disabled={formDisabled}
              required
            >
              {!agents.length ? <option value="">No saved Agents</option> : null}
              {agents.map((agent) => (
                <option value={agent.id} key={agent.id}>{agent.name || agent.id} · {agent.model}</option>
              ))}
            </select>
            <small>This Session snapshots the selected Agent. The saved Agent is never modified.</small>
          </label>
        ) : (
          <section className="form-stack" aria-label="Inline Agent">
            <label className="field">
              <span>Inline model</span>
              <input value={details.inlineModel} onChange={(event) => updateDetails("inlineModel", event.target.value)} disabled={formDisabled} spellCheck={false} required />
              <small>Required. This is sent inside <code>agent</code>; no <code>agent_id</code> is sent.</small>
            </label>
            <label className="field">
              <span>Inline instructions</span>
              <textarea value={details.inlineInstructions} onChange={(event) => updateDetails("inlineInstructions", event.target.value)} disabled={formDisabled} rows={4} />
              <small>Optional. Blank instructions are omitted so Core applies the inline default.</small>
            </label>
            <SessionToolsEditor label="Inline tools" tools={details.inlineTools} catalog={vaultCatalog} disabled={formDisabled} onChange={(tools) => updateDetails("inlineTools", tools)} />
          </section>
        )}

        {agentValidation.overrideError || agentError ? (
          <div className="notice warning" role="alert">{agentError ?? agentValidation.overrideError}</div>
        ) : null}

        <label className="field">
          <span>Title</span>
          <input value={details.title} onChange={(event) => { updateDetails("title", event.target.value); setMetadataError(null); }} placeholder={selectedAgent?.name || "Untitled Session"} aria-describedby={`${formId}-title-help${metadataError ? ` ${formId}-title-error` : ""}`} aria-invalid={Boolean(metadataError)} maxLength={512} disabled={formDisabled} />
          <small id={`${formId}-title-help`}>Optional, up to 512 characters. A blank title falls back to Core’s durable Agent snapshot naming.</small>
          {metadataError ? <small className="field-error" id={`${formId}-title-error`} role="alert">{metadataError}</small> : null}
        </label>

        <div className="field session-first-message">
          <span id={`${formId}-first-message-label`}>First message</span>
          {details.initialInput.mode === "text" ? (
            <textarea
              aria-labelledby={`${formId}-first-message-label`}
              value={details.initialInput.text}
              onChange={(event) => updateInitialText(event.target.value)}
              rows={4}
              placeholder={environmentType === "none" ? "Write the first message…" : "Optional first message…"}
              aria-required={environmentType === "none"}
              aria-describedby={`${formId}-first-message-help`}
              disabled={formDisabled}
            />
          ) : (
            <span className="session-structured-input-summary">
              <span>{details.initialInput.messages.length} ordered user {details.initialInput.messages.length === 1 ? "message" : "messages"} configured</span>
              <button className="button outline" type="button" onClick={() => setAdvancedOpen(true)} disabled={formDisabled}>Edit messages</button>
            </span>
          )}
          <small id={`${formId}-first-message-help`}>{environmentType === "none" ? "Required without an Environment. " : "Optional. "}A nonblank message starts the first Turn during Session creation.</small>
        </div>

        <fieldset className="session-environment-options" disabled={formDisabled}>
          <legend>Execution environment</legend>
          <label className="session-environment-option"><input type="radio" name={environmentName} value="none" checked={environmentType === "none"} onChange={() => { setEnvironmentType("none"); setRequestError(null); }} /><span><strong>No environment</strong><small>Create a normal chat Session without a Workspace executor.</small></span></label>
          {selfHostedEnabled ? <label className="session-environment-option"><input type="radio" name={environmentName} value="self_hosted" checked={environmentType === "self_hosted"} onChange={() => { setEnvironmentType("self_hosted"); setRequestError(null); }} /><span><strong>Self-hosted</strong><small>Connect an operator-managed Linux executor and its existing Workspace.</small></span></label> : null}
          {openAIHostedEnabled ? <label className="session-environment-option"><input type="radio" name={environmentName} value="openai_hosted" checked={environmentType === "openai_hosted"} onChange={() => { setEnvironmentType("openai_hosted"); setRequestError(null); }} /><span><strong>Managed hosted</strong><small>Ask an operator-qualified Core to provision an isolated managed Runtime and Workspace.</small></span></label> : null}
        </fieldset>

        {selfHostedEnabled && environmentType === "self_hosted" ? (
          <label className="field"><span>Workspace directory</span><input value={workspaceDirectory} onChange={(event) => { setWorkspaceDirectory(event.target.value); setRequestError(null); }} placeholder="/workspace" aria-describedby={`${formId}-workspace-help${workspaceError ? ` ${formId}-workspace-error` : ""}`} aria-invalid={Boolean(workspaceError)} disabled={formDisabled} required /><small id={`${formId}-workspace-help`}>Absolute path inside the executor host or container. This Web never receives its Environment key.</small>{workspaceError ? <small className="field-error" id={`${formId}-workspace-error`} role="alert">{workspaceError}</small> : null}</label>
        ) : null}
        {openAIHostedEnabled && environmentType === "openai_hosted" ? (
          <>
            <label className="field">
              <span>Environment Template</span>
              <select
                aria-label="Reusable Environment Template"
                value={templateId}
                onChange={(event) => { setTemplateId(event.target.value); setRequestError(null); }}
                disabled={formDisabled || environmentTemplates?.state !== "ready" || availableTemplates.length === 0}
              >
                <option value="">Inline configuration (no Template)</option>
                {availableTemplates.map((template) => (
                  <option value={template.id} key={template.id}>{environmentTemplateLabel(template)}</option>
                ))}
              </select>
              <small>
                {environmentTemplates?.state === "ready"
                  ? availableTemplates.length
                    ? "Core resolves the referenced configuration once and freezes it into this Session. The Template ID is never sent to execution, and a later Template change or deletion does not affect this Session."
                    : "This Core exposes no saved Templates. The Session uses inline configuration."
                  : environmentTemplates?.state === "unsupported"
                    ? "The connected Core does not expose the Environment Template resource. Only inline configuration is available."
                    : environmentTemplates?.state === "failed"
                      ? `Template selection is unavailable: ${environmentTemplates.message}`
                      : "Template availability has not been confirmed for this Core."}
              </small>
            </label>
            {onCreateEnvironmentTemplate && environmentTemplates?.state === "ready" ? (
              templateFormOpen ? (
                <section className="form-stack" aria-label="New Environment Template">
                  <label className="field">
                    <span>Template name</span>
                    <input
                      value={templateName}
                      onChange={(event) => { setTemplateName(event.target.value); setTemplateError(null); }}
                      placeholder="Restricted outbound access"
                      disabled={formDisabled || templateSaving}
                      maxLength={256}
                    />
                    <small>Optional. Core preserves this name verbatim, up to 256 characters.</small>
                  </label>
                  <label className="field">
                    <span>Template network access</span>
                    <select
                      aria-label="Template network access"
                      value={templateNetwork}
                      onChange={(event) => setTemplateNetwork(event.target.value as OpenAIHostedNetworkAccess)}
                      disabled={formDisabled || templateSaving}
                    >
                      <option value="enabled">Enabled</option>
                      <option value="disabled">Disabled</option>
                    </select>
                    <small>Only enabled and disabled are supported. Packages, files, skills, plugins, environment variables, and setup commands are not.</small>
                  </label>
                  {templateError ? <div className="notice warning" role="alert">{templateError}</div> : null}
                  <span className="session-structured-input-summary">
                    <span>Saving creates durable configuration only. No Runtime is allocated.</span>
                    <button className="button outline" type="button" onClick={() => { setTemplateFormOpen(false); setTemplateError(null); }} disabled={templateSaving}>Cancel</button>
                    <button className="button primary" type="button" onClick={() => void saveTemplate()} disabled={formDisabled || templateSaving}>
                      {templateSaving ? "Saving…" : "Save Template"}
                    </button>
                  </span>
                </section>
              ) : (
                <span className="session-structured-input-summary">
                  <span>Reuse one network policy across managed Sessions.</span>
                  <button className="button outline" type="button" onClick={() => setTemplateFormOpen(true)} disabled={formDisabled}>New Template</button>
                </span>
              )
            ) : null}
            <label className="field">
              <span>Network access</span>
              <select aria-label="Managed Environment network access" value={hostedNetwork} onChange={(event) => { setHostedNetwork(event.target.value as HostedNetworkChoice); setRequestError(null); }} disabled={formDisabled}>
                <option value="default">{selectedTemplate ? `Inherit from Template (${selectedTemplate.network.access})` : "Core default (enabled)"}</option>
                <option value="enabled">Enabled</option>
                <option value="disabled">Disabled</option>
              </select>
              <small>Disabled confines native tools while trusted model and Core connectivity remain operator-owned. Restricted domains are not supported.</small>
            </label>
            <p className="session-environment-note">
              A managed Runtime is provisioned by the connected Core. Core selects a sandbox node automatically, or you can choose an available node in Advanced settings. Local nodes run on the Core server. Provisioning success is not execution readiness, and a lost or expired Runtime destroys its Workspace without an automatic replacement.
            </p>
          </>
        ) : null}

        {environmentAdmissionBlocker ? <div className="notice warning" role="alert">{environmentAdmissionBlocker}</div> : null}
        {templateNetworkBlocker ? <div className="notice warning" role="alert">{templateNetworkBlocker}</div> : null}
        {vaultPlan?.blocker ? <div className="notice warning" role="alert">{vaultPlan.blocker}</div> : null}

        <div className="session-start-advanced">
          <button
            className="session-start-advanced-toggle"
            type="button"
            aria-expanded={advancedOpen}
            aria-controls={`${formId}-advanced-settings`}
            onClick={() => setAdvancedOpen((value) => !value)}
            disabled={formDisabled}
          >
            <span>
              <strong>Advanced settings</strong>
              <small>Agent source and overrides, Tools &amp; Vaults, and structured input.</small>
            </span>
            <span className="session-start-advanced-state">{advancedNeedsAttention ? "Needs attention" : advancedOpen ? "Hide" : "Show"}</span>
          </button>

          {advancedOpen ? (
            <section id={`${formId}-advanced-settings`} className="session-start-advanced-body form-stack" aria-label="Advanced Session settings">
              {environmentType === "openai_hosted" ? <SandboxNodeSelector value={sandboxNodeId} onChange={setSandboxNodeId} disabled={formDisabled} /> : null}
              <fieldset className="session-agent-source-options" disabled={formDisabled}>
                <legend>Agent source</legend>
                <label>
                  <input type="radio" name={agentModeName} value="saved" checked={agentMode === "saved"} onChange={() => setMode("saved")} disabled={!agents.length} />
                  Use Saved Agent
                </label>
                <label>
                  <input type="radio" name={agentModeName} value="inline" checked={agentMode === "inline"} onChange={() => setMode("inline")} />
                  Use Inline Agent
                </label>
              </fieldset>

              {agentMode === "saved" ? (
                <>
                  <label className="session-environment-option">
                    <input type="checkbox" checked={details.overridesEnabled} onChange={(event) => updateDetails("overridesEnabled", event.target.checked)} disabled={formDisabled || !selectedAgent} />
                    <span><strong>Configure Session-only overrides</strong><small>Replace supported fields for this Session without editing the saved Agent.</small></span>
                  </label>

                  {details.overridesEnabled ? (
                    <section className="form-stack session-agent-overrides" aria-label="Session-only Agent overrides">
                      <label className="agent-mcp-required"><input type="checkbox" checked={details.overrideModelEnabled} onChange={(event) => updateDetails("overrideModelEnabled", event.target.checked)} disabled={formDisabled} /> Replace model</label>
                      {details.overrideModelEnabled ? <label className="field"><span>Session model</span><input value={details.overrideModel} onChange={(event) => updateDetails("overrideModel", event.target.value)} disabled={formDisabled} spellCheck={false} required /></label> : null}

                      <label className="agent-mcp-required"><input type="checkbox" checked={details.overrideInstructionsEnabled} onChange={(event) => updateDetails("overrideInstructionsEnabled", event.target.checked)} disabled={formDisabled} /> Replace instructions</label>
                      {details.overrideInstructionsEnabled ? <label className="field"><span>Session instructions</span><textarea value={details.overrideInstructions} onChange={(event) => updateDetails("overrideInstructions", event.target.value)} disabled={formDisabled} rows={4} /><small>Blank clears the inherited instructions with explicit null.</small></label> : null}

                      <label className="agent-mcp-required"><input type="checkbox" checked={details.overrideTextEnabled} onChange={(event) => updateDetails("overrideTextEnabled", event.target.checked)} disabled={formDisabled} /> Replace text configuration and reset format to text</label>
                      {details.overrideTextEnabled ? <label className="field"><span>Text verbosity</span><select value={details.overrideTextVerbosity} onChange={(event) => updateDetails("overrideTextVerbosity", event.target.value as SessionStartDetailsValues["overrideTextVerbosity"])} disabled={formDisabled}><option value="low">low</option><option value="medium">medium</option><option value="high">high</option></select><small>JSON schema is never copied into an executable Session override.</small></label> : null}

                      <label className="agent-mcp-required"><input type="checkbox" checked={details.resetMultiAgent} onChange={(event) => updateDetails("resetMultiAgent", event.target.checked)} disabled={formDisabled} /> Reset multi-agent to disabled</label>
                      <label className="agent-mcp-required"><input type="checkbox" checked={details.resetReasoning} onChange={(event) => updateDetails("resetReasoning", event.target.checked)} disabled={formDisabled} /> Reset reasoning to Core defaults</label>
                      <label className="agent-mcp-required"><input type="checkbox" checked={details.resetServiceTier} onChange={(event) => updateDetails("resetServiceTier", event.target.checked)} disabled={formDisabled} /> Reset service tier to auto</label>

                      <fieldset className="session-initial-input-modes" disabled={formDisabled}>
                        <legend>Tools whole-field behavior</legend>
                        <label><input type="radio" checked={details.toolsMode === "inherit"} onChange={() => updateDetails("toolsMode", "inherit")} /> Inherit</label>
                        <label><input type="radio" checked={details.toolsMode === "clear"} onChange={() => updateDetails("toolsMode", "clear")} /> Clear all</label>
                        <label><input type="radio" checked={details.toolsMode === "replace"} onChange={() => updateDetails("toolsMode", "replace")} /> Replace</label>
                      </fieldset>
                      {details.toolsMode === "replace" ? <SessionToolsEditor label="Replacement tools" tools={details.overrideTools} catalog={vaultCatalog} disabled={formDisabled} onChange={(tools) => updateDetails("overrideTools", tools)} /> : null}
                    </section>
                  ) : null}
                </>
              ) : null}

              {showVaultAttachments ? (
                <section className="session-vault-plan" aria-labelledby={`${formId}-vault-plan-title`}>
                  <h3 id={`${formId}-vault-plan-title`}>Tools &amp; Vaults</h3>
                  <p>Attach Vaults only when an anonymous MCP should use a matching Credential. Explicit Credentials attach their owning Vault automatically.</p>
                  {vaultCatalog ? vaultCatalog.vaults.length ? (
                    <div className="form-stack">
                      {vaultCatalog.vaults.map((vault) => {
                        const required = vaultPlan?.requiredVaultIds.includes(vault.id) ?? false;
                        const checked = required || manualVaultIds.includes(vault.id);
                        return (
                          <label className="agent-mcp-required" key={vault.id}>
                            <input type="checkbox" checked={checked} disabled={formDisabled || required} onChange={(event) => toggleManualVault(vault.id, event.target.checked)} />
                            {vault.name ?? "Unnamed Vault"}{required ? " · attached automatically" : ""}
                          </label>
                        );
                      })}
                    </div>
                  ) : <small>No Vaults are available. MCP tools without an explicit Credential remain anonymous.</small> : (
                    <small>The complete Vault metadata catalog is unavailable. Credential-backed Session creation stays blocked.</small>
                  )}
                  {vaultPlan?.resolutions.length ? (
                    <ul>
                      {vaultPlan.resolutions.map((resolution, index) => (
                        <li key={`${resolution.serverLabel}:${index}`}>
                          <code>{resolution.serverLabel}</code>
                          <span>{resolution.kind === "anonymous" ? "Anonymous for this Session" : `${resolution.kind === "explicit" ? "Explicit" : "Implicit unique match"} · ${resolution.credential?.name} · ${resolution.vault?.name ?? "Unnamed Vault"}`}</span>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                  <small>Web previews metadata only and never reads Credential tokens.</small>
                </section>
              ) : null}

              <SessionInitialInputEditor required={environmentType === "none"} draft={details.initialInput} disabled={formDisabled} showTextField={false} onChange={(draft) => updateDetails("initialInput", draft)} />
            </section>
          ) : null}
        </div>
      </form>
    </Modal>
  );
}
