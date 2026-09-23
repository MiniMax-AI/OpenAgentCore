import { Info } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";

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

function harnessLabel(harness: CoreHarnessKind): string {
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
        <span>Name</span>
        <input
          ref={nameRef}
          value={name}
          onChange={(event) => {
            setName(event.target.value);
            if (nameError) setNameError(null);
          }}
          placeholder="Repository builder"
          data-agent-initial-focus="true"
          aria-describedby={`${formId}-name-help${nameError ? ` ${formId}-name-error` : ""}`}
          aria-invalid={Boolean(nameError)}
        />
        <small id={`${formId}-name-help`}>Optional. Agent Core accepts at most 128 Unicode characters.</small>
        {nameError ? <small className="field-error" id={`${formId}-name-error`} role="alert">{nameError}</small> : null}
      </label>
      <label className="field">
        <span>Instructions</span>
        <textarea
          value={instructions}
          onChange={(event) => setInstructions(event.target.value)}
          placeholder="Describe how this Agent should work…"
          rows={5}
        />
      </label>
      {enabledHarnesses !== null ? (
        <div className="field">
          <label className="field-label" htmlFor={`${formId}-harness`}>
            <span>Harness</span>
            <span className="field-optional">Core startup</span>
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
              <option value="">{harnessLabel(defaultHarness)} · Core default</option>
            ) : null}
            {harness && !enabledHarnesses.includes(harness) ? (
              <option value={harness} disabled>{harnessLabel(harness)} · Not enabled</option>
            ) : null}
            {enabledHarnesses.filter((value) => !(
              agent && !agent.x_agents_core && value === defaultHarness
            )).map((value) => (
              <option value={value} key={value}>{harnessLabel(value)}</option>
            ))}
            {enabledHarnesses.length === 0 ? <option value="">No harness enabled</option> : null}
          </select>
          <small id={`${formId}-harness-help`}>
            Only harnesses enabled when this Core process started are selectable. Selection does not prove Runtime, provider, or model readiness.
          </small>
        </div>
      ) : null}
      <div className="field">
        <label className="field-label" htmlFor={`${formId}-model`}>
          <span>Model</span>
          <span className="field-optional">Web suggestions</span>
        </label>
        <select
          id={`${formId}-model`}
          value={modelChoice}
          onChange={(event) => setModelChoice(event.target.value)}
          aria-describedby={`${formId}-model-help ${formId}-model-note${modelError ? ` ${formId}-model-error` : ""}`}
          aria-invalid={Boolean(modelError)}
          required
        >
          <optgroup label="Configured suggestions">
            {options.configured.map((modelId) => (
              <option value={modelOptionValue(modelId)} key={modelId}>
                {modelId}{modelId === options.defaultModel ? " · Default" : ""}
              </option>
            ))}
          </optgroup>
          {options.previouslyUsed.length ? (
            <optgroup label="Previously used by saved Agents">
              {options.previouslyUsed.map((modelId) => (
                <option value={modelOptionValue(modelId)} key={modelId}>{modelId}</option>
              ))}
            </optgroup>
          ) : null}
          <option value={CUSTOM_MODEL_OPTION}>Custom model ID…</option>
        </select>
        <small id={`${formId}-model-help`}>
          Choose a Web suggestion or enter an ID configured for your runtime.
        </small>
        {modelError ? <small className="field-error" id={`${formId}-model-error`} role="alert">{modelError}</small> : null}
      </div>
      {modelChoice === CUSTOM_MODEL_OPTION ? (
        <label className="field">
          <span>Custom model ID</span>
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
          Model choices are editable Web-side suggestions, not a discovered Core catalog. Saving <code>{model || "a model ID"}</code> does not prove the current executor can run it.
        </span>
      </div>
      <section className="agent-form-section" aria-labelledby={`${formId}-generation-title`}>
        <div className="agent-form-section-heading">
          <h3 id={`${formId}-generation-title`}>Generation</h3>
          <span>{agent ? "Advanced saved settings" : "Session-compatible defaults"}</span>
        </div>
        <div className="agent-form-grid">
          <label className="field">
            <span>Text format</span>
            <input
              value={textFormat.type === "text" ? "Text" : "JSON schema · preserved"}
              readOnly
              aria-describedby={`${formId}-text-format-help`}
            />
            <small id={`${formId}-text-format-help`}>
              Current Web-created Sessions use text. Existing JSON schemas are preserved read-only and block Session start.
            </small>
          </label>
          <label className="field">
            <span>Reasoning effort</span>
            <select
              value={reasoningEffort}
              onChange={(event) => setReasoningEffort(event.target.value as typeof reasoningEffort)}
              aria-describedby={`${formId}-generation-profile-help`}
              disabled={!agent}
            >
              <option value="">Core default</option>
              {(["none", "minimal", "low", "medium", "high", "xhigh", "max"] as const).map((value) => (
                <option value={value} key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Text verbosity</span>
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
            <span>Reasoning summary</span>
            <select
              value={reasoningSummary}
              onChange={(event) => setReasoningSummary(event.target.value as typeof reasoningSummary)}
              aria-describedby={`${formId}-generation-profile-help`}
              disabled={!agent}
            >
              <option value="">Core default</option>
              {(["concise", "detailed", "auto"] as const).map((value) => (
                <option value={value} key={value}>{value}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Service tier</span>
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
            ? "Agent Core can save these settings, but the current Session contract rejects explicit reasoning, non-auto service tiers, and non-text formats. Low or high verbosity also needs a compatible Codex model. Saving an incompatible value disables Start Session in this Web."
            : "Creation is locked to the current Session-compatible profile: text format, Core-default reasoning, medium verbosity, and service tier auto. Agent Core can store other values, but this Web cannot start a Session with most of them yet."}
        </p>
        {configurationError ? <p className="field-error" role="alert">{configurationError}</p> : null}
      </section>
      <section className="agent-form-section agent-tools-section" aria-labelledby={`${formId}-tools-title`}>
        <div className="agent-form-section-heading">
          <h3 id={`${formId}-tools-title`}>Tools</h3>
          <span>Core-owned execution only</span>
        </div>
        <p className="agent-form-capability-note">
          Functions cause Core to emit a <code>function_call</code>. An external application, or the existing Function result form, must perform the business action and submit <code>agent.session.input.tool_result</code> with the exact Turn and call identity. This Web does not execute Functions.
        </p>
        <p className="agent-form-capability-note">
          HTTP MCP discovery and calls run on trusted Core service compute. A <code>self_hosted</code> Workspace can run executor commands, but MCP never runs in this browser or that executor. Anonymous and Vault-backed static bearer service-origin HTTP MCP are configurable when the complete Core catalog is loaded.
        </p>
        <div className="agent-tools-list">
          {tools.map((tool, index) => tool.kind === "read-only" ? (
            <article className="agent-tool-card agent-tool-read-only" key={`read-only-${index}`} aria-label="Read-only saved tool">
              <div><strong>Read-only saved tool</strong><span>{tool.label}</span></div>
              <small>It is preserved unchanged. Web Search, Code Mode commands/files, credentials, OAuth, stdio, headers, and unknown tool variants cannot be enabled or edited here.</small>
            </article>
          ) : tool.kind === "function" ? (
            <article className="agent-tool-card" key={`function-${index}`}>
              <header><strong>Function</strong><button className="button outline" type="button" onClick={() => updateTools((current) => current.filter((_, candidate) => candidate !== index))}>Remove</button></header>
              <div className="agent-tool-grid">
                <label className="field"><span>Name</span><input value={tool.name} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, name: event.target.value } : candidate))} placeholder="lookup_customer" /></label>
                <label className="field"><span>Description</span><input value={tool.description} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, description: event.target.value } : candidate))} placeholder="Look up a customer record" /></label>
              </div>
              <label className="field"><span>Parameters JSON Schema</span><textarea value={tool.parameters} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, parameters: event.target.value } : candidate))} rows={7} spellCheck={false} /><small>Must be a JSON object. Functions are always non-deferred; at most 64 unique Function names are allowed, with each name limited to 512 UTF-8 bytes.</small></label>
            </article>
          ) : (
            <article className="agent-tool-card" key={`mcp-${index}`}>
              <header><strong>{tool.credentialId ? "Vault bearer HTTP MCP" : "Anonymous HTTP MCP"}</strong><button className="button outline" type="button" onClick={() => updateTools((current) => current.filter((_, candidate) => candidate !== index))}>Remove</button></header>
              <div className="agent-tool-grid">
                <label className="field"><span>Server label</span><input value={tool.serverLabel} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, serverLabel: event.target.value } : candidate))} placeholder="docs" /></label>
                <label className="field"><span>Authentication</span><select value={tool.credentialId ?? ""} onChange={(event) => {
                  const credentialId = event.target.value || null;
                  const credential = credentialId ? vaultCatalog?.credentials.find((candidate) => candidate.id === credentialId) : null;
                  updateTools((current) => current.map((candidate, position) => position === index ? {
                    ...tool,
                    credentialId,
                    serverUrl: credential?.auth.mcp_server_url ?? tool.serverUrl,
                  } : candidate));
                }}>
                  <option value="">Anonymous</option>
                  {vaultCatalog?.vaults.map((vault) => {
                    const credentials = vaultCatalog.credentials.filter((credential) => credential.vault_id === vault.id);
                    return credentials.length ? <optgroup label={vaultName(vault)} key={vault.id}>{credentials.map((credential) => <option value={credential.id} key={credential.id}>{credential.name} · {credential.auth.mcp_server_url}</option>)}</optgroup> : null;
                  })}
                </select><small>{vaultCatalog ? "A selected Credential determines and locks the exact MCP destination." : "Credential catalog unavailable. Only anonymous MCP can be configured."}</small></label>
                <label className="field"><span>Server URL</span><input value={tool.serverUrl} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, serverUrl: event.target.value } : candidate))} placeholder="https://mcp.example/tools" inputMode="url" spellCheck={false} readOnly={Boolean(tool.credentialId)} /></label>
              </div>
              <fieldset className="agent-mcp-allowed-tools">
                <legend>Allowed tools</legend>
                <label><input type="radio" checked={tool.allowedToolsMode === "all"} onChange={() => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, allowedToolsMode: "all", allowedToolsValue: null } : candidate))} /> All advertised tools</label>
                <label><input type="radio" checked={tool.allowedToolsMode === "list"} onChange={() => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, allowedToolsMode: "list" } : candidate))} /> Only the listed tools</label>
                {tool.allowedToolsMode === "list" ? <textarea value={tool.allowedTools} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, allowedTools: event.target.value } : candidate))} rows={4} placeholder={'search\nread_document'} spellCheck={false} aria-label={`Allowed tools for ${tool.serverLabel || "MCP server"}`} /> : null}
                <small>Omitted or null permits all advertised tools. An empty listed value serializes as an empty list and permits none.</small>
              </fieldset>
              <label className="agent-mcp-required"><input type="checkbox" checked={tool.required === true} onChange={(event) => updateTools((current) => current.map((candidate, position) => position === index ? { ...tool, required: event.target.checked } : candidate))} /> Require this server for Core execution</label>
              <small>Writes always use <code>transport.type=http</code> and <code>connection_origin=service</code>. Tokens are managed write-only in Vaults and never enter this form. Headers, request metadata, OAuth, stdio, and client-origin connections remain unsupported.</small>
            </article>
          ))}
        </div>
        <div className="agent-tool-actions">
          <button className="button outline" type="button" onClick={() => updateTools((current) => [...current, { kind: "function", name: "", description: "", parameters: "{\n  \"type\": \"object\"\n}" }])}>Add Function</button>
          <button className="button outline" type="button" onClick={() => updateTools((current) => [...current, { kind: "mcp", serverLabel: "", serverUrl: "", allowedToolsMode: "all", allowedTools: "", allowedToolsValue: null, required: false, credentialId: null }])}>Add HTTP MCP</button>
        </div>
        {toolsError ? <p className="field-error" role="alert">{toolsError}</p> : null}
      </section>
      <label className="field">
        <span>Metadata</span>
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
        <small id={`${formId}-metadata-help`}>JSON object with at most 16 string pairs; keys are limited to 64 characters and values to 512. Never store secrets in Agent metadata.</small>
        {metadataError ? <small className="field-error" id={`${formId}-metadata-error`} role="alert">{metadataError}</small> : null}
      </label>
      </fieldset>
    </form>
  );
}
