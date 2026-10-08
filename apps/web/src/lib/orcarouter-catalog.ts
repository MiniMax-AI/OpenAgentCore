/**
 * The console's OrcaRouter model catalog read.
 *
 * The browser never talks to OrcaRouter: the console server performs the
 * request with the operator's key, which travels in a request header and is
 * never placed in a URL, a log or browser storage. This module only composes
 * the same-origin request and reduces the answer.
 *
 * Live discovery is authoritative. When it cannot be read, the caller gets the
 * small verified fallback labelled `degraded` — never free text and never a
 * mixture of live records and seed records.
 */
import { catalogAnswer, fallbackAnswer, type CatalogAnswer, type CatalogSelector } from "./orcarouter";

export interface OrcarouterCatalog extends CatalogAnswer {
  /** True when OrcaRouter refused the key; the operator must replace it. */
  unauthorized: boolean;
  /** Why a fallback is in use, when one is. */
  reason?: string;
}

/**
 * Reads the catalog for one entrance. A rejection is reported as a failure so
 * the caller can point at the API key field; an outage offers the verified
 * fallback instead of an empty selector.
 */
export async function readOrcarouterCatalog(input: {
  selector: CatalogSelector;
  key: string;
  signal?: AbortSignal;
  request?: typeof fetch;
}): Promise<OrcarouterCatalog> {
  const request = input.request ?? fetch;
  const capability = input.selector.capability;
  const response = await request(`/console/orcarouter/catalog?capability=${encodeURIComponent(capability)}`, {
    credentials: "include",
    headers: { "X-OrcaRouter-Key": input.key.trim() },
    signal: input.signal,
  });
  if (response.status === 401 || response.status === 403) return { ...fallbackAnswer(input.selector, ""), unauthorized: true };
  const payload = (await response.json().catch(() => null)) as { models?: unknown; catalog_origin?: unknown; degraded?: unknown } | null;
  if (payload === null || !Array.isArray(payload.models)) return { ...fallbackAnswer(input.selector, ""), unauthorized: false };
  const origin = typeof payload.catalog_origin === "string" ? payload.catalog_origin : "";
  if (payload.degraded === true || !response.ok) return { ...fallbackAnswer(input.selector, origin), unauthorized: false };
  return { ...catalogAnswer({ data: payload.models }, input.selector, origin), unauthorized: false };
}
