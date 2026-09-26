import { createContext, useContext, useEffect, useRef } from "react";

import type { ConsoleView } from "./console-routes";

/**
 * Parameters carried in the hash after the view (`#session?project=…&id=…`) so
 * a link can open one resource of one project and survive a reload.
 */
export interface RouteParams {
  /** The project a page is filtered to, or a secondary page belongs to. */
  project?: string;
  /** The resource opened by a secondary page or focused in a list. */
  id?: string;
}

export function routeParamsFromHash(hash: string): RouteParams {
  const query = hash.includes("?") ? hash.slice(hash.indexOf("?") + 1) : "";
  const params = new URLSearchParams(query);
  const read = (name: string) => {
    const value = params.get(name);
    return value && /^[A-Za-z0-9_.:-]{1,128}$/.test(value) ? value : undefined;
  };
  return { project: read("project"), id: read("id") };
}

export function hashWithParams(base: string, params: RouteParams = {}): string {
  const query = new URLSearchParams();
  if (params.project) query.set("project", params.project);
  if (params.id) query.set("id", params.id);
  const suffix = query.toString();
  if (!suffix) return base;
  return `${base || "#overview"}?${suffix}`;
}

/**
 * What a link asks its target page to open on arrival, such as Add node from
 * Getting started, a project's call samples, or the Session log filtered to an
 * Agent (whose ID is the link's `id`) from Agent metrics. It lives only in
 * memory: a reload, Back or any other navigation drops it, so nothing reopens
 * on its own.
 */
export type ConsoleIntent = "add-node" | "create-project" | "issue-key" | "getting-started" | "default-model" | "how-to-call" | "agent-sessions";

/** Whether a page can act on an intent now, not yet, or not at all (then it drops the intent). */
export type IntentReadiness = "ready" | "wait" | "unavailable";

export interface ConsoleNavigation {
  view: ConsoleView;
  params: RouteParams;
  /** What the link that opened this page asked it to open, until the page takes it. */
  intent: ConsoleIntent | null;
  navigate: (view: ConsoleView, params?: RouteParams, intent?: ConsoleIntent) => void;
  /**
   * Returns to the page the user came from inside the console (a Skill opened
   * from a template goes back to the template); a page opened directly, from a
   * link or a reload, goes to `view` instead.
   */
  back: (view: ConsoleView, params?: RouteParams) => void;
  /** Drops the intent once its page has acted on it. */
  clearIntent: () => void;
}

/** How many console pages lie behind the current history entry. */
export function consoleDepth(): number {
  const state: unknown = window.history.state;
  const depth = state && typeof state === "object" && "consoleDepth" in state ? Number(state.consoleDepth) : 0;
  return Number.isSafeInteger(depth) && depth > 0 ? depth : 0;
}

export const ConsoleNavigationContext = createContext<ConsoleNavigation>({
  view: "overview",
  params: {},
  intent: null,
  navigate: () => undefined,
  back: () => undefined,
  clearIntent: () => undefined,
});

export function useConsoleNavigation(): ConsoleNavigation {
  return useContext(ConsoleNavigationContext);
}

/**
 * Runs `act` once when this page was opened with `intent` and can act on it;
 * an intent the page cannot act on is dropped, so it never fires later.
 */
export function useConsoleIntent(intent: ConsoleIntent, readiness: IntentReadiness, act: () => void): void {
  const { intent: current, clearIntent } = useConsoleNavigation();
  const action = useRef(act);
  action.current = act;
  useEffect(() => {
    if (current !== intent || readiness === "wait") return;
    clearIntent();
    if (readiness === "ready") action.current();
  }, [current, intent, readiness, clearIntent]);
}
