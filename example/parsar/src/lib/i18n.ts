import i18next from "i18next";
import english from "./en.json";
let language = "zh";
try {
  language = localStorage.getItem("parsar-language") === "en" ? "en" : "zh";
} catch {
  /* Use the default locale. */
}
void i18next.init({
  lng: language,
  fallbackLng: "zh",
  initAsync: false,
  keySeparator: false,
  nsSeparator: false,
  resources: { en: { translation: english }, zh: { translation: {} } },
  interpolation: { escapeValue: false },
});
export function tr(text: string, values: Record<string, unknown> = {}): string {
  return i18next.t(text, { ...values, defaultValue: text }) as string;
}
export function setLanguage(language: "zh" | "en") {
  localStorage.setItem("parsar-language", language);
  location.reload();
}
export const currentLanguage = language;

document.documentElement.lang = language === "en" ? "en" : "zh-CN";

export function englishTitle(title: string): string | undefined {
  return (english as Record<string, string>)[title];
}
