import { useState } from "react";
import type { SandboxDeployment, SandboxSpecification } from "@oac/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip, Section } from "../../components/console-ui";
import { useToast } from "../../components/Toast";
import { sandboxProviderLabel } from "../../lib/sandbox-labels";
import { domainIssues, maxDomains, networkSupport, parseDomains, type DomainIssue } from "./network-policy";

type Network = SandboxSpecification["network"];

/** Settings edits the deployment's network cap; it applies to new sandboxes only. */
export function NetworkSettings({ deployment, disabled, onSave }: { deployment: SandboxDeployment; disabled: boolean; onSave: (network: Network) => Promise<boolean> }) {
  const { t } = useTranslation("sandbox");
  const toast = useToast();
  async function save(network: Network) {
    const saved = await onSave(network);
    if (saved) toast.show(t("New sandboxes will run with this policy"));
    return saved;
  }
  return <NetworkForm deployment={deployment} disabled={disabled} onSave={save} />;
}

export function NetworkForm({ deployment, disabled, onSave }: { deployment: SandboxDeployment; disabled: boolean; onSave: (network: Network) => Promise<boolean> }) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const current = deployment.specification?.network;
  const [publicAccess, setPublicAccess] = useState<boolean | null>(null);
  const [text, setText] = useState<string | null>(null);
  if (!current || !deployment.provider) return null;
  const support = networkSupport(deployment.provider);
  const provider = sandboxProviderLabel(deployment.provider, locale);
  const allowPublic = publicAccess ?? current.public;
  const domains = text === null ? current.domains : parseDomains(text);
  const issues = domainIssues(domains);
  const changed = allowPublic !== current.public || domains.join("\n") !== current.domains.join("\n");
  const message = (issue: DomainIssue) => {
    switch (issue.problem) {
      case "too_many": return t("Add at most {{count}} domains.", { count: maxDomains });
      case "duplicate": return t("{{domain}} is listed twice.", { domain: issue.domain });
      case "too_long": return t("{{domain}} is too long.", { domain: issue.domain });
      default: return t("{{domain}} is not a valid host name.", { domain: issue.domain });
    }
  };
  return <Section headingId="sandbox-network-heading" title={t("Network")} help={t("A change applies to new sandboxes only. Existing sandboxes keep the policy they started with. The connection to Core and to the Session's model provider is always allowed.")}
    actions={<button type="submit" form="sandbox-network-form" className="button outline" disabled={disabled || !changed || issues.length > 0}>{t("Save")}</button>}>
    <form id="sandbox-network-form" className="settings-rows" onSubmit={async (event) => {
      event.preventDefault();
      if (disabled || !changed || issues.length > 0) return;
      if (await onSave({ public: allowPublic, domains })) { setPublicAccess(null); setText(null); }
    }}>
      <div className="settings-row">
        <div className="settings-row-label"><label htmlFor="sandbox-network-public">{t("Allow public internet")}</label><HelpTip id="sandbox-network-public-help">{support.restrict ? t("When off, sandboxes reach only the domains listed below.") : t("{{provider}} cannot restrict outbound traffic, so sandboxes always reach the public internet.", { provider })}</HelpTip></div>
        <div className="settings-row-control field">
          <input id="sandbox-network-public" className="network-switch" type="checkbox" role="switch" aria-describedby="sandbox-network-public-help" checked={allowPublic || !support.restrict} disabled={disabled || !support.restrict} onChange={(event) => setPublicAccess(event.target.checked)} />
        </div>
      </div>
      <div className="settings-row">
        <div className="settings-row-label"><label htmlFor="sandbox-network-domains">{t("Extra allowed domains")}</label><HelpTip id="sandbox-network-domains-help">{support.private_domains ? t("One host name per line, such as gitlab.example.com, up to {{count}}. Sandboxes can reach these names, including names on your private network. Wildcards are not accepted.", { count: maxDomains }) : t("{{provider}} sandboxes run in the provider's cloud and cannot reach your private network.", { provider })}</HelpTip></div>
        <div className="settings-row-control field">
          <textarea id="sandbox-network-domains" className="network-domains" rows={4} spellCheck={false} autoCapitalize="none" value={text ?? current.domains.join("\n")} disabled={disabled || !support.private_domains} aria-invalid={issues.length > 0} aria-describedby="sandbox-network-domains-help" onChange={(event) => setText(event.target.value)} />
          {issues.map((issue, index) => <span className="field-error" key={index}>{message(issue)}</span>)}
        </div>
      </div>
    </form>
  </Section>;
}
