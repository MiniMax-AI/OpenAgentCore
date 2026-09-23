import { chinese, type MessageKey } from "./locale-strings";
export type Locale = "en" | "zh";
export const localeStorageKey = "agents-core-web.locale";
export function resolveLocale(saved: string | null, languages: readonly string[]): Locale {
  if (saved === "en" || saved === "zh") return saved;
  return languages[0]?.toLowerCase().startsWith("zh") ? "zh" : "en";
}
export function translate(locale: Locale, key: MessageKey): string {
  return locale === "zh" ? chinese[key] : key;
}
