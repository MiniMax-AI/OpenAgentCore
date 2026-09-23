import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { localeStorageKey, resolveLocale, translate, type Locale } from "./locale";
import type { MessageKey } from "./locale-strings";
const LocaleContext = createContext({ locale: "en" as Locale, setLocale: (_locale: Locale) => {}, t: (key: MessageKey): string => key });
export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, updateLocale] = useState<Locale>(() => {
    let saved: string | null = null;
    try { saved = localStorage.getItem(localeStorageKey); } catch { /* Browser storage can be unavailable. */ }
    return resolveLocale(saved, navigator.languages);
  });
  const value = useMemo(() => ({ locale, t: (key: MessageKey) => translate(locale, key), setLocale: (next: Locale) => {
    updateLocale(next);
    try { localStorage.setItem(localeStorageKey, next); } catch { /* Keep the current-page preference. */ }
  } }), [locale]);
  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}
export function useLocale() { return useContext(LocaleContext); }
