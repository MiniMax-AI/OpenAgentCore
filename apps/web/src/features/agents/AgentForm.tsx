import { Info } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import type { CoreHarnessKind, SavedAgent } from "@agents-core-web/agents-client";

import {
  buildModelOptionGroups,
  CUSTOM_MODEL_OPTION,
  modelIdFromOption,
  modelOptionValue,
} from "../../lib/model-options";
import type { VaultCatalog } from "../vaults/vault-catalog";
import { vaultName } from "../vaults/vault-catalog";
import { type AgentFormSubmitInput, type AgentFormValues, type AgentToolDraft, validateAgentForm, valuesFromAgent } from "./agent-form";

interface AgentFormProps {
  agent?: SavedAgent;
  defaultHarness?: CoreHarnessKind;
  disabled?: boolean;
  enabledHarnesses?: readonly CoreHarnessKind[] | null;
  formId: string;
  initialValues?: AgentFormValues;
  knownModels: string[];
  vaultCatalog?: VaultCatalog | null;
  onDraftChange?: (values: AgentFormValues) => void;
  onSubmit: (input: AgentFormSubmitInput) => Promise<unknown>;
}

export function harnessLabel(harness: CoreHarnessKind): string {
  if (harness === "claude_sdk") return "Claude SDK";
  if (harness === "mcode") return "MiniMax Code";
  return "Codex";
}

export function AgentForm({
  agent,
  defaultHarness,
  disabled = false,
  enabledHarnesses = null,
  formId,
  initialValues,
  knownModels,
  vaultCatalog = null,
  onDraftChange,
  onSubmit,
}: AgentFormProps) {
  const { t } = useTranslation("agents");
  const nameRef = useRef<HTMLInputElement>(null);
  const initial = agent ? valuesFromAgent(agent, vaultCatalog) : initialValues ?? valuesFromAgent(undefined, vaultCatalog);
  const initialHarness = agent
    ? initial.harness
    : initial.harness || (defaultHarness && enabledHarnesses?.includes(defaultHarness) ? defaultHarness : "");
  const options = buildModelOptionGroups(
    knownModels,
    import.meta.env.VITE_AGENT_MODEL_PRESETS,
    initial.model || import.meta.env.VITE_AGENT_DEFAULT_MODEL,
  );
  const initialIsSuggested = [...options.configured, ...options.previouslyUsed].includes(initial.model);
  const [name, setName] = useState(initial.name);
  const [harness, setHarness] = useState<CoreHarnessKind | "">(initialHarness);
  const [harnessModified, setHarnessModified] = useState(initial.harnessModified);
  const [modelChoice, setModelChoice] = useState(
    initial.model && !initialIsSuggested ? CUSTOM_MODEL_OPTION : modelOptionValue(initial.model || options.defaultModel),
  );
  const [customModel, setCustomModel] = useState(initialIsSuggested ? "" : initial.model);
  const [instructions, setInstructions] = useState(initial.instructions);
  const [metadata, setMetadata] = useState(initial.metadata);
  const [reasoningEffort, setReasoningEffort] = useState(initial.reasoningEffort);
  const [reasoningSummary, setReasoningSummary] = useState(initial.reasoningSummary);
  const [serviceTier, setServiceTier] = useState(initial.serviceTier);
  const [textFormat] = useState(initial.textFormat);
  const [textVerbosity, setTextVerbosity] = useState(initial.textVerbosity);
  const [tools, setTools] = useState(initial.tools);
  const [toolsModified, setToolsModified] = useState(initial.toolsModified);
  const [configurationError, setConfigurationError] = useState<string | null>(null);
  const [modelError, setModelError] = useState<string | null>(null);
  const [metadataError, setMetadataError] = useState<string | null>(null);
  const [nameError, setNameError] = useState<string | null>(null);
  const [toolsError, setToolsError] = useState<string | null>(null);
  const model = modelChoice === CUSTOM_MODEL_OPTION ? customModel : modelIdFromOption(modelChoice) ?? "";

  const updateTools = (update: (current: AgentToolDraft[]) => AgentToolDraft[]) => {
    setTools((current) => update(current));
    setToolsModified(true);
    if (toolsError) setToolsError(null);
  };

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => nameRef.current?.focus());
    return () => window.cancelAnimationFrame(frame);
  }, []);

  useEffect(() => {
    if (!agent && !harness && defaultHarness && enabledHarnesses?.includes(defaultHarness)) {
      setHarness(defaultHarness);
    }
  }, [agent, defaultHarness, enabledHarnesses, harness]);

  useEffect(() => {
    onDraftChange?.({
      name,
      harness,
      harnessModified,
      model,
      instructions,
      metadata,
      reasoningEffort,
      reasoningSummary,
      serviceTier,
      textFormat,
      textVerbosity,
      tools,
      toolsModified,
    });
  }, [harness, harnessModified, instructions, metadata, model, name, onDraftChange, reasoningEffort, reasoningSummary, serviceTier, textFormat, textVerbosity, tools, toolsModified]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const result = validateAgentForm({
      name,
      harness,
      harnessModified,
      model,
      instructions,
      metadata,
      reasoningEffort,
      reasoningSummary,
      serviceTier,
      textFormat,
      textVerbosity,
      tools,
      toolsModified,
    }, agent ? "update" : "create", vaultCatalog);
    setConfigurationError(result.configurationError ?? null);
    setModelError(result.modelError ?? null);
    setMetadataError(result.metadataError ?? null);
    setNameError(result.nameError ?? null);
    setToolsError(result.toolsError ?? null);
    if (!result.input) return;
    await onSubmit(result.input);
  };

  return (
    <form id={formId} className="form-stack" onSubmit={(event) => void submit(event)} noValidate>
      <fieldset className="agent-form-fields" disabled={disabled}>
      <label className="field">
        <span>{t("form.name")}</span>
        <input
          ref={nameRef}
          value={name}
          onChange={(event) => {
            setName(event.target.value);
            if (nameError) setNameError(null);
          }}
          placeholder={t("form.namePlaceholder")}
          data-agent-initial-focus="true"
          aria-describedby={`${formId}-name-help${nameError ? ` ${formId}-name-error` : ""}`}
          aria-invalid={Boolean(nameError)}
        />
        <small id={`${formId}-name-help`}>{t("form.nameHelp")}</small>
        {nameError ? <small className="field-error" id={`${formId}-name-error`} role="alert">{nameError}</small> : null}
      </label>
      <label className="field">
        <span>{t("form.instructions")}</span>
        <textarea
          value={instructions}
          onChange={(event) => setInstructions(event.target.value)}
          placeholder={t("form.instructionsPlaceholder")}
          rows={5}
        />
      </label>
      {enabledHarnesses !== null ? (
        <div className="field">
          <label className="field-label" htmlFor={`${formId}-harness`}>
            <span>{t("form.harness")}</span>
            <span className="field-optional">{t("form.coreStartup")}</span>
          </label>
          <select
            id={`${formId}-harness`}
            value={harness}
            onChange={(event) => {
              setHarness(event.target.value as CoreHarnessKind | "");
              setHarnessModified(true);
            }}
            aria-describedby={`${formId}-harness-help`}
            disabled={enabledHarnesses.length === 0}
          >
            {agent && !agent.x_agents_core && defaultHarness ? (
              <option value="">{t("form.coreDefault", { name: harnessLabel(defaultHarness) })}</option>
            ) : null}
            {harness && !enabledHarnesses.includes(harness) ? (
              <option value={harness} disabled>{t("form.notEnabled", { name: harnessLabel(harness) })}</option>
            ) : null}
            {enabledHarnesses.filter((value) => !(
              agent && !agent.x_agents_core && value === defaultHarness
            )).map((value) => (
              <option value={value} key={value}>{harnessLabel(value)}</option>
            ))}
            {enabledHarnesses.length === 0 ? <option value="">{t("form.noHarness")}</option> : null}
          </select>
          <small id={`${formId}-harness-help`}>
            {t("form.harnessHelp")}
          </small>
        </div>
      ) : null}
      <div className="field">
        <label className="field-label" htmlFor={`${formId}-model`}>
          <span>{t("form.model")}</span>
          <span className="field-optional">{t("form.webSuggestions")}</span>
        </label>
        <select
          id={`${formId}-model`}
          value={modelChoice}
          onChange={(event) => setModelChoice(event.target.value)}
          aria-describedby={`${formId}-model-help ${formId}-model-note${modelError ? ` ${formId}-model-error` : ""}`}
          aria-invalid={Boolean(modelError)}
          required
        >
          <optgroup label={t("form.configured")}>
            {options.configured.map((modelId) => (
              <option value={modelOptionValue(modelId)} key={modelId}>
                {modelId}{modelId === options.defaultModel ? ` · ${t("form.default")}` : ""}
              </option>
            ))}
          </optgroup>
          {options.previouslyUsed.length ? (
            <optgroup label={t("form.previouslyUsed")}>
              {options.previouslyUsed.map((modelId) => (
                <option value={modelOptionValue(modelId)} key={modelId}>{modelId}</option>
              ))}
            </optgroup>
          ) : null}
          <option value={CUSTOM_MODEL_OPTION}>{t("form.customModel")}</option>
        </select>
        <small id={`${formId}-model-help`}>
          {t("form.modelHelp")}
        </small>
        {modelError ? <small className="field-error" id={`${formId}-model-error`} role="alert">{modelError}</small> : null}
      </div>
      {modelChoice === CUSTOM_MODEL_OPTION ? (
        <label className="field">
          <span>{t("form.customModelLabel")}</span>
          <input
            value={customModel}
            onChange={(event) => setCustomModel(event.target.value)}
            placeholder="provider/model-name"
            spellCheck={false}
            aria-describedby={`${formId}-model-note`}
            required
          />
        </label>
      ) : null}
      <div className="model-picker-note" id={`${formId}-model-note`} role="note">
        <Info size={14} strokeWidth={1.5} aria-hidden="true" />
        <span>
          {t("form.modelNote", { model: model || t("form.modelFallback") })}
        </span>
      </div>
      <section className="agent-form-section" aria-labelledby={`${formId}-generation-title`}>
        <div className="agent-form-section-heading">
          <h3 id={`${formId}-generation-title`}>{t("form.generation")}</h3>
          <span>{agent ? t("form.advancedSettings") : t("form.compatibleDefaults")}</span>
        </div>
        <div className="agent-form-grid">
          <label className="field">
            <span>{t("form.textFormat")}</span>
            <input
              value={textFormat.type === "text" ? t("form.text") : t("form.jsonPreserved")}
              readOnly
              aria-describedby={`${formId}-text-format-help`}
            />
            <small id={`${formId}-text-format-help`}>
              {t("form.textFormatHelp")}
            </small>
          </label>
          <label className="field">
            <span>{t("form.reasoningEffort")}</span>
            <select
              value={reasoningEffort}
              onChange={(event) => setReasoningEffort(event.target.value as typeof reasoningEffort)}
              aria-describedby={`${formId}-generation-profile-help`}
              disabled={!agent}
            >
              <option value="">{t("form.coreDefaultValue")}</option>
              {(["none", "minimal", "low", "medium", "high", "xhigh", "max"] as const).map((value) => (
                <option value={value} key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{t("form.textVerbosity")}</span>
            <select
              value={textVerbosity}
              onChange={(event) => setTextVerbosity(event.target.value as typeof textVerbosity)}
              aria-describedby={`${formId}-generation-profile-help`}
              disabled={!agent}
            >
              {(["low", "medium", "high"] as const).map((value) => (
                <option value={value} key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{t("form.reasoningSummary")}</span>
            <select
              value={reasoningSummary}
              onChange={(event) => setReasoningSummary(event.target.value as typeof reasoningSummary)}
              aria-describedby={`${formId}-generation-profile-help`}
              disabled={!agent}
            >
              <option value="">{t("form.coreDefaultValue")}</option>
              {(["concise", "detailed", "auto"] as const).map((value) => (
                <option value={value} key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{t("form.serviceTier")}</span>
            <select
              value={serviceTier}
              onChange={(event) => setServiceTier(event.target.value as typeof serviceTier)}
              aria-describedby={`${formId}-generation-profile-help`}
              disabled={!agent}
            >
              {(["auto", "default", "flex", "priority", "fast"] as const).map((value) => (
                <option value={value} key={value}>{value}</option>
              ))}
            </select>
          </label>
        </div>
        <p className="agent-form-capability-note" id={`${formId}-generation-profile-help`}>
          {agent
            ? t("form.existingProfileHelp")
            : t("form.createProfileHelp")}
        </p>
        {configurationError ? <p className="field-error" role="alert">{configurationError}</p> : null}
      </section>
      <section className="agent-form-section agent-tools-section" aria-labelledby={`${formId}-tools-title`}>
        <div className="agent-form-section-heading">
          <h3 id={`${formId}-tools-title`}>{t("form.tools")}</h3>
          <span>{t("form.coreExecution")}</span>
        </div>
        <p className="agent-form-capability-note">
          {t("form.functionBoundary")}
        </p>
        <p className="agent-form-capability-note">
          {t("form.mcpBoundary")}
        </p>
        <div className="agent-tools-list">
          {tools.map((tool, index) => tool.kind === "read-only" ? (
            <article className="agent-tool-card agent-tool-read-only" key={`read-only-${index}`} aria-label={t("form.readOnlyToolLabel")}>
              <div><strong>{t("form.readOnlyTool")}</strong><span>{tool.label}</span></div>
              <small>{t("form.readOnlyHelp")}</small>
            </article>
          ) : tool.kind === "function" ? (
            <article className="agent-tool-card" key={`function-${index}`}>
              <header><strong>{t("form.function")}</strong><button className="button outline" type="button" onClick={() => updateTools((current) => current.filter((_, candidate) => candidate !== index))}>{t("form.remove")}</button></header>
              <div className="agent-tool-grid">
                <label className="field"><span>{t("form.name")}</span><input value={tool.name} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, name: event.target.value } : candidate))} placeholder="lookup_customer" /></label>
                <label className="field"><span>{t("form.description")}</span><input value={tool.description} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, description: event.target.value } : candidate))} placeholder={t("form.descriptionPlaceholder")} /></label>
              </div>
              <label className="field"><span>{t("form.parameters")}</span><textarea value={tool.parameters} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, parameters: event.target.value } : candidate))} rows={7} spellCheck={false} /><small>{t("form.parametersHelp")}</small></label>
            </article>
          ) : (
            <article className="agent-tool-card" key={`mcp-${index}`}>
              <header><strong>{tool.credentialId ? t("form.vaultMcp") : t("form.anonymousMcp")}</strong><button className="button outline" type="button" onClick={() => updateTools((current) => current.filter((_, candidate) => candidate !== index))}>{t("form.remove")}</button></header>
              <div className="agent-tool-grid">
                <label className="field"><span>{t("form.serverLabel")}</span><input value={tool.serverLabel} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, serverLabel: event.target.value } : candidate))} placeholder="docs" /></label>
                <label className="field"><span>{t("form.authentication")}</span><select value={tool.credentialId ?? ""} onChange={(event) => {
                  const credentialId = event.target.value || null;
                  const credential = credentialId ? vaultCatalog?.credentials.find((candidate) => candidate.id === credentialId) : null;
                  updateTools((current) => current.map((candidate, position) => position === index ? {
                    ...tool,
                    credentialId,
                    serverUrl: credential?.auth.mcp_server_url ?? tool.serverUrl,
                  } : candidate));
                }}>
                  <option value="">{t("form.anonymous")}</option>
                  {vaultCatalog?.vaults.map((vault) => {
                    const credentials = vaultCatalog.credentials.filter((credential) => credential.vault_id === vault.id);
                    return credentials.length ? <optgroup label={vaultName(vault)} key={vault.id}>{credentials.map((credential) => <option value={credential.id} key={credential.id}>{credential.name} · {credential.auth.mcp_server_url}</option>)}</optgroup> : null;
                  })}
                </select><small>{vaultCatalog ? t("form.selectedCredentialHelp") : t("form.catalogUnavailable")}</small></label>
                <label className="field"><span>{t("form.serverUrl")}</span><input value={tool.serverUrl} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, serverUrl: event.target.value } : candidate))} placeholder="https://mcp.example/tools" inputMode="url" spellCheck={false} readOnly={Boolean(tool.credentialId)} /></label>
              </div>
              <fieldset className="agent-mcp-allowed-tools">
                <legend>{t("form.allowedTools")}</legend>
                <label><input type="radio" checked={tool.allowedToolsMode === "all"} onChange={() => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, allowedToolsMode: "all", allowedToolsValue: null } : candidate))} /> {t("form.allTools")}</label>
                <label><input type="radio" checked={tool.allowedToolsMode === "list"} onChange={() => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, allowedToolsMode: "list" } : candidate))} /> {t("form.listedTools")}</label>
                {tool.allowedToolsMode === "list" ? <textarea value={tool.allowedTools} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, allowedTools: event.target.value } : candidate))} rows={4} placeholder={'search\nread_document'} spellCheck={false} aria-label={t("form.allowedToolsLabel", { name: tool.serverLabel || t("form.mcpServer") })} /> : null}
                <small>{t("form.allowedToolsHelp")}</small>
              </fieldset>
              <label className="agent-mcp-required"><input type="checkbox" checked={tool.required === true} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, required: event.target.checked } : candidate))} /> {t("form.required")}</label>
              <small>{t("form.mcpWriteHelp")}</small>
            </article>
          ))}
        </div>
        <div className="agent-tool-actions">
          <button className="button outline" type="button" onClick={() => updateTools((current) => [...current, { kind: "function", name: "", description: "", parameters: "{\n  \"type\": \"object\"\n}" }])}>{t("form.addFunction")}</button>
          <button className="button outline" type="button" onClick={() => updateTools((current) => [...current, { kind: "mcp", serverLabel: "", serverUrl: "", allowedToolsMode: "all", allowedTools: "", allowedToolsValue: null, required: false, credentialId: null }])}>{t("form.addMcp")}</button>
        </div>
        {toolsError ? <p className="field-error" role="alert">{toolsError}</p> : null}
      </section>
      <label className="field">
        <span>{t("form.metadata")}</span>
        <textarea
          className="agent-metadata-input"
          value={metadata}
          onChange={(event) => {
            setMetadata(event.target.value);
            if (metadataError) setMetadataError(null);
          }}
          rows={5}
          spellCheck={false}
          aria-describedby={`${formId}-metadata-help${metadataError ? ` ${formId}-metadata-error` : ""}`}
          aria-invalid={Boolean(metadataError)}
        />
        <small id={`${formId}-metadata-help`}>{t("form.metadataHelp")}</small>
        {metadataError ? <small className="field-error" id={`${formId}-metadata-error`} role="alert">{metadataError}</small> : null}
      </label>
      </fieldset>
    </form>
  );
}
