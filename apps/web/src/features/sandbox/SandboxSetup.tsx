import { useState, type FormEvent } from "react";
import type { InitializeSandboxDeployment, SandboxProvider } from "@agents-core-web/agents-client";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxSetupOrigin } from "./core-origin";

export function SandboxSetup({ initialCoreUrl, disabled, onInitialize }: {
  initialCoreUrl: string;
  disabled: boolean;
  onInitialize: (input: InitializeSandboxDeployment) => Promise<void>;
}) {
  const { t } = useLocale();
  const [provider, setProvider] = useState<SandboxProvider | "">("");
  const [coreUrl, setCoreUrl] = useState(initialCoreUrl);
  const origin = sandboxSetupOrigin(coreUrl);
  const needsNetworkAddress = !sandboxSetupOrigin(initialCoreUrl);
  function submit(event: FormEvent) {
    event.preventDefault();
    if (provider && origin && !disabled) void onInitialize({ provider, core_url: origin });
  }
  return <form className="form-stack sandbox-enrollment" onSubmit={submit} aria-labelledby="sandbox-setup-heading">
    <h2 id="sandbox-setup-heading">{t("Set up hosted sandboxes")}</h2>
    <p>{t("Choose a provider for your nodes. This choice cannot be changed later.")}</p>
    <label className="field"><span>{t("Sandbox provider")}</span><select value={provider} onChange={(event) => setProvider(event.target.value as SandboxProvider)} disabled={disabled} required>
      <option value="" disabled>{t("Choose a provider")}</option><option value="docker">Docker</option><option value="microsandbox">microsandbox</option>
    </select></label>
    <details className="sandbox-network-settings" open={needsNetworkAddress}><summary>{t("Advanced network settings")}</summary>
    {needsNetworkAddress ? <p>{t("This console address cannot be used by sandbox guests. Enter the HTTPS Core address that your nodes and guests can reach.")}</p> : null}
    <label className="field"><span>{t("Core origin reachable from nodes and guests")}</span><input type="url" value={coreUrl} onChange={(event) => setCoreUrl(event.target.value)} placeholder="https://core.example" disabled={disabled} required aria-describedby="sandbox-core-origin-help" /></label>
    <p id="sandbox-core-origin-help">{t("Use an HTTPS Core origin reachable from every node and sandbox guest, without a path or credentials. The console URL may be different.")}</p>
    {coreUrl && !origin ? <p className="sandbox-error">{t("Enter a non-loopback HTTPS origin, such as https://core.example.")}</p> : null}
    </details>
    <button className="button primary" type="submit" disabled={disabled || !provider || !origin}>{t("Initialize sandbox deployment")}</button>
  </form>;
}
