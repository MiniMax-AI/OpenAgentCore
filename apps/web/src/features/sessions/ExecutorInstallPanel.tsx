import { useQuery } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import { useEffect, useId, useRef, type RefObject } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { installationQuery } from "../../lib/installation";
import { useCopy } from "../api-keys/IssuedKey";
import { sandboxConsoleConfigQuery } from "../sandbox/sandbox-queries";
import { executorInstall, type ExecutorInstall } from "./executor-install";

const DOCKER_SOCKET = "/var/run/docker.sock";

/** The install command's state, including the reads it waits for. */
export type ExecutorInstallRead = ExecutorInstall | { kind: "loading" } | { kind: "failed"; retry: () => void };

/**
 * Reads what Connect a host needs: the console's self-hosted installer and,
 * only when it is offered, Core's public address.
 */
export function useExecutorInstall(environmentId: string, remoteUrl: string): ExecutorInstallRead {
  const config = useQuery(sandboxConsoleConfigQuery);
  const offered = config.data?.self_hosted_installer === true;
  const installation = useQuery({ ...installationQuery, enabled: offered });
  if (!offered) return { kind: "unavailable" };
  if (installation.data === undefined) {
    return installation.isError && !installation.isFetching ? { kind: "failed", retry: () => void installation.refetch() } : { kind: "loading" };
  }
  return executorInstall({ config: config.data, publicUrl: installation.data.public_url, localOnly: installation.data.local_only, environmentId, remoteUrl });
}

/**
 * Connect a host: the command that installs this Environment's self-hosted
 * executor from Core's public address. It holds no secret; the installer asks
 * for a credential from the section above at a hidden prompt. Shown only when
 * the console serves the self-hosted installer with a verified digest.
 */
export function ExecutorInstallPanel({ install, archived }: { install: ExecutorInstallRead; archived: boolean }) {
  const { t } = useTranslation("sessions");
  const { t: tCommon } = useTranslation("common");
  const headingId = useId();
  if (install.kind === "unavailable") return null;

  let body;
  if (install.kind === "failed") {
    body = <p className="executor-install-note" role="alert">{t("executor.install.failed")} <button className="text-action" type="button" onClick={install.retry}>{tCommon("actions.retry")}</button></p>;
  } else if (install.kind === "loading") {
    body = <div className="executor-install-command executor-install-skeleton" role="status" aria-label={t("executor.install.loading")} aria-busy="true"><span className="skeleton-bar" /><span className="skeleton-bar" /></div>;
  } else {
    body = install.kind === "no_address" ? <p className="executor-install-note" role="note">{t("executor.install.noAddress")}</p>
      : install.kind === "local_only" ? <p className="executor-install-note" role="note">{t("executor.install.localOnly", { url: install.publicUrl })}</p>
      : install.kind === "not_wss" ? <p className="executor-install-note" role="note">{t("executor.install.notWss", { remote: install.remoteUrl || "—" })}</p>
      : <>
        <p className="executor-install-note">{archived ? t("executor.install.archived") : t("executor.install.steps")}</p>
        <InstallCommand value={install.command} />
        <div className="executor-install-requirements">
          {/* The list carries the label for assistive technology; the visible one is not read twice. Safari drops a list's semantics without list-style unless its role is explicit. */}
          <span aria-hidden="true">{t("executor.install.requirements")}</span>
          <ul role="list" aria-label={t("executor.install.requirements")}>
            {["Linux amd64", "Python 3.9+", "curl", "sha256sum", "Docker CLI", t("executor.install.docker", { socket: DOCKER_SOCKET }), t("executor.install.https", { url: install.publicUrl })].map((item, index) => (
              <li key={item}>{index ? <span className="executor-install-separator" aria-hidden="true">·</span> : null}{item}</li>
            ))}
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

/** Selects a block's text when the clipboard refused it (a plain-HTTP console has none), to copy by hand. */
export function useSelectWhenCopyFails(state: ReturnType<typeof useCopy>["state"], block: RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (state !== "failed" || !block.current) return;
    const range = document.createRange();
    range.selectNodeContents(block.current);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
  }, [state, block]);
}

/** The command with its copy button; when the clipboard refuses, the command is selected to copy by hand. */
export function InstallCommand({ value }: { value: string }) {
  const { t } = useTranslation("sessions");
  const code = useRef<HTMLPreElement>(null);
  const { state, copy } = useCopy(value);
  useSelectWhenCopyFails(state, code);
  const name = state === "copied" ? t("executor.install.copied") : t("executor.install.copy");
  return (
    <div className="executor-install-command" role="region" aria-label={t("executor.install.command")}>
      <div className="executor-install-command-head">
        <span>{t("executor.install.terminal")}</span>
        <button type="button" className="icon-button ghost copyable-id-button" aria-label={name} title={name} onClick={() => void copy()}>
          {state === "copied" ? <Check size={13} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.7} aria-hidden="true" />}
        </button>
      </div>
      <pre ref={code}><code>{value}</code></pre>
      {state === "failed" ? <p className="executor-install-error" role="alert">{t("executor.install.copyFailed")}</p> : null}
    </div>
  );
}
