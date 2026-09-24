import { useEffect, useMemo, useState, type ReactNode } from "react";

import {
  ThemeContext,
  defaultThemePreference,
  themeStorageKey,
  type ResolvedTheme,
  type ThemeContextValue,
  type ThemePreference,
} from "./theme";

function readStoredPreference(): ThemePreference {
  const stored = window.localStorage.getItem(themeStorageKey);
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : defaultThemePreference;
}

function systemTheme(): ResolvedTheme {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

/** `data-theme` drives the console's own tokens; the `dark` class drives Beautiful UI and Tailwind's `dark:` variant. */
function applyTheme(theme: ResolvedTheme): void {
  document.documentElement.dataset.theme = theme;
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.style.colorScheme = theme;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreference] = useState<ThemePreference>(() => readStoredPreference());
  const [systemPreference, setSystemPreference] = useState<ResolvedTheme>(() => systemTheme());
  const resolvedTheme = preference === "system" ? systemPreference : preference;

  useEffect(() => {
    applyTheme(resolvedTheme);
    window.localStorage.setItem(themeStorageKey, preference);
  }, [preference, resolvedTheme]);

  useEffect(() => {
    if (preference !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => setSystemPreference(systemTheme());
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [preference]);

  const value = useMemo<ThemeContextValue>(() => ({
    preference,
    resolvedTheme,
    setPreference,
  }), [preference, resolvedTheme]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
