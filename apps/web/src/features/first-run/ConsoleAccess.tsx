import { createContext, useCallback, useContext, useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { ArrowRight, LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";
import { HelpTip } from "../../components/console-ui";
import { ThemeMenu } from "../../components/ThemeMenu";
import { useToast } from "../../components/Toast";
import { setLanguage } from "../../i18n";
import { projectsQuery, queryClient } from "../../lib/queries";
import { OnboardingLayout } from "../onboarding/OnboardingLayout";
import { withTransition } from "../onboarding/view-transition";
import { changeConsoleAuth, ConsoleAuthError, readConsoleAuth, type ConsoleAuth } from "./auth";
import "./console-access.css";

/**
 * Where the installer writes the Core key: its file inside the installation
 * directory, and that file under the default installation directory. The
 * sign-in help names both.
 */
const CORE_KEY_LOCATION = { file: "admin/core.key", defaultPath: "~/.parsar/core/admin/core.key" } as const;

const ConsoleAccountContext = createContext<{ logout: () => Promise<void> } | null>(null);
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
  const toast = useToast();
  if (!account) return null;
  return <div className="console-account-menu">
    <button type="button" disabled={busy} onClick={async () => {
      setBusy(true);
      try { await account.logout(); } catch { setBusy(false); toast.show(t("Could not sign out. Try again."), { tone: "error" }); }
    }}><LogOut size={14} aria-hidden="true" /><span>{t(busy ? "Signing out…" : "Sign out")}</span></button>
  </div>;
}

/** True when the deployment has no project yet, so first-run setup follows signing in. */
async function needsFirstProject(): Promise<boolean> {
  try {
    return (await queryClient.fetchQuery(projectsQuery)).length === 0;
  } catch {
    return false; // The console reports the failed read itself.
  }
}

export function ConsoleAccess({ children }: { children: ReactNode }) {
  const { t } = useTranslation("firstRun");
  const [status, setStatus] = useState<ConsoleAuth | null>(null);
  const [failed, setFailed] = useState(false);
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  const refresh = useCallback(() => setRevision((current) => current + 1), []);
  useEffect(() => {
    const controller = new AbortController();
    const current = ++generation.current;
    setFailed(false);
    void readConsoleAuth(controller.signal).then((value) => {
      if (generation.current === current) setStatus(value);
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
  if (status?.mode === "authenticated") return <ConsoleAccountContext.Provider value={{ logout: async () => {
    const next = await changeConsoleAuth({ action: "logout" });
    generation.current++;
    setStatus(next);
  } }}>{children}</ConsoleAccountContext.Provider>;

  return <OnboardingLayout scene={status ? "login" : null} controls={<><ThemeMenu /><ConsoleLanguage /></>}>
    {status && !failed ? <CoreKeyForm key={revision} onAuthenticated={(next, firstRun, from) => {
      // Without a project, first-run setup continues on the same stage; otherwise the console opens.
      withTransition(firstRun ? "step" : "enter", () => {
        generation.current++;
        setStatus(next);
      }, from);
    }} /> :
      <div className="console-auth-form" aria-live="polite"><p>{t(failed ? "Could not connect to your console." : "Connecting to your console…")}</p>
        {failed ? <button className="button outline" onClick={refresh}>{t("Try again")}</button> : null}</div>}
  </OnboardingLayout>;
}

function CoreKeyForm({ onAuthenticated }: {
  onAuthenticated: (status: ConsoleAuth, firstRun: boolean, from: HTMLElement | null) => void;
}) {
  const { t } = useTranslation("firstRun");
  const id = useId();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const input = useRef<HTMLInputElement>(null);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);

  function errorMessage(cause: unknown): string {
    const status = cause instanceof ConsoleAuthError ? cause.status : 0;
    if (status === 401) return t("This Core key is not correct. Check it and try again.");
    if (status === 429) {
      const seconds = cause instanceof ConsoleAuthError ? cause.retryAfterSeconds : null;
      return seconds && seconds > 1 ? t("Too many attempts. Try again in {{seconds}} seconds.", { seconds }) : t("Too many attempts. Wait a moment before trying again.");
    }
    if (status === 503) return t("The console cannot check the key right now. Try again later.");
    return t("Could not sign in. Try again.");
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    const submitter = event.nativeEvent instanceof SubmitEvent && event.nativeEvent.submitter instanceof HTMLElement ? event.nativeEvent.submitter : null;
    const coreKey = String(new FormData(event.currentTarget).get("core-key") ?? "").trim();
    if (!coreKey) return;
    pending.current = true; setBusy(true); setError(null);
    const request = new AbortController(); controller.current = request;
    try {
      const next = await changeConsoleAuth({ action: "login", coreKey }, request.signal);
      const firstRun = await needsFirstProject();
      if (!request.signal.aborted) onAuthenticated(next, firstRun, submitter);
    } catch (cause) {
      if (request.signal.aborted) return;
      setError(errorMessage(cause));
      input.current?.select();
    } finally { pending.current = false; if (!request.signal.aborted) setBusy(false); }
  }

  return <form className="console-auth-form form-stack" onSubmit={(event) => void submit(event)}>
    <h2>{t("Sign in to Parsar Core")}</h2>
    {/* Where the key is and what it can do sit behind the help tip, not as small print under the field. */}
    <div className="field">
      <span className="field-label-row"><label htmlFor={`${id}-key`}>{t("Core key")}</label>
        <HelpTip>{t("This deployment's Core key. The installer saved it in a private file: {{file}} in the installation directory, by default {{defaultPath}}. It is an administration credential: it cannot call the /v1 Agents API, and the console never keeps it in your browser.", CORE_KEY_LOCATION)}</HelpTip></span>
      {/* Read-only rather than disabled while signing in, so a refused key can be selected for correction. */}
      <input ref={input} id={`${id}-key`} name="core-key" type="password" autoComplete="off" autoCapitalize="none" spellCheck={false} required autoFocus
        readOnly={busy} aria-invalid={error ? true : undefined} aria-describedby={error ? `${id}-error` : undefined} />
    </div>
    {error ? <p className="console-auth-error" id={`${id}-error`} role="alert">{error}</p> : null}
    <button className="button primary" type="submit" disabled={busy}>{t(busy ? "Signing in…" : "Sign in")}<ArrowRight size={15} aria-hidden="true" /></button>
  </form>;
}
