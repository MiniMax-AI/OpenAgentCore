import { useState, type FormEvent } from "react";
import type { InitializeSandboxDeployment, SandboxProvider } from "@agents-core-web/agents-client";
import { useLocale } from "../../lib/LocaleProvider";
import { sandboxCoreOrigin } from "./core-origin";

export function SandboxSetup({ initialCoreUrl, automaticInstall = false, disabled, onInitialize }: {
  initialCoreUrl: string;
  automaticInstall?: boolean;
  disabled: boolean;
  onInitialize: (input: InitializeSandboxDeployment) => Promise<void>;
}) {
  const { t } = useLocale();
  const [provider, setProvider] = useState<SandboxProvider | "">("");
  const [coreUrl, setCoreUrl] = useState(initialCoreUrl);
  const origin = sandboxCoreOrigin(coreUrl);
  function submit(event: FormEvent) {
    event.preventDefault();
    if (provider && origin && !disabled) void onInitialize({ provider, core_url: origin });
  }
  return <form className="form-stack sandbox-enrollment" onSubmit={submit} aria-labelledby="sandbox-setup-heading">
    <h2 id="sandbox-setup-heading">{t("Set up hosted sandboxes")}</h2>
    <p>{t("Core is running with no sandbox nodes. Choose one provider for this deployment; every node you add must use it. This choice cannot be changed here.")}</p>
    <label className="field"><span>{t("Sandbox provider")}</span><select value={provider} onChange={(event) => setProvider(event.target.value as SandboxProvider)} disabled={disabled} required>
      <option value="" disabled>{t("Choose a provider")}</option><option value="docker">Docker</option><option value="microsandbox">microsandbox</option>
    </select></label>
    <p>{provider === "docker" ? (automaticInstall ? t("The host needs Docker access. The node command installs the matched runtime image.") : t("Docker nodes need a local Docker Unix socket and a pinned runtime image.")) : provider === "microsandbox" ? (automaticInstall ? t("The host needs Linux with KVM and the required host libraries. The node command installs the matched runtime, helper and firmware.") : t("microsandbox nodes need Linux with KVM, the qualified runtime, helper and firmware.")) : t("Prepare a compatible host after selecting the provider.")}</p>
    <label className="field"><span>{t("Core origin reachable from nodes and guests")}</span><input type="url" value={coreUrl} onChange={(event) => setCoreUrl(event.target.value)} placeholder="https://core.example" disabled={disabled} required aria-describedby="sandbox-core-origin-help" /></label>
    <p id="sandbox-core-origin-help">{t("Use the Core API origin, reachable from every node and sandbox guest, without a path or credentials. Remote hosts require HTTPS; loopback HTTP is for local use only. The console URL may be different.")}</p>
    {coreUrl && !origin ? <p className="sandbox-error">{t("Enter an HTTPS origin such as https://core.example, or a loopback HTTP origin for local use.")}</p> : null}
    <button className="button primary" type="submit" disabled={disabled || !provider || !origin}>{t("Initialize sandbox deployment")}</button>
    <p>{t("Next, generate an enrollment command and run it on your prepared host. Initializing does not install a runtime or create a node.")}</p>
  </form>;
}
