import { ArrowUpRight, Bot, Check, Code2, Copy, Play, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { AgentCore, CoreHarnessKind, SavedAgent } from "@agents-core-web/agents-client";
import { useLocale } from "../../lib/LocaleProvider";
import { isLocalProxyBaseUrl, isValidDirectCoreBaseUrl, type CoreConnection } from "../../lib/connection";
import { buildModelOptionGroups } from "../../lib/model-options";
import { exampleForm, exampleInput, terminalExample } from "./example-request";
import { useExampleRequest } from "./use-example-request";

export function FirstRequest({ core, connection, scope, onCreated, onOpenAgent }: {
  core: AgentCore; connection: CoreConnection; scope: string;
  onCreated: (agent: SavedAgent) => void; onOpenAgent: (id: string) => void;
}) {
  const { t } = useLocale();
  const request = useExampleRequest(core, scope, onCreated);
  const [form, setForm] = useState(() => exampleForm(buildModelOptionGroups([], import.meta.env.VITE_AGENT_MODEL_PRESETS, import.meta.env.VITE_AGENT_DEFAULT_MODEL).defaultModel, request.marker));
  const [harnesses, setHarnesses] = useState<CoreHarnessKind[]>([]);
  const [providerEnabled, setProviderEnabled] = useState(true);
  const [providerUrl, setProviderUrl] = useState("");
  const [protocol, setProtocol] = useState<"responses" | "anthropic">("responses");
  const [modelKey, setModelKey] = useState("");
  const [apiUrl, setApiUrl] = useState(() => isLocalProxyBaseUrl(connection.baseUrl) ? `${window.location.origin}/v1` : connection.baseUrl);
  const [copyState, setCopyState] = useState<"idle" | "copying" | "copied" | "failed">("idle");
  const lifetime = useRef(0);
  const result = useRef<HTMLElement>(null);
  useEffect(() => {
    const controller = new AbortController();
    void core.retrieveStartupConfiguration({ signal: controller.signal }).then((config) => {
      if (!controller.signal.aborted) setHarnesses(config.configured.enabled_harnesses);
    }).catch(() => { /* An omitted harness uses the deployment's configured default. */ });
    return () => { lifetime.current++; controller.abort(); };
  }, [core]);
  useEffect(() => {
    if (!request.state.agent) return;
    setModelKey("");
    const frame = requestAnimationFrame(() => result.current?.scrollIntoView({ block: "nearest", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth" }));
    return () => cancelAnimationFrame(frame);
  }, [request.state.agent]);
  const baseInput = exampleInput(form);
  const providerValid = !providerEnabled || isValidDirectCoreBaseUrl(providerUrl);
  const input = baseInput && providerValid ? { ...baseInput, ...(providerEnabled ? { x_agents_core: { ...baseInput.x_agents_core, model_provider: { protocol, base_url: providerUrl.trim(), api_key: modelKey } } } : {}) } : null;
  const code = input ? terminalExample(input, apiUrl) : null;
  useEffect(() => { setCopyState("idle"); }, [code]);
  const pending = request.state.phase === "checking" || request.state.phase === "creating";
  const locked = request.state.phase !== "idle";
  const local = isLocalProxyBaseUrl(connection.baseUrl);
  const agent = request.state.agent;
  const busyCopy = copyState === "copying";
  async function copy() {
    if (!code || pending || busyCopy) return;
    const generation = lifetime.current;
    setCopyState("copying");
    try {
      await navigator.clipboard.writeText(code);
      if (generation !== lifetime.current) return;
      setCopyState("copied"); request.observeExternal(); setModelKey("");
    } catch { if (generation === lifetime.current) setCopyState("failed"); }
  }
  async function runHere() {
    if (!input) return;
    const operation = request.run(input);
    setModelKey("");
    await operation;
  }
  return <div className="first-request-workspace">
    <div className="first-request">
      <section className="first-request-form" aria-labelledby="first-agent-config-title">
        <header><h3 id="first-agent-config-title">{t("Agent configuration")}</h3><p>{t("Fill in your Agent's settings. The request updates as you type.")}</p></header>
        <fieldset disabled={pending || Boolean(agent) || busyCopy}>
          <label className="field"><span>{t("Agent name")}</span><input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} maxLength={128} /></label>
          <div className="first-request-form-row">
            <label className="field"><span>{t("Model ID")}</span><input value={form.model} onChange={(event) => setForm({ ...form, model: event.target.value })} spellCheck={false} required /></label>
            <label className="field"><span>{t("Harness")}</span><select value={form.harness} onChange={(event) => setForm({ ...form, harness: event.target.value as CoreHarnessKind | "" })}><option value="">{t("Deployment default")}</option>{harnesses.map((harness) => <option key={harness} value={harness}>{harness}</option>)}</select></label>
          </div>
          <label className="field"><span>{t("Instructions")}</span><textarea value={form.instructions} onChange={(event) => setForm({ ...form, instructions: event.target.value })} rows={3} /></label>
          <label className="first-request-provider-toggle"><input type="checkbox" checked={providerEnabled} onChange={(event) => { setProviderEnabled(event.target.checked); if (!event.target.checked) setModelKey(""); }} /><span>{t("Set a model provider for this Agent")}</span></label>
          {providerEnabled ? <div className="first-request-provider">
            <label className="field"><span>{t("Provider protocol")}</span><select value={protocol} onChange={(event) => setProtocol(event.target.value as typeof protocol)}><option value="responses">Responses</option><option value="anthropic">Anthropic</option></select></label>
            <label className="field"><span>{t("Provider base URL")}</span><input type="url" value={providerUrl} onChange={(event) => setProviderUrl(event.target.value)} placeholder="https://api.example/v1" spellCheck={false} required /></label>
            {providerUrl && !providerValid ? <p className="field-error" role="alert">{t("Enter an HTTPS URL or a loopback HTTP URL without credentials, query parameters, or fragments.")}</p> : null}
            <label className="field"><span>{t("Model API key")} <small>{t("Only for Run here")}</small></span><input type="password" value={modelKey} onChange={(event) => setModelKey(event.target.value)} autoComplete="off" spellCheck={false} placeholder="MODEL_API_KEY" /><small>{t("The local request asks for this key in your terminal. A key entered here is used only by Run here.")}</small></label>
          </div> : <p className="first-request-hint">{t("Use the model provider configured for this deployment.")}</p>}
        </fieldset>
        <footer className="first-request-local-action"><button className="button outline" type="button" onClick={() => void runHere()} disabled={!input || locked || busyCopy || !local || (providerEnabled && !modelKey.trim())}><Play size={13} />{request.state.phase === "creating" ? t("Creating Agent…") : request.state.phase === "checking" ? t("Checking for your Agent…") : t("Run here")}</button><small>{t("Uses your signed-in console connection.")}</small></footer>
      </section>
      <section className="first-request-editor" aria-labelledby="first-request-code-title">
        <div className="first-request-toolbar"><span id="first-request-code-title"><Code2 size={15} />POST <code>/v1/agents</code></span><span>Python 3</span></div>
        <label className="first-request-api-url field"><span>{t("Core API base URL")}</span><input type="url" value={apiUrl} onChange={(event) => { setApiUrl(event.target.value); setCopyState("idle"); }} spellCheck={false} disabled={pending || busyCopy} /></label>
        <p className="first-request-hint">{t(providerEnabled ? "Run locally. The request will ask for your Core API key and model key." : "Run locally. The request will ask for your Core API key.")}</p>
        <div className="first-request-code"><pre tabIndex={0}><code>{code ?? t("Complete the configuration to generate your request.")}</code></pre></div>
        <footer className="first-request-actions"><span>{t("Core API key authorizes this request. Model key authorizes your model provider.")}</span><button className="button primary" type="button" onClick={() => void copy()} disabled={!code || pending || busyCopy || Boolean(agent)}>{copyState === "copied" ? <Check size={14} /> : <Copy size={14} />}{copyState === "copied" ? t("Copied") : t("Copy request")}</button></footer>
      </section>
    </div>
    {copyState === "failed" ? <p className="first-request-notice" role="alert">{t("Select the request and copy it manually.")}</p> : null}
    {copyState === "copied" && !agent ? <p className="first-request-notice" role="status">{t("A copy is ready to run. We are watching for its Agent.")}</p> : null}
    {request.state.error ? <p className="first-request-notice" role="alert">{t(request.state.error === "rejected" ? "Request was rejected. Check your configuration and try again." : request.state.phase === "waiting" ? "The request may have completed. Check for its Agent before trying another request." : "Unable to check the result. Your last state is kept here.")}</p> : null}
    {request.state.phase === "waiting" ? <div className="first-request-waiting" role="status"><span>{t("Waiting for your request…")}</span><button className="button outline" type="button" onClick={() => void request.reconcile()}><RefreshCw size={13} />{t("Check result")}</button></div> : null}
    {request.state.phase === "waiting" ? <p className="first-request-notice">{t("You can edit and copy the request again. Check its result before sending another request.")}</p> : null}
    <section ref={result} className={`first-request-result ${agent ? "has-agent" : ""}`} aria-live="polite" aria-atomic="true">
      {agent ? <article className="first-agent-card" key={agent.id}>
        <div className="first-agent-icon"><Bot size={27} strokeWidth={1.4} /><span><Check size={11} /></span></div>
        <div className="first-agent-summary"><p className="first-agent-eyebrow">{t("Agent created")}</p><h3>{agent.name || agent.id}</h3><p>{agent.model}</p></div><code>{agent.id}</code>
        <button className="button primary" type="button" onClick={() => onOpenAgent(agent.id)}>{t("Open Agent")}<ArrowUpRight size={15} /></button>
      </article> : <div className="first-agent-empty"><Bot size={25} strokeWidth={1.2} /><div><h3>{t("Your Agent will appear here")}</h3><p>{t("Run the request to create your first saved Agent.")}</p></div></div>}
    </section>
  </div>;
}
