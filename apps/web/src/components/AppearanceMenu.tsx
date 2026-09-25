import { Check, ChevronDown, CircleUserRound, Languages, LogOut, Moon, Sun } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { setLanguage, type SupportedLanguage } from "../i18n";
import { useTheme, type ResolvedTheme } from "../lib/theme";
import { useConsoleAccount } from "../features/first-run/ConsoleAccess";

export function AppearanceMenu({ withAccount = false }: { withAccount?: boolean }) {
  const { i18n, t } = useTranslation("navigation");
  const { t: tAuth } = useTranslation("firstRun");
  const account = useConsoleAccount();
  const { resolvedTheme, setPreference } = useTheme();
  const language: SupportedLanguage = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const [open, setOpen] = useState(false);
  const [languageOpen, setLanguageOpen] = useState(false);
  const [signingOut, setSigningOut] = useState(false);
  const [signOutFailed, setSignOutFailed] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const languageRef = useRef<HTMLButtonElement>(null);
  const ThemeIcon = resolvedTheme === "dark" ? Moon : Sun;

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) { setOpen(false); setLanguageOpen(false); }
    };
    document.addEventListener("mousedown", closeOnOutsidePointer);
    return () => document.removeEventListener("mousedown", closeOnOutsidePointer);
  }, [open]);

  const focusMenuItem = (edge: "first" | "last") => {
    window.requestAnimationFrame(() => {
      const items = rootRef.current?.querySelectorAll<HTMLButtonElement>('button[role^="menuitem"]');
      (edge === "first" ? items?.[0] : items?.[items.length - 1])?.focus();
    });
  };

  const close = () => {
    setOpen(false);
    setLanguageOpen(false);
    triggerRef.current?.focus();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      if (languageOpen) { setLanguageOpen(false); languageRef.current?.focus(); return; }
      close();
      return;
    }
    if (!open || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    const items = Array.from(rootRef.current?.querySelectorAll<HTMLButtonElement>('button[role^="menuitem"]') ?? []);
    if (!items.length) return;
    event.preventDefault();
    if (event.key === "Home") return void items[0]?.focus();
    if (event.key === "End") return void items[items.length - 1]?.focus();
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    const delta = event.key === "ArrowDown" ? 1 : -1;
    items[(current + delta + items.length) % items.length]?.focus();
  };

  const chooseLanguage = (value: SupportedLanguage) => {
    void setLanguage(value);
    close();
  };
  const chooseTheme = (value: ResolvedTheme) => {
    setPreference(value);
    close();
  };

  const showAccount = withAccount && Boolean(account);
  const toggleTheme = () => {
    setPreference(resolvedTheme === "dark" ? "light" : "dark");
    close();
  };
  return <div className={`appearance-menu${showAccount ? " appearance-menu-account" : ""}`} ref={rootRef} onKeyDown={onKeyDown}>
    <button
      ref={triggerRef}
      className={`appearance-menu-trigger${showAccount ? " appearance-menu-account-trigger" : ""}`}
      type="button"
      aria-haspopup="menu"
      aria-expanded={open}
      aria-label={t(showAccount ? "accountAndAppearance" : "appearanceSettings")}
      title={t(showAccount ? "accountAndAppearance" : "appearanceSettings")}
      onClick={(event) => {
        const nextOpen = !open;
        setOpen(nextOpen);
        if (!nextOpen) setLanguageOpen(false);
        if (nextOpen && event.detail === 0) focusMenuItem("first");
      }}
      onKeyDown={(event) => {
        if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
        event.preventDefault();
        setOpen(true);
        focusMenuItem(event.key === "ArrowDown" ? "first" : "last");
      }}
    >
      {showAccount ? <><CircleUserRound size={17} strokeWidth={1.6} aria-hidden="true" /><span className="appearance-account-name">{account?.username}</span><ChevronDown size={13} strokeWidth={1.5} aria-hidden="true" /></> : <><Languages size={14} strokeWidth={1.5} aria-hidden="true" /><span>{language === "en" ? "EN" : "中"}</span><ThemeIcon size={14} strokeWidth={1.5} aria-hidden="true" /><ChevronDown size={12} strokeWidth={1.5} aria-hidden="true" /></>}
    </button>
    {open ? <div className={`appearance-menu-panel${showAccount ? " appearance-account-panel" : ""}`} role="menu" aria-label={t(showAccount ? "accountAndAppearance" : "appearanceSettings")}>
      {showAccount ? <>
        <div className="appearance-account-header"><span className="appearance-account-avatar" aria-hidden="true">{account?.username.slice(0, 1).toUpperCase()}</span><div><strong>{account?.username}</strong><small>{t("account")}</small></div></div>
        <div className="appearance-account-actions">
          <button ref={languageRef} type="button" role="menuitem" aria-haspopup="true" aria-expanded={languageOpen} onClick={() => setLanguageOpen((value) => !value)}><span><Languages size={16} aria-hidden="true" />{t("language")}</span><span className="appearance-account-value">{language === "en" ? "English" : "简体中文"}<ChevronDown size={13} aria-hidden="true" /></span></button>
          {languageOpen ? <div className="appearance-language-options" role="group" aria-label={t("language")}>
            <button type="button" role="menuitemradio" aria-checked={language === "en"} onClick={() => chooseLanguage("en")}>English{language === "en" ? <Check size={14} aria-hidden="true" /> : null}</button>
            <button type="button" role="menuitemradio" aria-checked={language === "zh-CN"} onClick={() => chooseLanguage("zh-CN")}>简体中文{language === "zh-CN" ? <Check size={14} aria-hidden="true" /> : null}</button>
          </div> : null}
          <button type="button" role="menuitem" aria-label={t(resolvedTheme === "dark" ? "switchToLightTheme" : "switchToDarkTheme")} onClick={toggleTheme}><span><ThemeIcon size={16} aria-hidden="true" />{t("theme")}</span><span className="appearance-account-value">{t(resolvedTheme === "dark" ? "darkMode" : "lightMode")}</span></button>
        </div>
        <div className="appearance-account-signout"><button type="button" role="menuitem" disabled={signingOut} onClick={async () => {
          if (!account) return;
          setSigningOut(true); setSignOutFailed(false);
          try { await account.logout(); close(); } catch { setSigningOut(false); setSignOutFailed(true); }
        }}><LogOut size={16} aria-hidden="true" />{tAuth(signingOut ? "Signing out…" : "Sign out")}</button>
        {signOutFailed ? <small role="alert">{tAuth("Could not sign out. Try again.")}</small> : null}</div>
      </> : <>
      <div className="appearance-menu-section" role="group" aria-label={t("language")}>
        <span>{t("language")}</span>
        <button type="button" role="menuitemradio" aria-checked={language === "en"} onClick={() => chooseLanguage("en")}><span>English</span>{language === "en" ? <Check size={14} aria-hidden="true" /> : null}</button>
        <button type="button" role="menuitemradio" aria-checked={language === "zh-CN"} onClick={() => chooseLanguage("zh-CN")}><span>简体中文</span>{language === "zh-CN" ? <Check size={14} aria-hidden="true" /> : null}</button>
      </div>
      <div className="appearance-menu-section" role="group" aria-label={t("theme")}>
        <span>{t("theme")}</span>
        <button type="button" role="menuitemradio" aria-checked={resolvedTheme === "light"} onClick={() => chooseTheme("light")}><span><Sun size={14} aria-hidden="true" />{t("lightTheme")}</span>{resolvedTheme === "light" ? <Check size={14} aria-hidden="true" /> : null}</button>
        <button type="button" role="menuitemradio" aria-checked={resolvedTheme === "dark"} onClick={() => chooseTheme("dark")}><span><Moon size={14} aria-hidden="true" />{t("darkTheme")}</span>{resolvedTheme === "dark" ? <Check size={14} aria-hidden="true" /> : null}</button>
      </div>
      </>}
    </div> : null}
  </div>;
}
