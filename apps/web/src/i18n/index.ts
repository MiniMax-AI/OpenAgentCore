import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import { defaultNamespace, resources } from "./resources";

export const supportedLanguages = ["en", "zh-CN"] as const;
export type SupportedLanguage = (typeof supportedLanguages)[number];

const LANGUAGE_STORAGE_KEY = "oac-web.language";

export function resolveLanguage(stored: string | null, languages: readonly string[]): SupportedLanguage {
  if (stored === "en" || stored === "zh-CN") return stored;
  for (const language of languages) {
    const normalized = language.toLowerCase();
    if (normalized.startsWith("zh")) return "zh-CN";
    if (normalized.startsWith("en")) return "en";
  }
  return "en";
}

function browserLanguage(): SupportedLanguage {
  if (typeof window === "undefined") return "en";
  let stored: string | null = null;
  try {
    stored = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
  const languages = window.navigator.languages?.length ? window.navigator.languages : [window.navigator.language];
  return resolveLanguage(stored, languages);
}

void i18n
  .use(initReactI18next)
  .init({
    resources,
    lng: browserLanguage(),
    fallbackLng: "en",
    supportedLngs: supportedLanguages,
    defaultNS: defaultNamespace,
    fallbackNS: "common",
    interpolation: { escapeValue: false },
    returnNull: false,
  });

export function setLanguage(language: SupportedLanguage): Promise<unknown> {
  if (typeof window !== "undefined") {
    try {
      window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
    } catch {
      // Keep the in-memory choice when persistence is unavailable.
    }
  }
  if (typeof document !== "undefined") document.documentElement.lang = language;
  return i18n.changeLanguage(language);
}

if (typeof document !== "undefined") document.documentElement.lang = browserLanguage();

export default i18n;
