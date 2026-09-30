import { Check, Copy } from "lucide-react";
import { useEffect, useId, useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip } from "../../components/console-ui";
import { ConsoleSelect } from "../../components/console-select";
import { useCopy } from "../api-keys/IssuedKey";
import { useQuery } from "@tanstack/react-query";
import { admin } from "../../lib/projects";
import type { ExecutorInstall, HostShell } from "./executor-install";

export function useExecutorInstall(projectId: string, environmentId: string, archived: boolean): ExecutorInstall {
  const query = useQuery({
    queryKey: ["environment-installation", projectId, environmentId],
    queryFn: ({ signal }) => admin.environmentInstallation(projectId, environmentId, { signal }),
    enabled: !archived,
    staleTime: 20 * 60_000,
    refetchInterval: 20 * 60_000,
    gcTime: 0,
    retry: false,
  });
  const data = query.data;
  return !archived && data?.status === "available" && data.commands && (data.expires_at ?? 0) > Date.now() / 1000
    ? { kind: "ready", commands: data.commands } : { kind: "unavailable" };
}

/** One home for native installation instructions; issuing a credential does not establish a connection. */
export function ExecutorInstallPanel({ install, archived, connected = false }: { install: ExecutorInstall; archived: boolean; connected?: boolean }) {
  const { t } = useTranslation("sessions");
  const headingId = useId();
  const [shell, setShell] = useState<HostShell>("posix");
  return <section className="executor-install" aria-labelledby={headingId}>
    <div className="executor-install-title">
      <h3 id={headingId}>{t("executor.install.title")}</h3>
      <HelpTip>{t("executor.install.lifecycle")}</HelpTip>
      <span className={`executor-install-progress${connected ? " is-done" : ""}`}>
        {connected ? <Check size={14} aria-hidden="true" /> : null}
        {t(connected ? "executor.install.hostDone" : "executor.install.hostPending")}
      </span>
    </div>
    <p className="executor-install-note">{t(archived ? "executor.install.archived" : "executor.install.steps")}</p>
    <a className="text-action" href="https://github.com/MiniMax-AI/parsar-core/blob/main/docs/getting-started/self-hosted.md" target="_blank" rel="noreferrer">{t("executor.install.guide")}</a>
    {install.kind === "ready" ? <>
      <ConsoleSelect label={t("executor.install.platform")} value={shell} options={[{ value: "posix", label: "Linux / macOS" }, { value: "powershell", label: "Windows · PowerShell" }]} onChange={(value) => { if (value === "posix" || value === "powershell") setShell(value); }} />
      <InstallCommand value={install.commands[shell]} />
      <p className="executor-install-note">{t("executor.install.start")}</p>
    </> : <p role="status" className="executor-install-note">{t("executor.install.unavailable")}</p>}
  </section>;
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
