import { createContext, useCallback, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ArrowRight, Cloud, LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ThemeMenu } from "../../components/ThemeMenu";
import { setLanguage } from "../../i18n";
import { changeConsoleAuth, ConsoleAuthError, readConsoleAuth, type ConsoleAuth } from "./auth";
import { defaultProgress, progressKey, saveProgress } from "./progress";
import "./console-access.css";

const ConsoleAccountContext = createContext<{ username: string; logout: () => Promise<void> } | null>(null);
export const useConsoleAccount = () => useContext(ConsoleAccountContext);

export function ConsoleLanguage() {
  const { t, i18n } = useTranslation("firstRun");
  const language = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  return <select className="console-language" aria-label={t("Console language")} value={language} onChange={(event) => void setLanguage(event.target.value === "zh-CN" ? "zh-CN" : "en")}>
    <option value="en">English</option><option value="zh-CN">中文</option>
  </select>;
}

export function ConsoleAccountMenu() {
  const account = useConsoleAccount();
  const { t } = useTranslation("firstRun");
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  if (!account) return null;
  return <div className="console-account-menu">
    <button type="button" title={account.username} disabled={busy} onClick={async () => {
      setBusy(true); setFailed(false);
      try { await account.logout(); } catch { setBusy(false); setFailed(true); }
    }}><LogOut size={14} aria-hidden="true" /><span>{t(busy ? "Signing out…" : "Sign out")}</span></button>
    {failed ? <small role="alert">{t("Could not sign out. Try again.")}</small> : null}
  </div>;
}

export function ConsoleAccess({ children }: { children: ReactNode }) {
  const { t } = useTranslation("firstRun");
  const [status, setStatus] = useState<ConsoleAuth | null>(null);
  const [failed, setFailed] = useState(false);
  const [revision, setRevision] = useState(0);
  const accountExpected = useRef(false);
  const generation = useRef(0);
  const refresh = useCallback(() => setRevision((current) => current + 1), []);
  useEffect(() => {
    const controller = new AbortController();
    const current = ++generation.current;
    setFailed(false);
    void readConsoleAuth(controller.signal, accountExpected.current).then((value) => {
      if (generation.current !== current) return;
      if (value.mode !== "legacy") accountExpected.current = true;
      setStatus(value);
    }).catch(() => { if (!controller.signal.aborted && generation.current === current) setFailed(true); });
    return () => { controller.abort(); generation.current++; };
  }, [revision]);
  useEffect(() => {
    if (status?.mode !== "authenticated") return;
    const check = () => { if (document.visibilityState === "visible") refresh(); };
    const timer = window.setInterval(check, 60_000);
    window.addEventListener("focus", check);
    return () => { window.clearInterval(timer); window.removeEventListener("focus", check); };
  }, [status?.mode, refresh]);
  if (status?.mode === "legacy") return children;
  if (status?.mode === "authenticated") return <ConsoleAccountContext.Provider value={{ username: status.username, logout: async () => {
    const next = await changeConsoleAuth("logout", {});
    generation.current++;
    setStatus(next);
  } }}>{children}</ConsoleAccountContext.Provider>;

  return <div className="app-shell console-access">
    <main className="app-main console-access-main">
      <header><ThemeMenu /><ConsoleLanguage /></header>
      <section className="console-access-stage">
        <div className="console-access-story"><div className="console-cloud-symbol" aria-hidden="true"><Cloud size={34} strokeWidth={1} /></div>
          <h1>{t("A place for your Agents to work.")}</h1>
          <p>{t("Connect your machines. Create Agents. Watch work happen.")}</p>
        </div>
        {status && !failed ? <AccountForm key={`${status.mode}:${revision}`} setup={status.mode === "setup"} onAuthenticated={(next) => { generation.current++; setStatus(next); }} onRefresh={refresh} /> :
          <div className="console-auth-form" aria-live="polite"><p>{t(failed ? "Could not connect to your console." : "Connecting to your console…")}</p>
            {failed ? <button className="button outline" onClick={refresh}>{t("Try again")}</button> : null}</div>}
      </section>
    </main>
  </div>;
}

function AccountForm({ setup, onAuthenticated, onRefresh }: {
  setup: boolean; onAuthenticated: (status: ConsoleAuth) => void; onRefresh: () => void;
}) {
  const { t } = useTranslation("firstRun");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const pending = useRef(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || uncertain) return;
    const data = new FormData(event.currentTarget);
    const password = String(data.get("password") ?? "");
    if (setup && password !== data.get("confirm")) { setError(t("Passwords do not match.")); return; }
    const bytes = new TextEncoder().encode(password).length;
    if (setup && (bytes < 12 || bytes > 72)) { setError(t("Use a password between 12 and 72 bytes.")); return; }
    pending.current = true; setBusy(true); setError(null);
    const request = new AbortController(); controller.current = request;
    try {
      const next = await changeConsoleAuth(setup ? "setup" : "login", {
        username: String(data.get("username") ?? ""), password,
      }, request.signal);
      if (!request.signal.aborted) {
        if (setup && next.mode === "authenticated") saveProgress(progressKey(window.location.origin, next.username), defaultProgress);
        onAuthenticated(next);
      }
    } catch (cause) {
      if (request.signal.aborted) return;
      const status = cause instanceof ConsoleAuthError ? cause.status : 0;
      if (status === 401 || status === 400) setError(t(setup ? "Check the account details and try again." : "Check your sign-in details and try again."));
      else if (status === 429) setError(t("Too many attempts. Wait a moment before trying again."));
      else {
        setUncertain(true);
        setError(t(status === 409 ? "An administrator already exists. Sign in to continue." : "Could not confirm the result. Check the account status before trying again."));
      }
    } finally { pending.current = false; if (!request.signal.aborted) setBusy(false); }
  }
  return <form className="console-auth-form form-stack" onSubmit={(event) => void submit(event)}>
    <div><h2>{t(setup ? "Create your administrator account" : "Welcome back")}</h2>
      <p>{t(setup ? "You manage this cloud." : "Use your administrator account to continue.")}</p></div>
    <label className="field"><span>{t("Administrator username")}</span><input name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} required maxLength={64} pattern={setup ? "[a-zA-Z0-9._\\-]+" : undefined} disabled={busy || uncertain} />
      {setup ? <small>{t("Letters, numbers, dots, underscores and hyphens.")}</small> : null}</label>
    <label className="field"><span>{t("Password")}</span><input name="password" type="password" autoComplete={setup ? "new-password" : "current-password"} required disabled={busy || uncertain} />
      {setup ? <small>{t("12–72 bytes. Keep this password somewhere safe.")}</small> : null}</label>
    {setup ? <label className="field"><span>{t("Confirm password")}</span><input name="confirm" type="password" autoComplete="new-password" required disabled={busy || uncertain} /></label> : null}
    {error ? <p className="console-auth-error" role="alert">{error}</p> : null}
    {uncertain ? <button className="button outline" type="button" onClick={onRefresh}>{t("Check account status")}</button> :
      <button className="button primary" type="submit" disabled={busy}>{t(busy ? setup ? "Creating account…" : "Signing in…" : setup ? "Create administrator account" : "Sign in")}<ArrowRight size={15} aria-hidden="true" /></button>}
  </form>;
}
