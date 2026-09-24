import { useState, type FormEvent } from "react";
import type { InitializeSandboxDeployment, SandboxProvider } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";
import { sandboxSetupOrigin } from "./core-origin";

export function SandboxSetup({ initialCoreUrl, disabled, onInitialize, switching = false }: {
  initialCoreUrl: string;
  disabled: boolean;
  onInitialize: (input: InitializeSandboxDeployment) => Promise<void>;
  switching?: boolean;
}) {
  const { t } = useTranslation("sandbox");
  const [location, setLocation] = useState<"" | "nodes" | "direct">("");
  const [provider, setProvider] = useState<SandboxProvider | "">("");
  const [coreUrl, setCoreUrl] = useState(initialCoreUrl);
  const [apiKey, setApiKey] = useState("");
  const [template, setTemplate] = useState("");
  const origin = sandboxSetupOrigin(coreUrl);
  const needsNetworkAddress = !sandboxSetupOrigin(initialCoreUrl);
  const validTemplate = /^[a-zA-Z0-9_-]+:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(template.trim());
  const validE2B = provider !== "e2b" || (apiKey.trim() && validTemplate);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!provider || !origin || !validE2B || disabled) return;
    try {
      await onInitialize({ provider, core_url: switching ? initialCoreUrl : origin, ...(provider === "e2b" ? { e2b: { api_key: apiKey.trim(), template: template.trim() } } : {}) });
    } finally { setApiKey(""); }
  }
  return <form className="form-stack sandbox-enrollment" onSubmit={(event) => void submit(event)} aria-labelledby="sandbox-setup-heading">
    <h2 id="sandbox-setup-heading">{t(switching ? "Choose the next provider" : "Set up hosted sandboxes")}</h2>
    <p>{t("One provider serves this entire Core deployment. Switching requires maintenance and verified cleanup of all existing execution resources.")}</p>
    <label className="field"><span>{t("Where to run sandboxes")}</span><select value={location} onChange={(event) => {
      const next = event.target.value as "nodes" | "direct"; setLocation(next); setProvider(next === "direct" ? "e2b" : ""); setApiKey(""); setTemplate("");
    }} disabled={disabled} required>
      <option value="" disabled>{t("Choose where to run")}</option><option value="direct">{t("E2B cloud")}</option><option value="nodes">{t("Own machines")}</option>
    </select></label>
    {location === "nodes" ? <label className="field"><span>{t("Sandbox provider")}</span><select value={provider} onChange={(event) => setProvider(event.target.value as SandboxProvider)} disabled={disabled} required>
      <option value="" disabled>{t("Choose a provider")}</option><option value="docker">Docker</option><option value="microsandbox">microsandbox</option>
    </select></label> : null}
    {location === "direct" ? <>
      <p>{t("Core creates E2B sandboxes directly. No node enrollment is needed.")}</p>
      <label className="field"><span>{t("E2B API key")}</span><input type="password" autoComplete="off" spellCheck={false} value={apiKey} onChange={(event) => setApiKey(event.target.value)} disabled={disabled} required aria-describedby="sandbox-e2b-key-help" /></label>
      <p id="sandbox-e2b-key-help">{t("The key is write-only. It is cleared from this form after submission and never saved in browser storage.")}</p>
      <label className="field"><span>{t("Immutable Runtime template")}</span><input value={template} onChange={(event) => setTemplate(event.target.value)} placeholder="template-id:00000000-0000-0000-0000-000000000000" autoComplete="off" spellCheck={false} disabled={disabled} required aria-invalid={Boolean(template && !validTemplate)} aria-describedby="sandbox-template-help sandbox-template-error" /></label>
      <p id="sandbox-template-error" className="sandbox-error">{template && !validTemplate ? t("Enter a template ID and build UUID separated by a colon.") : null}</p>
      <p id="sandbox-template-help">{t("Use the qualified managed Runtime template ID followed by its build UUID. A template alias or a generic E2B template cannot provide Core execution.")}</p>
    </> : null}
    {switching ? <p>{t("Core origin stays unchanged")}: <code>{initialCoreUrl}</code></p> : <details className="sandbox-network-settings" open={needsNetworkAddress}><summary>{t("Advanced network settings")}</summary>
      {needsNetworkAddress ? <p>{t("This console address cannot be used by sandbox guests. Enter the HTTPS Core address that your nodes and guests can reach.")}</p> : null}
      <label className="field"><span>{t("Core origin reachable from nodes and guests")}</span><input type="url" value={coreUrl} onChange={(event) => setCoreUrl(event.target.value)} placeholder="https://core.example" disabled={disabled} required aria-describedby="sandbox-core-origin-help" /></label>
      <p id="sandbox-core-origin-help">{t("Use an HTTPS Core origin reachable from every node and sandbox guest, without a path or credentials. The console URL may be different.")}</p>
      {coreUrl && !origin ? <p className="sandbox-error">{t("Enter a non-loopback HTTPS origin, such as https://core.example.")}</p> : null}
    </details>}
    <button className="button primary" type="submit" disabled={disabled || !provider || !origin || !validE2B}>{t(switching ? "Save provider and stay in maintenance" : "Initialize sandbox deployment")}</button>
  </form>;
}
