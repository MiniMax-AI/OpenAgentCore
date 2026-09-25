import { useQuery } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import { useEffect, useId, useRef } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { installationQuery } from "../../lib/installation";
import { useCopy } from "../api-keys/IssuedKey";
import { sandboxConsoleConfigQuery } from "../sandbox/sandbox-queries";
import { executorInstall } from "./executor-install";

const DOCKER_SOCKET = "/var/run/docker.sock";

/**
 * Connect a host: the command that installs this Environment's self-hosted
 * executor from Core's public address. It holds no secret; the installer asks
 * for a credential from the section above at a hidden prompt. Shown only when
 * the console serves the self-hosted installer with a verified digest.
 */
export function ExecutorInstallPanel({ environmentId, remoteUrl, archived }: { environmentId: string; remoteUrl: string; archived: boolean }) {
  const { t } = useTranslation("sessions");
  const { t: tCommon } = useTranslation("common");
  const headingId = useId();
  const config = useQuery(sandboxConsoleConfigQuery);
  const offered = config.data?.self_hosted_installer === true;
  const installation = useQuery({ ...installationQuery, enabled: offered });
  if (!offered) return null;

  let body;
  if (installation.data === undefined) {
    body = installation.isError && !installation.isFetching
      ? <p className="executor-install-note" role="alert">{t("executor.install.failed")} <button className="text-action" type="button" onClick={() => void installation.refetch()}>{tCommon("actions.retry")}</button></p>
      : <div className="executor-install-command executor-install-skeleton" role="status" aria-label={t("executor.install.loading")} aria-busy="true"><span className="skeleton-bar" /><span className="skeleton-bar" /></div>;
  } else {
    const install = executorInstall({ config: config.data, publicUrl: installation.data.public_url, environmentId, remoteUrl });
    if (install.kind === "unavailable") return null;
    body = install.kind === "no_address" ? <p className="executor-install-note" role="note">{t("executor.install.noAddress")}</p>
      : install.kind === "not_wss" ? <p className="executor-install-note" role="note">{t("executor.install.notWss", { remote: install.remoteUrl || "—" })}</p>
      : <>
        <p className="executor-install-note">{archived ? t("executor.install.archived") : t("executor.install.steps")}</p>
        <InstallCommand value={install.command} />
        <div className="executor-install-requirements">
          <span>{t("executor.install.requirements")}</span>
          <ul aria-label={t("executor.install.requirements")}>
            <li>Linux amd64</li>
            <li>Python 3.9+</li>
            <li>curl</li>
            <li>sha256sum</li>
            <li>{t("executor.install.docker", { socket: DOCKER_SOCKET })}</li>
            <li>{t("executor.install.https", { url: install.publicUrl })}</li>
          </ul>
        </div>
      </>;
  }
  return (
    <section className="executor-install" aria-labelledby={headingId}>
      <div className="executor-install-title">
        <h3 id={headingId}>{t("executor.install.title")}</h3>
        <HelpTip>{t("executor.install.lifecycle")}</HelpTip>
      </div>
      {body}
    </section>
  );
}

/** The command with its copy button; when the clipboard refuses, the command is selected to copy by hand. */
function InstallCommand({ value }: { value: string }) {
  const { t } = useTranslation("sessions");
  const code = useRef<HTMLPreElement>(null);
  const { state, copy } = useCopy(value);
  useEffect(() => {
    if (state !== "failed" || !code.current) return;
    const range = document.createRange();
    range.selectNodeContents(code.current);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
  }, [state]);
  const name = state === "copied" ? t("executor.install.copied") : t("executor.install.copy");
  return (
    <div className="executor-install-command">
      <div className="executor-install-command-head">
        <span>{t("executor.install.terminal")}</span>
        <button type="button" className="icon-button ghost copyable-id-button" aria-label={name} title={name} onClick={() => void copy()}>
          {state === "copied" ? <Check size={13} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.7} aria-hidden="true" />}
        </button>
      </div>
      <pre ref={code} aria-label={t("executor.install.command")} tabIndex={0}><code>{value}</code></pre>
      {state === "failed" ? <p className="executor-install-error" role="alert">{t("executor.install.copyFailed")}</p> : null}
    </div>
  );
}
