export interface IntroductionProgress { step: 0 | 1 | 2; dismissed: boolean }
export const defaultProgress: IntroductionProgress = { step: 0, dismissed: false };
export function progressKey(origin: string, username: string) { return `parsar.introduction.v1:${origin}:${username}`; }
export function parseProgress(raw: string | null): IntroductionProgress {
  try {
    const value: unknown = JSON.parse(raw ?? "null");
    if (value && typeof value === "object" && "step" in value && "dismissed" in value &&
      (value.step === 0 || value.step === 1 || value.step === 2) && typeof value.dismissed === "boolean") {
      return { step: value.step, dismissed: value.dismissed };
    }
  } catch { /* Invalid browser preferences do not prevent opening the console. */ }
  return defaultProgress;
}
export function readProgress(key: string): IntroductionProgress {
  try { return parseProgress(localStorage.getItem(key)); } catch { return defaultProgress; }
}
export function saveProgress(key: string, value: IntroductionProgress) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* This visit still works without persistent preferences. */ }
}
