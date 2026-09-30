import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ShieldCheck } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { HelpTip, PageBody, RefreshButton } from "../../components/console-ui";
import { ErrorState } from "../../components/ErrorState";
import { TableSkeleton } from "../../components/Skeleton";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { useConsoleAccount } from "../first-run/ConsoleAccess";
import { applyDomain, domainHostname, domainInProgress, domainQuery, domainReconnecting, DomainRequestError, type DomainStatus } from "./domain-api";
import { DomainHandoff } from "./DomainHandoff";
import "./domain.css";

export function DomainPage() {
  const { t } = useTranslation("system");
  const { navigate } = useConsoleNavigation();
  const query = useQuery(domainQuery);
  return <section className="page-section console-page" aria-labelledby="domain-heading">
    <header className="page-header">
      <div className="console-page-heading">
        <button className="icon-button ghost back-button" aria-label={t("domain.back")} onClick={() => navigate("system")}><ArrowLeft size={16} aria-hidden="true" /></button>
        <h1 id="domain-heading">{t("domain.title")}</h1><HelpTip>{t("domain.help")}</HelpTip>
      </div>
    </header>
    <PageBody>
      {query.data ? <DomainForm initial={query.data} /> : query.isError
        ? <ErrorState title={t("domain.loadFailed")} onRetry={() => void query.refetch()} />
        : <TableSkeleton rows={3} columns={2} />}
    </PageBody>
  </section>;
}

function initialHost(snapshot: DomainStatus): string {
  try { return domainHostname(new URL(snapshot.target_url ?? snapshot.public_url ?? "").hostname) ?? ""; }
  catch { return ""; }
}

function DomainForm({ initial }: { initial: DomainStatus }) {
  const { t } = useTranslation("system");
  const account = useConsoleAccount();
  const setDomainHandoff = account?.setDomainHandoff;
  const queryClient = useQueryClient();
  const query = useQuery(domainQuery);
  const snapshot = query.data ?? initial;
  const [hostname, setHostname] = useState(() => initialHost(initial));
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [attemptedUrl, setAttemptedUrl] = useState<string | null>(null);
  const [error, setError] = useState<{ text: string; detail?: string } | null>(null);
  const [confirmation, setConfirmation] = useState<string | null>(null);
  const pending = useRef(false);
  const lifetime = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController(); lifetime.current = controller;
    return () => controller.abort();
  }, []);

  const host = domainHostname(hostname);
  const active = domainInProgress(snapshot);
  const blocked = busy || active || uncertain || query.isError || query.isFetching || !snapshot.supported;
  const failed = query.isError && !domainReconnecting(query);
  const reconnect = uncertain || busy ? attemptedUrl : snapshot.state === "ready" ? snapshot.target_url ?? snapshot.public_url : (active || query.isError) ? snapshot.target_url : null;
  useEffect(() => {
    if (reconnect) setDomainHandoff?.(reconnect);
    else if (snapshot.state === "failed" || snapshot.state === "unconfigured") setDomainHandoff?.(null);
  }, [reconnect, snapshot.state, setDomainHandoff]);

  async function refresh() {
    const current = lifetime.current;
    const result = await query.refetch();
    if (current?.signal.aborted || result.isError) return;
    setUncertain(false); setError(null); setAttemptedUrl(null);
    if (result.data?.state === "failed" || result.data?.state === "unconfigured") account?.setDomainHandoff(null);
  }

  async function save(name: string, confirmed = false) {
    const current = lifetime.current;
    if (!current || current.signal.aborted || pending.current || blocked) return;
    pending.current = true; setBusy(true); setError(null);
    const target = `https://${name}`;
    setAttemptedUrl(target);
    try {
      await queryClient.cancelQueries({ queryKey: domainQuery.queryKey });
      if (current.signal.aborted) return;
      account?.setDomainHandoff(target);
      const next = await applyDomain(name, confirmed, AbortSignal.any([current.signal, AbortSignal.timeout(30_000)]));
      if (current.signal.aborted) return;
      queryClient.setQueryData(domainQuery.queryKey, next);
      setConfirmation(null);
      account?.setDomainHandoff(next.target_url);
    } catch (cause) {
      if (current.signal.aborted) return;
      if (cause instanceof DomainRequestError && cause.status === 409 && cause.code === "public_url_confirmation_required") {
        account?.setDomainHandoff(null);
        setConfirmation(name);
      } else if (cause instanceof DomainRequestError && cause.status >= 400 && cause.status < 500 && cause.status !== 408) {
        account?.setDomainHandoff(null);
        setError({ text: t("domain.rejected"), detail: cause.message });
      } else {
        setConfirmation(null); setUncertain(true); setError({ text: t("domain.uncertain") });
        account?.setDomainHandoff(target);
      }
    } finally {
      pending.current = false;
      if (!current.signal.aborted) setBusy(false);
    }
  }

  function submit(event: FormEvent) { event.preventDefault(); if (host && !confirmation) void save(host); }

  return <div className="domain-settings">
    {!snapshot.supported ? <p role="status">{t("domain.unsupported")} {snapshot.message ? <HelpTip label={t("domain.details")}>{snapshot.message}</HelpTip> : null}</p> : <>
      <form className="domain-form" onSubmit={submit}>
        <div className="field">
          <span className="field-label-row"><label htmlFor="domain-hostname">{t("domain.hostname")}</label><HelpTip>{t("domain.hostnameHelp")}</HelpTip></span>
          <input id="domain-hostname" autoComplete="off" autoCapitalize="none" spellCheck={false} placeholder="core.example.com" value={hostname}
            disabled={blocked || confirmation !== null} aria-invalid={hostname.trim() !== "" && !host || undefined}
            aria-describedby={hostname.trim() !== "" && !host ? "domain-invalid" : undefined} onChange={(event) => { setHostname(event.target.value); setError(null); }} />
          {hostname.trim() && !host ? <p className="field-error" id="domain-invalid">{t("domain.invalidHostname")}</p> : null}
        </div>
        <div className="domain-security"><ShieldCheck size={18} aria-hidden="true" /><span>{t("domain.automatic")}</span><HelpTip>{t("domain.dnsHelp")}</HelpTip></div>
        <p className="domain-prerequisites">{t("domain.prerequisites")}</p>
        <div className="domain-actions">
          <button type="submit" className="button primary" disabled={blocked || !host || confirmation !== null || (snapshot.state === "ready" && `https://${host}` === snapshot.public_url)}>
            {busy ? t("domain.submitting") : t(snapshot.state === "failed" ? "domain.retry" : "domain.apply")}
          </button>
          <RefreshButton label={t("domain.refresh")} refreshing={query.isFetching} disabled={busy} onClick={() => void refresh()} />
        </div>
      </form>
      {(active || snapshot.state === "ready") && !failed && !busy && !uncertain ? <p className="domain-state" role="status">{t(`domain.states.${snapshot.state}`)}</p> : null}
      {snapshot.state === "failed" ? <p className="domain-error" role="alert">{t("domain.failed")} {snapshot.message ? <HelpTip label={t("domain.details")}>{snapshot.message}</HelpTip> : null}</p> : null}
      {failed ? <p className="domain-error" role="alert">{t(active ? "domain.disconnected" : "domain.loadFailed")}</p> : null}
      {error && !confirmation ? <p className="domain-error" role="alert">{error.text} {error.detail ? <HelpTip label={t("domain.details")}>{error.detail}</HelpTip> : null}</p> : null}
      <DomainHandoff url={reconnect} />
    </>}
    <ConfirmDialog open={confirmation !== null} title={t("domain.confirmTitle")} confirmLabel={t("domain.confirm")} busyLabel={t("domain.submitting")} busy={busy}
      error={error?.text} onClose={() => { setConfirmation(null); setError(null); }} onConfirm={() => { if (confirmation) void save(confirmation, true); }}>
      <p>{t("domain.confirmBody", { url: confirmation ? `https://${confirmation}` : "" })}</p>
      {error?.detail ? <HelpTip label={t("domain.details")}>{error.detail}</HelpTip> : null}
    </ConfirmDialog>
  </div>;
}
