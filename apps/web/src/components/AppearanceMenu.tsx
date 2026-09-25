import { Check, ChevronDown, Languages, Moon, Sun } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { setLanguage, type SupportedLanguage } from "../i18n";
import { useTheme, type ResolvedTheme } from "../lib/theme";

export function AppearanceMenu() {
  const { i18n, t } = useTranslation("navigation");
  const { resolvedTheme, setPreference } = useTheme();
  const language: SupportedLanguage = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const ThemeIcon = resolvedTheme === "dark" ? Moon : Sun;

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", closeOnOutsidePointer);
    return () => document.removeEventListener("mousedown", closeOnOutsidePointer);
  }, [open]);

  const focusMenuItem = (edge: "first" | "last") => {
    window.requestAnimationFrame(() => {
      const items = rootRef.current?.querySelectorAll<HTMLButtonElement>('button[role="menuitemradio"]');
      (edge === "first" ? items?.[0] : items?.[items.length - 1])?.focus();
    });
  };

  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // Escape belongs to the menu only while it is open; otherwise the page may use it.
    if (event.key === "Escape") {
      if (!open) return;
      event.preventDefault();
      close();
      return;
    }
    if (!open || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    const items = Array.from(rootRef.current?.querySelectorAll<HTMLButtonElement>('button[role="menuitemradio"]') ?? []);
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

  return <div className="appearance-menu" ref={rootRef} onKeyDown={onKeyDown}>
    <button
      ref={triggerRef}
      className="appearance-menu-trigger"
      type="button"
      aria-haspopup="menu"
      aria-expanded={open}
      aria-label={t("appearanceSettings")}
      title={t("appearanceSettings")}
      onClick={(event) => {
        const nextOpen = !open;
        setOpen(nextOpen);
        if (nextOpen && event.detail === 0) focusMenuItem("first");
      }}
      onKeyDown={(event) => {
        if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
        event.preventDefault();
        setOpen(true);
        focusMenuItem(event.key === "ArrowDown" ? "first" : "last");
      }}
    >
      <Languages size={14} strokeWidth={1.5} aria-hidden="true" />
      <span>{language === "en" ? "EN" : "中"}</span>
      <ThemeIcon size={14} strokeWidth={1.5} aria-hidden="true" />
      <ChevronDown size={12} strokeWidth={1.5} aria-hidden="true" />
    </button>
    {open ? <div className="appearance-menu-panel" role="menu" aria-label={t("appearanceSettings")}>
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
    </div> : null}
  </div>;
}
