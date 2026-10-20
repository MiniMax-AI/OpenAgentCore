import { useEffect, useState } from "react";

export type ThemeChoice = "light" | "dark" | "system";
const key = "oac-example-theme";
const event = "parsar-theme";

function read(): ThemeChoice {
  try {
    const value = localStorage.getItem(key);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}

export function setTheme(choice: ThemeChoice) {
  try {
    localStorage.setItem(key, choice);
  } catch {
    /* Theme remains usable without storage. */
  }
  window.dispatchEvent(new Event(event));
}

// One theme state shared by the sidebar toggle and the settings page.
export function useTheme() {
  const [choice, setChoice] = useState(read);
  const [systemDark, setSystemDark] = useState(
    () => matchMedia("(prefers-color-scheme: dark)").matches,
  );
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const system = () => setSystemDark(media.matches);
    const stored = () => setChoice(read());
    media.addEventListener("change", system);
    window.addEventListener(event, stored);
    return () => {
      media.removeEventListener("change", system);
      window.removeEventListener(event, stored);
    };
  }, []);
  const dark = choice === "dark" || (choice === "system" && systemDark);
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  }, [dark]);
  return { choice, dark };
}
