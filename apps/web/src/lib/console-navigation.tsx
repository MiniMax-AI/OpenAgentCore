import { createContext, useContext } from "react";

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

export interface ConsoleNavigation {
  view: ConsoleView;
  params: RouteParams;
  navigate: (view: ConsoleView, params?: RouteParams) => void;
}

export const ConsoleNavigationContext = createContext<ConsoleNavigation>({
  view: "overview",
  params: {},
  navigate: () => undefined,
});

export function useConsoleNavigation(): ConsoleNavigation {
  return useContext(ConsoleNavigationContext);
}
