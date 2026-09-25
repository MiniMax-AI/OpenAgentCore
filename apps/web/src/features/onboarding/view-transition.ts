import { flushSync } from "react-dom";

/**
 * How the page changes: `enter` dissolves the old page and reveals the new
 * one in a circle growing from the button that was pressed (signing in, and
 * opening or leaving the tour).
 */
export type OnboardingTransition = "enter";

/**
 * Applies a state change inside a View Transition when the browser supports
 * one and motion is allowed; otherwise applies it directly. The CSS in
 * onboarding.css keys its animations on `html[data-transition]`.
 */
export function withTransition(kind: OnboardingTransition, update: () => void, from?: Element | null): void {
  const root = document.documentElement;
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (reduced || typeof document.startViewTransition !== "function") {
    update();
    return;
  }
  root.dataset.transition = kind;
  if (from) {
    const box = from.getBoundingClientRect();
    root.style.setProperty("--reveal-x", `${Math.round(box.left + box.width / 2)}px`);
    root.style.setProperty("--reveal-y", `${Math.round(box.top + box.height / 2)}px`);
  }
  const transition = document.startViewTransition(() => flushSync(update));
  void transition.finished.finally(() => {
    delete root.dataset.transition;
    root.style.removeProperty("--reveal-x");
    root.style.removeProperty("--reveal-y");
  });
}
