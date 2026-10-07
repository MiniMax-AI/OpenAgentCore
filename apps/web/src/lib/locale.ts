import { chinese, type MessageKey } from "./locale-strings";
export type Locale = "en" | "zh";
export function translate(locale: Locale, key: MessageKey): string {
  return locale === "zh" ? chinese[key] : key;
}
